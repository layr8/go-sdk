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
