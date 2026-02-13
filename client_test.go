package layr8

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// Ensure fmt is used (for handler error tests in future tasks)
var _ = fmt.Errorf
