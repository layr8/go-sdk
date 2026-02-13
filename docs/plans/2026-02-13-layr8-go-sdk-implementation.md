# Layr8 Go SDK — v1 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a Go SDK that abstracts Phoenix Channel/WebSocket and DIDComm messaging, exposing `Send` (fire-and-forget), `Request` (request/response), and `Handle` (inbound) as the public API.

**Architecture:** A single `layr8` package with 5 public types (`Client`, `Config`, `Message`, `MessageContext`, `ProblemReportError`) and internal transport code isolated in `channel.go` behind a `transport` interface for testability and future QUIC support. The `Client` manages a single WebSocket connection, multiplexes messages via thread-ID correlation, and routes inbound messages to registered handlers.

**Tech Stack:** Go 1.22+, `github.com/gorilla/websocket`, `github.com/google/uuid`, standard library for everything else.

**Design document:** `docs/plans/2026-02-13-go-sdk-design.md`

**Reference implementations:**
- Postgres agent: `/Users/kaijiezhan/Developments/layr8-demos/` (search for `postgres-agent`)
- HTTP agent: `/Users/kaijiezhan/Developments/layr8-demos/` (search for `http-agent`)
- Go Chat: `/Users/kaijiezhan/Developments/plugin-examples/go_chat/internal/chat/websocket.go`

---

## Task 1: Project Setup

**Files:**
- Create: `go.mod`
- Create: `doc.go` (package documentation)

**Step 1: Initialize Go module**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go mod init github.com/layr8/go-sdk
```

**Step 2: Create package doc file**

Create `doc.go`:
```go
// Package layr8 provides a Go SDK for building DIDComm agents on the Layr8 platform.
//
// The SDK abstracts Phoenix Channel/WebSocket transport and DIDComm message
// formatting, exposing three core operations:
//
//   - Send: fire-and-forget messaging
//   - Request: request/response with automatic thread correlation
//   - Handle: register handlers for inbound message types
//
// Basic usage:
//
//	client, err := layr8.NewClient(layr8.Config{
//	    NodeURL:  "ws://localhost:4000/plugin_socket/websocket",
//	    AgentDID: "did:web:mycompany:my-agent",
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	client.Handle("https://layr8.io/protocols/echo/1.0/request",
//	    func(msg *layr8.Message) (*layr8.Message, error) {
//	        return &layr8.Message{
//	            Type: "https://layr8.io/protocols/echo/1.0/response",
//	            Body: map[string]string{"echo": "hello"},
//	        }, nil
//	    },
//	)
//
//	if err := client.Connect(ctx); err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Close()
package layr8
```

**Step 3: Add dependencies**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go get github.com/gorilla/websocket
go get github.com/google/uuid
```

**Step 4: Verify module compiles**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go build ./...
```
Expected: success (no errors)

**Step 5: Initialize git and commit**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
git init
git add go.mod go.sum doc.go
git commit -m "feat: initialize go-sdk module"
```

---

## Task 2: Error Types

**Files:**
- Create: `errors.go`
- Create: `errors_test.go`

**Step 1: Write failing tests**

Create `errors_test.go`:
```go
package layr8

import (
	"errors"
	"testing"
)

func TestProblemReportError_Error(t *testing.T) {
	err := &ProblemReportError{
		Code:    "e.p.xfer.cant-process",
		Comment: "database unavailable",
	}
	want := "problem report [e.p.xfer.cant-process]: database unavailable"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestProblemReportError_ErrorsAs(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &ProblemReportError{
		Code:    "e.p.xfer.cant-process",
		Comment: "not found",
	})
	var probErr *ProblemReportError
	if !errors.As(err, &probErr) {
		t.Fatal("errors.As should match ProblemReportError")
	}
	if probErr.Code != "e.p.xfer.cant-process" {
		t.Errorf("Code = %q, want %q", probErr.Code, "e.p.xfer.cant-process")
	}
}

func TestConnectionError_Error(t *testing.T) {
	err := &ConnectionError{
		URL:    "ws://localhost:4000",
		Reason: "connection refused",
	}
	want := "connection error [ws://localhost:4000]: connection refused"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestConnectionError_ErrorsAs(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &ConnectionError{
		URL:    "ws://localhost:4000",
		Reason: "auth failed",
	})
	var connErr *ConnectionError
	if !errors.As(err, &connErr) {
		t.Fatal("errors.As should match ConnectionError")
	}
	if connErr.Reason != "auth failed" {
		t.Errorf("Reason = %q, want %q", connErr.Reason, "auth failed")
	}
}

func TestSentinelErrors(t *testing.T) {
	if !errors.Is(ErrNotConnected, ErrNotConnected) {
		t.Error("ErrNotConnected should match itself")
	}
	if !errors.Is(ErrAlreadyConnected, ErrAlreadyConnected) {
		t.Error("ErrAlreadyConnected should match itself")
	}
	if !errors.Is(ErrClientClosed, ErrClientClosed) {
		t.Error("ErrClientClosed should match itself")
	}
}
```

**Step 2: Run tests to verify they fail**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v -run TestProblemReport
```
Expected: FAIL — types not defined

**Step 3: Implement error types**

Create `errors.go`:
```go
package layr8

import (
	"errors"
	"fmt"
)

// Sentinel errors for client state.
var (
	ErrNotConnected    = errors.New("client is not connected")
	ErrAlreadyConnected = errors.New("client is already connected")
	ErrClientClosed    = errors.New("client is closed")
)

// ProblemReportError represents a DIDComm problem report received from a remote agent.
// See: https://identity.foundation/didcomm-messaging/spec/#problem-reports
type ProblemReportError struct {
	Code    string `json:"code"`
	Comment string `json:"comment"`
}

func (e *ProblemReportError) Error() string {
	return fmt.Sprintf("problem report [%s]: %s", e.Code, e.Comment)
}

// ConnectionError represents a failure to connect or maintain connection to the cloud-node.
type ConnectionError struct {
	URL    string
	Reason string
}

func (e *ConnectionError) Error() string {
	return fmt.Sprintf("connection error [%s]: %s", e.URL, e.Reason)
}
```

**Step 4: Run tests to verify they pass**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v
```
Expected: PASS

**Step 5: Commit**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
git add errors.go errors_test.go
git commit -m "feat: add error types — ProblemReportError, ConnectionError, sentinels"
```

---

## Task 3: Message Types & Marshaling

**Files:**
- Create: `message.go`
- Create: `message_test.go`

**Step 1: Write failing tests**

Create `message_test.go`:
```go
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
```

**Step 2: Run tests to verify they fail**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v -run TestMessage
```
Expected: FAIL — types not defined

**Step 3: Implement message types**

Create `message.go`:
```go
package layr8

import (
	"encoding/json"
	"errors"
	"fmt"
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

// didcommEnvelope is the internal DIDComm wire format for marshaling outbound messages.
type didcommEnvelope struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	From     string          `json:"from"`
	To       []string        `json:"to"`
	ThreadID string          `json:"thid,omitempty"`
	PThID    string          `json:"pthid,omitempty"`
	Body     json.RawMessage `json:"body"`
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
		PThID:    msg.ParentThreadID,
		Body:     bodyBytes,
	}
	return json.Marshal(env)
}

// inboundEnvelope is the wire format for messages received from the cloud-node.
// Messages arrive wrapped in context + plaintext.
type inboundEnvelope struct {
	Context *inboundContext  `json:"context"`
	Plaintext json.RawMessage `json:"plaintext"`
}

type inboundContext struct {
	Recipient         string             `json:"recipient"`
	Authorized        bool               `json:"authorized"`
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

	msg := &Message{
		ID:             plaintext.ID,
		Type:           plaintext.Type,
		From:           plaintext.From,
		To:             plaintext.To,
		ThreadID:       plaintext.ThID,
		ParentThreadID: plaintext.PThID,
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
```

**Step 4: Run tests to verify they pass**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v
```
Expected: ALL PASS

**Step 5: Commit**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
git add message.go message_test.go
git commit -m "feat: add Message struct with DIDComm marshal/unmarshal and MessageContext"
```

---

## Task 4: Config & Validation

**Files:**
- Create: `config.go`
- Create: `config_test.go`

**Step 1: Write failing tests**

Create `config_test.go`:
```go
package layr8

import (
	"os"
	"testing"
)

func TestResolveConfig_ExplicitValues(t *testing.T) {
	cfg := Config{
		NodeURL:  "ws://localhost:4000/plugin_socket/websocket",
		APIKey:   "test-key",
		AgentDID: "did:web:test",
	}
	resolved, err := resolveConfig(cfg)
	if err != nil {
		t.Fatalf("resolveConfig() error: %v", err)
	}
	if resolved.NodeURL != "ws://localhost:4000/plugin_socket/websocket" {
		t.Errorf("NodeURL = %q, want explicit value", resolved.NodeURL)
	}
	if resolved.APIKey != "test-key" {
		t.Errorf("APIKey = %q, want %q", resolved.APIKey, "test-key")
	}
	if resolved.AgentDID != "did:web:test" {
		t.Errorf("AgentDID = %q, want %q", resolved.AgentDID, "did:web:test")
	}
}

func TestResolveConfig_EnvFallback(t *testing.T) {
	os.Setenv("LAYR8_NODE_URL", "ws://env-host:4000")
	os.Setenv("LAYR8_API_KEY", "env-key")
	os.Setenv("LAYR8_AGENT_DID", "did:web:env-agent")
	defer func() {
		os.Unsetenv("LAYR8_NODE_URL")
		os.Unsetenv("LAYR8_API_KEY")
		os.Unsetenv("LAYR8_AGENT_DID")
	}()

	resolved, err := resolveConfig(Config{})
	if err != nil {
		t.Fatalf("resolveConfig() error: %v", err)
	}
	if resolved.NodeURL != "ws://env-host:4000" {
		t.Errorf("NodeURL = %q, want env value", resolved.NodeURL)
	}
	if resolved.APIKey != "env-key" {
		t.Errorf("APIKey = %q, want env value", resolved.APIKey)
	}
	if resolved.AgentDID != "did:web:env-agent" {
		t.Errorf("AgentDID = %q, want env value", resolved.AgentDID)
	}
}

func TestResolveConfig_ExplicitOverridesEnv(t *testing.T) {
	os.Setenv("LAYR8_API_KEY", "env-key")
	defer os.Unsetenv("LAYR8_API_KEY")

	resolved, err := resolveConfig(Config{
		NodeURL: "ws://localhost:4000",
		APIKey:  "explicit-key",
	})
	if err != nil {
		t.Fatalf("resolveConfig() error: %v", err)
	}
	if resolved.APIKey != "explicit-key" {
		t.Errorf("APIKey = %q, want explicit value over env", resolved.APIKey)
	}
}

func TestResolveConfig_MissingNodeURL(t *testing.T) {
	_, err := resolveConfig(Config{APIKey: "key"})
	if err == nil {
		t.Fatal("resolveConfig() should error when NodeURL is missing")
	}
}

func TestResolveConfig_MissingAPIKey(t *testing.T) {
	_, err := resolveConfig(Config{NodeURL: "ws://localhost:4000"})
	if err == nil {
		t.Fatal("resolveConfig() should error when APIKey is missing")
	}
}

func TestResolveConfig_EmptyAgentDID_IsAllowed(t *testing.T) {
	cfg := Config{
		NodeURL: "ws://localhost:4000",
		APIKey:  "key",
	}
	resolved, err := resolveConfig(cfg)
	if err != nil {
		t.Fatalf("resolveConfig() should allow empty AgentDID: %v", err)
	}
	if resolved.AgentDID != "" {
		t.Errorf("AgentDID should remain empty, got %q", resolved.AgentDID)
	}
}
```

**Step 2: Run tests to verify they fail**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v -run TestResolveConfig
```
Expected: FAIL

**Step 3: Implement config**

Create `config.go`:
```go
package layr8

import (
	"fmt"
	"os"
)

// Config holds the configuration for a Layr8 client.
type Config struct {
	// NodeURL is the WebSocket URL of the Layr8 cloud-node.
	// Fallback: LAYR8_NODE_URL environment variable.
	NodeURL string

	// APIKey is the authentication key for the cloud-node.
	// Fallback: LAYR8_API_KEY environment variable.
	APIKey string

	// AgentDID is the DID identity of this agent.
	// If empty, an ephemeral DID is created on Connect().
	// Fallback: LAYR8_AGENT_DID environment variable.
	AgentDID string
}

// resolveConfig fills empty fields from environment variables and validates required fields.
func resolveConfig(cfg Config) (Config, error) {
	if cfg.NodeURL == "" {
		cfg.NodeURL = os.Getenv("LAYR8_NODE_URL")
	}
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("LAYR8_API_KEY")
	}
	if cfg.AgentDID == "" {
		cfg.AgentDID = os.Getenv("LAYR8_AGENT_DID")
	}

	if cfg.NodeURL == "" {
		return cfg, fmt.Errorf("NodeURL is required (set in Config or LAYR8_NODE_URL env)")
	}
	if cfg.APIKey == "" {
		return cfg, fmt.Errorf("APIKey is required (set in Config or LAYR8_API_KEY env)")
	}

	return cfg, nil
}
```

**Step 4: Run tests to verify they pass**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v
```
Expected: ALL PASS

