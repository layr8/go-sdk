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

// The JSON-RPC envelope, the type mapping and the result unwrapping, checked
// against a node that answers MCP requests over the real request/reply path.

func TestMCPTypeForMethod(t *testing.T) {
	base := "https://layr8.io/protocols/mcp/1.0"
	cases := map[string]string{
		"tools/call": base + "/tools-call",
		"tools/list": base + "/tools-list",
		"initialize": base + "/initialize",
	}
	for method, want := range cases {
		if got := MCPTypeForMethod(base, method); got != want {
			t.Errorf("MCPTypeForMethod(%q) = %q, want %q", method, got, want)
		}
	}
}

func TestDefaultMCPBase(t *testing.T) {
	if DefaultMCPBase != "https://layr8.io/protocols/mcp/1.0" {
		t.Fatalf("DefaultMCPBase = %q", DefaultMCPBase)
	}
}

// mcpNode answers every outbound MCP request with a scripted JSON-RPC body,
// echoing the request's thid so the client's own correlation does the work.
type mcpNode struct {
	mock *mockPhoenixServer

	mu       sync.Mutex
	reply    string // JSON-RPC body to answer with
	requests []map[string]any
}

func (n *mcpNode) sentRequests() []map[string]any {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]map[string]any(nil), n.requests...)
}

// setupMCPNode answers every MCP request with a JSON-RPC body.
func setupMCPNode(t *testing.T, reply string) (*mcpNode, string) {
	t.Helper()
	return setupMCPNodeWith(t, func(sentType, thid string) string {
		return fmt.Sprintf(
			`{"plaintext":{"id":"reply-1","type":%q,"from":"did:web:loom.localhost","thid":%q,"body":%s}}`,
			sentType+"-result", thid, reply,
		)
	})
}

// setupSilentMCPNode accepts requests and never answers them — what an
// unreachable peer looks like from here.
func setupSilentMCPNode(t *testing.T) (*mcpNode, string) {
	t.Helper()
	return setupMCPNodeWith(t, func(string, string) string { return "" })
}

// setupMCPNodeWith lets a test decide the whole inbound envelope, so a denial
// (a problem report, not a JSON-RPC error) can be delivered on the same path.
func setupMCPNodeWith(t *testing.T, replyFor func(sentType, thid string) string) (*mcpNode, string) {
	t.Helper()

	mock := newMockServer()
	node := &mcpNode{mock: mock}

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
		if msg.Event != "message" {
			return
		}

		var sent struct {
			Type     string         `json:"type"`
			ThreadID string         `json:"thid"`
			To       []string       `json:"to"`
			Body     map[string]any `json:"body"`
		}
		if err := json.Unmarshal(msg.Payload, &sent); err != nil {
			return
		}
		if sent.Body == nil || sent.Body["jsonrpc"] == nil {
			return
		}

		node.mu.Lock()
		node.requests = append(node.requests, map[string]any{
			"type": sent.Type, "to": sent.To, "body": sent.Body,
		})
		node.mu.Unlock()

		envelope := replyFor(sent.Type, sent.ThreadID)
		if envelope == "" {
			return
		}
		mock.sendToClient(phoenixMessage{
			Topic: msg.Topic, Event: "message", Payload: json.RawMessage(envelope),
		})
	}

	server := httptest.NewServer(http.HandlerFunc(mock.handler))
	t.Cleanup(server.Close)
	return node, "ws" + strings.TrimPrefix(server.URL, "http") + "/plugin_socket/websocket"
}

