package layr8

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocket liveness defaults. These detect half-dead connections in tens of
// seconds rather than relying on the upstream LB idle timeout (AWS NLB
// default: 350s).
//
//   - pingPeriod: how often we send a WS-level ping frame to the server.
//   - pongWait:   how long we wait for ANY incoming frame (pong or message)
//     before declaring the connection dead. Must be > pingPeriod so a single
//     missed pong does not trip the deadline.
//   - writeWait:  how long any single WriteMessage / WriteControl may block
//     before failing — bounds the time a stuck TCP write buffer can hold the
//     mutex.
//
// Stored per-channel (not package vars) so tests can compress timings without
// racing with goroutines from concurrent test runs.
const (
	defaultPongWait   = 60 * time.Second
	defaultPingPeriod = 30 * time.Second
	defaultWriteWait  = 10 * time.Second
	// heartbeatInterval is the cadence at which Phoenix-layer `heartbeat`
	// events are sent to the channel topic `"phoenix"`. Must match
	// what cloud-node expects.
	heartbeatInterval = 30 * time.Second
	// heartbeatMaxSilent is the maximum time the channel may go without
	// observing ANY application-layer inbound frame (phx_reply,
	// "message" event, etc.) before the Phoenix-heartbeat watchdog
	// trips and forces a reconnect.
	//
	// Distinct from pongWait above: pongWait is the WS-level read
	// deadline, reset on every frame INCLUDING server-emitted pongs.
	// cowboy auto-pongs at the WS protocol layer even when the
	// per-tenant Phoenix Channel GenServer has stopped processing —
	// pongWait alone cannot distinguish "TCP alive + Channel hung"
	// from "TCP alive + Channel healthy".
	//
	// 2.5× heartbeatInterval — tolerates one missed reply, trips on
	// two consecutive misses. Closes layr8/go-sdk#10.
	heartbeatMaxSilent = 75 * time.Second
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
	wsURL       string
	apiKey      string
	agentDID    string
	persistent  bool
	topic       string
	dialContext func(ctx context.Context, network, addr string) (net.Conn, error)

	conn *websocket.Conn
	mu   sync.Mutex // protects conn writes, refCounter, and reconnecting

	refCounter   int
	joinRef      string
	protocols    []string // stored from connect() for reconnect
	reconnecting bool     // true while reconnect loop is running

	pendingJoin chan json.RawMessage
	pendingRefs sync.Map // ref → chan serverReply

	msgHandler   func(payload []byte)
	disconnectFn func(error)
	reconnectFn  func()
	delegatedFn  func(did string, reading *DelegatedCredentialsReading)
	refreshedFn  func(did string, reading *DelegatedCredentialsReading, revision int64)

	assignedDIDVal    string
	replyProtocolMode bool // true if server supports reply_protocol/1

	// parentDID and childNameSource are sent inside did_spec only when set,
	// so a join that names no parent puts exactly the payload on the wire it
	// put there before these fields existed. See borrowed_did.go.
	parentDID       string
	childNameSource ChildNameSource

	// delegated is nil until a join reply that names a parent arrives. Nil is
	// its own reading — "no reading" — and is never coerced into an empty
	// complete one. See DelegatedCredentialsReading.
	//
	// delegMu guards delegated and every field below it: the join writes them
	// on the dialing goroutine, a push on the read loop, and the accessors
	// read them from the caller's.
	delegMu             sync.RWMutex
	delegated           *DelegatedCredentialsReading
	ephemeralDelegation bool // true if server supports ephemeral_delegation/1
	// refreshSupported is true if the node announced
	// ephemeral_delegation_refresh/1 at the last join.
	refreshSupported bool
	// refreshRequested is true if the last join sent delegation_refresh: true.
	// A push is applied only then.
	refreshRequested bool
	// revision of the reading in delegated; reset by every join.
	revision int64

	done chan struct{}

	// Liveness timings, defaulting to the package defaults. Tests may override
	// before calling connect().
	pongWait   time.Duration
	pingPeriod time.Duration
	writeWait  time.Duration

	// lastAppFrameAt records the timestamp of the most recently observed
	// application-layer inbound frame (phx_reply, "message", phx_error,
	// etc. — anything that flows through readLoop). Distinct from the
	// WS-level read deadline reset by pongs: this measures application
	// liveness, which catches cloud-node Phoenix Channel GenServer hangs
	// that WS pong/pong cannot detect. Read+write under mu.
	lastAppFrameAt time.Time
}