**Step 5: Commit**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
git add config.go config_test.go
git commit -m "feat: add Config struct with env-var fallback and validation"
```

---

## Task 5: Handler Options

**Files:**
- Create: `options.go`
- Create: `options_test.go`

**Step 1: Write failing tests**

Create `options_test.go`:
```go
package layr8

import "testing"

func TestWithManualAck(t *testing.T) {
	opts := handlerDefaults()
	WithManualAck()(&opts)
	if !opts.manualAck {
		t.Error("WithManualAck should set manualAck to true")
	}
}

func TestHandlerDefaults(t *testing.T) {
	opts := handlerDefaults()
	if opts.manualAck {
		t.Error("default manualAck should be false (auto-ack)")
	}
}

func TestWithParentThread(t *testing.T) {
	opts := requestDefaults()
	WithParentThread("parent-123")(&opts)
	if opts.parentThreadID != "parent-123" {
		t.Errorf("parentThreadID = %q, want %q", opts.parentThreadID, "parent-123")
	}
}

func TestRequestDefaults(t *testing.T) {
	opts := requestDefaults()
	if opts.parentThreadID != "" {
		t.Error("default parentThreadID should be empty")
	}
}
```

**Step 2: Run tests to verify they fail**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v -run "TestWith|TestHandler|TestRequest"
```
Expected: FAIL

**Step 3: Implement options**

Create `options.go`:
```go
package layr8

// HandlerOption configures handler behavior.
type HandlerOption func(*handlerOptions)

type handlerOptions struct {
	manualAck bool
}

func handlerDefaults() handlerOptions {
	return handlerOptions{
		manualAck: false,
	}
}

// WithManualAck disables auto-acknowledgment for a handler.
// The handler must call msg.Ack() explicitly after successful processing.
func WithManualAck() HandlerOption {
	return func(o *handlerOptions) {
		o.manualAck = true
	}
}

// RequestOption configures request behavior.
type RequestOption func(*requestOptions)

type requestOptions struct {
	parentThreadID string
}

func requestDefaults() requestOptions {
	return requestOptions{}
}

// WithParentThread sets the parent thread ID (pthid) for nested thread correlation.
func WithParentThread(pthid string) RequestOption {
	return func(o *requestOptions) {
		o.parentThreadID = pthid
	}
}
```

**Step 4: Run tests to verify they pass**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v
```
Expected: ALL PASS

**Step 5: Commit**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
git add options.go options_test.go
git commit -m "feat: add handler and request options — WithManualAck, WithParentThread"
```

---

## Task 6: Handler Registry & Protocol Derivation

**Files:**
- Create: `handler.go`
- Create: `handler_test.go`

**Step 1: Write failing tests**

Create `handler_test.go`:
```go
package layr8

import (
	"testing"
)

func TestHandlerRegistry_Register(t *testing.T) {
	r := newHandlerRegistry()
	handler := func(msg *Message) (*Message, error) { return nil, nil }

	err := r.register("https://layr8.io/protocols/echo/1.0/request", handler)
	if err != nil {
		t.Fatalf("register() error: %v", err)
	}

	entry, ok := r.lookup("https://layr8.io/protocols/echo/1.0/request")
	if !ok {
		t.Fatal("lookup() should find registered handler")
	}
	if entry.fn == nil {
		t.Error("handler function should not be nil")
	}
}

func TestHandlerRegistry_RegisterWithManualAck(t *testing.T) {
	r := newHandlerRegistry()
	handler := func(msg *Message) (*Message, error) { return nil, nil }

	r.register("https://layr8.io/protocols/echo/1.0/request", handler, WithManualAck())

	entry, _ := r.lookup("https://layr8.io/protocols/echo/1.0/request")
	if !entry.manualAck {
		t.Error("handler should have manualAck=true")
	}
}

func TestHandlerRegistry_DuplicateRegistration(t *testing.T) {
	r := newHandlerRegistry()
	handler := func(msg *Message) (*Message, error) { return nil, nil }

	r.register("https://layr8.io/protocols/echo/1.0/request", handler)
	err := r.register("https://layr8.io/protocols/echo/1.0/request", handler)
	if err == nil {
		t.Fatal("register() should error on duplicate message type")
	}
}

func TestHandlerRegistry_LookupMissing(t *testing.T) {
	r := newHandlerRegistry()
	_, ok := r.lookup("https://layr8.io/protocols/echo/1.0/unknown")
	if ok {
		t.Error("lookup() should return false for unregistered type")
	}
}

func TestHandlerRegistry_DeriveProtocols(t *testing.T) {
	r := newHandlerRegistry()
	handler := func(msg *Message) (*Message, error) { return nil, nil }

	r.register("https://layr8.io/protocols/echo/1.0/request", handler)
	r.register("https://layr8.io/protocols/echo/1.0/response", handler)
	r.register("https://layr8.io/protocols/postgres/1.0/query", handler)

	protocols := r.protocols()

	// Should deduplicate: echo/1.0 appears once, postgres/1.0 once
	if len(protocols) != 2 {
		t.Fatalf("protocols() len = %d, want 2, got %v", len(protocols), protocols)
	}

	has := func(p string) bool {
		for _, proto := range protocols {
			if proto == p {
				return true
			}
		}
		return false
	}

	if !has("https://layr8.io/protocols/echo/1.0") {
		t.Error("protocols should include echo/1.0")
	}
	if !has("https://layr8.io/protocols/postgres/1.0") {
		t.Error("protocols should include postgres/1.0")
	}
}

func TestHandlerRegistry_DeriveProtocols_DIDComm(t *testing.T) {
	r := newHandlerRegistry()
	handler := func(msg *Message) (*Message, error) { return nil, nil }

	r.register("https://didcomm.org/basicmessage/2.0/message", handler)
	r.register("https://didcomm.org/report-problem/2.0/problem-report", handler)

	protocols := r.protocols()
	if len(protocols) != 2 {
		t.Fatalf("protocols() len = %d, want 2, got %v", len(protocols), protocols)
	}
}

func TestDeriveProtocol(t *testing.T) {
	tests := []struct {
		msgType string
		want    string
	}{
		{
			"https://layr8.io/protocols/echo/1.0/request",
			"https://layr8.io/protocols/echo/1.0",
		},
		{
			"https://layr8.io/protocols/postgres/1.0/query",
			"https://layr8.io/protocols/postgres/1.0",
		},
		{
			"https://didcomm.org/basicmessage/2.0/message",
			"https://didcomm.org/basicmessage/2.0",
		},
	}

	for _, tt := range tests {
		got := deriveProtocol(tt.msgType)
		if got != tt.want {
			t.Errorf("deriveProtocol(%q) = %q, want %q", tt.msgType, got, tt.want)
		}
	}
}
```