// mcpPeer connects a client to wsURL and returns a peer bound to a test DID.
func mcpPeer(t *testing.T, wsURL string) (*MCPPeer, context.Context) {
	t.Helper()

	client, err := NewClient(Config{
		NodeURL: wsURL, APIKey: "test-api-key", AgentDID: "did:web:alice.localhost",
	}, discardErrors)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	binding, err := client.MCP()
	if err != nil {
		t.Fatalf("MCP: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	return binding.Peer("did:web:loom.localhost"), ctx
}

func TestMCP_SendsOneJSONRPCRequestAndReturnsItsResult(t *testing.T) {
	node, wsURL := setupMCPNode(t, `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`)
	peer, ctx := mcpPeer(t, wsURL)

	var out struct {
		OK bool `json:"ok"`
	}
	if err := peer.Call(ctx, "tools/call", map[string]any{"name": "send"}, &out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !out.OK {
		t.Fatalf("result = %+v", out)
	}

	reqs := node.sentRequests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d", len(reqs))
	}
	if reqs[0]["type"] != "https://layr8.io/protocols/mcp/1.0/tools-call" {
		t.Errorf("type = %v", reqs[0]["type"])
	}
	body := reqs[0]["body"].(map[string]any)
	if body["jsonrpc"] != "2.0" || body["method"] != "tools/call" {
		t.Errorf("body = %v", body)
	}
	if body["params"] == nil {
		t.Errorf("params missing: %v", body)
	}
	if body["id"] == nil {
		t.Errorf("id missing: %v", body)
	}
}

func TestMCP_OmitsParamsEntirelyWhenThereAreNone(t *testing.T) {
	node, wsURL := setupMCPNode(t, `{"result":{}}`)
	peer, ctx := mcpPeer(t, wsURL)

	if err := peer.Call(ctx, "tools/list", nil, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}

	body := node.sentRequests()[0]["body"].(map[string]any)
	if _, present := body["params"]; present {
		t.Fatalf("params should be absent: %v", body)
	}
}

func TestMCP_IDsAreUniqueSoTwoInFlightCannotBeConfused(t *testing.T) {
	node, wsURL := setupMCPNode(t, `{"result":null}`)
	peer, ctx := mcpPeer(t, wsURL)

	if err := peer.Call(ctx, "tools/list", nil, nil); err != nil {
		t.Fatalf("first Call: %v", err)
	}
	if err := peer.Call(ctx, "tools/list", nil, nil); err != nil {
		t.Fatalf("second Call: %v", err)
	}

	reqs := node.sentRequests()
	a := reqs[0]["body"].(map[string]any)["id"]
	b := reqs[1]["body"].(map[string]any)["id"]
	if a == b {
		t.Fatalf("both calls used id %v", a)
	}
}

func TestMCP_AJSONRPCErrorBecomesAnMCPError(t *testing.T) {
	_, wsURL := setupMCPNode(t, `{"error":{"code":-32602,"message":"unknown tool","data":{"tool":"x"}}}`)
	peer, ctx := mcpPeer(t, wsURL)

	err := peer.Call(ctx, "tools/call", map[string]any{"name": "x"}, nil)

	var mcpErr *MCPError
	if !errors.As(err, &mcpErr) {
		t.Fatalf("err = %v (%T), want *MCPError", err, err)
	}
	if mcpErr.Code != -32602 || mcpErr.Message != "unknown tool" {
		t.Errorf("MCPError = %+v", mcpErr)
	}
}

func TestMCP_ADenialStaysAProblemReportNotAnMCPError(t *testing.T) {
	// The usual cause is a Verifiable Grant that never reached the wire rather
	// than one that is misconfigured — see wallet.go.
	_, wsURL := setupMCPNodeWith(t, func(_, thid string) string {
		return fmt.Sprintf(
			`{"plaintext":{"id":"denial-1","type":"https://didcomm.org/report-problem/2.0/problem-report","from":"did:web:loom.localhost","thid":%q,"body":{"code":"e.m.authz.denied","comment":"no grant covers this call"}}}`,
			thid,
		)
	})
	peer, ctx := mcpPeer(t, wsURL)

	err := peer.Call(ctx, "tools/call", map[string]any{"name": "send"}, nil)

	var prob *ProblemReportError
	if !errors.As(err, &prob) {
		t.Fatalf("err = %v (%T), want *ProblemReportError", err, err)
	}
	if prob.Code != "e.m.authz.denied" {
		t.Errorf("code = %q", prob.Code)
	}
	var mcpErr *MCPError
	if errors.As(err, &mcpErr) {
		t.Error("a denial must not read as an MCP error")
	}
}

func TestMCP_ALapsedDeadlineIsTheContextError(t *testing.T) {
	// A node that never answers: the caller's context is the bound.
	_, wsURL := setupSilentMCPNode(t)
	peer, _ := mcpPeer(t, wsURL)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if err := peer.Call(ctx, "tools/list", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestMCP_AReplyThatIsNeitherResultNorErrorIsNotSilentlyZero(t *testing.T) {
	_, wsURL := setupMCPNode(t, `{"something":"else"}`)
	peer, ctx := mcpPeer(t, wsURL)

	if err := peer.Call(ctx, "tools/list", nil, nil); err == nil {
		t.Fatal("a non-JSON-RPC reply must be an error, not a zero value")
	}
}

func TestMCP_CallToolBuildsTheParamsMCPSpecifies(t *testing.T) {
	node, wsURL := setupMCPNode(t, `{"result":{"content":[]}}`)
	peer, ctx := mcpPeer(t, wsURL)

	if err := peer.CallTool(ctx, "send_email", map[string]any{"to": "bob@example.com"}, nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	params := node.sentRequests()[0]["body"].(map[string]any)["params"].(map[string]any)
	if params["name"] != "send_email" {
		t.Errorf("name = %v", params["name"])
	}
	args, ok := params["arguments"].(map[string]any)
	if !ok || args["to"] != "bob@example.com" {
		t.Errorf("arguments = %v", params["arguments"])
	}
}

func TestMCP_CallToolSendsAnEmptyArgumentsObjectRatherThanOmittingIt(t *testing.T) {
	node, wsURL := setupMCPNode(t, `{"result":{}}`)
	peer, ctx := mcpPeer(t, wsURL)

	if err := peer.CallTool(ctx, "ping", nil, nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	params := node.sentRequests()[0]["body"].(map[string]any)["params"].(map[string]any)
	args, ok := params["arguments"].(map[string]any)
	if !ok || len(args) != 0 {
		t.Fatalf("arguments = %v", params["arguments"])
	}
}

func TestMCP_ListToolsUnwrapsTheToolsArray(t *testing.T) {
	_, wsURL := setupMCPNode(t, `{"result":{"tools":[{"name":"send_email","description":"send one"}]}}`)
	peer, ctx := mcpPeer(t, wsURL)

	tools, err := peer.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "send_email" {
		t.Fatalf("tools = %+v", tools)
	}
	// Raw carries the whole entry, so a caller needing inputSchema or
	// annotations is not blocked on this struct growing a field.
	if len(tools[0].Raw) == 0 {
		t.Error("Raw should carry the original entry")
	}
}

func TestMCP_ListToolsOnAPeerWithNoToolsKeyIsEmpty(t *testing.T) {
	_, wsURL := setupMCPNode(t, `{"result":{}}`)
	peer, ctx := mcpPeer(t, wsURL)

	tools, err := peer.ListTools(ctx)
	if err != nil || len(tools) != 0 {
		t.Fatalf("tools = %+v err = %v", tools, err)
	}
}

func TestMCP_ListToolsPropagatesAnErrorRatherThanReadingAsNoTools(t *testing.T) {
	// "I could not ask" and "there are none" are different answers, and
	// collapsing them is how a dead credential reads as an empty tool surface.
	_, wsURL := setupSilentMCPNode(t)
	peer, _ := mcpPeer(t, wsURL)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if _, err := peer.ListTools(ctx); err == nil {
		t.Fatal("ListTools must not report an empty tool surface when it could not ask")
	}
}

func TestMCP_InitializeNamesTheSDKByDefault(t *testing.T) {
	node, wsURL := setupMCPNode(t, `{"result":{"protocolVersion":"2025-06-18"}}`)
	peer, ctx := mcpPeer(t, wsURL)

	if _, err := peer.Initialize(ctx, nil); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	body := node.sentRequests()[0]["body"].(map[string]any)
	if body["method"] != "initialize" {
		t.Errorf("method = %v", body["method"])
	}
	info := body["params"].(map[string]any)["clientInfo"].(map[string]any)
	if info["name"] != "layr8-go-sdk" {
		t.Errorf("clientInfo.name = %v", info["name"])
	}
}

// --- Client.MCP registration ---

func TestClientMCP_SubscribesToTheProtocolBase(t *testing.T) {
	// Without the subscription the node has nowhere to deliver the
	// {base}/…-result reply, and every call times out.
	client, _ := NewClient(Config{
		NodeURL: "ws://127.0.0.1:1/plugin_socket/websocket", APIKey: "k", AgentDID: "did:web:alice",
	}, discardErrors)

	binding, err := client.MCP()
	if err != nil {
		t.Fatalf("MCP: %v", err)
	}
	if binding.Base() != DefaultMCPBase {
		t.Errorf("Base = %q", binding.Base())
	}
	if !contains(client.registry.payloadTypes(), DefaultMCPBase) {
		t.Errorf("payload types = %v", client.registry.payloadTypes())
	}
}

func TestClientMCP_IsIdempotentPerBase(t *testing.T) {
	// A second call must not error the way a duplicate Handle would.
	client, _ := NewClient(Config{
		NodeURL: "ws://127.0.0.1:1/plugin_socket/websocket", APIKey: "k", AgentDID: "did:web:alice",
	}, discardErrors)

	if _, err := client.MCP(); err != nil {
		t.Fatalf("first MCP: %v", err)
	}
	if _, err := client.MCP(); err != nil {
		t.Fatalf("second MCP: %v", err)
	}

	count := 0
	for _, p := range client.registry.payloadTypes() {
		if p == DefaultMCPBase {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("base registered %d times", count)
	}
}

func TestClientMCP_CustomBase(t *testing.T) {
	client, _ := NewClient(Config{
		NodeURL: "ws://127.0.0.1:1/plugin_socket/websocket", APIKey: "k", AgentDID: "did:web:alice",
	}, discardErrors)

	binding, err := client.MCP("https://example.com/protocols/mcp/2.0")
	if err != nil {
		t.Fatalf("MCP: %v", err)
	}
	if binding.Base() != "https://example.com/protocols/mcp/2.0" {
		t.Errorf("Base = %q", binding.Base())
	}
	if !contains(client.registry.payloadTypes(), "https://example.com/protocols/mcp/2.0") {
		t.Errorf("payload types = %v", client.registry.payloadTypes())
	}
}

func TestClientMCP_PeerBindsToADID(t *testing.T) {
	client, _ := NewClient(Config{
		NodeURL: "ws://127.0.0.1:1/plugin_socket/websocket", APIKey: "k", AgentDID: "did:web:alice",
	}, discardErrors)

	binding, _ := client.MCP()
	peer := binding.Peer("did:web:loom.localhost")

	if peer.DID() != "did:web:loom.localhost" || peer.Base() != DefaultMCPBase {
		t.Fatalf("peer = %q / %q", peer.DID(), peer.Base())
	}
}

func TestClientMCP_AfterConnectIsRefused(t *testing.T) {
	_, _, wsURL := setupMockServer(t)
	client, _ := NewClient(Config{NodeURL: wsURL, APIKey: "k", AgentDID: "did:web:alice"}, discardErrors)
	_ = client.Handle("https://layr8.io/protocols/echo/1.0/request",
		func(*Message) (*Message, error) { return nil, nil })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	if _, err := client.MCP(); !errors.Is(err, ErrAlreadyConnected) {
		t.Fatalf("err = %v, want ErrAlreadyConnected", err)
	}
}

func TestClientMCP_CallThroughAnUnconnectedClientIsAnErrorNotAHang(t *testing.T) {
	client, _ := NewClient(Config{
		NodeURL: "ws://127.0.0.1:1/plugin_socket/websocket", APIKey: "k", AgentDID: "did:web:alice",
	}, discardErrors)
	binding, _ := client.MCP()

	if _, err := binding.Peer("did:web:loom.localhost").ListTools(context.Background()); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v, want ErrNotConnected", err)
	}
}
