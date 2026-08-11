# Layr8 Go SDK

The official Go SDK for building agents on the [Layr8](https://layr8.com) platform. Agents connect to Layr8 cloud-nodes via WebSocket and exchange [DIDComm v2](https://identity.foundation/didcomm-messaging/spec/) messages with other agents across the network.

Full documentation at [docs.layr8.io/build/go-sdk](https://docs.layr8.io/build/go-sdk).

## Installation

```bash
go get github.com/layr8/go-sdk
```

Requires Go 1.25 or later.

## Quick Start

```go
package main

import (
    "context"
    "log"
    "os"
    "os/signal"

    layr8 "github.com/layr8/go-sdk"
)

func main() {
    client, err := layr8.NewClient(layr8.Config{
        NodeURL:  "ws://localhost:4000/plugin_socket/websocket",
        APIKey:   "your-api-key",
        AgentDID: "did:web:myorg:my-agent",
    }, layr8.LogErrors(log.Default()))
    if err != nil {
        log.Fatal(err)
    }

    client.Handle("https://layr8.io/protocols/echo/1.0/request",
        func(msg *layr8.Message) (*layr8.Message, error) {
            var body struct{ Message string `json:"message"` }
            msg.UnmarshalBody(&body)

            return &layr8.Message{
                Type: "https://layr8.io/protocols/echo/1.0/response",
                Body: map[string]string{"echo": body.Message},
            }, nil
        },
    )

    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()

    client.Connect(ctx)
    defer client.Close()

    <-ctx.Done()
}
```

## Core Concepts

### Config

Configuration can be set explicitly or via environment variables (used as fallbacks when the field is empty).

| Field | Env Variable | Required | Description |
|---|---|---|---|
| `NodeURL` | `LAYR8_NODE_URL` | Yes | WebSocket URL of the cloud-node |
| `APIKey` | `LAYR8_API_KEY` | Yes | API key for authentication |
| `AgentDID` | `LAYR8_AGENT_DID` | Yes | Agent DID identity |
| `Persistent` | -- | No | Persist DID keys across node restarts |
| `Protocols` | -- | No | Additional protocol URIs to advertise on join (for sender-only actors) |
| `AttachGrants` | `LAYR8_ATTACH_GRANTS` | No | Attach Verifiable Grants to outbound messages. Default on |
| `GrantCacheTTL` | `LAYR8_GRANT_CACHE_MS` | No | How long held grants are cached. Default 60s |
| `GrantReadTimeout` | `LAYR8_GRANT_READ_TIMEOUT_MS` | No | Deadline on the credential read. Default 2s |
| `RESTTimeout` | `LAYR8_REST_TIMEOUT_MS` | No | Deadline on every other REST call. Default 30s; negative for none |
| `OnGrantMiss` | -- | No | Called when a grant was needed and not attached — see [Verifiable Grants](#verifiable-grants) |

```go
// Explicit configuration
client, err := layr8.NewClient(layr8.Config{
    NodeURL:  "ws://localhost:4000/plugin_socket/websocket",
    APIKey:   "my-api-key",
    AgentDID: "did:web:myorg:my-agent",
}, layr8.LogErrors(log.Default()))

// Environment-only configuration (set LAYR8_NODE_URL, LAYR8_API_KEY, LAYR8_AGENT_DID)
client, err := layr8.NewClient(layr8.Config{}, layr8.LogErrors(log.Default()))
```

### Handlers

Register handlers with `client.Handle()` before calling `Connect()`. The SDK auto-derives protocol base URIs from message types and registers them with the cloud-node. The problem report protocol (`https://didcomm.org/report-problem/2.0`) is always included, ensuring at least one protocol is present. The cloud-node requires at least one protocol on join.

A handler receives a `*Message` and returns:

| Return value | Behavior |
|---|---|
| `(&Message{...}, nil)` | Sends response to sender. `From`, `To`, `ThreadID` auto-filled. |
| `(nil, nil)` | No response sent. |
| `(nil, ErrPass)` | Declines the message. Cloud-node tries the next matching handler. |
| `(nil, error)` | Sends a DIDComm [problem report](https://identity.foundation/didcomm-messaging/spec/#problem-reports) to the sender. |

### Wildcard Handler

Use `HandleAll` to catch any message type not matched by a specific `Handle()` call:

```go
client.HandleAll(func(msg *layr8.Message) (*layr8.Message, error) {
    log.Printf("received %s from %s", msg.Type, msg.From)
    return nil, nil
})
```

Dispatch priority: specific handler > catch-all > auto-pass to cloud-node.

### Send

Send a one-way message. By default waits for server acknowledgment:

```go
err := client.Send(ctx, &layr8.Message{
    Type: "https://didcomm.org/basicmessage/2.0/message",
    To:   []string{"did:web:other-org:their-agent"},
    Body: ChatMessage{Content: "hello!"},
})
```

Use `WithFireAndForget()` to skip the server ack:

```go
err := client.Send(ctx, msg, layr8.WithFireAndForget())
```

### Request

Send a message and block until a correlated response arrives:

```go
resp, err := client.Request(ctx, &layr8.Message{
    Type: "https://layr8.io/protocols/echo/1.0/request",
    To:   []string{"did:web:other-org:echo-agent"},
    Body: EchoRequest{Message: "ping"},
})

var result EchoResponse
resp.UnmarshalBody(&result)
```

Thread correlation is automatic. Use `WithParentThread(pthid)` for nested conversations.

### Messages

`Message` represents a DIDComm v2 message:

| Field | Type | Description |
|---|---|---|
| `ID` | `string` | Unique message ID (auto-generated if empty) |
| `Type` | `string` | DIDComm message type URI |
| `From` | `string` | Sender DID (auto-filled from client) |
| `To` | `[]string` | Recipient DIDs |
| `ThreadID` | `string` | Thread correlation ID |
| `ParentThreadID` | `string` | Parent thread for nested conversations |
| `Body` | `any` | Message payload (serialized to JSON) |
| `Context` | `*MessageContext` | Cloud-node metadata (inbound only) |
| `Attachments` | `[]Attachment` | DIDComm v2 attachments |

Decode inbound message bodies with `msg.UnmarshalBody(&target)`. Inbound `Context` includes `Recipient` (string), `Authorized` (bool), and `SenderCredentials` (`[]SenderCredential`).

## Durable Handlers

Use `WithManualAck()` to acknowledge messages only after successful processing. Unacknowledged messages are redelivered by the cloud-node.

```go
client.Handle("https://layr8.io/protocols/order/1.0/created",
    func(msg *layr8.Message) (*layr8.Message, error) {
        if err := persistToDB(msg); err != nil {
            return nil, err // NOT acked -- cloud-node will redeliver
        }
        msg.Ack()
        return nil, nil
    },
    layr8.WithManualAck(),
)
```

## Verifiable Grants

The cloud-node requires a Verifiable Grant for anything its policy does not allow outright. **The SDK attaches the grants covering each outbound message automatically** — on `Send`, on `Request`, and on a handler's reply — so there is nothing to wire up. Turn it off with `Config{AttachGrants: &off}`.

Selection mirrors the policy and deliberately errs wide: everything that plausibly applies goes on the wire, because over-attaching is free (the policy allows on the first passing grant) while withholding one costs a working call and fails silently. Validity and revocation are the node's decision, not this side's.

```go
// A grant you were just given is invisible until the cache lapses (60s).
// If you have just been told you were granted something, say so:
client.RefreshGrants("")
```

### When a message goes out with nothing attached

The node's denial names the grant it could not find, which reads as "your grant is misconfigured" when the truth is "no credential was ever put on the wire". Only the sender knows which one it was. Wire `OnGrantMiss` and the next such incident is one log line:

```go
client, err := layr8.NewClient(layr8.Config{
    OnGrantMiss: func(info layr8.GrantMissInfo) {
        log.Printf("grant miss: %+v", info)
    },
}, layr8.LogErrors(log.Default()))
```

It fires in three cases, distinguished by which field is set:

| Field | Meaning |
|---|---|
| `DenialCode` | The node denied a message we sent with **nothing attached** |
| `Capped` | More grants covered the message than fit on it (`{Covering: n, Attached: 16}`) |
| `Err` | The grants could not be **read** — every send after this is flying blind |

It deliberately does **not** fire merely because a message went out unattached: most traffic (discovery, trust-ping, problem reports) needs no grant, and a diagnostic that fires constantly is one nobody reads when it matters.

### Attaching one by hand

`MediaType` is the only field the node's credential extractor filters on, by exact string equality, and it drops everything else **silently** — producing a denial byte-for-byte identical to the one for attaching nothing. Attach the credential **bare**; a Verifiable Presentation (`application/vp+jwt`) is dropped on that rule.

```go
layr8.Attachment{
    ID:        "urn:uuid:…",
    MediaType: "application/vc+jwt",
    Data:      layr8.AttachmentData{JWS: compactJWS},
}
```

## MCP (tool calling) over DIDComm

Layr8 services expose an MCP surface as DIDComm request/reply. `client.MCP()` removes the boilerplate — the protocol subscription, the type mapping (`tools/call` → `{base}/tools-call`), the JSON-RPC envelope, and unwrapping `result`.

It must be called **before** `Connect`, like `Handle`: it registers the protocol subscription the node needs in order to deliver replies.

```go
mcp, err := client.MCP()               // default base: mcp/1.0
if err := client.Connect(ctx); err != nil { ... }

loom := mcp.Peer(loomDID)

info, err := loom.Initialize(ctx, nil)
tools, err := loom.ListTools(ctx)

var out MyResult
err = loom.CallTool(ctx, "create_workflow", map[string]any{"name": "onboarding"}, &out)
```

`*MCPError` is returned when the peer answers with a JSON-RPC `error`; a DIDComm-level failure — including an authorization denial — returns `*ProblemReportError`, and an unanswered call returns the context's error.

## Watching for changes (SpaceWatcher)

Nothing on the wire tells an SDK "your wallet changed" or "a resource came up", so both are polled. `SpaceWatcher` is the one place that loop lives, on semantics shared with every other Layr8 SDK.

```go
watcher := layr8.NewSpaceWatcher(layr8.SpaceWatcherOptions{
    FetchWallet:       listMyGrantIDs,
    FetchResources:    listMCPInstanceDIDs,
    OnWalletChange:    func([]string) { rebuildTools() },
    OnResourcesChange: func(rs []string) { rebuildRoutes(rs) },
})
watcher.Start(ctx)     // seeds both baselines silently
defer watcher.Stop()

watcher.RefreshWallet(ctx)  // pull the next check forward
```

Neither callback fires on the first successful poll — a cold start is not a change. A fetch error never wipes state: it goes to `OnError` and the last-accepted value is retained, so a transient failure never reads as "everything disappeared". An empty *resource* result is only believed after two consecutive empty polls, since a directory answering with nothing is as likely to be a keepalive blip as a real teardown; an empty *wallet* is believed immediately, because that is a real answer.

## W3C Verifiable Credentials

Sign, verify, store, list, and retrieve [W3C Verifiable Credentials](https://www.w3.org/TR/vc-data-model-2.0/) via the cloud-node's REST API.

```go
// Sign
cred := layr8.Credential{
    Context:           []string{"https://www.w3.org/ns/credentials/v2"},
    Type:              []string{"VerifiableCredential"},
    Issuer:            client.DID(),
    CredentialSubject: map[string]any{"id": holderDID, "name": "Alice"},
}
signedJWT, err := client.SignCredential(ctx, cred)

// Verify
verified, err := client.VerifyCredential(ctx, signedJWT)

// Store, List, Get
stored, err := client.StoreCredential(ctx, signedJWT)
creds, err := client.ListCredentials(ctx)
fetched, err := client.GetCredential(ctx, stored.ID)
```

Sign options: `WithIssuerDID(did)`, `WithCredentialFormat(format)`. Verify options: `WithVerifierDID(did)`. Store options: `WithHolderDID(did)`, `WithStoreMeta(issuerDID, validUntil)`. List options: `WithListHolderDID(did)`. Formats: `FormatCompactJWT` (default), `FormatJSON`, `FormatJWT`, `FormatEnveloped`.

## W3C Verifiable Presentations

Wrap signed credentials into a holder-signed presentation envelope.

> **A presentation is not how you authorize a message.** The node keeps only attachments whose `media_type` is exactly `application/vc+jwt` and drops a `vp+jwt` silently — an identical denial to attaching nothing. Attach the credential bare, or let the SDK do it, which it does by default. See [Verifiable Grants](#verifiable-grants).

```go
// Sign
signedPres, err := client.SignPresentation(ctx, []string{signedJWT},
    layr8.WithNonce("challenge-from-verifier"),
)

// Verify
verified, err := client.VerifyPresentation(ctx, signedPres)
```

Sign options: `WithPresentationHolderDID(did)`, `WithPresentationFormat(format)`, `WithNonce(nonce)`. Verify options: `WithPresentationVerifierDID(did)`.

## Connection Lifecycle

**Agent DID:** `AgentDID` is required — it's the DID your agent connects as and the address other agents use to reach it. Set it via `Config` or the `LAYR8_AGENT_DID` env var; read it back at runtime with `client.DID()`. Set `Persistent: true` to persist the DID's keys across node restarts.

**Reconnection:** The SDK automatically reconnects with exponential backoff (1s to 30s) when the connection drops. During reconnection, `Send()` and `Request()` return `ErrNotConnected`.

```go
client.OnDisconnect(func(err error) {
    log.Printf("disconnected: %v", err)
})
client.OnReconnect(func() {
    log.Println("reconnected")
})
```

## Error Handling

`NewClient` requires an `ErrorHandler` for SDK-level errors (parse failures, missing handlers, panics, server rejections, transport errors):

```go
// Built-in logger
client, err := layr8.NewClient(cfg, layr8.LogErrors(log.Default()))

// Custom handler
client, err := layr8.NewClient(cfg, func(e layr8.SDKError) {
    slog.Error("sdk error", "kind", e.Kind, "error", e.Cause)
})
```

Error kinds: `ErrParseFailure`, `ErrNoHandler`, `ErrHandlerPanic`, `ErrServerReject`, `ErrTransportWrite`.

**Problem reports:** Handler errors automatically send DIDComm problem reports. `Request()` returns `*ProblemReportError` when the remote agent reports an error.

**Sentinel errors:** `ErrNotConnected`, `ErrAlreadyConnected`, `ErrClientClosed`.

**Connection errors:** `Connect()` returns `*ConnectionError` on failure. REST API calls return `*RESTError`.

## Examples

The [examples/](examples/) directory contains runnable agents:

| Example | Description |
|---|---|
| [echo-agent](examples/echo-agent) | Request/response echo service with auto-ack and ping loop |
| [chat](examples/chat) | Interactive DIDComm basic messaging client |
| [http-agent](examples/http-agent) | DIDComm-to-HTTP proxy agent |
| [postgres-agent](examples/postgres-agent) | SQL query agent with manual ack |
| [durable-handler](examples/durable-handler) | Persist-then-ack pattern with JSON-lines file |

```bash
LAYR8_API_KEY=your-key go run ./examples/echo-agent
```

## Development

```bash
make test        # Run unit tests
make test-race   # Tests with race detector
make lint        # Run golangci-lint
make build       # Build all packages
make examples    # Build example agents
```

Requires Go 1.25+ and optionally [golangci-lint](https://golangci-lint.run/).

## Compat Suite

The `compat/` directory contains integration scenarios that test the SDK against real cloud-nodes. It's a separate Go module.

```bash
cd compat && go build ./...              # Build scenarios
cd compat && go run ./cmd/compat/ --list-scenarios  # List available scenarios
```

Scenarios: `echo`, `pass`, `wildcard`, `disconnected`. See [notes/features/prd-go-sdk-compat.md](notes/features/prd-go-sdk-compat.md) for architecture details.

The CI pipeline builds the compat module on every PR. On release, a Docker image is published to `ghcr.io/layr8/go-sdk/compat:{version}` and the cross-language compatibility gate is triggered.

## Architecture

```
Client          -> public API (Connect, Send, Request, Handle, Close)
  |-- Config    -> configuration with env var fallback
  |-- Message   -> DIDComm v2 message envelope
  |-- Handler   -> message type -> handler function registry
  \-- Transport -> WebSocket/Phoenix Channel (pluggable interface)
```

The transport layer implements Phoenix Channel V2 over WebSocket with join negotiation, heartbeats, acknowledgment, and automatic reconnection. Credentials and presentations use the cloud-node's REST API.

## Links

- [Layr8 Documentation](https://docs.layr8.io)
- [Go SDK Guide](https://docs.layr8.io/build/go-sdk)
- [DIDComm v2 Specification](https://identity.foundation/didcomm-messaging/spec/)
- [W3C Verifiable Credentials](https://www.w3.org/TR/vc-data-model-2.0/)

## License

Copyright Layr8 Inc. All rights reserved.