**Step 2: Run tests to verify they fail**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v -run "TestHandler|TestDerive"
```
Expected: FAIL

**Step 3: Implement handler registry**

Create `handler.go`:
```go
package layr8

import (
	"fmt"
	"strings"
	"sync"
)

// HandlerFunc is the signature for message handlers.
// Return a response Message to reply, an error to send a problem report,
// or (nil, nil) for fire-and-forget inbound messages.
type HandlerFunc func(msg *Message) (*Message, error)

type handlerEntry struct {
	fn        HandlerFunc
	manualAck bool
}

type handlerRegistry struct {
	mu       sync.RWMutex
	handlers map[string]handlerEntry // message type → handler
}

func newHandlerRegistry() *handlerRegistry {
	return &handlerRegistry{
		handlers: make(map[string]handlerEntry),
	}
}

func (r *handlerRegistry) register(msgType string, fn HandlerFunc, opts ...HandlerOption) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.handlers[msgType]; exists {
		return fmt.Errorf("handler already registered for message type %q", msgType)
	}

	o := handlerDefaults()
	for _, opt := range opts {
		opt(&o)
	}

	r.handlers[msgType] = handlerEntry{
		fn:        fn,
		manualAck: o.manualAck,
	}
	return nil
}

func (r *handlerRegistry) lookup(msgType string) (handlerEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.handlers[msgType]
	return entry, ok
}

// protocols returns the unique protocol base URIs derived from registered handler message types.
// For example, "https://layr8.io/protocols/echo/1.0/request" derives "https://layr8.io/protocols/echo/1.0".
func (r *handlerRegistry) protocols() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := make(map[string]struct{})
	var protocols []string

	for msgType := range r.handlers {
		proto := deriveProtocol(msgType)
		if _, ok := seen[proto]; !ok {
			seen[proto] = struct{}{}
			protocols = append(protocols, proto)
		}
	}
	return protocols
}

// deriveProtocol extracts the protocol base URI by removing the last path segment.
// "https://layr8.io/protocols/echo/1.0/request" → "https://layr8.io/protocols/echo/1.0"
func deriveProtocol(msgType string) string {
	idx := strings.LastIndex(msgType, "/")
	if idx == -1 {
		return msgType
	}
	return msgType[:idx]
}
```

**Step 4: Run tests to verify they pass**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v
```
Expected: ALL PASS

**Step 5: Commit**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
git add handler.go handler_test.go
git commit -m "feat: add handler registry with protocol auto-derivation"
```

---

## Task 7: Internal Transport Interface

**Files:**
- Create: `transport.go`

This task has no tests — it's a pure interface definition that will be tested through its implementations (Task 8 and Task 10).

**Step 1: Create the transport interface**

Create `transport.go`:
```go
package layr8

import "context"

// transport is the internal interface for communication with the cloud-node.
// The current implementation uses WebSocket/Phoenix Channel (channel.go).
// Future implementations may use QUIC (quic.go).
type transport interface {
	// connect establishes the connection and joins the channel with the given protocols.
	connect(ctx context.Context, protocols []string) error

	// send writes a raw Phoenix Channel message to the connection.
	send(event string, payload []byte) error

	// sendAck acknowledges message IDs to the cloud-node.
	sendAck(ids []string) error

	// setMessageHandler registers the callback for inbound "message" events.
	// The callback receives the raw payload bytes.
	setMessageHandler(fn func(payload []byte))

	// close gracefully shuts down the connection.
	close() error

	// onDisconnect registers a callback for when the connection drops.
	onDisconnect(fn func(error))

	// onReconnect registers a callback for when the connection is restored.
	onReconnect(fn func())

	// assignedDID returns the DID assigned by the cloud-node on join (for ephemeral DIDs).
	assignedDID() string
}
```

**Step 2: Verify module still compiles**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go build ./...
```
Expected: success

**Step 3: Commit**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
git add transport.go
git commit -m "feat: add internal transport interface for WebSocket/QUIC abstraction"
```

---

## Task 8: Phoenix Channel Transport

**Files:**
- Create: `channel.go`
- Create: `channel_test.go`

This is the largest task. The channel implements the Phoenix Channel protocol over WebSocket.

**Step 1: Write failing tests**

Create `channel_test.go`:
```go
package layr8

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// mockPhoenixServer simulates a Layr8 cloud-node for testing.
type mockPhoenixServer struct {
	upgrader websocket.Upgrader
	mu       sync.Mutex
	received []phoenixMessage
	conn     *websocket.Conn
	onMsg    func(phoenixMessage)
}

func newMockServer() *mockPhoenixServer {
	return &mockPhoenixServer{
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
	}
}

func (s *mockPhoenixServer) handler(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var msg phoenixMessage
		json.Unmarshal(data, &msg)

		s.mu.Lock()
		s.received = append(s.received, msg)
		handler := s.onMsg
		s.mu.Unlock()

		if handler != nil {
			handler(msg)
		}
	}
}

func (s *mockPhoenixServer) sendToClient(msg phoenixMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		data, _ := json.Marshal(msg)
		s.conn.WriteMessage(websocket.TextMessage, data)
	}
}

func (s *mockPhoenixServer) getReceived() []phoenixMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]phoenixMessage, len(s.received))
	copy(cp, s.received)
	return cp
}

