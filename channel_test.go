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
// Uses Phoenix V2 JSON array format: [join_ref, ref, topic, event, payload].
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
		msg, err := unmarshalPhoenixMsg(data)
		if err != nil {
			continue
		}

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
		data, _ := marshalPhoenixMsg(msg)
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

	// Server sends a message to client (topic must match channel's topic)
	mock.sendToClient(phoenixMessage{
		Topic:   ch.topic,
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

func TestPhoenixChannel_JoinRejected_IncludesReason(t *testing.T) {
	mock := newMockServer()
	mock.onMsg = func(msg phoenixMessage) {
		if msg.Event == "phx_join" {
			mock.sendToClient(phoenixMessage{
				JoinRef: msg.Ref,
				Ref:     msg.Ref,
				Topic:   msg.Topic,
				Event:   "phx_reply",
				Payload: json.RawMessage(`{"status":"error","response":{"reason":"e.connect.plugin.failed: protocols_already_bound"}}`),
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
	if err == nil {
		ch.close()
		t.Fatal("expected error from rejected join")
	}

	connErr, ok := err.(*ConnectionError)
	if !ok {
		t.Fatalf("expected *ConnectionError, got %T: %v", err, err)
	}

	if !strings.Contains(connErr.Reason, "protocols_already_bound") {
		t.Errorf("error reason should contain server reason, got: %s", connErr.Reason)
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
