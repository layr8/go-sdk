# Layr8 Go SDK

The official Go SDK for building agents on the [Layr8](https://layr8.com) platform. Agents connect to Layr8 cloud-nodes via WebSocket and exchange [DIDComm v2](https://identity.foundation/didcomm-messaging/spec/) messages with other agents across the network.

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
    })
    if err != nil {
        log.Fatal(err)
    }

    // Handle incoming messages
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

### Client

The `Client` is the main entry point. It manages the WebSocket connection to a cloud-node, routes inbound messages to handlers, and provides methods for sending outbound messages.

```go
client, err := layr8.NewClient(layr8.Config{...})

// Register handlers before connecting
client.Handle(messageType, handlerFunc)

// Connect to the cloud-node
client.Connect(ctx)
defer client.Close()
```

### Messages

`Message` represents a DIDComm v2 message with standard fields:

```go
type Message struct {
    ID             string          // unique message ID (auto-generated if empty)
    Type           string          // DIDComm message type URI
    From           string          // sender DID (auto-filled from client)
    To             []string        // recipient DIDs
    ThreadID       string          // thread correlation ID
    ParentThreadID string          // parent thread for nested conversations
    Body           any             // message payload (serialized to JSON)
    Context        *MessageContext // cloud-node metadata (inbound only)
}
```

Decode the body of an inbound message with `UnmarshalBody`:

```go
var req MyRequest
if err := msg.UnmarshalBody(&req); err != nil {
    return nil, err // sends a DIDComm problem report to the sender
}
```

### Handlers

Handlers process inbound messages. Register them with `client.Handle()` before calling `Connect()`.

A handler receives a `*Message` and returns:

| Return value | Behavior |
|---|---|
| `(&Message{...}, nil)` | Sends response to the sender. `From`, `To`, and `ThreadID` are auto-filled. |
| `(nil, nil)` | Fire-and-forget — no response sent. |
| `(nil, error)` | Sends a DIDComm [problem report](https://identity.foundation/didcomm-messaging/spec/#problem-reports) to the sender. |

```go
client.Handle("https://layr8.io/protocols/echo/1.0/request",
    func(msg *layr8.Message) (*layr8.Message, error) {
        var req EchoRequest
        if err := msg.UnmarshalBody(&req); err != nil {
            return nil, err
        }

        return &layr8.Message{
            Type: "https://layr8.io/protocols/echo/1.0/response",
            Body: EchoResponse{Echo: req.Message},
        }, nil
    },
)
```

#### Protocol Registration

The SDK automatically derives protocol base URIs from your handler message types and registers them with the cloud-node on connect. For example, handling `https://layr8.io/protocols/echo/1.0/request` registers the protocol `https://layr8.io/protocols/echo/1.0`.

## Sending Messages

### Send (Fire-and-Forget)

Send a one-way message with no response expected:

```go
err := client.Send(ctx, &layr8.Message{
    Type: "https://didcomm.org/basicmessage/2.0/message",
    To:   []string{"did:web:other-org:their-agent"},
    Body: ChatMessage{Content: "hello!"},
})
```

### Request (Request/Response)

Send a message and block until a correlated response arrives:

```go
resp, err := client.Request(ctx, &layr8.Message{
    Type: "https://layr8.io/protocols/echo/1.0/request",
    To:   []string{"did:web:other-org:echo-agent"},
    Body: EchoRequest{Message: "ping"},
})
if err != nil {
    log.Fatal(err)
}

var result EchoResponse
resp.UnmarshalBody(&result)
fmt.Println(result.Echo) // "ping"
```

Thread correlation is automatic — the SDK generates a `ThreadID`, attaches it to the outbound message, and matches the inbound response by the same `ThreadID`.

#### Request Options

```go
// Set parent thread ID for nested conversations
resp, err := client.Request(ctx, msg, layr8.WithParentThread("parent-thread-id"))
```

## Configuration

Configuration can be set explicitly or via environment variables. Environment variables are used as fallbacks when the corresponding `Config` field is empty.

| Field | Environment Variable | Required | Description |
|---|---|---|---|
| `NodeURL` | `LAYR8_NODE_URL` | Yes | WebSocket URL of the cloud-node |
| `APIKey` | `LAYR8_API_KEY` | Yes | API key for authentication |
| `AgentDID` | `LAYR8_AGENT_DID` | No | Agent DID identity |

If `AgentDID` is not provided, the cloud-node creates an ephemeral DID on connect. Retrieve it with `client.DID()`.

```go
// Explicit configuration
client, err := layr8.NewClient(layr8.Config{
    NodeURL:  "ws://localhost:4000/plugin_socket/websocket",
    APIKey:   "my-api-key",
    AgentDID: "did:web:myorg:my-agent",
})

// Environment-only configuration
// Set LAYR8_NODE_URL, LAYR8_API_KEY, LAYR8_AGENT_DID
client, err := layr8.NewClient(layr8.Config{})
```

## Handler Options

### Manual Acknowledgment

By default, messages are acknowledged to the cloud-node before the handler runs (auto-ack). For handlers where you need guaranteed processing, use manual ack to acknowledge only after successful execution. Unacknowledged messages are redelivered by the cloud-node.

```go
client.Handle(queryType,
    func(msg *layr8.Message) (*layr8.Message, error) {
        result, err := executeQuery(msg)
        if err != nil {
            return nil, err // message NOT acked — will be redelivered
        }

        msg.Ack() // explicitly acknowledge after success
        return &layr8.Message{Type: resultType, Body: result}, nil
    },
    layr8.WithManualAck(),
)
```

## Connection Lifecycle

### DID Assignment

If no `AgentDID` is configured, the cloud-node assigns an ephemeral DID on connect:

```go
client, _ := layr8.NewClient(layr8.Config{
    NodeURL: "ws://localhost:4000/plugin_socket/websocket",
    APIKey:  "my-key",
})
client.Connect(ctx)

fmt.Println(client.DID()) // "did:web:myorg:abc123" (assigned by node)
```

### Disconnect and Reconnect Callbacks

Monitor connection state with callbacks:

```go
client.OnDisconnect(func(err error) {
    log.Printf("disconnected: %v", err)
})

client.OnReconnect(func() {
    log.Println("reconnected")
})
```

## Message Context

Inbound messages include a `Context` field with metadata from the cloud-node:

```go
client.Handle(messageType, func(msg *layr8.Message) (*layr8.Message, error) {
    if msg.Context != nil {
        fmt.Println("Recipient:", msg.Context.Recipient)
        fmt.Println("Authorized:", msg.Context.Authorized)

        for _, cred := range msg.Context.SenderCredentials {
            fmt.Printf("Sender credential: %s (%s)\n", cred.Name, cred.ID)
        }
    }
    return nil, nil
})
```

| Field | Type | Description |
|---|---|---|
| `Recipient` | `string` | The DID that received this message |
| `Authorized` | `bool` | Whether the sender is authorized by the node's policy |
| `SenderCredentials` | `[]Credential` | Verifiable credentials presented by the sender |

## Error Handling

### Problem Reports

When a handler returns an error, the SDK automatically sends a [DIDComm problem report](https://identity.foundation/didcomm-messaging/spec/#problem-reports) to the sender:

```go
client.Handle(msgType, func(msg *layr8.Message) (*layr8.Message, error) {
    return nil, fmt.Errorf("something went wrong") // sends problem report
})
```

When `Request()` receives a problem report as the response, it returns a `*ProblemReportError`:

```go
resp, err := client.Request(ctx, msg)
if err != nil {
    var prob *layr8.ProblemReportError
    if errors.As(err, &prob) {
        fmt.Printf("Remote error [%s]: %s\n", prob.Code, prob.Comment)
    }
}
```

### Connection Errors

Connection failures return a `*ConnectionError`:

```go
err := client.Connect(ctx)
if err != nil {
    var connErr *layr8.ConnectionError
    if errors.As(err, &connErr) {
        fmt.Printf("Failed to connect to %s: %s\n", connErr.URL, connErr.Reason)
    }
}
```

### Sentinel Errors

| Error | Description |
|---|---|
| `ErrNotConnected` | Operation attempted before `Connect()` or after `Close()` |
| `ErrAlreadyConnected` | `Connect()` called on an already-connected client |
| `ErrClientClosed` | `Connect()` called on a closed client |

## Examples

The [examples/](examples/) directory contains complete, runnable agents:

### Echo Agent

A minimal agent that echoes back any message it receives. Demonstrates request/response handlers with auto-ack and auto-thread correlation.

```bash
LAYR8_API_KEY=your-key go run ./examples/echo-agent
```

### Chat Client

An interactive chat client for DIDComm basic messaging. Demonstrates fire-and-forget `Send()`, inbound message handling, `MessageContext` for sender credentials, and multi-recipient messaging.

```bash
LAYR8_API_KEY=your-key go run ./examples/chat did:web:friend:chat-agent
```

### HTTP Agent

A DIDComm-to-HTTP proxy agent. Receives DIDComm query requests and forwards them to a backend REST API. Demonstrates request/response patterns with structured protocol types.

```bash
BACKEND_URL=http://localhost:3000 LAYR8_API_KEY=your-key go run ./examples/http-agent
```

### Postgres Agent

A database query agent with manual acknowledgment. Receives SQL query requests over DIDComm, executes them against PostgreSQL, and returns results. Demonstrates `WithManualAck()` for guaranteed processing — queries are only acknowledged after successful execution.

```bash
DATABASE_URL=postgres://localhost/mydb LAYR8_API_KEY=your-key go run ./examples/postgres-agent
```

## Development

### Prerequisites

- Go 1.25+
- [golangci-lint](https://golangci-lint.run/) (optional, for linting)

### Makefile Targets

```bash
make test             # Run unit tests
make test-race        # Run tests with race detector
make test-v           # Run tests with verbose output
make lint             # Run golangci-lint
make build            # Build all packages
make examples         # Build example agents
make integration-test # Run integration tests against live nodes
make clean            # Remove build artifacts
```

### Running Tests

```bash
make test
```

### Integration Testing

The integration test suite runs against live Layr8 cloud-nodes. Set up port-forwards first:

```bash
kubectl port-forward -n cust-alice-test svc/node 14000:4000 &
kubectl port-forward -n cust-bob-test svc/node 14001:4000 &
make integration-test
```

## Architecture

The SDK is structured around a small set of types:

```
Client          → public API (Connect, Send, Request, Handle, Close)
  ├── Config    → configuration with env var fallback
  ├── Message   → DIDComm v2 message envelope
  ├── Handler   → message type → handler function registry
  └── Transport → WebSocket/Phoenix Channel (pluggable interface)
```

The transport layer implements the Phoenix Channel V2 wire protocol over WebSocket, including join negotiation, heartbeats, and message acknowledgment. The transport interface is designed to be pluggable for future protocols (e.g., QUIC).

## License

Copyright Layr8 Inc. All rights reserved.
