# Layr8 Go SDK — v1 Design

## Overview

A Go SDK that abstracts away Phoenix Channel/WebSocket plumbing and DIDComm message formatting, letting developers build on top of DIDComm with Layr8 using idiomatic Go patterns.

**Design principles:**
- Simple as `net/http` — familiar patterns, minimal surface area
- Concurrency-safe by design — developers use goroutines and context, SDK handles multiplexing
- Transport-agnostic public API — ready for QUIC expansion without breaking changes

## Public API Surface

Five types the developer interacts with: `Client`, `Config`, `Message`, `MessageContext`, `ProblemReportError`.

Everything else (Phoenix Channel, WebSocket, heartbeat, correlation map) is internal.

## Package Structure

```
go-sdk/
├── go.mod                  # module github.com/layr8/go-sdk
├── client.go               # Client struct, NewClient, Connect, Close, Send, Request
├── config.go               # Config struct, env-var loading
├── message.go              # Message, MessageContext structs, marshaling
├── handler.go              # Handler registration, routing
├── channel.go              # Phoenix Channel internals (unexported)
├── reconnect.go            # Reconnect logic (unexported)
├── errors.go               # ProblemReportError, sentinel errors
└── ack.go                  # Ack behavior
```

## Client Initialization & Configuration

```go
type Config struct {
    NodeURL    string   // fallback: LAYR8_NODE_URL
    APIKey     string   // fallback: LAYR8_API_KEY
    AgentDID   string   // fallback: LAYR8_AGENT_DID (empty = ephemeral DID)
}

func NewClient(cfg Config) (*Client, error)
func (c *Client) Connect(ctx context.Context) error
func (c *Client) Close() error
```

- `NewClient` validates config but does not connect.
- Empty fields fall back to environment variables.
- Empty `AgentDID` creates an ephemeral `did:web` on `Connect()`.
- `Connect` establishes WebSocket, joins Phoenix Channel with auto-derived protocols.
- `Close` is graceful — drains in-flight requests, stops heartbeat, leaves channel.

### Lifecycle

```go
client, err := layr8.NewClient(cfg)  // 1. configure
client.Handle("...", myHandler)       // 2. register handlers (derives protocols)
client.Connect(ctx)                   // 3. connect (sends derived protocols on join)
// ... use Send/Request/Handle ...
client.Close()                        // 4. shutdown
```

Handlers must be registered before `Connect()`. Calling `Handle` after `Connect` returns an error.

## Message Struct

```go
type Message struct {
    ID             string          // auto-generated UUID if empty
    Type           string          // DIDComm message type (required)
    From           string          // auto-set to AgentDID if empty
    To             []string        // recipient DIDs (required for Send/Request)
    ThreadID       string          // auto-managed by SDK
    ParentThreadID string          // opt-in, for nested threads
    Body           any             // developer's struct, JSON marshaled automatically
    Context        *MessageContext // cloud-node metadata (populated on inbound messages, nil on outbound)
}

// MessageContext contains metadata from the cloud-node, present on inbound messages.
type MessageContext struct {
    Recipient         string
    Authorized        bool
    SenderCredentials []Credential
}

type Credential struct {
    ID   string // DID of the credential subject
    Name string // human-readable name
}

func (m *Message) UnmarshalBody(v any) error
```

## Sending Messages

Two methods for two patterns:

### Send — Fire-and-Forget

Send a message without expecting a response. Returns once the message is written to the WebSocket.

```go
func (c *Client) Send(ctx context.Context, msg *Message) error
```

```go
// Chat message — no response expected
err := client.Send(ctx, &layr8.Message{
    Type: "https://didcomm.org/basicmessage/2.0/message",
    To:   []string{"did:web:bob", "did:web:charlie"},
    Body: ChatMessage{Content: "Hello everyone!", Locale: "en"},
})
```

**What happens internally on `Send`:**
1. Auto-fill `ID` (UUID), `From` (AgentDID)
2. Marshal `Body` to JSON
3. Wrap in DIDComm envelope, then Phoenix Channel message
4. Send over WebSocket
5. Return once write succeeds

### Request — Request/Response

Send a message and block until a correlated response arrives or the context expires.

```go
func (c *Client) Request(ctx context.Context, msg *Message, opts ...RequestOption) (*Message, error)
```

```go
// Database query — need a response
resp, err := client.Request(ctx, &layr8.Message{
    Type: "https://layr8.io/protocols/postgres/1.0/query",
    To:   []string{"did:web:db-agent"},
    Body: QueryRequest{Query: "SELECT * FROM users", Action: "read"},
})
if err != nil {
    return err
}

var result QueryResponse
resp.UnmarshalBody(&result)
```

