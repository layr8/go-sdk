package layr8

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Attachment represents a DIDComm v2 attachment (spec section 5).
type Attachment struct {
	ID          string         `json:"id,omitempty"`
	Description string         `json:"description,omitempty"`
	Filename    string         `json:"filename,omitempty"`
	MediaType   string         `json:"media_type,omitempty"`
	Format      string         `json:"format,omitempty"`
	LastmodTime int64          `json:"lastmod_time,omitempty"`
	ByteCount   int64          `json:"byte_count,omitempty"`
	Data        AttachmentData `json:"data"`
}

// AttachmentData carries the attachment payload.
type AttachmentData struct {
	Base64 string   `json:"base64,omitempty"`
	JSON   any      `json:"json,omitempty"`
	JWS    any      `json:"jws,omitempty"`
	Hash   string   `json:"hash,omitempty"`
	Links  []string `json:"links,omitempty"`
}

// Message represents a DIDComm v2 message.
type Message struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	From           string          `json:"from"`
	To             []string        `json:"to"`
	ThreadID       string          `json:"thid,omitempty"`
	ParentThreadID string          `json:"pthid,omitempty"`
	Body           any             `json:"-"`
	Context        *MessageContext `json:"-"`
	Attachments    []Attachment    `json:"-"` // DIDComm v2 attachments (spec §5)

	// Internal fields
	bodyRaw json.RawMessage // raw JSON body for lazy deserialization
	ackFn   func(id string) // set by client for manual ack
}

// MessageContext contains metadata from the cloud-node, present on inbound messages.
type MessageContext struct {
	Recipient         string       `json:"recipient"`
	Authorized        bool         `json:"authorized"`
	SenderCredentials []SenderCredential `json:"sender_credentials"`
}

// SenderCredential represents a sender credential from the cloud-node's message context.
type SenderCredential struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// UnmarshalBody decodes the message body into the provided struct.
func (m *Message) UnmarshalBody(v any) error {
	if m.bodyRaw == nil {
		return errors.New("message has no body")
	}
	return json.Unmarshal(m.bodyRaw, v)
}

// Ack acknowledges this message to the cloud-node.
// Only meaningful when the handler was registered with WithManualAck().
func (m *Message) Ack() {
	if m.ackFn != nil {
		m.ackFn(m.ID)
	}
}

// generateID returns a new unique message ID.
func generateID() string {
	return uuid.New().String()
}

// didcommEnvelope is the internal DIDComm wire format for marshaling outbound messages.
type didcommEnvelope struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	From           string          `json:"from"`
	To             []string        `json:"to"`
	ThreadID       string          `json:"thid,omitempty"`
	ParentThreadID string          `json:"pthid,omitempty"`
	Body           json.RawMessage `json:"body"`
	Attachments    []Attachment    `json:"attachments,omitempty"`
}

// marshalDIDComm serializes a Message into DIDComm JSON wire format.
func marshalDIDComm(msg *Message) ([]byte, error) {
	var bodyBytes json.RawMessage
	if msg.Body != nil {
		b, err := json.Marshal(msg.Body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyBytes = b
	} else if msg.bodyRaw != nil {
		bodyBytes = msg.bodyRaw
	} else {
		bodyBytes = json.RawMessage(`{}`)
	}

	env := didcommEnvelope{
		ID:       msg.ID,
		Type:     msg.Type,
		From:     msg.From,
		To:       msg.To,
		ThreadID: msg.ThreadID,
		ParentThreadID: msg.ParentThreadID,
		Body:     bodyBytes,
	}
	if len(msg.Attachments) > 0 {
		env.Attachments = msg.Attachments
	}
	return json.Marshal(env)
}

// inboundEnvelope is the wire format for messages received from the cloud-node.
// Messages arrive wrapped in context + plaintext.
type inboundEnvelope struct {
	Context   *inboundContext `json:"context"`
	Plaintext json.RawMessage `json:"plaintext"`
}

type inboundContext struct {
	Recipient         string              `json:"recipient"`
	Authorized        bool                `json:"authorized"`
	SenderCredentials []inboundCredential `json:"sender_credentials"`
}

type inboundCredential struct {
	CredentialSubject struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"credential_subject"`
}

// parseDIDComm parses an inbound cloud-node message (context + plaintext) into a Message.
func parseDIDComm(data json.RawMessage) (*Message, error) {
	var env inboundEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("parse envelope: %w", err)
	}

	var plaintext struct {
		ID          string          `json:"id"`
		Type        string          `json:"type"`
		From        string          `json:"from"`
		To          []string        `json:"to"`
		ThID        string          `json:"thid"`
		PThID       string          `json:"pthid"`
		Body        json.RawMessage `json:"body"`
		Attachments []Attachment    `json:"attachments"`
	}
	if err := json.Unmarshal(env.Plaintext, &plaintext); err != nil {
		return nil, fmt.Errorf("parse plaintext: %w", err)
	}

	msg := &Message{
		ID:             plaintext.ID,
		Type:           plaintext.Type,
		From:           plaintext.From,
		To:             plaintext.To,
		ThreadID:       plaintext.ThID,
		ParentThreadID: plaintext.PThID,
		bodyRaw:        plaintext.Body,
	}
	msg.Attachments = plaintext.Attachments

	if env.Context != nil {
		creds := make([]SenderCredential, len(env.Context.SenderCredentials))
		for i, c := range env.Context.SenderCredentials {
			creds[i] = SenderCredential{
				ID:   c.CredentialSubject.ID,
				Name: c.CredentialSubject.Name,
			}
		}
		msg.Context = &MessageContext{
			Recipient:         env.Context.Recipient,
			Authorized:        env.Context.Authorized,
			SenderCredentials: creds,
		}
	}

	return msg, nil
}