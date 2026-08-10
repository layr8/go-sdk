package layr8

// MCP (Model Context Protocol) over Layr8 DIDComm.
//
// A growing set of Layr8 services expose an MCP surface as
// DIDComm request/reply: a request of type {base}/<method> carrying a JSON-RPC
// 2.0 body, answered by a {base}/<method>-result message whose body is the
// JSON-RPC response. The reply echoes the request's DIDComm thid, so
// Client.Request correlates it automatically — this file just removes the
// boilerplate (protocol subscription, the {base}/… type, the JSON-RPC envelope,
// and unwrapping result / returning error).
//
// Every Layr8 SDK exposes the same binding, so a peer is called the same way
// whatever language the caller is written in.
//
// Usage — Client.MCP must be called BEFORE Connect, like Handle, because it
// registers the protocol subscription the node needs in order to deliver
// replies:
//
//	mcp, err := client.MCP()          // default base, registers subscription
//	if err := client.Connect(ctx); err != nil { ... }
//
//	loom := mcp.Peer(loomDID)
//	_, err = loom.Initialize(ctx, nil)
//	tools, err := loom.ListTools(ctx)
//	var out MyResult
//	err = loom.CallTool(ctx, "create_workflow", map[string]any{"name": name}, &out)

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
)

// DefaultMCPBase is the default MCP protocol base (mcp/1.0).
const DefaultMCPBase = "https://layr8.io/protocols/mcp/1.0"

// MCPError is returned when a peer answers a call with a JSON-RPC error object.
//
// Distinct from ProblemReportError, which is the DIDComm-level failure —
// including an authorization denial, whose usual cause is a Verifiable Grant
// that never reached the wire rather than one that is misconfigured. See
// wallet.go and Config.OnGrantMiss.
type MCPError struct {
	Code    int
	Message string
	Data    json.RawMessage
}

func (e *MCPError) Error() string {
	return fmt.Sprintf("MCP error %d: %s", e.Code, e.Message)
}

// MCPTypeForMethod returns the DIDComm type for an MCP method:
// "tools/call" → "{base}/tools-call".
func MCPTypeForMethod(base, method string) string {
	return base + "/" + strings.ReplaceAll(method, "/", "-")
}

// MCPBinding is a base-bound MCP binding, returned by Client.MCP.
// Call Peer to get a caller.
type MCPBinding struct {
	client *Client
	base   string
}

// Base returns the MCP protocol base this binding subscribed to.
func (b *MCPBinding) Base() string { return b.base }

// Peer returns a caller bound to did on this binding's protocol base.
func (b *MCPBinding) Peer(did string) *MCPPeer {
	return &MCPPeer{client: b.client, did: did, base: b.base}
}

// MCPPeer is a peer-bound MCP caller, obtained via MCPBinding.Peer.
// Each Call sends one JSON-RPC request and returns its result.
type MCPPeer struct {
	client *Client
	did    string
	base   string
	nextID atomic.Int64
}

// DID returns the peer DID this caller targets.
func (p *MCPPeer) DID() string { return p.did }

// Base returns the MCP protocol base this caller uses.
func (p *MCPPeer) Base() string { return p.base }

// Call calls an MCP method on the peer with optional params and decodes the
// JSON-RPC result into out (which may be nil to discard it).
//
// It returns *MCPError if the peer answers with an error object, or whatever
// Client.Request returns (a *ProblemReportError, or the context's error when the
// deadline lapses).
func (p *MCPPeer) Call(ctx context.Context, method string, params any, out any, opts ...RequestOption) error {
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      p.nextID.Add(1),
		"method":  method,
	}
	if params != nil {
		body["params"] = params
	}

	reply, err := p.client.Request(ctx, &Message{
		Type: MCPTypeForMethod(p.base, method),
		To:   []string{p.did},
		Body: body,
	}, opts...)
	if err != nil {
		return err
	}

	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int             `json:"code"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		} `json:"error"`
	}
	if err := reply.UnmarshalBody(&envelope); err != nil {
		return fmt.Errorf("peer returned a body that is not a JSON-RPC response: %w", err)
	}
	if envelope.Error != nil {
		return &MCPError{
			Code:    envelope.Error.Code,
			Message: envelope.Error.Message,
			Data:    envelope.Error.Data,
		}
	}
	if envelope.Result == nil {
		// A reply with neither result nor error is not a JSON-RPC response.
		// Leaving out zero-valued would be indistinguishable from a peer that
		// genuinely answered with a null result.
		return fmt.Errorf("peer returned neither result nor error")
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(envelope.Result, out)
}

// CallTool is the convenience for MCP tools/call.
func (p *MCPPeer) CallTool(ctx context.Context, name string, arguments map[string]any, out any, opts ...RequestOption) error {
	if arguments == nil {
		arguments = map[string]any{}
	}
	return p.Call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	}, out, opts...)
}

// MCPTool is one entry of an MCP tools/list result. Raw carries the whole
// entry, so a caller that needs inputSchema or annotations is not blocked on
// this struct growing a field.
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Raw         json.RawMessage `json:"-"`
}

// ListTools is the convenience for MCP tools/list; it returns the tools array.
func (p *MCPPeer) ListTools(ctx context.Context, opts ...RequestOption) ([]MCPTool, error) {
	var result struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := p.Call(ctx, "tools/list", nil, &result, opts...); err != nil {
		return nil, err
	}

	tools := make([]MCPTool, 0, len(result.Tools))
	for _, raw := range result.Tools {
		var tool MCPTool
		if err := json.Unmarshal(raw, &tool); err != nil {
			continue
		}
		tool.Raw = raw
		tools = append(tools, tool)
	}
	return tools, nil
}

// Initialize is the convenience for MCP initialize. Pass nil clientInfo to be
// announced as this SDK.
func (p *MCPPeer) Initialize(ctx context.Context, clientInfo map[string]any, opts ...RequestOption) (json.RawMessage, error) {
	if clientInfo == nil {
		clientInfo = map[string]any{"name": "layr8-go-sdk"}
	}
	var out json.RawMessage
	if err := p.Call(ctx, "initialize", map[string]any{"clientInfo": clientInfo}, &out, opts...); err != nil {
		return nil, err
	}
	return out, nil
}