func TestPhoenixChannel_Connect_JoinReply(t *testing.T) {
	mock := newMockServer()
	mock.onMsg = func(msg phoenixMessage) {
		if msg.Event == "phx_join" {
			mock.sendToClient(phoenixMessage{
				JoinRef: msg.Ref,
				Ref:     msg.Ref,
				Topic:   msg.Topic,
				Event:   "phx_reply",
				Payload: json.RawMessage(`{"status":"ok","response":{"did":"did:web:node:test-123"}}`),
			})
		}
	}

	server := httptest.NewServer(http.HandlerFunc(mock.handler))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/plugin_socket/websocket"
	ch := newPhoenixChannel(wsURL, "test-key", "did:web:test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := ch.connect(ctx, []string{"https://layr8.io/protocols/echo/1.0"})
	if err != nil {
		t.Fatalf("connect() error: %v", err)
	}
	defer ch.close()

	// Verify join was sent with payload_types
	received := mock.getReceived()
	if len(received) == 0 {
		t.Fatal("server should have received at least one message")
	}

	joinMsg := received[0]
	if joinMsg.Event != "phx_join" {
		t.Errorf("first message event = %q, want %q", joinMsg.Event, "phx_join")
	}

	var payload map[string]interface{}
	json.Unmarshal(joinMsg.Payload, &payload)
	types, ok := payload["payload_types"]
	if !ok {
		t.Fatal("join payload should contain payload_types")
	}
	arr := types.([]interface{})
	if len(arr) != 1 || arr[0] != "https://layr8.io/protocols/echo/1.0" {
		t.Errorf("payload_types = %v, want [echo/1.0]", types)
	}
}

func TestPhoenixChannel_Send(t *testing.T) {
	mock := newMockServer()
	mock.onMsg = func(msg phoenixMessage) {
		if msg.Event == "phx_join" {
			mock.sendToClient(phoenixMessage{
				JoinRef: msg.Ref,
				Ref:     msg.Ref,
				Topic:   msg.Topic,
				Event:   "phx_reply",
				Payload: json.RawMessage(`{"status":"ok","response":{}}`),
			})
		}
	}

	server := httptest.NewServer(http.HandlerFunc(mock.handler))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/plugin_socket/websocket"
	ch := newPhoenixChannel(wsURL, "test-key", "did:web:test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch.connect(ctx, []string{})
	defer ch.close()

	payload := []byte(`{"id":"msg-1","type":"test","from":"did:web:test","to":["did:web:bob"],"body":{}}`)
	err := ch.send("message", payload)
	if err != nil {
		t.Fatalf("send() error: %v", err)
	}

	// Wait for server to receive
	time.Sleep(100 * time.Millisecond)

	received := mock.getReceived()
	// Should have join + message
	var found bool
	for _, msg := range received {
		if msg.Event == "message" {
			found = true
			break
		}
	}
	if !found {
		t.Error("server should have received a message event")
	}
}

func TestPhoenixChannel_ReceiveMessage(t *testing.T) {
	mock := newMockServer()
	mock.onMsg = func(msg phoenixMessage) {
		if msg.Event == "phx_join" {
			mock.sendToClient(phoenixMessage{
				JoinRef: msg.Ref,
				Ref:     msg.Ref,
				Topic:   msg.Topic,
				Event:   "phx_reply",
				Payload: json.RawMessage(`{"status":"ok","response":{}}`),
			})
		}
	}

	server := httptest.NewServer(http.HandlerFunc(mock.handler))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/plugin_socket/websocket"
	ch := newPhoenixChannel(wsURL, "test-key", "did:web:test")

	var receivedPayload []byte
	done := make(chan struct{})
	ch.setMessageHandler(func(payload []byte) {
		receivedPayload = payload
		close(done)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch.connect(ctx, []string{})
	defer ch.close()

	// Server sends a message to client
	mock.sendToClient(phoenixMessage{
		Topic:   "plugin:lobby",
		Event:   "message",
		Payload: json.RawMessage(`{"plaintext":{"id":"msg-1","type":"test","body":{}}}`),
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for message")
	}

	if receivedPayload == nil {
		t.Fatal("should have received payload")
	}
}

func TestPhoenixChannel_SendAck(t *testing.T) {
	mock := newMockServer()
	mock.onMsg = func(msg phoenixMessage) {
		if msg.Event == "phx_join" {
			mock.sendToClient(phoenixMessage{
				JoinRef: msg.Ref,
				Ref:     msg.Ref,
				Topic:   msg.Topic,
				Event:   "phx_reply",
				Payload: json.RawMessage(`{"status":"ok","response":{}}`),
			})
		}
	}

	server := httptest.NewServer(http.HandlerFunc(mock.handler))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/plugin_socket/websocket"
	ch := newPhoenixChannel(wsURL, "test-key", "did:web:test")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch.connect(ctx, []string{})
	defer ch.close()

	err := ch.sendAck([]string{"msg-1", "msg-2"})
	if err != nil {
		t.Fatalf("sendAck() error: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	received := mock.getReceived()
	var found bool
	for _, msg := range received {
		if msg.Event == "ack" {
			found = true
			var payload map[string]interface{}
			json.Unmarshal(msg.Payload, &payload)
			ids, ok := payload["ids"]
			if !ok {
				t.Error("ack payload should contain ids")
			}
			arr := ids.([]interface{})
			if len(arr) != 2 {
				t.Errorf("ack ids len = %d, want 2", len(arr))
			}
			break
		}
	}
	if !found {
		t.Error("server should have received an ack event")
	}
}

func TestPhoenixChannel_AssignedDID(t *testing.T) {
	mock := newMockServer()
	mock.onMsg = func(msg phoenixMessage) {
		if msg.Event == "phx_join" {
			mock.sendToClient(phoenixMessage{
				JoinRef: msg.Ref,
				Ref:     msg.Ref,
				Topic:   msg.Topic,
				Event:   "phx_reply",
				Payload: json.RawMessage(`{"status":"ok","response":{"did":"did:web:node:assigned-123"}}`),
			})
		}
	}

	server := httptest.NewServer(http.HandlerFunc(mock.handler))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/plugin_socket/websocket"
	ch := newPhoenixChannel(wsURL, "test-key", "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch.connect(ctx, []string{})
	defer ch.close()

	if ch.assignedDID() != "did:web:node:assigned-123" {
		t.Errorf("assignedDID() = %q, want %q", ch.assignedDID(), "did:web:node:assigned-123")
	}
}
```

**Step 2: Run tests to verify they fail**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v -run TestPhoenixChannel
```
Expected: FAIL — types not defined

**Step 3: Implement Phoenix Channel**

Create `channel.go`:
```go
package layr8

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// phoenixMessage is the wire format for Phoenix Channel protocol (JSON object variant).
type phoenixMessage struct {
	JoinRef string          `json:"join_ref,omitempty"`
	Ref     string          `json:"ref,omitempty"`
	Topic   string          `json:"topic"`
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload"`
}

// phoenixChannel implements the transport interface using WebSocket/Phoenix Channels.
type phoenixChannel struct {
	wsURL    string
	apiKey   string
	agentDID string
	topic    string

	conn *websocket.Conn
	mu   sync.Mutex // protects conn writes and refCounter

	refCounter int
	joinRef    string

	msgHandler    func(payload []byte)
	disconnectFn  func(error)
	reconnectFn   func()

	assignedDIDVal string

	done chan struct{}
}

func newPhoenixChannel(wsURL, apiKey, agentDID string) *phoenixChannel {
	return &phoenixChannel{
		wsURL:    wsURL,
		apiKey:   apiKey,
		agentDID: agentDID,
		topic:    "plugin:lobby",
		done:     make(chan struct{}),
	}
}

func (c *phoenixChannel) connect(ctx context.Context, protocols []string) error {
	// Build URL with API key
	u, err := url.Parse(c.wsURL)
	if err != nil {
		return fmt.Errorf("parse URL: %w", err)
	}
	q := u.Query()
	q.Set("api_key", c.apiKey)
	q.Set("vsn", "2.0.0")
	u.RawQuery = q.Encode()

	// Connect WebSocket
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	conn, _, err := dialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		return &ConnectionError{URL: c.wsURL, Reason: err.Error()}
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	// Start reader goroutine
	go c.readLoop()

	// Send phx_join
	if err := c.join(ctx, protocols); err != nil {
		conn.Close()
		return err
	}

	// Start heartbeat
	go c.heartbeatLoop()

	return nil
}

func (c *phoenixChannel) join(ctx context.Context, protocols []string) error {
	ref := c.nextRef()
	c.joinRef = ref

	payload, _ := json.Marshal(map[string]interface{}{
		"payload_types": protocols,
	})

	msg := phoenixMessage{
		JoinRef: ref,
		Ref:     ref,
		Topic:   c.topic,
		Event:   "phx_join",
		Payload: payload,
	}

	// Set up reply channel
	replyCh := make(chan json.RawMessage, 1)
	c.mu.Lock()
	c.pendingJoin = replyCh
	c.mu.Unlock()

	if err := c.writeMsg(msg); err != nil {
		return fmt.Errorf("send join: %w", err)
	}

	// Wait for join reply
	select {
	case payload := <-replyCh:
		var reply struct {
			Status   string `json:"status"`
			Response struct {
				DID string `json:"did"`
			} `json:"response"`
		}
		json.Unmarshal(payload, &reply)
		if reply.Status != "ok" {
			return &ConnectionError{URL: c.wsURL, Reason: fmt.Sprintf("join rejected: %s", reply.Status)}
		}
		if reply.Response.DID != "" {
			c.assignedDIDVal = reply.Response.DID
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *phoenixChannel) send(event string, payload []byte) error {
	msg := phoenixMessage{
		Ref:     c.nextRef(),
		Topic:   c.topic,
		Event:   event,
		Payload: payload,
	}
	return c.writeMsg(msg)
}

func (c *phoenixChannel) sendAck(ids []string) error {
	payload, _ := json.Marshal(map[string]interface{}{
		"ids": ids,
	})
	return c.send("ack", payload)
}

func (c *phoenixChannel) setMessageHandler(fn func(payload []byte)) {
	c.msgHandler = fn
}

func (c *phoenixChannel) onDisconnect(fn func(error)) {
	c.disconnectFn = fn
}

func (c *phoenixChannel) onReconnect(fn func()) {
	c.reconnectFn = fn
}

func (c *phoenixChannel) assignedDID() string {
	return c.assignedDIDVal
}

func (c *phoenixChannel) close() error {
	select {
	case <-c.done:
		return nil // already closed
	default:
		close(c.done)
	}

	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	if conn != nil {
		// Send phx_leave
		leaveMsg := phoenixMessage{
			Ref:   c.nextRef(),
			Topic: c.topic,
			Event: "phx_leave",
		}
		c.writeMsg(leaveMsg)
		return conn.Close()
	}
	return nil
}

// pendingJoin is used to route join replies back to the connect method.
// Protected by c.mu.
var _ = (*phoenixChannel)(nil) // ensure type exists

func (c *phoenixChannel) readLoop() {
	for {
		select {
		case <-c.done:
			return
		default:
		}

		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()

		if conn == nil {
			return
		}

		_, data, err := conn.ReadMessage()
		if err != nil {
			select {
			case <-c.done:
				return
			default:
				if c.disconnectFn != nil {
					c.disconnectFn(err)
				}
				return
			}
		}

		var msg phoenixMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		c.handleInbound(msg)
	}
}

func (c *phoenixChannel) handleInbound(msg phoenixMessage) {
	switch msg.Event {
	case "phx_reply":
		c.mu.Lock()
		ch := c.pendingJoin
		c.mu.Unlock()
		if ch != nil && msg.Ref == c.joinRef {
			select {
			case ch <- msg.Payload:
			default:
			}
		}
	case "message":
		if c.msgHandler != nil {
			c.msgHandler(msg.Payload)
		}
	case "phx_error", "phx_close":
		if c.disconnectFn != nil {
			c.disconnectFn(fmt.Errorf("channel %s", msg.Event))
		}
	}
}

func (c *phoenixChannel) heartbeatLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			msg := phoenixMessage{
				Ref:     c.nextRef(),
				Topic:   "phoenix",
				Event:   "heartbeat",
				Payload: json.RawMessage(`{}`),
			}
			if err := c.writeMsg(msg); err != nil {
				return
			}
		}
	}
}

func (c *phoenixChannel) nextRef() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refCounter++
	return fmt.Sprintf("%d", c.refCounter)
}

func (c *phoenixChannel) writeMsg(msg phoenixMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return ErrNotConnected
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.TextMessage, data)
}
```

**Note:** The above code has a `pendingJoin` field that needs to be added to the struct. Update the struct definition to include:
```go
pendingJoin chan json.RawMessage
```

**Step 4: Run tests to verify they pass**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v -run TestPhoenixChannel
```
Expected: ALL PASS

**Step 5: Commit**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
git add channel.go channel_test.go
git commit -m "feat: add Phoenix Channel transport — connect, join, send, ack, heartbeat"
```

---

## Task 9: Reconnect Logic

**Files:**
- Create: `reconnect.go`
- Create: `reconnect_test.go`

**Step 1: Write failing tests**

Create `reconnect_test.go`:
```go
package layr8

import (
	"testing"
	"time"
)

func TestBackoff_ExponentialWithCap(t *testing.T) {
	b := newBackoff(1*time.Second, 30*time.Second)

	d1 := b.next()
	if d1 != 1*time.Second {
		t.Errorf("first backoff = %v, want 1s", d1)
	}

	d2 := b.next()
	if d2 != 2*time.Second {
		t.Errorf("second backoff = %v, want 2s", d2)
	}

	d3 := b.next()
	if d3 != 4*time.Second {
		t.Errorf("third backoff = %v, want 4s", d3)
	}

	d4 := b.next()
	if d4 != 8*time.Second {
		t.Errorf("fourth backoff = %v, want 8s", d4)
	}

	d5 := b.next()
	if d5 != 16*time.Second {
		t.Errorf("fifth backoff = %v, want 16s", d5)
	}

	d6 := b.next()
	if d6 != 30*time.Second {
		t.Errorf("sixth backoff = %v, want 30s (capped)", d6)
	}

	d7 := b.next()
	if d7 != 30*time.Second {
		t.Errorf("seventh backoff = %v, want 30s (capped)", d7)
	}
}

func TestBackoff_Reset(t *testing.T) {
	b := newBackoff(1*time.Second, 30*time.Second)

	b.next() // 1s
	b.next() // 2s
	b.next() // 4s

	b.reset()

	d := b.next()
	if d != 1*time.Second {
		t.Errorf("after reset, backoff = %v, want 1s", d)
	}
}
```

**Step 2: Run tests to verify they fail**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v -run TestBackoff
```
Expected: FAIL

**Step 3: Implement reconnect**

Create `reconnect.go`:
```go
package layr8

import "time"

// backoff implements exponential backoff with a maximum delay.
type backoff struct {
	initial time.Duration
	max     time.Duration
	current time.Duration
}

func newBackoff(initial, max time.Duration) *backoff {
	return &backoff{
		initial: initial,
		max:     max,
		current: initial,
	}
}

func (b *backoff) next() time.Duration {
	d := b.current
	b.current *= 2
	if b.current > b.max {
		b.current = b.max
	}
	if d > b.max {
		d = b.max
	}
	return d
}

func (b *backoff) reset() {
	b.current = b.initial
}
```

**Step 4: Run tests to verify they pass**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v
```
Expected: ALL PASS

**Step 5: Commit**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
git add reconnect.go reconnect_test.go
git commit -m "feat: add exponential backoff for reconnect logic"
```

---

## Task 10: Client — Core (NewClient, Connect, Close, Handle)

**Files:**
- Create: `client.go`
- Create: `client_test.go`

**Step 1: Write failing tests**

Create `client_test.go`:
```go
package layr8

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func setupMockServer(t *testing.T) (*mockPhoenixServer, *httptest.Server, string) {
	t.Helper()
	mock := newMockServer()
	mock.onMsg = func(msg phoenixMessage) {
		if msg.Event == "phx_join" {
			mock.sendToClient(phoenixMessage{
				JoinRef: msg.Ref,
				Ref:     msg.Ref,
				Topic:   msg.Topic,
				Event:   "phx_reply",
				Payload: json.RawMessage(`{"status":"ok","response":{"did":"did:web:node:test"}}`),
			})
		}
	}
	server := httptest.NewServer(http.HandlerFunc(mock.handler))
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/plugin_socket/websocket"
	t.Cleanup(server.Close)
	return mock, server, wsURL
}

func TestNewClient_ValidConfig(t *testing.T) {
	client, err := NewClient(Config{
		NodeURL:  "ws://localhost:4000/plugin_socket/websocket",
		APIKey:   "test-key",
		AgentDID: "did:web:test",
	})
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}
	if client == nil {
		t.Fatal("NewClient() returned nil")
	}
}

func TestNewClient_MissingNodeURL(t *testing.T) {
	_, err := NewClient(Config{APIKey: "test-key"})
	if err == nil {
		t.Fatal("NewClient() should error when NodeURL is missing")
	}
}

func TestNewClient_MissingAPIKey(t *testing.T) {
	_, err := NewClient(Config{NodeURL: "ws://localhost:4000"})
	if err == nil {
		t.Fatal("NewClient() should error when APIKey is missing")
	}
}

func TestClient_Handle_BeforeConnect(t *testing.T) {
	client, _ := NewClient(Config{
		NodeURL:  "ws://localhost:4000",
		APIKey:   "test-key",
		AgentDID: "did:web:test",
	})

	err := client.Handle("https://layr8.io/protocols/echo/1.0/request",
		func(msg *Message) (*Message, error) { return nil, nil },
	)
	if err != nil {
		t.Fatalf("Handle() before Connect should succeed: %v", err)
	}
}

func TestClient_Handle_AfterConnect(t *testing.T) {
	_, _, wsURL := setupMockServer(t)

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:test",
	})
	client.Handle("https://layr8.io/protocols/echo/1.0/request",
		func(msg *Message) (*Message, error) { return nil, nil },
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client.Connect(ctx)
	defer client.Close()

	err := client.Handle("https://layr8.io/protocols/echo/1.0/response",
		func(msg *Message) (*Message, error) { return nil, nil },
	)
	if err == nil {
		t.Fatal("Handle() after Connect should return error")
	}
}

func TestClient_ConnectAndClose(t *testing.T) {
	_, _, wsURL := setupMockServer(t)

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:test",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	err = client.Close()
	if err != nil {
		t.Fatalf("Close() error: %v", err)
	}
}

func TestClient_DoubleConnect(t *testing.T) {
	_, _, wsURL := setupMockServer(t)

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:test",
	})
	client.Handle("https://layr8.io/protocols/echo/1.0/request",
		func(msg *Message) (*Message, error) { return nil, nil },
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client.Connect(ctx)
	defer client.Close()

	err := client.Connect(ctx)
	if err == nil {
		t.Fatal("second Connect() should error")
	}
}

func TestClient_OnDisconnect(t *testing.T) {
	_, _, wsURL := setupMockServer(t)

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:test",
	})

	called := make(chan struct{})
	client.OnDisconnect(func(err error) {
		close(called)
	})

	// Just verify it doesn't panic — functional test requires server disconnect
	_ = called
}

func TestClient_OnReconnect(t *testing.T) {
	_, _, wsURL := setupMockServer(t)

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:test",
	})

	called := make(chan struct{})
	client.OnReconnect(func() {
		close(called)
	})

	// Just verify it doesn't panic
	_ = called
}
```

**Step 2: Run tests to verify they fail**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v -run "TestNewClient|TestClient_"
```
Expected: FAIL

**Step 3: Implement client core**

Create `client.go`:
```go
package layr8

import (
	"context"
	"sync"
)

// Client is the main entry point for interacting with the Layr8 platform.
type Client struct {
	cfg       Config
	transport transport
	registry  *handlerRegistry

	connected bool
	closed    bool
	mu        sync.Mutex

	agentDID string // resolved DID (explicit or assigned by node)

	// Correlation map for Request/Response pattern
	pending   sync.Map // threadID → chan *Message

	disconnectFn func(error)
	reconnectFn  func()
}

// NewClient creates a new Layr8 client with the given configuration.
// The client is not connected until Connect() is called.
func NewClient(cfg Config) (*Client, error) {
	resolved, err := resolveConfig(cfg)
	if err != nil {
		return nil, err
	}

	return &Client{
		cfg:      resolved,
		registry: newHandlerRegistry(),
		agentDID: resolved.AgentDID,
	}, nil
}

// Handle registers a handler for the given DIDComm message type.
// Handlers must be registered before Connect(). The protocol base URI
// is automatically derived and registered with the cloud-node on Connect().
func (c *Client) Handle(msgType string, fn HandlerFunc, opts ...HandlerOption) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return ErrAlreadyConnected
	}

	return c.registry.register(msgType, fn, opts...)
}

// Connect establishes the WebSocket connection and joins the Phoenix Channel
// with the protocols derived from registered handlers.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	if c.connected {
		c.mu.Unlock()
		return ErrAlreadyConnected
	}
	if c.closed {
		c.mu.Unlock()
		return ErrClientClosed
	}
	c.mu.Unlock()

	protocols := c.registry.protocols()

	ch := newPhoenixChannel(c.cfg.NodeURL, c.cfg.APIKey, c.cfg.AgentDID)

	// Wire up message handler
	ch.setMessageHandler(c.handleInboundMessage)

	// Wire up disconnect/reconnect callbacks
	if c.disconnectFn != nil {
		ch.onDisconnect(c.disconnectFn)
	}
	if c.reconnectFn != nil {
		ch.onReconnect(c.reconnectFn)
	}

	if err := ch.connect(ctx, protocols); err != nil {
		return err
	}

	// If no DID was provided, use the one assigned by the node
	if c.agentDID == "" && ch.assignedDID() != "" {
		c.agentDID = ch.assignedDID()
	}

	c.mu.Lock()
	c.transport = ch
	c.connected = true
	c.mu.Unlock()

	return nil
}

