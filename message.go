package layr8

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

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

	// Internal fields
	bodyRaw json.RawMessage // raw JSON body for lazy deserialization
	ackFn   func(id string) // set by client for manual ack
}

// MessageContext contains metadata from the cloud-node, present on inbound messages.
type MessageContext struct {
	Recipient         string       `json:"recipient"`
	Authorized        bool         `json:"authorized"`
	SenderCredentials []Credential `json:"sender_credentials"`
}

// Credential represents a sender credential from the cloud-node.
type Credential struct {
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
		ID    string          `json:"id"`
		Type  string          `json:"type"`
		From  string          `json:"from"`
		To    []string        `json:"to"`
		ThID  string          `json:"thid"`
		PThID string          `json:"pthid"`
		Body  json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(env.Plaintext, &plaintext); err != nil {
		return nil, fmt.Errorf("parse plaintext: %w", err)
	}

	// Decode body into a generic map so Body is accessible without UnmarshalBody
	var bodyMap any
	if len(plaintext.Body) > 0 {
		_ = json.Unmarshal(plaintext.Body, &bodyMap)
	}

	msg := &Message{
		ID:             plaintext.ID,
		Type:           plaintext.Type,
		From:           plaintext.From,
		To:             plaintext.To,
		ThreadID:       plaintext.ThID,
		ParentThreadID: plaintext.PThID,
		Body:           bodyMap,
		bodyRaw:        plaintext.Body,
	}

	if env.Context != nil {
		creds := make([]Credential, len(env.Context.SenderCredentials))
		for i, c := range env.Context.SenderCredentials {
			creds[i] = Credential{
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
