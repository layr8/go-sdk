package layr8

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
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
	onError  ErrorHandler

	// Correlation map for Request/Response pattern
	pending sync.Map // threadID -> chan *Message

	disconnectFn func(error)
	reconnectFn  func()
}

// NewClient creates a new Layr8 client with the given configuration.
// The onError handler is called for SDK-level errors that cannot be returned
// to a direct caller (e.g., inbound parse failures, missing handlers).
// The client is not connected until Connect() is called.
func NewClient(cfg Config, onError ErrorHandler) (*Client, error) {
	resolved, err := resolveConfig(cfg)
	if err != nil {
		return nil, err
	}

	if onError == nil {
		return nil, errors.New("ErrorHandler must not be nil")
	}

	return &Client{
		cfg:      resolved,
		registry: newHandlerRegistry(),
		agentDID: resolved.AgentDID,
		onError:  onError,
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

// DID returns the agent's DID — either the one provided in Config
// or the ephemeral DID assigned by the cloud-node on Connect.
func (c *Client) DID() string {
	return c.agentDID
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
		c.onError(SDKError{
			Kind:      ErrParseFailure,
			Raw:       payload,
			Cause:     err,
			Timestamp: time.Now(),
		})
		return
	}

	// Check if this is a response to a pending Request (by thread ID)
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

	// Route to registered handler
	entry, ok := c.registry.lookup(msg.Type)
	if !ok {
		c.onError(SDKError{
			Kind:      ErrNoHandler,
			MessageID: msg.ID,
			Type:      msg.Type,
			From:      msg.From,
			Timestamp: time.Now(),
		})
		return
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

	// Run handler asynchronously
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
	threadID := original.ThreadID
	if threadID == "" {
		threadID = original.ID
	}
	report := &Message{
		Type:     "https://didcomm.org/report-problem/2.0/problem-report",
		To:       []string{original.From},
		ThreadID: threadID,
		Body: &ProblemReportError{
			Code:    "e.p.xfer.cant-process",
			Comment: handlerErr.Error(),
		},
	}
	c.sendMessage(report)
}

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

	// Send DIDComm message directly as the payload (no envelope wrapper).
	// The node wraps inbound messages in context+plaintext, but outbound
	// messages are sent as bare DIDComm JSON.
	return c.transport.send("message", data)
}
