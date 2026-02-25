package layr8

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// phoenixMessage is the internal representation of a Phoenix Channel message.
// On the wire, it uses the V2 JSON array format: [join_ref, ref, topic, event, payload].
type phoenixMessage struct {
	JoinRef string          // null on wire when empty
	Ref     string          // null on wire when empty
	Topic   string
	Event   string
	Payload json.RawMessage
}

// marshalPhoenixMsg encodes a phoenixMessage as a V2 JSON array.
func marshalPhoenixMsg(msg phoenixMessage) ([]byte, error) {
	arr := make([]interface{}, 5)

	if msg.JoinRef == "" {
		arr[0] = nil
	} else {
		arr[0] = msg.JoinRef
	}

	if msg.Ref == "" {
		arr[1] = nil
	} else {
		arr[1] = msg.Ref
	}

	arr[2] = msg.Topic
	arr[3] = msg.Event
	arr[4] = json.RawMessage(msg.Payload)

	return json.Marshal(arr)
}

// unmarshalPhoenixMsg decodes a V2 JSON array into a phoenixMessage.
func unmarshalPhoenixMsg(data []byte) (phoenixMessage, error) {
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err != nil {
		return phoenixMessage{}, fmt.Errorf("decode phoenix array: %w", err)
	}
	if len(arr) != 5 {
		return phoenixMessage{}, fmt.Errorf("expected 5-element array, got %d", len(arr))
	}

	var msg phoenixMessage

	// join_ref (nullable string)
	var joinRef *string
	if err := json.Unmarshal(arr[0], &joinRef); err == nil && joinRef != nil {
		msg.JoinRef = *joinRef
	}

	// ref (nullable string)
	var ref *string
	if err := json.Unmarshal(arr[1], &ref); err == nil && ref != nil {
		msg.Ref = *ref
	}

	// topic
	json.Unmarshal(arr[2], &msg.Topic)

	// event
	json.Unmarshal(arr[3], &msg.Event)

	// payload (keep as raw JSON)
	msg.Payload = arr[4]

	return msg, nil
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
		topic:    fmt.Sprintf("plugins:%s", agentDID),
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
	// Use a custom dialer that resolves *.localhost to loopback (RFC 6761).
	// Go's net package doesn't implement this, unlike curl and browsers.
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err == nil && isLocalhost(host) {
				addr = net.JoinHostPort("127.0.0.1", port)
			}
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
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

	joinParams := map[string]interface{}{
		"payload_types": protocols,
		"did_spec": map[string]interface{}{
			"mode":    "Create",
			"storage": "ephemeral",
			"type":    "plugin",
			"verificationMethods": []map[string]string{
				{"purpose": "authentication"},
				{"purpose": "assertionMethod"},
				{"purpose": "keyAgreement"},
			},
		},
	}

	payload, _ := json.Marshal(joinParams)

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
				DID    string `json:"did"`
				Reason string `json:"reason"`
			} `json:"response"`
		}
		json.Unmarshal(payload, &reply)
		if reply.Status != "ok" {
			reason := reply.Response.Reason
			if reason != "" {
				return &ConnectionError{URL: c.wsURL, Reason: reason}
			}
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

		msg, err := unmarshalPhoenixMsg(data)
		if err != nil {
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
	data, err := marshalPhoenixMsg(msg)
	if err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// isLocalhost returns true if host is "localhost" or a subdomain of it.
// Per RFC 6761, *.localhost should resolve to loopback.
func isLocalhost(host string) bool {
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}