func newPhoenixChannel(wsURL, apiKey, agentDID string, persistent bool, dialContext func(ctx context.Context, network, addr string) (net.Conn, error)) *phoenixChannel {
	return &phoenixChannel{
		wsURL:       wsURL,
		apiKey:      apiKey,
		agentDID:    agentDID,
		persistent:  persistent,
		dialContext: dialContext,
		topic:       fmt.Sprintf("plugins:%s", agentDID),
		done:        make(chan struct{}),
		pongWait:    defaultPongWait,
		pingPeriod:  defaultPingPeriod,
		writeWait:   defaultWriteWait,
	}
}

func (c *phoenixChannel) connect(ctx context.Context, protocols []string) error {
	c.protocols = protocols
	return c.dial(ctx)
}

// dial establishes the WebSocket connection, joins the channel, and starts
// the read loop and heartbeat. Used by both initial connect() and reconnect.
func (c *phoenixChannel) dial(ctx context.Context) error {
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
	dialFn := c.dialContext
	if dialFn == nil {
		// Default: resolve *.localhost to loopback (RFC 6761).
		// Go's net package doesn't implement this, unlike curl and browsers.
		dialFn = func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err == nil && isLocalhost(host) {
				addr = net.JoinHostPort("127.0.0.1", port)
			}
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}
	}
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		NetDialContext:   dialFn,
	}
	conn, _, err := dialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		return &ConnectionError{URL: c.wsURL, Reason: err.Error()}
	}

	// Application-level liveness check: reset the read deadline whenever ANY
	// frame arrives (data or pong). Combined with the periodic pings sent by
	// pingLoop, this detects half-dead connections (TCP alive but peer
	// unresponsive) within ~pongWait — independent of any upstream LB idle
	// timeout. Without this, ReadMessage blocks forever and pending Request()
	// calls hang until the LB tears down the TCP connection (5+ minutes on
	// AWS NLB defaults).
	conn.SetReadDeadline(time.Now().Add(c.pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(c.pongWait))
		return nil
	})

	c.mu.Lock()
	c.conn = conn
	c.refCounter = 0
	// Reset the application-layer watchdog clock so the first heartbeat
	// tick after (re)connect measures silence from "just now", not from
	// before the disconnect.
	c.lastAppFrameAt = time.Now()
	c.mu.Unlock()

	// Start reader goroutine
	go c.readLoop()

	// Send phx_join
	if err := c.join(ctx, c.protocols); err != nil {
		conn.Close()
		return err
	}

	// Start heartbeat (Phoenix channel keepalive) and ping loop (WS-level
	// liveness probe). Both are needed: heartbeat keeps the Phoenix channel
	// from being reaped by the server, ping detects half-dead transports.
	go c.heartbeatLoop()
	go c.pingLoop(conn)

	return nil
}

