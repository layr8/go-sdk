package layr8

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// Attachment represents a DIDComm v2 attachment (spec section 5).
type Attachment struct {
	ID          string          `json:"id,omitempty"`
	Description string          `json:"description,omitempty"`
	Filename    string          `json:"filename,omitempty"`
	MediaType   string          `json:"media_type,omitempty"`
	Format      string          `json:"format,omitempty"`
	LastmodTime *AttachmentTime `json:"lastmod_time,omitempty"`
	ByteCount   int64           `json:"byte_count,omitempty"`
	Data        AttachmentData  `json:"data"`
}

// AttachmentTime carries a DIDComm attachment's `lastmod_time`.
//
// DIDComm v2 defines the field, in the Attachments section, as "OPTIONAL. A
// hint about when the content in this attachment was last modified" — and
// states no type for it at all. The same spec pins `created_time` and
// `expires_time` to "UTC Epoch Seconds (seconds since 1970-01-01T00:00:00Z) as
// an integer", so the omission is visible rather than accidental. Both DIF
// reference implementations (didcomm-rust, didcomm-python) carry the field as
// an integer, and that is what this SDK emits; senders in the wild have also
// emitted an RFC 3339 string. Both are read here.
//
// A hint must never cost the reader the message. An authorization denial whose
// timestamp hint this SDK cannot decode is still a denial the caller has to
// see, so decoding never fails — it records that the value was not read.
//
// The three cases are three different values and must not be folded together:
//
//	field absent      the *AttachmentTime is nil
//	field read        Known is true; Seconds holds UTC epoch seconds
//	field not read    Known is false; Raw holds the JSON that arrived
//
// Reading Seconds without checking Known reports epoch 0 for a value nobody
// managed to read.
type AttachmentTime struct {
	// Seconds is UTC epoch seconds. Meaningful only when Known is true.
	Seconds int64
	// Known reports whether Seconds was actually read off the wire.
	Known bool
	// Raw is the value exactly as it arrived, retained whether or not it was
	// read. MarshalJSON re-emits it for a value that was NOT read, so relaying
	// a peer's message never rewrites a field nobody decoded; a value that WAS
	// read is re-emitted as epoch seconds.
	Raw json.RawMessage
}

// NewAttachmentTime returns a read AttachmentTime for t, truncated to seconds.
func NewAttachmentTime(t time.Time) *AttachmentTime {
	return &AttachmentTime{Seconds: t.Unix(), Known: true}
}

// Time returns the hint as a time.Time. The second return value is false when
// the value was not read, in which case the time.Time is the zero value and
// means nothing.
func (a *AttachmentTime) Time() (time.Time, bool) {
	if a == nil || !a.Known {
		return time.Time{}, false
	}
	return time.Unix(a.Seconds, 0).UTC(), true
}

// UnmarshalJSON decodes a `lastmod_time` value. It never returns an error: a
// value this SDK cannot read is recorded as unread rather than failing the
// enclosing message.
func (a *AttachmentTime) UnmarshalJSON(data []byte) error {
	a.Seconds = 0
	a.Known = false
	a.Raw = append(json.RawMessage(nil), data...)

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		a.Raw = nil
		return nil
	}

	var asNumber json.Number
	if err := json.Unmarshal(trimmed, &asNumber); err == nil {
		if secs, ok := secondsFromNumeric(string(asNumber)); ok {
			a.Seconds = secs
			a.Known = true
		}
		return nil
	}

	var asString string
	if err := json.Unmarshal(trimmed, &asString); err == nil {
		if secs, ok := secondsFromTimestampString(asString); ok {
			a.Seconds = secs
			a.Known = true
		}
		return nil
	}

	// An object, an array or a bool. Nothing to read; Raw keeps what came.
	return nil
}

// MarshalJSON emits epoch seconds for a value that was read, and otherwise
// re-emits exactly what arrived, so relaying a peer's message does not rewrite
// a field this SDK did not understand.
func (a AttachmentTime) MarshalJSON() ([]byte, error) {
	if a.Known {
		return []byte(strconv.FormatInt(a.Seconds, 10)), nil
	}
	if len(a.Raw) > 0 {
		return append(json.RawMessage(nil), a.Raw...), nil
	}
	return []byte("null"), nil
}

// secondsFromNumeric reads a JSON number as epoch seconds. A fractional value
// is truncated towards zero.
func secondsFromNumeric(s string) (int64, bool) {
	if secs, err := strconv.ParseInt(s, 10, 64); err == nil {
		return secs, true
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f), true
	}
	return 0, false
}

// secondsFromTimestampString reads the string forms seen on the wire: an RFC
// 3339 timestamp (what Elixir's DateTime.to_iso8601/1 produces, fractional
// seconds included) or epoch seconds spelled as digits.
func secondsFromTimestampString(s string) (int64, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix(), true
	}
	if secs, err := strconv.ParseInt(s, 10, 64); err == nil {
		return secs, true
	}
	return 0, false
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

	// AttachmentsUnread is non-nil when the message carried an `attachments`
	// header this SDK could not decode. Attachments is then nil for that
	// reason, not because the message carried none — reporting "no
	// attachments" for a header nobody could read states something that was
	// never measured. The message itself is still delivered: a denial must not
	// disappear because a hint travelling beside it was malformed.
	AttachmentsUnread error `json:"-"`

	// Internal fields
	bodyRaw json.RawMessage // raw JSON body for lazy deserialization
	ackFn   func(id string) // set by client for manual ack
}

// MessageContext contains metadata from the cloud-node, present on inbound messages.
type MessageContext struct {
	Recipient         string             `json:"recipient"`
	Authorized        bool               `json:"authorized"`
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
		ID:             msg.ID,
		Type:           msg.Type,
		From:           msg.From,
		To:             msg.To,
		ThreadID:       msg.ThreadID,
		ParentThreadID: msg.ParentThreadID,
		Body:           bodyBytes,
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
		Attachments json.RawMessage `json:"attachments"`
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
	// Attachments are decoded in a second pass, on purpose. Attachments are a
	// hint alongside the message, and a hint this SDK cannot read must not
	// take the message down with it: the symptom of the all-or-nothing decode
	// was an `e.m.authz.denied` problem report that never reached its caller,
	// because one attachment field had a type this SDK did not accept.
	if trimmed := bytes.TrimSpace(plaintext.Attachments); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		var attachments []Attachment
		if err := json.Unmarshal(trimmed, &attachments); err != nil {
			msg.AttachmentsUnread = fmt.Errorf("decode attachments: %w", err)
		} else {
			msg.Attachments = attachments
		}
	}

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
