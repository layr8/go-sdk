package layr8

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Client-level Verifiable Grant attachment: what actually reaches the wire.
//
// The mock node serves BOTH the Phoenix WebSocket and /api/v1/credentials on
// one httptest server, exactly as a real cloud-node does — the REST base URL is
// derived from the WebSocket URL, so they share a port either way. That makes
// the REST shape, the attachment shape and the marshalled envelope all real
// here rather than checked against a mock's beliefs about them.

type grantNode struct {
	mock *mockPhoenixServer

	mu sync.Mutex
	// credentials is what /api/v1/credentials answers with.
	credentials []map[string]json.RawMessage
	status      int
	block       chan struct{} // when non-nil, the handler waits on it
	reads       int
	lastQuery   string
	lastAPIKey  string
}

func (n *grantNode) readCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.reads
}

func (n *grantNode) serveCredentials(w http.ResponseWriter, r *http.Request) {
	n.mu.Lock()
	n.reads++
	n.lastQuery = r.URL.Query().Get("holder_did")
	n.lastAPIKey = r.Header.Get("x-api-key")
	block := n.block
	status := n.status
	creds := n.credentials
	n.mu.Unlock()

	if block != nil {
		// Accept the connection and say nothing — what a hung node looks like
		// from here.
		select {
		case <-block:
		case <-r.Context().Done():
			return
		}
	}

	if status != 0 && status >= 400 {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
		return
	}

	if creds == nil {
		creds = []map[string]json.RawMessage{}
	}
	body, _ := json.Marshal(map[string]any{"credentials": creds})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// setupGrantNode returns a node speaking both Phoenix over WebSocket and the
// credential REST API, plus the ws:// URL to point a client at.
func setupGrantNode(t *testing.T) (*grantNode, string) {
	t.Helper()

	mock := newMockServer()
	mock.onMsg = func(msg phoenixMessage) {
		if msg.Event == "phx_join" {
			mock.sendToClient(phoenixMessage{
				JoinRef: msg.Ref, Ref: msg.Ref, Topic: msg.Topic, Event: "phx_reply",
				Payload: json.RawMessage(`{"status":"ok","response":{"did":"did:web:node:test"}}`),
			})
			return
		}
		if msg.Ref != "" {
			mock.sendToClient(phoenixMessage{
				Ref: msg.Ref, Topic: msg.Topic, Event: "phx_reply",
				Payload: json.RawMessage(`{"status":"ok","response":{}}`),
			})
		}
	}

	node := &grantNode{mock: mock}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/credentials") {
			node.serveCredentials(w, r)
			return
		}
		mock.handler(w, r)
	}))
	t.Cleanup(server.Close)

	return node, "ws" + strings.TrimPrefix(server.URL, "http") + "/plugin_socket/websocket"
}

func coveringRecord(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	return grantRecord(t, grantOpts{})
}

func rawJWTOf(t *testing.T, rec map[string]json.RawMessage) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(rec["credential_jwt"], &s); err != nil {
		t.Fatalf("fixture jwt: %v", err)
	}
	return s
}

func connectedClient(t *testing.T, wsURL string, cfg Config) (*Client, context.Context) {
	t.Helper()

	cfg.NodeURL = wsURL
	if cfg.APIKey == "" {
		cfg.APIKey = "test-api-key"
	}
	if cfg.AgentDID == "" {
		cfg.AgentDID = "did:web:alice.localhost"
	}

	client, err := NewClient(cfg, discardErrors)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	// At least one handler so protocols are derived.
	_ = client.Handle("https://layr8.io/protocols/echo/1.0/request",
		func(msg *Message) (*Message, error) { return nil, nil })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	return client, ctx
}

func toolCall() *Message {
	return &Message{
		Type: "https://layr8.io/protocols/mcp/1.0/tools-call",
		To:   []string{"did:web:bob.localhost"},
		Body: map[string]any{
			"jsonrpc": "2.0", "method": "tools/call",
			"params": map[string]any{"name": "send"},
		},
	}
}

// sentAttachments returns the attachments on the first "message" event the node
// received.
func sentAttachments(t *testing.T, node *grantNode) []map[string]any {
	t.Helper()
	for _, msg := range node.mock.getReceived() {
		if msg.Event != "message" {
			continue
		}
		var env struct {
			Attachments []map[string]any `json:"attachments"`
		}
		if err := json.Unmarshal(msg.Payload, &env); err != nil {
			t.Fatalf("unmarshal sent message: %v", err)
		}
		return env.Attachments
	}
	t.Fatal("the node never received a message event")
	return nil
}