func (c *phoenixChannel) join(ctx context.Context, protocols []string) error {
	ref := c.nextRef()
	c.joinRef = ref

	storage := "ephemeral"
	if c.persistent {
		storage = "persistent"
	}

	didSpec := map[string]interface{}{
		"mode":    "Create",
		"storage": storage,
		"type":    "plugin",
		"verificationMethods": []map[string]string{
			{"purpose": "authentication"},
			{"purpose": "assertionMethod"},
			{"purpose": "keyAgreement"},
		},
	}
	// Sent only when set, so a join that names no parent is byte for byte the
	// payload this SDK sent before the field existed.
	if c.parentDID != "" {
		didSpec["parentDid"] = c.parentDID
	}
	// Sent only when a parent was named and somebody therefore chose a
	// borrower's name. An empty value is not sent at all, so "this client does
	// not report it" stays a third answer rather than becoming "the caller
	// chose it".
	if c.childNameSource != "" {
		didSpec["childNameSource"] = string(c.childNameSource)
	}

	joinParams := map[string]interface{}{
		"payload_types":  protocols,
		"reply_protocol": true,
		"did_spec":       didSpec,
	}
	// Ask the node to keep a borrowed child's set current. Only a join that
	// names a parent borrows anything, so the key is not sent otherwise and an
	// unparented join is byte for byte what it was before.
	requestRefresh := c.parentDID != ""
	if requestRefresh {
		joinParams["delegation_refresh"] = true
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
				DID                  string          `json:"did"`
				Reason               string          `json:"reason"`
				Capabilities         []string        `json:"capabilities"`
				DelegatedCredentials json.RawMessage `json:"delegated_credentials"`
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
		c.replyProtocolMode = hasCapability(reply.Response.Capabilities, "reply_protocol/1")

		// The node omits the key when the join named no parent, and otherwise
		// sends a reading that says whether it could read the parent's wallet
		// at all. Only a well-formed reading is a reading; treating anything
		// else as an empty complete one would state that a wallet was read and
		// grants nothing, which none of those inputs says.
		reading := parseDelegatedCredentials(reply.Response.DelegatedCredentials)
		c.delegMu.Lock()
		c.ephemeralDelegation = hasCapability(reply.Response.Capabilities, "ephemeral_delegation/1")
		c.refreshSupported = hasCapability(reply.Response.Capabilities, DelegationRefreshCapability)
		c.refreshRequested = requestRefresh
		c.delegated = reading
		// A rejoin starts the revision again from the join reply, so a push on
		// the new connection is never compared with one from the old.
		c.revision = joinRevision(reply.Response.DelegatedCredentials)
		c.delegMu.Unlock()
		// UNCONDITIONAL, nil included. A rejoin whose reply carries no reading
		// is a rejoin after which the previous set must go: it was minted for a
		// DID document this rejoin may have replaced, and the node that would
		// have re-minted it did not. Firing only when there is something to
		// hand over leaves the wallet attaching the last join's credentials
		// while delegatedCredentials() reports there are none.
		if c.delegatedFn != nil {
			c.delegatedFn(c.effectiveDID(), reading)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *phoenixChannel) send(ctx context.Context, event string, payload []byte) (serverReply, error) {
	ref := c.nextRef()

	replyCh := make(chan serverReply, 1)
	c.pendingRefs.Store(ref, replyCh)
	defer c.pendingRefs.Delete(ref)

	msg := phoenixMessage{
		Ref:     ref,
		Topic:   c.topic,
		Event:   event,
		Payload: payload,
	}
	if err := c.writeMsg(msg); err != nil {
		return serverReply{}, err
	}

	select {
	case reply := <-replyCh:
		return reply, nil
	case <-ctx.Done():
		return serverReply{}, ctx.Err()
	}
}

func (c *phoenixChannel) sendFireAndForget(event string, payload []byte) error {
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
	return c.sendFireAndForget("ack", payload)
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

// effectiveDID is the DID this channel speaks as: the one the caller supplied,
// or the one the node assigned when the caller supplied none.
func (c *phoenixChannel) effectiveDID() string {
	if c.agentDID != "" {
		return c.agentDID
	}
	return c.assignedDIDVal
}

// setBorrower records the parent whose authority this channel's DID borrows,
// and who chose the borrower's name. Both are sent inside did_spec only when
// set.
func (c *phoenixChannel) setBorrower(parentDID string, source ChildNameSource) {
	c.parentDID = parentDID
	c.childNameSource = source
}

// onDelegatedCredentials registers a callback that fires after EVERY
// successful join and rejoin, with the reading the node returned or nil when
// it returned none.
func (c *phoenixChannel) onDelegatedCredentials(fn func(did string, reading *DelegatedCredentialsReading)) {
	c.delegatedFn = fn
}

// delegatedCredentials reports what this join learned about the parent's
// wallet. See Client.DelegatedCredentials.
func (c *phoenixChannel) delegatedCredentials() *DelegatedCredentialsReading {
	c.delegMu.RLock()
	defer c.delegMu.RUnlock()
	return c.delegated
}

// onDelegationRefreshed registers a callback that fires after a pushed
// replacement reading was applied, after onDelegatedCredentials has already
// run with it.
func (c *phoenixChannel) onDelegationRefreshed(fn func(did string, reading *DelegatedCredentialsReading, revision int64)) {
	c.refreshedFn = fn
}

// supportsEphemeralDelegationRefresh reports whether the node announced
// ephemeral_delegation_refresh/1 at the last join.
func (c *phoenixChannel) supportsEphemeralDelegationRefresh() bool {
	c.delegMu.RLock()
	defer c.delegMu.RUnlock()
	return c.refreshSupported
}

// applyDelegationPush handles an inbound delegated_credentials push.
//
// The push carries the WHOLE current set, so applying it replaces what is
// held; it never appends. It is dropped, and the last reading stands, when
// this join did not ask for refreshes, there is no reading to replace (the
// join named no parent), it does not parse, or its revision is not greater
// than the one held.
//
// The wallet swap happens in onDelegatedCredentials under the wallet's own
// lock, as one map assignment. A send already choosing its attachments took
// the slice before, so it uses the old set or the new one, never a mix.
func (c *phoenixChannel) applyDelegationPush(raw json.RawMessage) {
	reading, revision, ok := parseDelegationPush(raw)
	if !ok {
		return
	}
	c.delegMu.Lock()
	if !c.refreshRequested || c.delegated == nil || revision <= c.revision {
		c.delegMu.Unlock()
		return
	}
	c.delegated = reading
	c.revision = revision
	c.delegMu.Unlock()

	did := c.effectiveDID()
	if c.delegatedFn != nil {
		c.delegatedFn(did, reading)
	}
	if c.refreshedFn != nil {
		c.refreshedFn(did, reading, revision)
	}
}

// supportsEphemeralDelegation reports whether the node advertised
// ephemeral_delegation/1 at join.
func (c *phoenixChannel) supportsEphemeralDelegation() bool {
	c.delegMu.RLock()
	defer c.delegMu.RUnlock()
	return c.ephemeralDelegation
}

func (c *phoenixChannel) replyMode() bool {
	return c.replyProtocolMode
}

func (c *phoenixChannel) close() error {
	select {
	case <-c.done:
		return nil // already closed
	default:
		close(c.done) // signals reconnectLoop and heartbeatLoop to stop
	}

	c.mu.Lock()
	conn := c.conn
	c.reconnecting = false
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
				// Connection dropped (TCP error, server close, OR read deadline
				// exceeded — meaning no pong/data within pongWait, i.e. the
				// connection is half-dead). Reject pending refs and reconnect.
				c.rejectPendingRefs()
				if c.disconnectFn != nil {
					c.disconnectFn(err)
				}
				go c.reconnectLoop(err)
				return
			}
		}
		// Successful read: reset both watchdogs.
		// 1. WS-level read deadline — any frame (including pongs handled
		//    by the SetPongHandler above) proves TCP liveness.
		// 2. Application-layer watchdog — only application frames flow
		//    through ReadMessage (pongs are control frames handled by
		//    gorilla internally and never reach here), so any successful
		//    ReadMessage proves the cloud-node Phoenix Channel
		//    GenServer is still processing — distinct from cowboy
		//    auto-ponging at the WS layer.
		now := time.Now()
		conn.SetReadDeadline(now.Add(c.pongWait))
		c.mu.Lock()
		c.lastAppFrameAt = now
		c.mu.Unlock()

		msg, err := unmarshalPhoenixMsg(data)
		if err != nil {
			continue
		}

		c.handleInbound(msg)
	}
}