// Close gracefully shuts down the client connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true
	c.connected = false

	if c.transport != nil {
		return c.transport.close()
	}
	return nil
}

// OnDisconnect registers a callback invoked when the connection drops.
func (c *Client) OnDisconnect(fn func(error)) {
	c.disconnectFn = fn
}

// OnReconnect registers a callback invoked when the connection is restored.
func (c *Client) OnReconnect(fn func()) {
	c.reconnectFn = fn
}

// handleInboundMessage is called by the transport for each inbound "message" event.
func (c *Client) handleInboundMessage(payload []byte) {
	msg, err := parseDIDComm(payload)
	if err != nil {
		return // silently drop unparseable messages
	}

	// Check if this is a response to a pending Request
	if msg.ThreadID != "" {
		if ch, ok := c.pending.LoadAndDelete(msg.ThreadID); ok {
			respCh := ch.(chan *Message)
			select {
			case respCh <- msg:
			default:
			}
			return
		}
	}

	// Check for problem reports on pending requests
	if msg.Type == "https://didcomm.org/report-problem/2.0/problem-report" && msg.ThreadID != "" {
		if ch, ok := c.pending.LoadAndDelete(msg.ThreadID); ok {
			respCh := ch.(chan *Message)
			select {
			case respCh <- msg:
			default:
			}
			return
		}
	}

	// Route to registered handler
	entry, ok := c.registry.lookup(msg.Type)
	if !ok {
		return // no handler registered for this type
	}

	// Auto-ack before handler (unless manual ack)
	if !entry.manualAck {
		c.transport.sendAck([]string{msg.ID})
	} else {
		// Set up manual ack function
		msg.ackFn = func(id string) {
			c.transport.sendAck([]string{id})
		}
	}

	// Run handler
	go c.runHandler(entry, msg)
}