**What happens internally on `Request`:**
1. Auto-fill `ID` (UUID), `From` (AgentDID), `ThreadID` (new UUID)
2. Marshal `Body` to JSON
3. Wrap in DIDComm envelope, then Phoenix Channel message
4. Register `ThreadID` in concurrent-safe correlation map with response channel
5. Send over WebSocket
6. Block on `select` — response channel or `ctx.Done()`
7. On response: remove from correlation map, return `*Message`

**Optional parent thread:**
```go
resp, err := client.Request(ctx, msg, layr8.WithParentThread(parentThreadID))
```

## Handling Inbound Messages

Handler signature mirrors `net/http` but returns a response:

```go
type HandlerFunc func(msg *Message) (*Message, error)
```

Three outcomes:

```go
// Success — SDK sends response with correct thid
client.Handle(".../query", func(msg *layr8.Message) (*layr8.Message, error) {
    var req MyRequest
    msg.UnmarshalBody(&req)
    return &layr8.Message{
        Type: ".../result",
        Body: doWork(req),
    }, nil
})

// Error — SDK sends DIDComm problem report automatically
client.Handle(".../query", func(msg *layr8.Message) (*layr8.Message, error) {
    return nil, fmt.Errorf("database unavailable")
})

// Fire-and-forget inbound — nil, nil = no response, no error
client.Handle(".../notify", func(msg *layr8.Message) (*layr8.Message, error) {
    log.Println("got notification:", msg.ID)
    return nil, nil
})
```

On response, SDK auto-fills `To` (from inbound `From`), `From` (AgentDID), and `ThreadID` (from inbound message).

Handlers can access cloud-node metadata via `msg.Context`:
```go
client.Handle("https://didcomm.org/basicmessage/2.0/message",
    func(msg *layr8.Message) (*layr8.Message, error) {
        name := msg.Context.SenderCredentials[0].Name
        var chat ChatMessage
        msg.UnmarshalBody(&chat)
        fmt.Printf("%s: %s\n", name, chat.Content)
        return nil, nil
    },
)
```

## Protocol Registration

Protocols are auto-derived from registered handlers. No manual config needed.

```go
client.Handle("https://layr8.io/protocols/my-app/1.0/query", queryHandler)
client.Handle("https://layr8.io/protocols/my-app/1.0/command", commandHandler)
// SDK derives: ["https://layr8.io/protocols/my-app/1.0"]
// Sent to cloud-node on Connect()
```

## Acknowledgment

```go
// Default — auto-ack when handler is invoked (before handler runs)
client.Handle(".../query", myHandler)

// Manual — developer controls ack timing
client.Handle(".../query", func(msg *layr8.Message) (*layr8.Message, error) {
    result, err := riskyOperation()
    if err != nil {
        return nil, err  // no ack sent, node can redeliver
    }
    msg.Ack()  // explicit ack after success
    return &layr8.Message{Type: ".../result", Body: result}, nil
}, layr8.WithManualAck())
```

## Error Handling & Problem Reports

Three error types:

| Type | When | Contains |
|------|------|----------|
| `ConnectionError` | Connect fails, connection lost | URL, reason |
| `ProblemReportError` | Remote returns DIDComm problem report | Code, Comment |
| Standard `error` | Timeouts, marshal failures | Go stdlib errors |

```go
// Request errors
resp, err := client.Request(ctx, msg)
if err != nil {
    if errors.Is(err, context.DeadlineExceeded) { ... }

    var probErr *layr8.ProblemReportError
    if errors.As(err, &probErr) {
        probErr.Code    // "e.p.xfer.cant-process"
        probErr.Comment // "database unavailable"
    }

    var connErr *layr8.ConnectionError
    if errors.As(err, &connErr) { ... }
}

// Handler errors become problem reports automatically
client.Handle(".../query", func(msg *layr8.Message) (*layr8.Message, error) {
    return nil, fmt.Errorf("not found")
    // SDK sends: problem-report with code "e.p.xfer.cant-process"
})
```

## Connection Management & Retry

Connection-level retries only (no message-level retries in v1).

