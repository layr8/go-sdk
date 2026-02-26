---
name: build-layr8-agent
description: Use when building a Go agent for the Layr8 platform. Covers the full SDK API — config, handlers, messaging, error handling, and DIDComm conventions.
---

# Building Layr8 Agents with the Go SDK

Full documentation: https://docs.layr8.io/reference/go-sdk

## Import

```go
import layr8 "github.com/layr8/go-sdk"
```

## Config

```go
client, err := layr8.NewClient(layr8.Config{
    NodeURL:  "ws://mynode.localhost/plugin_socket/websocket",
    APIKey:   "my_api_key",
    AgentDID: "did:web:mynode.localhost:my-agent",
}, layr8.LogErrors(log.Default()))
```

`NewClient` requires an `ErrorHandler` as its second argument. `LogErrors` is a built-in helper that logs SDK-level errors (parse failures, missing handlers, handler panics, server rejections). For custom handling, pass any `func(layr8.SDKError)`.

All fields fall back to environment variables if empty:
- `NodeURL`  → `LAYR8_NODE_URL`
- `APIKey`   → `LAYR8_API_KEY`
- `AgentDID` → `LAYR8_AGENT_DID`

`AgentDID` is optional — if omitted, the node assigns an ephemeral DID on connect.

## Lifecycle

```
NewClient → Handle (register handlers) → Connect → ... → Close
```

- `Handle` must be called BEFORE `Connect` — returns `ErrAlreadyConnected` after.
- `Connect(ctx)` establishes WebSocket and joins the Phoenix Channel.
- `Close()` sends `phx_leave` and shuts down gracefully.
- `DID()` returns the agent's DID (explicit or node-assigned).

## Registering Handlers

```go
client.Handle("https://layr8.io/protocols/echo/1.0/request",
    func(msg *layr8.Message) (*layr8.Message, error) {
        var req MyRequest
        if err := msg.UnmarshalBody(&req); err != nil {
            return nil, err // sends problem report to sender
        }

        return &layr8.Message{
            Type: "https://layr8.io/protocols/echo/1.0/response",
            Body: MyResponse{Echo: req.Message},
        }, nil
    },
)
```

Handler return values:
- `(*Message, nil)` → send response to sender
- `(nil, error)` → send DIDComm problem report to sender
- `(nil, nil)` → no response (fire-and-forget inbound)

The protocol base URI is derived automatically from the message type
(last path segment removed) and registered with the node on connect.

## Sending Messages

### Send

By default, `Send` waits for the server to acknowledge the message and returns an error on rejection:

```go
err := client.Send(ctx, &layr8.Message{
    Type: "https://didcomm.org/basicmessage/2.0/message",
    To:   []string{"did:web:other-node:agent"},
    Body: map[string]string{"content": "Hello!"},
})
```

For fire-and-forget (no server ack):

```go
err := client.Send(ctx, msg, layr8.WithFireAndForget())
```

### Request/Response

```go
resp, err := client.Request(ctx, &layr8.Message{
    Type: "https://layr8.io/protocols/echo/1.0/request",
    To:   []string{"did:web:other-node:agent"},
    Body: EchoRequest{Message: "ping"},
})
// resp is the correlated response message (matched by thread ID)
```

## Message Structure

```go
type Message struct {
    ID             string          // auto-generated if empty
    Type           string          // DIDComm message type URI
    From           string          // auto-filled with agent DID
    To             []string        // recipient DIDs
    ThreadID       string          // auto-generated for Request
    ParentThreadID string          // set via WithParentThread
    Body           any             // serialized to JSON
    Context        *MessageContext // populated on inbound messages
}
```

### Inbound Message Context

```go
if msg.Context != nil {
    msg.Context.Authorized        // bool — node authorization result
    msg.Context.Recipient         // string — recipient DID
    msg.Context.SenderCredentials // []Credential{ID, Name}
}
```

## Options

### Manual Ack

By default, messages are auto-acked before the handler runs.
Use `WithManualAck` to control ack timing (e.g., ack only after DB commit):

```go
client.Handle(msgType, handler, layr8.WithManualAck())

// Inside handler:
msg.Ack() // explicitly ack after processing
```

### Parent Thread

For nested thread correlation:

```go
resp, err := client.Request(ctx, msg, layr8.WithParentThread("parent-thread-id"))
```

## Error Handling

### ErrorHandler (Required)

`NewClient` requires an `ErrorHandler` as its second argument — SDK-level errors are never silently swallowed:

```go
// Built-in logger
client, err := layr8.NewClient(cfg, layr8.LogErrors(log.Default()))

// Custom handler
client, err := layr8.NewClient(cfg, func(e layr8.SDKError) {
    slog.Error("sdk error", "kind", e.Kind, "error", e.Cause)
})
```

Error kinds: `ErrParseFailure`, `ErrNoHandler`, `ErrHandlerPanic`, `ErrServerReject`, `ErrTransportWrite`.

### Problem Reports

When a remote handler returns an error, `Request` returns `*ProblemReportError`:

```go
resp, err := client.Request(ctx, msg)
if err != nil {
    var prob *layr8.ProblemReportError
    if errors.As(err, &prob) {
        log.Printf("remote error [%s]: %s", prob.Code, prob.Comment)
    }
}
```

### Sentinel Errors

- `ErrNotConnected` — `Send`/`Request` called before `Connect`
- `ErrAlreadyConnected` — `Handle` called after `Connect`
- `ErrClientClosed` — `Connect` called after `Close`

### Connection Error

```go
var connErr *layr8.ConnectionError
if errors.As(err, &connErr) {
    log.Printf("failed to connect to %s: %s", connErr.URL, connErr.Reason)
}
```

## Connection Callbacks

```go
client.OnDisconnect(func(err error) {
    log.Printf("connection lost: %v", err)
})
client.OnReconnect(func() {
    log.Println("reconnected")
})
```

Note: `OnDisconnect` fires only on unexpected drops, not on `Close()`.

## DID and Protocol Conventions

### DID Format

```
did:web:{node-domain}:{agent-path}
```

Examples:
- `did:web:alice-test.localhost:my-agent`
- `did:web:earth.node.layr8.org:echo-service`

### Protocol URI Format

```
https://layr8.io/protocols/{name}/{version}/{message-type}
```

The base URI (without the last segment) is the protocol identifier.
Example: `https://layr8.io/protocols/echo/1.0/request` → protocol `https://layr8.io/protocols/echo/1.0`

### Standard Protocols

- Basic message: `https://didcomm.org/basicmessage/2.0/message`
- Problem report: `https://didcomm.org/report-problem/2.0/problem-report`

## Complete Example: Echo Agent

```go
package main

import (
    "context"
    "log"
    "os"
    "os/signal"

    layr8 "github.com/layr8/go-sdk"
)

type EchoRequest struct {
    Message string `json:"message"`
}

type EchoResponse struct {
    Echo string `json:"echo"`
}

func main() {
    client, err := layr8.NewClient(layr8.Config{}, layr8.LogErrors(log.Default()))
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

    log.Printf("echo agent running as %s", client.DID())
    <-ctx.Done()
}
```

## Complete Example: Request/Response Client

```go
package main

import (
    "context"
    "errors"
    "log"
    "time"

    layr8 "github.com/layr8/go-sdk"
)

type EchoRequest struct {
    Message string `json:"message"`
}

type EchoResponse struct {
    Echo string `json:"echo"`
}

func main() {
    client, err := layr8.NewClient(layr8.Config{}, layr8.LogErrors(log.Default()))
    if err != nil {
        log.Fatal(err)
    }

    // Must register the protocol even if not handling inbound
    client.Handle("https://layr8.io/protocols/echo/1.0/request",
        func(msg *layr8.Message) (*layr8.Message, error) { return nil, nil },
    )

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    if err := client.Connect(ctx); err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    resp, err := client.Request(ctx, &layr8.Message{
        Type: "https://layr8.io/protocols/echo/1.0/request",
        To:   []string{"did:web:other-node:echo-agent"},
        Body: EchoRequest{Message: "Hello!"},
    })
    if err != nil {
        var prob *layr8.ProblemReportError
        if errors.As(err, &prob) {
            log.Fatalf("remote error [%s]: %s", prob.Code, prob.Comment)
        }
        log.Fatal(err)
    }

    var result EchoResponse
    if err := resp.UnmarshalBody(&result); err != nil {
        log.Fatal(err)
    }
    log.Printf("response: %s", result.Echo)
}
```

## More Examples

See the `examples/` directory in the SDK repo for complete working agents:
- `examples/chat/` — interactive chat client
- `examples/echo-agent/` — minimal echo service
- `examples/http-agent/` — HTTP REST proxy agent
- `examples/postgres-agent/` — database query agent with manual ack
