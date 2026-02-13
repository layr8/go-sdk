package layr8

import (
	"encoding/json"
	"testing"
)

type testBody struct {
	Content string `json:"content"`
	Locale  string `json:"locale"`
}

func TestMessage_UnmarshalBody(t *testing.T) {
	msg := &Message{
		bodyRaw: json.RawMessage(`{"content":"hello","locale":"en"}`),
	}
	var body testBody
	if err := msg.UnmarshalBody(&body); err != nil {
		t.Fatalf("UnmarshalBody() error: %v", err)
	}
	if body.Content != "hello" {
		t.Errorf("Content = %q, want %q", body.Content, "hello")
	}
	if body.Locale != "en" {
		t.Errorf("Locale = %q, want %q", body.Locale, "en")
	}
}

func TestMessage_UnmarshalBody_NilBody(t *testing.T) {
	msg := &Message{}
	var body testBody
	if err := msg.UnmarshalBody(&body); err == nil {
		t.Fatal("UnmarshalBody() should error on nil body")
	}
}

func TestMarshalDIDComm(t *testing.T) {
	msg := &Message{
		ID:       "test-id",
		Type:     "https://layr8.io/protocols/echo/1.0/request",
		From:     "did:web:alice",
		To:       []string{"did:web:bob"},
		ThreadID: "thread-1",
		Body:     testBody{Content: "hello", Locale: "en"},
	}

	data, err := marshalDIDComm(msg)
	if err != nil {
		t.Fatalf("marshalDIDComm() error: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if raw["id"] != "test-id" {
		t.Errorf("id = %v, want %q", raw["id"], "test-id")
	}
	if raw["type"] != "https://layr8.io/protocols/echo/1.0/request" {
		t.Errorf("type = %v, want protocol URI", raw["type"])
	}
	if raw["from"] != "did:web:alice" {
		t.Errorf("from = %v, want %q", raw["from"], "did:web:alice")
	}
	if raw["thid"] != "thread-1" {
		t.Errorf("thid = %v, want %q", raw["thid"], "thread-1")
	}

	body, ok := raw["body"].(map[string]interface{})
	if !ok {
		t.Fatal("body should be a JSON object")
	}
	if body["content"] != "hello" {
		t.Errorf("body.content = %v, want %q", body["content"], "hello")
	}
}

func TestMarshalDIDComm_WithParentThread(t *testing.T) {
	msg := &Message{
		ID:             "test-id",
		Type:           "https://layr8.io/protocols/echo/1.0/request",
		From:           "did:web:alice",
		To:             []string{"did:web:bob"},
		ThreadID:       "thread-1",
		ParentThreadID: "parent-thread-1",
		Body:           testBody{Content: "hello"},
	}

	data, err := marshalDIDComm(msg)
	if err != nil {
		t.Fatalf("marshalDIDComm() error: %v", err)
	}

	var raw map[string]interface{}
	json.Unmarshal(data, &raw)

	if raw["pthid"] != "parent-thread-1" {
		t.Errorf("pthid = %v, want %q", raw["pthid"], "parent-thread-1")
	}
}

func TestParseDIDComm(t *testing.T) {
	payload := json.RawMessage(`{
		"context": {
			"recipient": "did:web:alice",
			"authorized": true,
			"sender_credentials": [
				{"credential_subject": {"id": "did:web:bob", "name": "Bob"}}
			]
		},
		"plaintext": {
			"id": "msg-1",
			"type": "https://didcomm.org/basicmessage/2.0/message",
			"from": "did:web:bob",
			"to": ["did:web:alice"],
			"thid": "thread-1",
			"pthid": "parent-1",
			"body": {"content": "hello", "locale": "en"}
		}
	}`)

	msg, err := parseDIDComm(payload)
	if err != nil {
		t.Fatalf("parseDIDComm() error: %v", err)
	}

	if msg.ID != "msg-1" {
		t.Errorf("ID = %q, want %q", msg.ID, "msg-1")
	}
	if msg.Type != "https://didcomm.org/basicmessage/2.0/message" {
		t.Errorf("Type = %q, want basicmessage type", msg.Type)
	}
	if msg.From != "did:web:bob" {
		t.Errorf("From = %q, want %q", msg.From, "did:web:bob")
	}
	if len(msg.To) != 1 || msg.To[0] != "did:web:alice" {
		t.Errorf("To = %v, want [did:web:alice]", msg.To)
	}
	if msg.ThreadID != "thread-1" {
		t.Errorf("ThreadID = %q, want %q", msg.ThreadID, "thread-1")
	}
	if msg.ParentThreadID != "parent-1" {
		t.Errorf("ParentThreadID = %q, want %q", msg.ParentThreadID, "parent-1")
	}
	if msg.Context == nil {
		t.Fatal("Context should not be nil")
	}
	if msg.Context.Recipient != "did:web:alice" {
		t.Errorf("Context.Recipient = %q, want %q", msg.Context.Recipient, "did:web:alice")
	}
	if !msg.Context.Authorized {
		t.Error("Context.Authorized should be true")
	}
	if len(msg.Context.SenderCredentials) != 1 {
		t.Fatalf("SenderCredentials len = %d, want 1", len(msg.Context.SenderCredentials))
	}
	if msg.Context.SenderCredentials[0].Name != "Bob" {
		t.Errorf("SenderCredentials[0].Name = %q, want %q", msg.Context.SenderCredentials[0].Name, "Bob")
	}

	// Verify body can be unmarshaled
	var body testBody
	if err := msg.UnmarshalBody(&body); err != nil {
		t.Fatalf("UnmarshalBody() error: %v", err)
	}
	if body.Content != "hello" {
		t.Errorf("body.Content = %q, want %q", body.Content, "hello")
	}
}

func TestMessage_Ack(t *testing.T) {
	acked := false
	msg := &Message{
		ID:    "msg-1",
		ackFn: func(id string) { acked = true },
	}
	msg.Ack()
	if !acked {
		t.Error("Ack() should call ackFn")
	}
}

func TestMessage_Ack_Noop_WhenNoFn(t *testing.T) {
	msg := &Message{ID: "msg-1"}
	msg.Ack() // should not panic
}