func (c *Client) runHandler(entry handlerEntry, msg *Message) {
	resp, err := entry.fn(msg)

	if err != nil {
		// Send problem report
		c.sendProblemReport(msg, err)
		return
	}

	if resp != nil {
		// Auto-fill response fields
		if resp.From == "" {
			resp.From = c.agentDID
		}
		if len(resp.To) == 0 && msg.From != "" {
			resp.To = []string{msg.From}
		}
		if resp.ThreadID == "" && msg.ThreadID != "" {
			resp.ThreadID = msg.ThreadID
		} else if resp.ThreadID == "" {
			resp.ThreadID = msg.ID
		}

		c.sendMessage(resp)
	}
}

func (c *Client) sendProblemReport(original *Message, handlerErr error) {
	report := &Message{
		Type:     "https://didcomm.org/report-problem/2.0/problem-report",
		To:       []string{original.From},
		ThreadID: original.ThreadID,
		Body: &ProblemReportError{
			Code:    "e.p.xfer.cant-process",
			Comment: handlerErr.Error(),
		},
	}
	c.sendMessage(report)
}

func (c *Client) sendMessage(msg *Message) error {
	if msg.ID == "" {
		msg.ID = generateID()
	}
	if msg.From == "" {
		msg.From = c.agentDID
	}

	data, err := marshalDIDComm(msg)
	if err != nil {
		return err
	}

	// Wrap in plaintext envelope for sending
	envelope, _ := json.Marshal(map[string]json.RawMessage{
		"plaintext": data,
	})

	return c.transport.send("message", envelope)
}
```

Also create a small helper for ID generation. Add to `message.go`:
```go
import "github.com/google/uuid"

func generateID() string {
	return uuid.New().String()
}
```

**Step 4: Run tests to verify they pass**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v
```
Expected: ALL PASS

**Step 5: Commit**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
git add client.go client_test.go message.go
git commit -m "feat: add Client with NewClient, Connect, Close, Handle, inbound message routing"
```

---

## Task 11: Client — Send & Request

**Files:**
- Modify: `client.go`
- Modify: `client_test.go`

**Step 1: Write failing tests**

Add to `client_test.go`:
```go
func TestClient_Send(t *testing.T) {
	mock, _, wsURL := setupMockServer(t)

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:alice",
	})
	// Need at least one handler so protocols are derived
	client.Handle("https://layr8.io/protocols/echo/1.0/request",
		func(msg *Message) (*Message, error) { return nil, nil },
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client.Connect(ctx)
	defer client.Close()

	err := client.Send(ctx, &Message{
		Type: "https://didcomm.org/basicmessage/2.0/message",
		To:   []string{"did:web:bob"},
		Body: map[string]string{"content": "hello"},
	})
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	// Verify message was sent to server
	time.Sleep(200 * time.Millisecond)
	received := mock.getReceived()

	var found bool
	for _, msg := range received {
		if msg.Event == "message" {
			found = true
			break
		}
	}
	if !found {
		t.Error("server should have received a message event")
	}
}

func TestClient_Send_NotConnected(t *testing.T) {
	client, _ := NewClient(Config{
		NodeURL:  "ws://localhost:4000",
		APIKey:   "test-key",
		AgentDID: "did:web:test",
	})

	err := client.Send(context.Background(), &Message{
		Type: "test",
		To:   []string{"did:web:bob"},
	})
	if err == nil {
		t.Fatal("Send() should error when not connected")
	}
}

func TestClient_Send_AutoFillsFields(t *testing.T) {
	mock, _, wsURL := setupMockServer(t)

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:alice",
	})
	client.Handle("https://layr8.io/protocols/echo/1.0/request",
		func(msg *Message) (*Message, error) { return nil, nil },
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client.Connect(ctx)
	defer client.Close()

	msg := &Message{
		Type: "https://didcomm.org/basicmessage/2.0/message",
		To:   []string{"did:web:bob"},
		Body: map[string]string{"content": "hello"},
	}
	client.Send(ctx, msg)

	// ID and From should be auto-filled
	if msg.ID == "" {
		t.Error("Send should auto-fill ID")
	}
	if msg.From == "" {
		t.Error("Send should auto-fill From")
	}
}

func TestClient_Request_ResponseCorrelation(t *testing.T) {
	mock, _, wsURL := setupMockServer(t)

	// When server receives a message event, send back a response with matching thid
	mock.onMsg = func(msg phoenixMessage) {
		if msg.Event == "phx_join" {
			mock.sendToClient(phoenixMessage{
				JoinRef: msg.Ref,
				Ref:     msg.Ref,
				Topic:   msg.Topic,
				Event:   "phx_reply",
				Payload: json.RawMessage(`{"status":"ok","response":{}}`),
			})
			return
		}

		if msg.Event == "message" {
			// Parse outbound to get thid
			var envelope struct {
				Plaintext struct {
					ThID string `json:"thid"`
					From string `json:"from"`
				} `json:"plaintext"`
			}
			json.Unmarshal(msg.Payload, &envelope)

			// Send response with matching thid
			resp, _ := json.Marshal(map[string]interface{}{
				"plaintext": map[string]interface{}{
					"id":   "resp-1",
					"type": "https://layr8.io/protocols/echo/1.0/response",
					"from": "did:web:bob",
					"to":   []string{envelope.Plaintext.From},
					"thid": envelope.Plaintext.ThID,
					"body": map[string]string{"echo": "hello"},
				},
			})
			mock.sendToClient(phoenixMessage{
				Topic:   "plugin:lobby",
				Event:   "message",
				Payload: resp,
			})
		}
	}

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:alice",
	})
	client.Handle("https://layr8.io/protocols/echo/1.0/request",
		func(msg *Message) (*Message, error) { return nil, nil },
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client.Connect(ctx)
	defer client.Close()

	resp, err := client.Request(ctx, &Message{
		Type: "https://layr8.io/protocols/echo/1.0/request",
		To:   []string{"did:web:bob"},
		Body: map[string]string{"message": "hello"},
	})
	if err != nil {
		t.Fatalf("Request() error: %v", err)
	}
	if resp == nil {
		t.Fatal("Request() returned nil response")
	}
	if resp.Type != "https://layr8.io/protocols/echo/1.0/response" {
		t.Errorf("resp.Type = %q, want echo response type", resp.Type)
	}

	var body map[string]string
	resp.UnmarshalBody(&body)
	if body["echo"] != "hello" {
		t.Errorf("resp body echo = %q, want %q", body["echo"], "hello")
	}
}

