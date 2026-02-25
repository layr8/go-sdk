package layr8

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// discardErrors is a no-op ErrorHandler used in tests that don't assert error handler behavior.
var discardErrors = func(SDKError) {}

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

func TestNewClient_NilErrorHandler(t *testing.T) {
	_, err := NewClient(Config{
		NodeURL: "ws://localhost:4000",
		APIKey:  "test-key",
	}, nil)
	if err == nil {
		t.Fatal("NewClient() should error when ErrorHandler is nil")
	}
}

func TestNewClient_ValidConfig(t *testing.T) {
	client, err := NewClient(Config{
		NodeURL:  "ws://localhost:4000/plugin_socket/websocket",
		APIKey:   "test-key",
		AgentDID: "did:web:test",
	}, discardErrors)
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}
	if client == nil {
		t.Fatal("NewClient() returned nil")
	}
}

func TestNewClient_MissingNodeURL(t *testing.T) {
	_, err := NewClient(Config{APIKey: "test-key"}, discardErrors)
	if err == nil {
		t.Fatal("NewClient() should error when NodeURL is missing")
	}
}

func TestNewClient_MissingAPIKey(t *testing.T) {
	_, err := NewClient(Config{NodeURL: "ws://localhost:4000"}, discardErrors)
	if err == nil {
		t.Fatal("NewClient() should error when APIKey is missing")
	}
}

func TestClient_Handle_BeforeConnect(t *testing.T) {
	client, _ := NewClient(Config{
		NodeURL:  "ws://localhost:4000",
		APIKey:   "test-key",
		AgentDID: "did:web:test",
	}, discardErrors)

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
	}, discardErrors)
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
	}, discardErrors)

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
	}, discardErrors)
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
	}, discardErrors)

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
	}, discardErrors)

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
	}, discardErrors)
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
	}, discardErrors)

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
	}, discardErrors)
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
			// Parse outbound DIDComm message (bare, no plaintext wrapper)
			var outbound struct {
				ThID string `json:"thid"`
				From string `json:"from"`
			}
			json.Unmarshal(msg.Payload, &outbound)

			// Send inbound response (node wraps in context+plaintext)
			resp, _ := json.Marshal(map[string]interface{}{
				"plaintext": map[string]interface{}{
					"id":   "resp-1",
					"type": "https://layr8.io/protocols/echo/1.0/response",
					"from": "did:web:bob",
					"to":   []string{outbound.From},
					"thid": outbound.ThID,
					"body": map[string]string{"echo": "hello"},
				},
			})
			mock.sendToClient(phoenixMessage{
				Topic:   "plugins:did:web:alice",
				Event:   "message",
				Payload: resp,
			})
		}
	}

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:alice",
	}, discardErrors)
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
	}, discardErrors)
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
			var outbound struct {
				ThID string `json:"thid"`
			}
			json.Unmarshal(msg.Payload, &outbound)

			resp, _ := json.Marshal(map[string]interface{}{
				"plaintext": map[string]interface{}{
					"id":   "err-1",
					"type": "https://didcomm.org/report-problem/2.0/problem-report",
					"from": "did:web:bob",
					"thid": outbound.ThID,
					"body": map[string]string{
						"code":    "e.p.xfer.cant-process",
						"comment": "database unavailable",
					},
				},
			})
			mock.sendToClient(phoenixMessage{
				Topic:   "plugins:did:web:alice",
				Event:   "message",
				Payload: resp,
			})
		}
	}

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:alice",
	}, discardErrors)
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
			var outbound struct {
				ThID  string `json:"thid"`
				PThID string `json:"pthid"`
			}
			json.Unmarshal(msg.Payload, &outbound)
			sentPthid = outbound.PThID

			// Send response
			resp, _ := json.Marshal(map[string]interface{}{
				"plaintext": map[string]interface{}{
					"id":   "resp-1",
					"type": "https://layr8.io/protocols/echo/1.0/response",
					"thid": outbound.ThID,
					"body": map[string]string{},
				},
			})
			mock.sendToClient(phoenixMessage{
				Topic:   "plugins:did:web:alice",
				Event:   "message",
				Payload: resp,
			})
		}
	}

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:alice",
	}, discardErrors)
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

