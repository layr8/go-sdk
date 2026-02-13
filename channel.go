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

	pendingJoin chan json.RawMessage

	msgHandler   func(payload []byte)
	disconnectFn func(error)
	reconnectFn  func()

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
			Ref:     c.nextRef(),
			Topic:   c.topic,
			Event:   "phx_leave",
			Payload: json.RawMessage(`{}`),
		}
		c.writeMsg(leaveMsg)
		return conn.Close()
	}
	return nil
}

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