**Reconnect behavior:**
- Exponential backoff: 1s, 2s, 4s, 8s, ... 30s max
- Re-joins channel with same protocols and DID
- Pending `Request` calls block during reconnect (don't fail unless ctx expires)
- Handlers re-registered automatically

**Internal goroutines (after Connect):**
1. Heartbeat — Phoenix heartbeat every 30s
2. Reader — routes WebSocket frames to handlers/correlation map
3. Reconnect — watches for disconnects, reconnects with backoff

**Observable state:**
```go
client.OnDisconnect(func(err error) { log.Println("disconnected:", err) })
client.OnReconnect(func() { log.Println("reconnected") })
```

## Concurrency — Multiple DIDs

`Request` is blocking per call. SDK multiplexes internally over one WebSocket. Fan out with standard Go:

```go
g, gCtx := errgroup.WithContext(ctx)

dids := []string{"did:web:agent-a", "did:web:agent-b", "did:web:agent-c"}
results := make([]*layr8.Message, len(dids))

for i, did := range dids {
    g.Go(func() error {
        resp, err := client.Request(gCtx, &layr8.Message{
            Type: ".../query",
            To:   []string{did},
            Body: request,
        })
        results[i] = resp
        return err
    })
}

err := g.Wait()
```

Internally:
- Each `Request` has unique thread ID + response channel in `sync.Map`
- WebSocket writes protected by mutex
- Reader goroutine routes responses by thread ID
- `errgroup` cancels remaining requests if one fails

## QUIC Extensibility

The public API is transport-agnostic. All WebSocket/Phoenix logic is isolated in `channel.go`. Future QUIC support requires:

1. Extract implicit `transport` interface from `channel.go`
2. Implement `quicTransport` behind the same interface
3. URL-scheme detection (`ws://` vs `quic://`)

Developer code stays identical:
```go
// Future — just change the URL
client, _ := layr8.NewClient(layr8.Config{
    NodeURL: "quic://localhost:4000/...",
})
```

QUIC also enables per-message streams, potentially removing the need for the correlation map (the stream *is* the correlation). But that's a future optimization.

## Full Examples

### Echo Agent (Request/Response)

```go
package main

import (
    "context"
    "log"
    "os"
    "os/signal"

    "github.com/layr8/go-sdk"
)

type EchoRequest struct {
    Message string `json:"message"`
}

type EchoResponse struct {
    Echo string `json:"echo"`
}

func main() {
    client, err := layr8.NewClient(layr8.Config{
        NodeURL:  "ws://localhost:4000/plugin_socket/websocket",
        AgentDID: "did:web:mycompany:echo-agent",
        // APIKey from LAYR8_API_KEY env
    })
    if err != nil {
        log.Fatal(err)
    }

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

    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()

    if err := client.Connect(ctx); err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    <-ctx.Done()
    log.Println("shutting down")
}
```

### Chat Client (Fire-and-Forget)

```go
package main

import (
    "bufio"
    "context"
    "fmt"
    "log"
    "os"
    "os/signal"

    "github.com/layr8/go-sdk"
)

type ChatMessage struct {
    Content string `json:"content"`
    Locale  string `json:"locale"`
}

func main() {
    client, err := layr8.NewClient(layr8.Config{
        NodeURL:  "wss://earth.node.layr8.org:443/plugin_socket/websocket",
        AgentDID: "did:web:earth.node.layr8.org:alice",
        // APIKey from LAYR8_API_KEY env
    })
    if err != nil {
        log.Fatal(err)
    }

    // Receive chat messages
    client.Handle("https://didcomm.org/basicmessage/2.0/message",
        func(msg *layr8.Message) (*layr8.Message, error) {
            name := msg.Context.SenderCredentials[0].Name
            var chat ChatMessage
            msg.UnmarshalBody(&chat)
            fmt.Printf("[%s] %s\n", name, chat.Content)
            return nil, nil // no response for chat
        },
    )

    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()

    if err := client.Connect(ctx); err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    // Read from stdin and send
    scanner := bufio.NewScanner(os.Stdin)
    for scanner.Scan() {
        err := client.Send(ctx, &layr8.Message{
            Type: "https://didcomm.org/basicmessage/2.0/message",
            To:   []string{"did:web:earth.node.layr8.org:bob"},
            Body: ChatMessage{Content: scanner.Text(), Locale: "en"},
        })
        if err != nil {
            log.Println("send error:", err)
        }
    }
}
```

## Decisions Deferred to Later Versions

- **Message-level retries**: Requires idempotency keys, deferred
- **Idempotency**: Optional per m.md, deferred
- **QUIC transport**: Design accommodated, not implemented
- **Audit logging**: Not part of SDK v1 (application-level concern)
- **Policy enforcement**: Not part of SDK v1 (application-level concern)
- **Grant management**: Not part of SDK v1 (application-level concern)