func TestClient_Request_Timeout(t *testing.T) {
	_, _, wsURL := setupMockServer(t)

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:alice",
	})
	client.Handle("https://layr8.io/protocols/echo/1.0/request",
		func(msg *Message) (*Message, error) { return nil, nil },
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client.Connect(ctx)
	defer client.Close()

	// Request with short timeout — no one will respond
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer shortCancel()

	_, err := client.Request(shortCtx, &Message{
		Type: "https://layr8.io/protocols/echo/1.0/request",
		To:   []string{"did:web:nobody"},
		Body: map[string]string{"message": "hello"},
	})
	if err == nil {
		t.Fatal("Request() should error on timeout")
	}
}

func TestClient_Request_ProblemReport(t *testing.T) {
	mock, _, wsURL := setupMockServer(t)

	mock.onMsg = func(msg phoenixMessage) {
		if msg.Event == "phx_join" {
			mock.sendToClient(phoenixMessage{
				JoinRef: msg.Ref,
				Ref:     msg.Ref,
				Topic:   msg.Topic,
				Event:   "phx_reply",
				Payload: json.RawMessage(`{"status":"ok","response":{}}`),
			})
			return
		}
		if msg.Event == "message" {
			var envelope struct {
				Plaintext struct {
					ThID string `json:"thid"`
				} `json:"plaintext"`
			}
			json.Unmarshal(msg.Payload, &envelope)

			resp, _ := json.Marshal(map[string]interface{}{
				"plaintext": map[string]interface{}{
					"id":   "err-1",
					"type": "https://didcomm.org/report-problem/2.0/problem-report",
					"from": "did:web:bob",
					"thid": envelope.Plaintext.ThID,
					"body": map[string]string{
						"code":    "e.p.xfer.cant-process",
						"comment": "database unavailable",
					},
				},
			})
			mock.sendToClient(phoenixMessage{
				Topic:   "plugin:lobby",
				Event:   "message",
				Payload: resp,
			})
		}
	}

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:alice",
	})
	client.Handle("https://layr8.io/protocols/echo/1.0/request",
		func(msg *Message) (*Message, error) { return nil, nil },
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client.Connect(ctx)
	defer client.Close()

	_, err := client.Request(ctx, &Message{
		Type: "https://layr8.io/protocols/echo/1.0/request",
		To:   []string{"did:web:bob"},
		Body: map[string]string{"message": "hello"},
	})
	if err == nil {
		t.Fatal("Request() should return error for problem report")
	}

	var probErr *ProblemReportError
	if !errors.As(err, &probErr) {
		t.Fatalf("error should be ProblemReportError, got %T: %v", err, err)
	}
	if probErr.Code != "e.p.xfer.cant-process" {
		t.Errorf("Code = %q, want %q", probErr.Code, "e.p.xfer.cant-process")
	}
}

func TestClient_Request_WithParentThread(t *testing.T) {
	mock, _, wsURL := setupMockServer(t)

	var sentPthid string
	mock.onMsg = func(msg phoenixMessage) {
		if msg.Event == "phx_join" {
			mock.sendToClient(phoenixMessage{
				JoinRef: msg.Ref,
				Ref:     msg.Ref,
				Topic:   msg.Topic,
				Event:   "phx_reply",
				Payload: json.RawMessage(`{"status":"ok","response":{}}`),
			})
			return
		}
		if msg.Event == "message" {
			var envelope struct {
				Plaintext struct {
					ThID  string `json:"thid"`
					PThID string `json:"pthid"`
				} `json:"plaintext"`
			}
			json.Unmarshal(msg.Payload, &envelope)
			sentPthid = envelope.Plaintext.PThID

			// Send response
			resp, _ := json.Marshal(map[string]interface{}{
				"plaintext": map[string]interface{}{
					"id":   "resp-1",
					"type": "https://layr8.io/protocols/echo/1.0/response",
					"thid": envelope.Plaintext.ThID,
					"body": map[string]string{},
				},
			})
			mock.sendToClient(phoenixMessage{
				Topic:   "plugin:lobby",
				Event:   "message",
				Payload: resp,
			})
		}
	}

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:alice",
	})
	client.Handle("https://layr8.io/protocols/echo/1.0/request",
		func(msg *Message) (*Message, error) { return nil, nil },
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client.Connect(ctx)
	defer client.Close()

	client.Request(ctx, &Message{
		Type: "https://layr8.io/protocols/echo/1.0/request",
		To:   []string{"did:web:bob"},
		Body: map[string]string{},
	}, WithParentThread("parent-thread-123"))

	time.Sleep(200 * time.Millisecond)
	if sentPthid != "parent-thread-123" {
		t.Errorf("pthid = %q, want %q", sentPthid, "parent-thread-123")
	}
}
```

**Step 2: Run tests to verify they fail**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v -run "TestClient_Send|TestClient_Request"
```
Expected: FAIL — Send and Request methods not defined

**Step 3: Implement Send and Request**

Add to `client.go`:
```go
import "encoding/json"

// Send sends a fire-and-forget message. Returns once the message is written to the connection.
func (c *Client) Send(ctx context.Context, msg *Message) error {
	c.mu.Lock()
	if !c.connected {
		c.mu.Unlock()
		return ErrNotConnected
	}
	c.mu.Unlock()

	if msg.ID == "" {
		msg.ID = generateID()
	}
	if msg.From == "" {
		msg.From = c.agentDID
	}

	return c.sendMessage(msg)
}

// Request sends a message and blocks until a correlated response arrives or the context expires.
func (c *Client) Request(ctx context.Context, msg *Message, opts ...RequestOption) (*Message, error) {
	c.mu.Lock()
	if !c.connected {
		c.mu.Unlock()
		return nil, ErrNotConnected
	}
	c.mu.Unlock()

	o := requestDefaults()
	for _, opt := range opts {
		opt(&o)
	}

	if msg.ID == "" {
		msg.ID = generateID()
	}
	if msg.From == "" {
		msg.From = c.agentDID
	}
	if msg.ThreadID == "" {
		msg.ThreadID = generateID()
	}
	if o.parentThreadID != "" {
		msg.ParentThreadID = o.parentThreadID
	}

	// Register response channel
	respCh := make(chan *Message, 1)
	c.pending.Store(msg.ThreadID, respCh)
	defer c.pending.Delete(msg.ThreadID)

	// Send the message
	if err := c.sendMessage(msg); err != nil {
		return nil, err
	}

	// Wait for response or timeout
	select {
	case resp := <-respCh:
		// Check if response is a problem report
		if resp.Type == "https://didcomm.org/report-problem/2.0/problem-report" {
			var prob ProblemReportError
			if err := resp.UnmarshalBody(&prob); err != nil {
				return nil, fmt.Errorf("failed to parse problem report: %w", err)
			}
			return nil, &prob
		}
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
```

**Step 4: Run tests to verify they pass**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v
```
Expected: ALL PASS

**Step 5: Commit**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
git add client.go client_test.go
git commit -m "feat: add Send (fire-and-forget) and Request (request/response with correlation)"
```

---

## Task 12: Handler Inbound Integration Test

**Files:**
- Modify: `client_test.go`

**Step 1: Write integration test for inbound handler with auto-ack**