func TestClient_Grants_CoveringGrantRidesOutAsVCJWT(t *testing.T) {
	node, wsURL := setupGrantNode(t)
	rec := coveringRecord(t)
	node.credentials = []map[string]json.RawMessage{rec}

	client, ctx := connectedClient(t, wsURL, Config{})
	if err := client.Send(ctx, toolCall()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	atts := sentAttachments(t, node)
	if len(atts) != 1 {
		t.Fatalf("attachments = %d, want 1", len(atts))
	}
	// media_type is the ONLY field the node's extractor filters on, by exact
	// string equality; anything else is dropped silently and denied identically
	// to attaching nothing.
	if atts[0]["media_type"] != "application/vc+jwt" {
		t.Errorf("media_type = %v", atts[0]["media_type"])
	}
	data, _ := atts[0]["data"].(map[string]any)
	if data["jws"] != rawJWTOf(t, rec) {
		t.Errorf("data = %v", data)
	}

	node.mu.Lock()
	defer node.mu.Unlock()
	if node.lastQuery != "did:web:alice.localhost" {
		t.Errorf("holder_did query = %q", node.lastQuery)
	}
	if node.lastAPIKey != "test-api-key" {
		t.Errorf("x-api-key = %q", node.lastAPIKey)
	}
}

func TestClient_Grants_NothingCoveringSendsNoAttachments(t *testing.T) {
	node, wsURL := setupGrantNode(t)

	client, ctx := connectedClient(t, wsURL, Config{})
	if err := client.Send(ctx, toolCall()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	if atts := sentAttachments(t, node); len(atts) != 0 {
		t.Fatalf("attachments = %v, want none", atts)
	}
}

func TestClient_Grants_CallerSuppliedAttachmentsAreNeverDisplaced(t *testing.T) {
	// Someone passing their own has a reason, and silently overriding it would
	// be the second confusing thing to happen to that message.
	node, wsURL := setupGrantNode(t)
	node.credentials = []map[string]json.RawMessage{coveringRecord(t)}

	client, ctx := connectedClient(t, wsURL, Config{})
	msg := toolCall()
	msg.Attachments = []Attachment{{
		ID: "mine", MediaType: "application/vc+jwt", Data: AttachmentData{JWS: "caller.jwt.x"},
	}}
	if err := client.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	atts := sentAttachments(t, node)
	if len(atts) != 1 || atts[0]["id"] != "mine" {
		t.Fatalf("attachments = %v", atts)
	}
	if node.readCount() != 0 {
		t.Errorf("the wallet was read anyway (%d times)", node.readCount())
	}
}

func TestClient_Grants_AttachGrantsOffReadsNothing(t *testing.T) {
	node, wsURL := setupGrantNode(t)
	node.credentials = []map[string]json.RawMessage{coveringRecord(t)}

	off := false
	client, ctx := connectedClient(t, wsURL, Config{AttachGrants: &off})
	if client.wallet != nil {
		t.Fatal("wallet should be nil when AttachGrants is off")
	}
	if err := client.Send(ctx, toolCall()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	if node.readCount() != 0 {
		t.Errorf("credentials were read %d times", node.readCount())
	}
}

func TestClient_Grants_ReadOnceThenCachedAndRefreshable(t *testing.T) {
	node, wsURL := setupGrantNode(t)
	node.credentials = []map[string]json.RawMessage{coveringRecord(t)}

	client, ctx := connectedClient(t, wsURL, Config{})
	if err := client.Send(ctx, toolCall()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := client.Send(ctx, toolCall()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if node.readCount() != 1 {
		t.Fatalf("reads = %d, want 1 (cached)", node.readCount())
	}

	// A grant minted seconds ago is invisible until the TTL lapses; an agent
	// that has just been told it was granted something should not have to wait
	// out a timer it cannot see.
	client.RefreshGrants("")
	if err := client.Send(ctx, toolCall()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if node.readCount() != 2 {
		t.Fatalf("reads = %d, want 2 after RefreshGrants", node.readCount())
	}
}

func TestClient_Grants_ReadFailureIsAnnouncedAndDoesNotBlockTheSend(t *testing.T) {
	// The node is the authority on whether this message needed a grant, and most
	// traffic needs none; refusing here on a transient failure would take down
	// calls that were never going to need us.
	node, wsURL := setupGrantNode(t)
	node.status = http.StatusUnauthorized

	var mu sync.Mutex
	var misses []GrantMissInfo
	client, ctx := connectedClient(t, wsURL, Config{
		OnGrantMiss: func(info GrantMissInfo) {
			mu.Lock()
			defer mu.Unlock()
			misses = append(misses, info)
		},
	})

	if err := client.Send(ctx, toolCall()); err != nil {
		t.Fatalf("the send was blocked by a wallet failure: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(misses) != 1 || misses[0].Err == nil {
		t.Fatalf("misses = %+v", misses)
	}
	if len(misses[0].To) != 1 || misses[0].To[0] != "did:web:bob.localhost" {
		t.Errorf("To = %v", misses[0].To)
	}
}

func TestClient_Grants_AHungNodeCostsOneDeadlineNotAnUnboundedStall(t *testing.T) {
	node, wsURL := setupGrantNode(t)
	release := make(chan struct{})
	node.block = release
	t.Cleanup(func() { close(release) })

	client, ctx := connectedClient(t, wsURL, Config{GrantReadTimeout: 150 * time.Millisecond})

	started := time.Now()
	if err := client.Send(ctx, toolCall()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("the grant read was not bounded (took %v)", elapsed)
	}
}

func TestClient_Grants_CapIsAnnouncedAtOnce(t *testing.T) {
	// Unlike "nothing covered it", this is never the normal shape of a message
	// that needs no grant, and it recurs on every send until someone prunes the
	// wallet.
	node, wsURL := setupGrantNode(t)
	for i := 0; i < 20; i++ {
		node.credentials = append(node.credentials, grantRecord(t, grantOpts{
			id: fmt.Sprintf("urn:uuid:g%d", i), sig: fmt.Sprintf("s%d", i),
		}))
	}

	var mu sync.Mutex
	var capped []GrantCapInfo
	client, ctx := connectedClient(t, wsURL, Config{
		OnGrantMiss: func(info GrantMissInfo) {
			if info.Capped == nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			capped = append(capped, *info.Capped)
		},
	})

	if err := client.Send(ctx, toolCall()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(capped) != 1 || capped[0].Covering != 20 || capped[0].Attached != MaxAttachedGrants {
		t.Fatalf("capped = %+v", capped)
	}
}

func TestClient_Grants_UnattachedAloneSaysNothing(t *testing.T) {
	// Discovery, trust-ping and problem reports legitimately need no grant. A
	// diagnostic that fires on every one of them is one nobody reads when it
	// matters.
	_, wsURL := setupGrantNode(t)

	var mu sync.Mutex
	fired := 0
	client, ctx := connectedClient(t, wsURL, Config{
		OnGrantMiss: func(GrantMissInfo) {
			mu.Lock()
			defer mu.Unlock()
			fired++
		},
	})

	for i := 0; i < 5; i++ {
		if err := client.Send(ctx, toolCall()); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if fired != 0 {
		t.Fatalf("OnGrantMiss fired %d times for messages that simply needed no grant", fired)
	}
}

// deliverDenial pushes an authorization problem report to the client, linked to
// threadID via pthid — which is what the node's own denial sets, and why the
// pthid lookup is the one that matches in production.
func deliverDenial(t *testing.T, node *grantNode, pthid, code string) {
	t.Helper()
	plaintext := fmt.Sprintf(
		`{"plaintext":{"id":"denial-1","type":"https://didcomm.org/report-problem/2.0/problem-report","from":"did:web:bob.localhost","to":["did:web:alice.localhost"],"pthid":%q,"body":{"code":%q,"comment":"no grant covers this call"}}}`,
		pthid, code,
	)
	node.mock.sendToClient(phoenixMessage{
		Topic: "plugin:did:web:alice.localhost", Event: "message",
		Payload: json.RawMessage(plaintext),
	})
	time.Sleep(200 * time.Millisecond)
}

func TestClient_Grants_DenialForAnUnattachedMessageIsTheCaseItExistsFor(t *testing.T) {
	node, wsURL := setupGrantNode(t)

	var mu sync.Mutex
	var misses []GrantMissInfo
	client, ctx := connectedClient(t, wsURL, Config{
		OnGrantMiss: func(info GrantMissInfo) {
			mu.Lock()
			defer mu.Unlock()
			misses = append(misses, info)
		},
	})

	msg := toolCall()
	msg.ThreadID = "thread-42"
	if err := client.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	deliverDenial(t, node, "thread-42", "e.m.authz.denied")

	mu.Lock()
	defer mu.Unlock()
	if len(misses) != 1 {
		t.Fatalf("misses = %+v", misses)
	}
	if misses[0].DenialCode != "e.m.authz.denied" {
		t.Errorf("DenialCode = %q", misses[0].DenialCode)
	}
	if misses[0].Type != "https://layr8.io/protocols/mcp/1.0/tools-call" {
		t.Errorf("Type = %q", misses[0].Type)
	}
}

func TestClient_Grants_DenialForAMessageThatDidCarryAGrantSaysNothing(t *testing.T) {
	node, wsURL := setupGrantNode(t)
	node.credentials = []map[string]json.RawMessage{coveringRecord(t)}

	var mu sync.Mutex
	fired := 0
	client, ctx := connectedClient(t, wsURL, Config{
		OnGrantMiss: func(GrantMissInfo) {
			mu.Lock()
			defer mu.Unlock()
			fired++
		},
	})

	msg := toolCall()
	msg.ThreadID = "thread-7"
	if err := client.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	deliverDenial(t, node, "thread-7", "e.m.authz.denied")

	mu.Lock()
	defer mu.Unlock()
	if fired != 0 {
		t.Fatalf("OnGrantMiss fired %d times for a message that did carry a grant", fired)
	}
}

func TestClient_Grants_NonAuthzProblemReportIsNotAGrantMiss(t *testing.T) {
	node, wsURL := setupGrantNode(t)

	var mu sync.Mutex
	fired := 0
	client, ctx := connectedClient(t, wsURL, Config{
		OnGrantMiss: func(GrantMissInfo) {
			mu.Lock()
			defer mu.Unlock()
			fired++
		},
	})

	msg := toolCall()
	msg.ThreadID = "thread-9"
	if err := client.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	deliverDenial(t, node, "thread-9", "e.p.xfer.cant-process")

	mu.Lock()
	defer mu.Unlock()
	if fired != 0 {
		t.Fatalf("OnGrantMiss fired %d times for a non-authz problem report", fired)
	}
}
