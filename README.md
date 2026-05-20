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
| `AgentDID` | `LAYR8_AGENT_DID` | No | Agent DID identity (ephemeral if omitted) |
| `Persistent` | -- | No | Persist DID keys across node restarts |
| `Protocols` | -- | No | Additional protocol URIs to advertise on join (for sender-only actors) |

```go
// Explicit configuration
client, err := layr8.NewClient(layr8.Config{
    NodeURL:  "ws://localhost:4000/plugin_socket/websocket",
    APIKey:   "my-api-key",
    AgentDID: "did:web:myorg:my-agent",
}, layr8.LogErrors(log.Default()))

// Environment-only configuration (set LAYR8_NODE_URL, LAYR8_API_KEY)
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

**DID assignment:** If no `AgentDID` is configured, the cloud-node assigns an ephemeral DID on connect. Retrieve it with `client.DID()`. Set `Persistent: true` to persist DID keys across node restarts.

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

The CI pipeline builds the compat module on every PR. On release, a Docker image is published to `ghcr.io/layr8/go-sdk/compat:{version}` and the compat-suite orchestrator is triggered.

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