// pingLoop sends a WebSocket-level ping every pingPeriod. Tied to the conn
// passed in so each dial() spawns its own pingLoop that exits when its conn
// dies. WriteControl is documented as safe to call concurrently with
// WriteMessage, so this does not need to coordinate with writeMsg().
func (c *phoenixChannel) pingLoop(conn *websocket.Conn) {
	ticker := time.NewTicker(c.pingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			if err := conn.WriteControl(
				websocket.PingMessage,
				nil,
				time.Now().Add(c.writeWait),
			); err != nil {
				// Write failed — readLoop will observe the same and trigger
				// reconnect. Just exit this goroutine.
				return
			}
		}
	}
}

// reconnectLoop attempts to re-establish the connection with exponential backoff.
// It runs until the connection is restored or close() is called.
func (c *phoenixChannel) reconnectLoop(initialErr error) {
	c.mu.Lock()
	if c.reconnecting {
		c.mu.Unlock()
		return // another goroutine is already reconnecting
	}
	c.reconnecting = true
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.mu.Unlock()

	bo := newBackoff(1*time.Second, 30*time.Second)

	for {
		delay := bo.next()
		slog.Info("reconnecting", "delay", delay, "url", c.wsURL)

		select {
		case <-c.done:
			return
		case <-time.After(delay):
		}

		// Check if closed while waiting
		select {
		case <-c.done:
			return
		default:
		}

		// Temporarily clear reconnecting so dial()'s writeMsg calls succeed
		c.mu.Lock()
		c.reconnecting = false
		c.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := c.dial(ctx)
		cancel()

		if err != nil {
			c.mu.Lock()
			c.reconnecting = true
			c.mu.Unlock()
			slog.Warn("reconnect failed", "error", err, "url", c.wsURL)
			continue
		}

		// Success — reconnecting is already false
		slog.Info("reconnected", "url", c.wsURL)
		if c.reconnectFn != nil {
			c.reconnectFn()
		}
		return
	}
}