Add to `client_test.go`:
```go
func TestClient_InboundHandler_AutoAck(t *testing.T) {
	mock, _, wsURL := setupMockServer(t)

	handlerCalled := make(chan *Message, 1)

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:alice",
	})
	client.Handle("https://didcomm.org/basicmessage/2.0/message",
		func(msg *Message) (*Message, error) {
			handlerCalled <- msg
			return nil, nil
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client.Connect(ctx)
	defer client.Close()

	// Server sends an inbound message
	inbound, _ := json.Marshal(map[string]interface{}{
		"context": map[string]interface{}{
			"recipient":  "did:web:alice",
			"authorized": true,
			"sender_credentials": []map[string]interface{}{
				{"credential_subject": map[string]string{"id": "did:web:bob", "name": "Bob"}},
			},
		},
		"plaintext": map[string]interface{}{
			"id":   "inbound-1",
			"type": "https://didcomm.org/basicmessage/2.0/message",
			"from": "did:web:bob",
			"to":   []string{"did:web:alice"},
			"body": map[string]string{"content": "hello alice"},
		},
	})
	mock.sendToClient(phoenixMessage{
		Topic:   "plugin:lobby",
		Event:   "message",
		Payload: inbound,
	})

	select {
	case msg := <-handlerCalled:
		if msg.From != "did:web:bob" {
			t.Errorf("msg.From = %q, want %q", msg.From, "did:web:bob")
		}
		if msg.Context == nil {
			t.Fatal("msg.Context should not be nil")
		}
		if !msg.Context.Authorized {
			t.Error("msg.Context.Authorized should be true")
		}
		if len(msg.Context.SenderCredentials) != 1 || msg.Context.SenderCredentials[0].Name != "Bob" {
			t.Error("msg.Context.SenderCredentials should contain Bob")
		}
		var body map[string]string
		msg.UnmarshalBody(&body)
		if body["content"] != "hello alice" {
			t.Errorf("body content = %q, want %q", body["content"], "hello alice")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for handler to be called")
	}

	// Verify ack was sent
	time.Sleep(200 * time.Millisecond)
	received := mock.getReceived()
	var ackFound bool
	for _, msg := range received {
		if msg.Event == "ack" {
			ackFound = true
			break
		}
	}
	if !ackFound {
		t.Error("auto-ack should have been sent")
	}
}

func TestClient_InboundHandler_ResponseAutoFill(t *testing.T) {
	mock, _, wsURL := setupMockServer(t)

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:alice",
	})
	client.Handle("https://layr8.io/protocols/echo/1.0/request",
		func(msg *Message) (*Message, error) {
			return &Message{
				Type: "https://layr8.io/protocols/echo/1.0/response",
				Body: map[string]string{"echo": "pong"},
			}, nil
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client.Connect(ctx)
	defer client.Close()

	// Server sends request
	inbound, _ := json.Marshal(map[string]interface{}{
		"plaintext": map[string]interface{}{
			"id":   "req-1",
			"type": "https://layr8.io/protocols/echo/1.0/request",
			"from": "did:web:bob",
			"to":   []string{"did:web:alice"},
			"thid": "thread-abc",
			"body": map[string]string{"message": "ping"},
		},
	})
	mock.sendToClient(phoenixMessage{
		Topic:   "plugin:lobby",
		Event:   "message",
		Payload: inbound,
	})

	// Wait for response to be sent back to server
	time.Sleep(500 * time.Millisecond)
	received := mock.getReceived()

	var responseFound bool
	for _, msg := range received {
		if msg.Event == "message" {
			var envelope struct {
				Plaintext struct {
					Type string `json:"type"`
					To   []string `json:"to"`
					From string `json:"from"`
					ThID string `json:"thid"`
				} `json:"plaintext"`
			}
			json.Unmarshal(msg.Payload, &envelope)
			if envelope.Plaintext.Type == "https://layr8.io/protocols/echo/1.0/response" {
				responseFound = true
				if envelope.Plaintext.From != "did:web:alice" {
					t.Errorf("response From = %q, want auto-filled %q", envelope.Plaintext.From, "did:web:alice")
				}
				if len(envelope.Plaintext.To) != 1 || envelope.Plaintext.To[0] != "did:web:bob" {
					t.Errorf("response To = %v, want auto-filled [did:web:bob]", envelope.Plaintext.To)
				}
				if envelope.Plaintext.ThID != "thread-abc" {
					t.Errorf("response thid = %q, want auto-filled %q", envelope.Plaintext.ThID, "thread-abc")
				}
			}
		}
	}
	if !responseFound {
		t.Error("server should have received an echo response")
	}
}

func TestClient_InboundHandler_ErrorSendsProblemReport(t *testing.T) {
	mock, _, wsURL := setupMockServer(t)

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:alice",
	})
	client.Handle("https://layr8.io/protocols/echo/1.0/request",
		func(msg *Message) (*Message, error) {
			return nil, fmt.Errorf("something went wrong")
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client.Connect(ctx)
	defer client.Close()

	inbound, _ := json.Marshal(map[string]interface{}{
		"plaintext": map[string]interface{}{
			"id":   "req-1",
			"type": "https://layr8.io/protocols/echo/1.0/request",
			"from": "did:web:bob",
			"to":   []string{"did:web:alice"},
			"body": map[string]string{"message": "ping"},
		},
	})
	mock.sendToClient(phoenixMessage{
		Topic:   "plugin:lobby",
		Event:   "message",
		Payload: inbound,
	})

	time.Sleep(500 * time.Millisecond)
	received := mock.getReceived()

	var problemReportFound bool
	for _, msg := range received {
		if msg.Event == "message" {
			var envelope struct {
				Plaintext struct {
					Type string `json:"type"`
				} `json:"plaintext"`
			}
			json.Unmarshal(msg.Payload, &envelope)
			if envelope.Plaintext.Type == "https://didcomm.org/report-problem/2.0/problem-report" {
				problemReportFound = true
			}
		}
	}
	if !problemReportFound {
		t.Error("handler error should result in a problem report being sent")
	}
}
```

**Step 2: Run integration tests**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v -run "TestClient_Inbound"
```
Expected: ALL PASS (if Tasks 2-11 are complete)

**Step 3: Commit**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
git add client_test.go
git commit -m "test: add integration tests for inbound handler routing, auto-ack, and problem reports"
```

---

## Task 13: Concurrent Request Fan-Out Test

**Files:**
- Modify: `client_test.go`

**Step 1: Write concurrency test**

Add to `client_test.go`:
```go
func TestClient_ConcurrentRequests(t *testing.T) {
	mock, _, wsURL := setupMockServer(t)

	// Server echoes back each request with matching thid
	mock.onMsg = func(msg phoenixMessage) {
		if msg.Event == "phx_join" {
			mock.sendToClient(phoenixMessage{
				JoinRef: msg.Ref,
				Ref:     msg.Ref,
				Topic:   msg.Topic,
				Event:   "phx_reply",
				Payload: json.RawMessage(`{"status":"ok","response":{}}`),
			})
			return
		}
		if msg.Event == "message" {
			var envelope struct {
				Plaintext struct {
					ThID string `json:"thid"`
					Body struct {
						Index int `json:"index"`
					} `json:"body"`
				} `json:"plaintext"`
			}
			json.Unmarshal(msg.Payload, &envelope)

			resp, _ := json.Marshal(map[string]interface{}{
				"plaintext": map[string]interface{}{
					"id":   generateID(),
					"type": "https://layr8.io/protocols/echo/1.0/response",
					"thid": envelope.Plaintext.ThID,
					"body": map[string]interface{}{"index": envelope.Plaintext.Body.Index},
				},
			})
			mock.sendToClient(phoenixMessage{
				Topic:   "plugin:lobby",
				Event:   "message",
				Payload: resp,
			})
		}
	}

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:alice",
	})
	client.Handle("https://layr8.io/protocols/echo/1.0/request",
		func(msg *Message) (*Message, error) { return nil, nil },
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client.Connect(ctx)
	defer client.Close()

	// Fan out 10 concurrent requests
	const n = 10
	results := make([]*Message, n)
	errs := make([]error, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			resp, err := client.Request(ctx, &Message{
				Type: "https://layr8.io/protocols/echo/1.0/request",
				To:   []string{"did:web:bob"},
				Body: map[string]int{"index": idx},
			})
			results[idx] = resp
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Errorf("Request[%d] error: %v", i, errs[i])
			continue
		}
		if results[i] == nil {
			t.Errorf("Request[%d] returned nil", i)
			continue
		}
		var body map[string]interface{}
		results[i].UnmarshalBody(&body)
		idx, ok := body["index"].(float64)
		if !ok || int(idx) != i {
			t.Errorf("Request[%d] got index %v, want %d", i, body["index"], i)
		}
	}
}
```

**Step 2: Run concurrency test**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v -run TestClient_ConcurrentRequests -race
```
Expected: PASS with no race conditions

**Step 3: Commit**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
git add client_test.go
git commit -m "test: add concurrent request fan-out test with race detection"
```

---

## Task 14: Final Cleanup & Examples

**Files:**
- Move existing examples into examples/ directory (already created during design)
- Run full test suite with race detection

**Step 1: Run full test suite**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go test ./... -v -race -count=1
```
Expected: ALL PASS

**Step 2: Run go vet**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
go vet ./...
```
Expected: no issues

**Step 3: Add examples to git and final commit**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
git add examples/ docs/
git commit -m "docs: add examples for echo-agent, chat, postgres-agent, and http-agent"
```

**Step 4: Final integration commit**

```bash
cd /Users/kaijiezhan/Developments/go-sdk
git log --oneline
```

Expected commit history:
```
docs: add examples for echo-agent, chat, postgres-agent, and http-agent
test: add concurrent request fan-out test with race detection
test: add integration tests for inbound handler routing, auto-ack, and problem reports
feat: add Send (fire-and-forget) and Request (request/response with correlation)
feat: add Client with NewClient, Connect, Close, Handle, inbound message routing
feat: add internal transport interface for WebSocket/QUIC abstraction
feat: add exponential backoff for reconnect logic
feat: add Phoenix Channel transport — connect, join, send, ack, heartbeat
feat: add handler registry with protocol auto-derivation
feat: add handler and request options — WithManualAck, WithParentThread
feat: add Config struct with env-var fallback and validation
feat: add Message struct with DIDComm marshal/unmarshal and MessageContext
feat: add error types — ProblemReportError, ConnectionError, sentinels
feat: initialize go-sdk module
```
