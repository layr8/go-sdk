package layr8

import (
	"encoding/json"
	"fmt"
	"strings"
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

// --- Attachment tests ---

func TestMarshalAttachments(t *testing.T) {
	msg := &Message{
		ID:   "msg-1",
		Type: "https://layr8.io/protocols/test/1.0/msg",
		From: "did:web:alice",
		To:   []string{"did:web:bob"},
		Attachments: []Attachment{
			{
				ID:          "att-1",
				Description: "test file",
				Filename:    "test.txt",
				MediaType:   "text/plain",
				Format:      "base64",
				Data: AttachmentData{
					Base64: "aGVsbG8=",
				},
			},
		},
	}
	data, err := marshalDIDComm(msg)
	if err != nil {
		t.Fatalf("marshalDIDComm() error: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	attVal, ok := raw["attachments"]
	if !ok {
		t.Fatalf("attachments key missing")
	}
	attSlice, ok := attVal.([]interface{})
	if !ok || len(attSlice) != 1 {
		t.Fatalf("attachments should be slice of length 1, got %v", attVal)
	}
	attMap, ok := attSlice[0].(map[string]interface{})
	if !ok {
		t.Fatalf("attachment element not a map")
	}
	if attMap["id"] != "att-1" {
		t.Errorf("attachment id = %v, want %q", attMap["id"], "att-1")
	}
	if attMap["media_type"] != "text/plain" {
		t.Errorf("media_type = %v, want %q", attMap["media_type"], "text/plain")
	}
	dataMap, ok := attMap["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("attachment data not a map")
	}
	if dataMap["base64"] != "aGVsbG8=" {
		t.Errorf("data.base64 = %v, want %q", dataMap["base64"], "aGVsbG8=")
	}
}

func TestMarshalNoAttachments(t *testing.T) {
	msg := &Message{
		ID:   "msg-2",
		Type: "https://layr8.io/protocols/test/1.0/msg",
		From: "did:web:alice",
		To:   []string{"did:web:bob"},
	}
	data, err := marshalDIDComm(msg)
	if err != nil {
		t.Fatalf("marshalDIDComm() error: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := raw["attachments"]; ok {
		t.Errorf("attachments key should not be present when empty")
	}
}

func TestParseAttachments(t *testing.T) {
	payload := json.RawMessage(`{
		"context": {
			"recipient": "did:web:bob",
			"authorized": true,
			"sender_credentials": []
		},
		"plaintext": {
			"id": "msg-3",
			"type": "https://layr8.io/protocols/test/1.0/msg",
			"from": "did:web:alice",
			"to": ["did:web:bob"],
			"attachments": [
				{
					"id": "att-2",
					"description": "a doc",
					"filename": "doc.pdf",
					"media_type": "application/pdf",
					"format": "base64",
					"byte_count": 12345,
					"data": {"base64":"AQID"}
				}
			],
			"body": {}
		}
	}`)
	msg, err := parseDIDComm(payload)
	if err != nil {
		t.Fatalf("parseDIDComm() error: %v", err)
	}
	if len(msg.Attachments) != 1 {
		t.Fatalf("attachments length = %d, want 1", len(msg.Attachments))
	}
	att := msg.Attachments[0]
	if att.ID != "att-2" {
		t.Errorf("ID = %q, want %q", att.ID, "att-2")
	}
	if att.Description != "a doc" {
		t.Errorf("Description = %q, want %q", att.Description, "a doc")
	}
	if att.Filename != "doc.pdf" {
		t.Errorf("Filename = %q, want %q", att.Filename, "doc.pdf")
	}
	if att.MediaType != "application/pdf" {
		t.Errorf("MediaType = %q, want %q", att.MediaType, "application/pdf")
	}
	if att.Format != "base64" {
		t.Errorf("Format = %q, want %q", att.Format, "base64")
	}
	if att.ByteCount != 12345 {
		t.Errorf("ByteCount = %d, want %d", att.ByteCount, 12345)
	}
	if att.Data.Base64 != "AQID" {
		t.Errorf("Data.Base64 = %q, want %q", att.Data.Base64, "AQID")
	}
}

func TestAttachmentRoundTrip(t *testing.T) {
	msg := &Message{
		ID:   "msg-4",
		Type: "https://layr8.io/protocols/test/1.0/msg",
		From: "did:web:alice",
		To:   []string{"did:web:bob"},
		Attachments: []Attachment{
			{
				ID:        "rt-1",
				MediaType: "application/json",
				Data: AttachmentData{
					JSON: map[string]any{"key": "value"},
				},
			},
			{
				ID:        "rt-2",
				MediaType: "text/plain",
				Data: AttachmentData{
					Base64: "dGVzdA==",
				},
			},
		},
	}
	marshaled, err := marshalDIDComm(msg)
	if err != nil {
		t.Fatalf("marshalDIDComm() error: %v", err)
	}
	envelope := fmt.Sprintf(`{"context":{"recipient":"did:web:bob","authorized":true,"sender_credentials":[]},"plaintext":%s}`, string(marshaled))
	parsed, err := parseDIDComm(json.RawMessage(envelope))
	if err != nil {
		t.Fatalf("parseDIDComm() error: %v", err)
	}
	if len(parsed.Attachments) != 2 {
		t.Fatalf("attachments length = %d, want 2", len(parsed.Attachments))
	}
	if parsed.Attachments[0].ID != "rt-1" {
		t.Errorf("first attachment ID = %q, want %q", parsed.Attachments[0].ID, "rt-1")
	}
	if parsed.Attachments[1].ID != "rt-2" {
		t.Errorf("second attachment ID = %q, want %q", parsed.Attachments[1].ID, "rt-2")
	}
	if parsed.Attachments[0].Data.JSON == nil {
		t.Fatalf("first attachment JSON data is nil")
	}
	b, err := json.Marshal(parsed.Attachments[0].Data.JSON)
	if err != nil {
		t.Fatalf("marshal attachment JSON data: %v", err)
	}
	if !strings.Contains(string(b), `"key"`) || !strings.Contains(string(b), `"value"`) {
		t.Errorf("attachment JSON does not contain expected key/value, got %s", string(b))
	}
}