// rejectPendingRefs cancels all in-flight send() calls waiting for server replies.
func (c *phoenixChannel) rejectPendingRefs() {
	c.pendingRefs.Range(func(key, value interface{}) bool {
		ch := value.(chan serverReply)
		select {
		case ch <- serverReply{Status: "error", Reason: "disconnected"}:
		default:
		}
		c.pendingRefs.Delete(key)
		return true
	})
}

func (c *phoenixChannel) handleInbound(msg phoenixMessage) {
	switch msg.Event {
	case "phx_reply":
		// Join reply
		c.mu.Lock()
		ch := c.pendingJoin
		c.mu.Unlock()
		if ch != nil && msg.Ref == c.joinRef {
			select {
			case ch <- msg.Payload:
			default:
			}
			return
		}

		// Message send reply (ref tracking)
		if val, ok := c.pendingRefs.LoadAndDelete(msg.Ref); ok {
			replyCh := val.(chan serverReply)
			var parsed struct {
				Status   string `json:"status"`
				Response struct {
					Reason string `json:"reason"`
				} `json:"response"`
			}
			json.Unmarshal(msg.Payload, &parsed)
			select {
			case replyCh <- serverReply{Status: parsed.Status, Reason: parsed.Response.Reason}:
			default:
			}
		}
	case "message":
		if c.msgHandler != nil {
			c.msgHandler(msg.Payload)
		}
	case "delegated_credentials":
		// A replacement reading for a borrowed child. The channel decides
		// whether it applies.
		c.applyDelegationPush(msg.Payload)
	case "phx_error", "phx_close":
		err := fmt.Errorf("channel %s", msg.Event)
		c.rejectPendingRefs()
		if c.disconnectFn != nil {
			c.disconnectFn(err)
		}
		go c.reconnectLoop(err)
	}
}

func (c *phoenixChannel) heartbeatLoop() {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			// Application-layer watchdog: if no inbound app frame has
			// been observed within heartbeatMaxSilent, the cloud-node
			// Phoenix Channel GenServer is hung even though TCP +
			// cowboy pong are likely fine. Force-close the connection
			// so the reconnect path takes over.
			c.mu.Lock()
			conn := c.conn
			silentSince := c.lastAppFrameAt
			c.mu.Unlock()

			if conn != nil && time.Since(silentSince) > heartbeatMaxSilent {
				// Closing the conn makes ReadMessage in the read loop
				// return an error, which triggers reconnectLoop via the
				// existing path.
				conn.Close()
				return
			}

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
	if c.conn == nil || c.reconnecting {
		return ErrNotConnected
	}
	data, err := marshalPhoenixMsg(msg)
	if err != nil {
		return err
	}
	// Bound the time a stuck TCP write buffer can hold the mutex.
	c.conn.SetWriteDeadline(time.Now().Add(c.writeWait))
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// hasCapability checks if a capability string is present in the list.
func hasCapability(caps []string, name string) bool {
	for _, c := range caps {
		if c == name {
			return true
		}
	}
	return false
}

// isLocalhost returns true if host is "localhost" or a subdomain of it.
// Per RFC 6761, *.localhost should resolve to loopback.
func isLocalhost(host string) bool {
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}