func TestClient_InboundHandler_AutoAck(t *testing.T) {
	mock, _, wsURL := setupMockServer(t)

	handlerCalled := make(chan *Message, 1)

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:alice",
	}, discardErrors)
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
	}, discardErrors)
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
			var outbound struct {
				Type string   `json:"type"`
				To   []string `json:"to"`
				From string   `json:"from"`
				ThID string   `json:"thid"`
			}
			json.Unmarshal(msg.Payload, &outbound)
			if outbound.Type == "https://layr8.io/protocols/echo/1.0/response" {
				responseFound = true
				if outbound.From != "did:web:alice" {
					t.Errorf("response From = %q, want auto-filled %q", outbound.From, "did:web:alice")
				}
				if len(outbound.To) != 1 || outbound.To[0] != "did:web:bob" {
					t.Errorf("response To = %v, want auto-filled [did:web:bob]", outbound.To)
				}
				if outbound.ThID != "thread-abc" {
					t.Errorf("response thid = %q, want auto-filled %q", outbound.ThID, "thread-abc")
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
	}, discardErrors)
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
			var outbound struct {
				Type string `json:"type"`
			}
			json.Unmarshal(msg.Payload, &outbound)
			if outbound.Type == "https://didcomm.org/report-problem/2.0/problem-report" {
				problemReportFound = true
			}
		}
	}
	if !problemReportFound {
		t.Error("handler error should result in a problem report being sent")
	}
}

func TestClient_OnError_ParseFailure(t *testing.T) {
	mock, _, wsURL := setupMockServer(t)

	errCh := make(chan SDKError, 1)
	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:alice",
	}, func(e SDKError) { errCh <- e })

	client.Handle("https://layr8.io/protocols/echo/1.0/request",
		func(msg *Message) (*Message, error) { return nil, nil },
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client.Connect(ctx)
	defer client.Close()

	// Send a valid JSON payload that is not a valid DIDComm envelope
	// (missing "plaintext" field causes parseDIDComm to fail)
	mock.sendToClient(phoenixMessage{
		Topic:   "plugins:did:web:alice",
		Event:   "message",
		Payload: json.RawMessage(`{"garbage": true}`),
	})

	select {
	case e := <-errCh:
		if e.Kind != ErrParseFailure {
			t.Errorf("Kind = %v, want ErrParseFailure", e.Kind)
		}
		if e.Cause == nil {
			t.Error("Cause should not be nil")
		}
		if e.Raw == nil {
			t.Error("Raw should contain the unparseable payload")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for onError callback")
	}
}

func TestClient_OnError_NoHandler(t *testing.T) {
	mock, _, wsURL := setupMockServer(t)

	errCh := make(chan SDKError, 1)
	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:alice",
	}, func(e SDKError) { errCh <- e })

	// Register handler for echo, but NOT for basicmessage
	client.Handle("https://layr8.io/protocols/echo/1.0/request",
		func(msg *Message) (*Message, error) { return nil, nil },
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client.Connect(ctx)
	defer client.Close()

	// Send a message type with no handler
	inbound, _ := json.Marshal(map[string]interface{}{
		"plaintext": map[string]interface{}{
			"id":   "msg-1",
			"type": "https://didcomm.org/basicmessage/2.0/message",
			"from": "did:web:bob",
			"to":   []string{"did:web:alice"},
			"body": map[string]string{"content": "hello"},
		},
	})
	mock.sendToClient(phoenixMessage{
		Topic:   "plugins:did:web:alice",
		Event:   "message",
		Payload: inbound,
	})

	select {
	case e := <-errCh:
		if e.Kind != ErrNoHandler {
			t.Errorf("Kind = %v, want ErrNoHandler", e.Kind)
		}
		if e.MessageID != "msg-1" {
			t.Errorf("MessageID = %q, want %q", e.MessageID, "msg-1")
		}
		if e.Type != "https://didcomm.org/basicmessage/2.0/message" {
			t.Errorf("Type = %q, want basicmessage type", e.Type)
		}
		if e.From != "did:web:bob" {
			t.Errorf("From = %q, want %q", e.From, "did:web:bob")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for onError callback")
	}
}

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
			var outbound struct {
				ThID string `json:"thid"`
				Body struct {
					Index int `json:"index"`
				} `json:"body"`
			}
			json.Unmarshal(msg.Payload, &outbound)

			resp, _ := json.Marshal(map[string]interface{}{
				"plaintext": map[string]interface{}{
					"id":   generateID(),
					"type": "https://layr8.io/protocols/echo/1.0/response",
					"thid": outbound.ThID,
					"body": map[string]interface{}{"index": outbound.Body.Index},
				},
			})
			mock.sendToClient(phoenixMessage{
				Topic:   "plugins:did:web:alice",
				Event:   "message",
				Payload: resp,
			})
		}
	}

	client, _ := NewClient(Config{
		NodeURL:  wsURL,
		APIKey:   "test-key",
		AgentDID: "did:web:alice",
	}, discardErrors)
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
