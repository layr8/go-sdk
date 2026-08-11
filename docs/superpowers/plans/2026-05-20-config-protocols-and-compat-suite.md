# Config.Protocols & Compat Suite Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `Config.Protocols` for sender-only actors and implement the full the compatibility suite (echo, pass, wildcard, disconnected scenarios) with CLI adapter, Dockerfile, and CI.

**Architecture:** `Config.Protocols` merges with handler-derived protocols (deduplicated, problem-report always first). The `compat/` directory is a separate Go module with a `replace` directive pointing to the parent SDK. Scenarios are plain functions matching the Python SDK's pattern. The CLI adapter implements the compatibility orchestrator contract.

**Tech Stack:** Go 1.25, Docker, GitHub Actions, ghcr.io

---

## File Map

### Config.Protocols (SDK core)

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `config.go` | Add `Protocols []string` field to `Config` |
| Modify | `config_test.go` | Test that Protocols passes through resolveConfig |
| Modify | `handler.go` | Accept extra protocols in `payloadTypes()` |
| Modify | `handler_test.go` | Test merge + dedup logic |
| Modify | `client.go` | Pass `cfg.Protocols` into `payloadTypes()` |

### Compat Module

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `compat/go.mod` | Separate module with replace directive |
| Create | `compat/cloud-nodes.json` | Cloud-node version declaration |
| Create | `compat/scenarios/types.go` | ScenarioContext, SenderContext, ScenarioResult |
| Create | `compat/scenarios/echo.go` | Echo request/response scenario |
| Create | `compat/scenarios/pass.go` | PASS sentinel scenario |
| Create | `compat/scenarios/wildcard.go` | HandleAll catch-all scenario |
| Create | `compat/scenarios/disconnected.go` | Offline receiver scenario |
| Create | `compat/cmd/compat/main.go` | CLI adapter (Layer 2) |
| Create | `compat/Dockerfile` | Container image for the compatibility suite |
| Modify | `.github/workflows/ci.yaml` | Add compat build check |

---

## Task 1: Add Config.Protocols — failing tests

**Files:**
- Modify: `config.go:12-38`
- Modify: `config_test.go`
- Modify: `handler.go:86-112`
- Modify: `handler_test.go`

- [ ] **Step 1: Write failing test for Config.Protocols passthrough**

Add to `config_test.go`:

```go
func TestResolveConfig_ProtocolsPassthrough(t *testing.T) {
	cfg := Config{
		NodeURL:   "ws://localhost:4000",
		APIKey:    "test-key",
		Protocols: []string{"https://layr8.test/echo/1.0"},
	}
	resolved, err := resolveConfig(cfg)
	if err != nil {
		t.Fatalf("resolveConfig() error: %v", err)
	}
	if len(resolved.Protocols) != 1 || resolved.Protocols[0] != "https://layr8.test/echo/1.0" {
		t.Errorf("Protocols = %v, want [https://layr8.test/echo/1.0]", resolved.Protocols)
	}
}

func TestResolveConfig_NilProtocols(t *testing.T) {
	cfg := Config{
		NodeURL: "ws://localhost:4000",
		APIKey:  "test-key",
	}
	resolved, err := resolveConfig(cfg)
	if err != nil {
		t.Fatalf("resolveConfig() error: %v", err)
	}
	if resolved.Protocols != nil {
		t.Errorf("Protocols = %v, want nil", resolved.Protocols)
	}
}
```

- [ ] **Step 2: Write failing test for payloadTypes merge with extra protocols**

Add to `handler_test.go`:

```go
func TestHandlerRegistry_PayloadTypesWithExtraProtocols(t *testing.T) {
	r := newHandlerRegistry()
	handler := func(msg *Message) (*Message, error) { return nil, nil }
	r.register("https://layr8.io/protocols/echo/1.0/request", handler)

	extra := []string{"https://layr8.test/custom/1.0"}
	protocols := r.payloadTypes(extra...)

	has := func(p string) bool {
		for _, proto := range protocols {
			if proto == p {
				return true
			}
		}
		return false
	}

	if !has("https://didcomm.org/report-problem/2.0") {
		t.Error("should always include problem-report")
	}
	if !has("https://layr8.test/custom/1.0") {
		t.Error("should include extra protocol")
	}
	if !has("https://layr8.io/protocols/echo/1.0") {
		t.Error("should include handler-derived protocol")
	}
}

func TestHandlerRegistry_PayloadTypesDeduplicatesExtras(t *testing.T) {
	r := newHandlerRegistry()
	handler := func(msg *Message) (*Message, error) { return nil, nil }
	r.register("https://layr8.io/protocols/echo/1.0/request", handler)

	// Pass echo protocol as extra too — should not duplicate
	extra := []string{"https://layr8.io/protocols/echo/1.0"}
	protocols := r.payloadTypes(extra...)

	count := 0
	for _, p := range protocols {
		if p == "https://layr8.io/protocols/echo/1.0" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("echo/1.0 appears %d times, want 1", count)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd <repo> && go test ./... -run "TestResolveConfig_Protocols|TestHandlerRegistry_PayloadTypesWith|TestHandlerRegistry_PayloadTypesDedup" -v`

Expected: Compilation errors — `Config` has no field `Protocols`, `payloadTypes` takes no arguments.

---

## Task 2: Add Config.Protocols — make tests pass

**Files:**
- Modify: `config.go:12-38`
- Modify: `handler.go:86-112`
- Modify: `client.go:101`

- [ ] **Step 1: Add Protocols field to Config**

In `config.go`, add the `Protocols` field to the `Config` struct:

```go
// Protocols lists additional protocol URIs to advertise on join.
// Use this for sender-only actors that need to declare protocols
// without registering handlers. Merged with handler-derived protocols.
Protocols []string
```

Add it after the `Persistent` field (around line 32).

- [ ] **Step 2: Update payloadTypes to accept extra protocols**

In `handler.go`, change the `payloadTypes` signature and merge logic:

```go
func (r *handlerRegistry) payloadTypes(extra ...string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := make(map[string]struct{})
	types := make([]string, 0)

	// Always register for problem reports
	const problemReportProtocol = "https://didcomm.org/report-problem/2.0"
	seen[problemReportProtocol] = struct{}{}
	types = append(types, problemReportProtocol)

	// Config-level protocols (sender-only actors)
	for _, proto := range extra {
		if _, ok := seen[proto]; !ok {
			seen[proto] = struct{}{}
			types = append(types, proto)
		}
	}

	// Handler-derived protocols
	for msgType := range r.handlers {
		proto := deriveProtocol(msgType)
		if _, ok := seen[proto]; !ok {
			seen[proto] = struct{}{}
			types = append(types, proto)
		}
	}

	if r.catchAll != nil {
		types = append(types, "*")
	}

	return types
}
```

- [ ] **Step 3: Pass Config.Protocols to payloadTypes in client.go**

In `client.go`, change line 101 from:

```go
protocols := c.registry.payloadTypes()
```

to:

```go
protocols := c.registry.payloadTypes(c.cfg.Protocols...)
```

- [ ] **Step 4: Run all tests**

Run: `cd <repo> && go test ./... -v`

Expected: All tests pass. The existing `payloadTypes()` callers with no args still work because of the variadic signature.

- [ ] **Step 5: Commit**

```bash
cd <repo>
git add config.go config_test.go handler.go handler_test.go client.go
git commit -m "Add Config.Protocols for sender-only actors

Merge config-level protocols with handler-derived protocols,
deduplicated. Problem-report protocol always included first.
Matches Python SDK's Config.protocols pattern."
```

---

## Task 3: Scaffold compat module

**Files:**
- Create: `compat/go.mod`
- Create: `compat/cloud-nodes.json`
- Create: `compat/scenarios/types.go`

- [ ] **Step 1: Create compat/go.mod**

```
module github.com/layr8/go-sdk/compat

go 1.25.5

require github.com/layr8/go-sdk v0.0.0

replace github.com/layr8/go-sdk => ../
```

- [ ] **Step 2: Run go mod tidy to resolve dependencies**

Run: `cd <repo>/compat && go mod tidy`

Expected: `go.sum` is generated, indirect dependencies pulled in.

- [ ] **Step 3: Create cloud-nodes.json**

```json
{
  "image": "ghcr.io/layr-8/cloud-node",
  "min": "4.13.0",
  "exclude": {
    "4.14.0": "Accepts reply_protocol from join but doesn't advertise capability"
  }
}
```

- [ ] **Step 4: Create scenarios/types.go**

```go
package scenarios

import "time"

// ScenarioContext is provided to both sender and receiver scenario functions.
type ScenarioContext struct {
	NodeURL  string
	APIKey   string
	TestID   string
	Timeout  time.Duration
	AgentDID string // optional — cloud-node assigns ephemeral DID if empty
}

// SenderContext extends ScenarioContext with the receiver's DID.
type SenderContext struct {
	ScenarioContext
	ReceiverDID string
}

// ScenarioResult is the JSON output from a sender scenario.
type ScenarioResult struct {
	Status     string `json:"status"`
	Scenario   string `json:"scenario"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

// ElapsedMs returns milliseconds since start.
func ElapsedMs(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}
```

- [ ] **Step 5: Verify the module compiles**

Run: `cd <repo>/compat && go build ./scenarios/...`

Expected: Compiles with no errors.

- [ ] **Step 6: Commit**

```bash
cd <repo>
git add compat/go.mod compat/go.sum compat/cloud-nodes.json compat/scenarios/types.go
git commit -m "Scaffold compat module with types and cloud-nodes

Separate Go module with replace directive for local dev.
ScenarioContext/SenderContext/ScenarioResult match the
contract used by Python and Node SDK compat suites."
```

---

## Task 4: Echo scenario

**Files:**
- Create: `compat/scenarios/echo.go`

- [ ] **Step 1: Create echo.go**

```go
package scenarios

import (
	"context"
	"fmt"
	"log"
	"time"

	layr8 "github.com/layr8/go-sdk"
)

const (
	echoType         = "https://layr8.test/echo/1.0/request"
	echoResponseType = "https://layr8.test/echo/1.0/response"
	echoProtocol     = "https://layr8.test/echo/1.0"
)

// EchoRunReceiver connects, registers an echo handler, and blocks.
func EchoRunReceiver(ctx context.Context, sc ScenarioContext, onReady func(did string)) error {
	client, err := layr8.NewClient(layr8.Config{
		NodeURL:  sc.NodeURL,
		APIKey:   sc.APIKey,
		AgentDID: sc.AgentDID,
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	client.Handle(echoType, func(msg *layr8.Message) (*layr8.Message, error) {
		var body map[string]interface{}
		msg.UnmarshalBody(&body)
		return &layr8.Message{
			Type: echoResponseType,
			Body: map[string]interface{}{"echo": body, "from": client.DID()},
		}, nil
	})

	connectCtx, cancel := context.WithTimeout(ctx, sc.Timeout)
	defer cancel()
	if err := client.Connect(connectCtx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Close()

	if onReady != nil {
		onReady(client.DID())
	}

	<-ctx.Done()
	return nil
}

// EchoRunSender sends an echo request and verifies the response.
func EchoRunSender(ctx context.Context, sc SenderContext) ScenarioResult {
	client, err := layr8.NewClient(layr8.Config{
		NodeURL:   sc.NodeURL,
		APIKey:    sc.APIKey,
		AgentDID:  sc.AgentDID,
		Protocols: []string{echoProtocol},
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		return ScenarioResult{Status: "fail", Scenario: "echo", Error: err.Error()}
	}

	connectCtx, cancel := context.WithTimeout(ctx, sc.Timeout)
	defer cancel()
	if err := client.Connect(connectCtx); err != nil {
		return ScenarioResult{Status: "fail", Scenario: "echo", Error: err.Error()}
	}
	defer client.Close()

	start := time.Now()
	reqCtx, reqCancel := context.WithTimeout(ctx, sc.Timeout)
	defer reqCancel()

	resp, err := client.Request(reqCtx, &layr8.Message{
		Type: echoType,
		To:   []string{sc.ReceiverDID},
		Body: map[string]interface{}{"ping": sc.TestID},
	})
	if err != nil {
		return ScenarioResult{Status: "fail", Scenario: "echo", DurationMs: ElapsedMs(start), Error: err.Error()}
	}

	var body map[string]interface{}
	resp.UnmarshalBody(&body)
	echo, _ := body["echo"].(map[string]interface{})
	if echo == nil || echo["ping"] != sc.TestID {
		return ScenarioResult{
			Status:     "fail",
			Scenario:   "echo",
			DurationMs: ElapsedMs(start),
			Error:      fmt.Sprintf("unexpected echo: %v", body),
		}
	}

	return ScenarioResult{Status: "pass", Scenario: "echo", DurationMs: ElapsedMs(start)}
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd <repo>/compat && go build ./scenarios/...`

Expected: Compiles with no errors.

- [ ] **Step 3: Commit**

```bash
cd <repo>
git add compat/scenarios/echo.go
git commit -m "Add echo compat scenario

Request/response round-trip test. Sender uses
Config.Protocols to declare echo protocol without
registering a handler. Matches Python SDK pattern."
```

---

## Task 5: Pass scenario

**Files:**
- Create: `compat/scenarios/pass.go`

- [ ] **Step 1: Create pass.go**

```go
package scenarios

import (
	"context"
	"fmt"
	"log"
	"time"

	layr8 "github.com/layr8/go-sdk"
)

// PassRunReceiver connects with a handler that returns ErrPass. Blocks until ctx done.
func PassRunReceiver(ctx context.Context, sc ScenarioContext, onReady func(did string)) error {
	client, err := layr8.NewClient(layr8.Config{
		NodeURL:   sc.NodeURL,
		APIKey:    sc.APIKey,
		AgentDID:  sc.AgentDID,
		Protocols: []string{echoProtocol},
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	client.Handle(echoType, func(msg *layr8.Message) (*layr8.Message, error) {
		return nil, layr8.ErrPass
	})

	connectCtx, cancel := context.WithTimeout(ctx, sc.Timeout)
	defer cancel()
	if err := client.Connect(connectCtx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Close()

	if onReady != nil {
		onReady(client.DID())
	}

	<-ctx.Done()
	return nil
}

// PassRunSender sends a request and expects a timeout (receiver PASSes).
func PassRunSender(ctx context.Context, sc SenderContext) ScenarioResult {
	client, err := layr8.NewClient(layr8.Config{
		NodeURL:   sc.NodeURL,
		APIKey:    sc.APIKey,
		AgentDID:  sc.AgentDID,
		Protocols: []string{echoProtocol},
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		return ScenarioResult{Status: "fail", Scenario: "pass", Error: err.Error()}
	}

	connectCtx, cancel := context.WithTimeout(ctx, sc.Timeout)
	defer cancel()
	if err := client.Connect(connectCtx); err != nil {
		return ScenarioResult{Status: "fail", Scenario: "pass", Error: err.Error()}
	}
	defer client.Close()

	start := time.Now()
	reqCtx, reqCancel := context.WithTimeout(ctx, sc.Timeout)
	defer reqCancel()

	_, err = client.Request(reqCtx, &layr8.Message{
		Type: echoType,
		To:   []string{sc.ReceiverDID},
		Body: map[string]interface{}{"ping": sc.TestID},
	})
	if err != nil {
		// Timeout or error means PASS behavior worked
		return ScenarioResult{Status: "pass", Scenario: "pass", DurationMs: ElapsedMs(start)}
	}

	return ScenarioResult{
		Status:     "fail",
		Scenario:   "pass",
		DurationMs: ElapsedMs(start),
		Error:      "expected timeout but received a response",
	}
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd <repo>/compat && go build ./scenarios/...`

Expected: Compiles with no errors.

- [ ] **Step 3: Commit**

```bash
cd <repo>
git add compat/scenarios/pass.go
git commit -m "Add pass compat scenario

Tests that ErrPass causes the sender to time out,
proving the cloud-node does not deliver a response
when the receiver declines the message."
```

---

## Task 6: Wildcard scenario

**Files:**
- Create: `compat/scenarios/wildcard.go`

- [ ] **Step 1: Create wildcard.go**

```go
package scenarios

import (
	"context"
	"fmt"
	"log"
	"time"

	layr8 "github.com/layr8/go-sdk"
)

const (
	pingType             = "https://didcomm.org/trust-ping/2.0/ping"
	pingResponseType     = "https://didcomm.org/trust-ping/2.0/ping-response"
	wildcardResponseType = "https://layr8.test/wildcard/1.0/response"
	trustPingProtocol    = "https://didcomm.org/trust-ping/2.0"
)

// WildcardRunReceiver connects with only a catch-all handler. Blocks until ctx done.
func WildcardRunReceiver(ctx context.Context, sc ScenarioContext, onReady func(did string)) error {
	client, err := layr8.NewClient(layr8.Config{
		NodeURL:  sc.NodeURL,
		APIKey:   sc.APIKey,
		AgentDID: sc.AgentDID,
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	client.HandleAll(func(msg *layr8.Message) (*layr8.Message, error) {
		var body map[string]interface{}
		msg.UnmarshalBody(&body)

		reply := map[string]interface{}{
			"received": body,
			"from":     client.DID(),
		}

		var replyType string
		switch msg.Type {
		case echoType:
			replyType = echoResponseType
		case pingType:
			replyType = pingResponseType
			reply["intercepted"] = true
		default:
			replyType = wildcardResponseType
		}

		return &layr8.Message{Type: replyType, Body: reply}, nil
	})

	connectCtx, cancel := context.WithTimeout(ctx, sc.Timeout)
	defer cancel()
	if err := client.Connect(connectCtx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Close()

	if onReady != nil {
		onReady(client.DID())
	}

	<-ctx.Done()
	return nil
}

// WildcardRunSender sends two messages (echo + trust-ping) to a catch-all receiver.
func WildcardRunSender(ctx context.Context, sc SenderContext) ScenarioResult {
	client, err := layr8.NewClient(layr8.Config{
		NodeURL:   sc.NodeURL,
		APIKey:    sc.APIKey,
		AgentDID:  sc.AgentDID,
		Protocols: []string{echoProtocol, trustPingProtocol},
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		return ScenarioResult{Status: "fail", Scenario: "wildcard", Error: err.Error()}
	}

	connectCtx, cancel := context.WithTimeout(ctx, sc.Timeout)
	defer cancel()
	if err := client.Connect(connectCtx); err != nil {
		return ScenarioResult{Status: "fail", Scenario: "wildcard", Error: err.Error()}
	}
	defer client.Close()

	start := time.Now()

	// 1. Send echo request — proves catch-all handles custom protocols.
	reqCtx1, cancel1 := context.WithTimeout(ctx, sc.Timeout)
	defer cancel1()
	echoResp, err := client.Request(reqCtx1, &layr8.Message{
		Type: echoType,
		To:   []string{sc.ReceiverDID},
		Body: map[string]interface{}{"data": sc.TestID},
	})
	if err != nil {
		return ScenarioResult{Status: "fail", Scenario: "wildcard", DurationMs: ElapsedMs(start), Error: err.Error()}
	}

	var echoBody map[string]interface{}
	echoResp.UnmarshalBody(&echoBody)
	received, _ := echoBody["received"].(map[string]interface{})
	if received == nil || received["data"] != sc.TestID {
		return ScenarioResult{
			Status:     "fail",
			Scenario:   "wildcard",
			DurationMs: ElapsedMs(start),
			Error:      "echo reply missing expected data",
		}
	}

	// 2. Send trust-ping — proves catch-all intercepts standard protocols.
	reqCtx2, cancel2 := context.WithTimeout(ctx, sc.Timeout)
	defer cancel2()
	pingResp, err := client.Request(reqCtx2, &layr8.Message{
		Type: pingType,
		To:   []string{sc.ReceiverDID},
		Body: map[string]interface{}{"responseRequested": true},
	})
	if err != nil {
		return ScenarioResult{Status: "fail", Scenario: "wildcard", DurationMs: ElapsedMs(start), Error: err.Error()}
	}

	var pingBody map[string]interface{}
	pingResp.UnmarshalBody(&pingBody)
	if pingBody["intercepted"] != true {
		return ScenarioResult{
			Status:     "fail",
			Scenario:   "wildcard",
			DurationMs: ElapsedMs(start),
			Error:      "ping reply missing intercepted field",
		}
	}

	return ScenarioResult{Status: "pass", Scenario: "wildcard", DurationMs: ElapsedMs(start)}
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd <repo>/compat && go build ./scenarios/...`

Expected: Compiles with no errors.

- [ ] **Step 3: Commit**

```bash
cd <repo>
git add compat/scenarios/wildcard.go
git commit -m "Add wildcard compat scenario

Tests that HandleAll catch-all responds to both custom
protocols (echo) and standard protocols (trust-ping).
Matches Python and Node SDK wildcard scenarios."
```

---

## Task 7: Disconnected scenario

**Files:**
- Create: `compat/scenarios/disconnected.go`

- [ ] **Step 1: Create disconnected.go**

```go
package scenarios

import (
	"context"
	"fmt"
	"log"
	"time"

	layr8 "github.com/layr8/go-sdk"
)

// DisconnectedRunReceiver connects, signals ready, then immediately disconnects.
func DisconnectedRunReceiver(ctx context.Context, sc ScenarioContext, onReady func(did string)) error {
	client, err := layr8.NewClient(layr8.Config{
		NodeURL:   sc.NodeURL,
		APIKey:    sc.APIKey,
		AgentDID:  sc.AgentDID,
		Protocols: []string{echoProtocol},
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	client.Handle(echoType, func(msg *layr8.Message) (*layr8.Message, error) {
		return &layr8.Message{
			Type: echoResponseType,
			Body: map[string]interface{}{"echo": "should not arrive"},
		}, nil
	})

	connectCtx, cancel := context.WithTimeout(ctx, sc.Timeout)
	defer cancel()
	if err := client.Connect(connectCtx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	if onReady != nil {
		onReady(client.DID())
	}

	// Disconnect immediately — the whole point is the receiver is offline
	return client.Close()
}

// DisconnectedRunSender sends to an offline DID and expects a timeout.
func DisconnectedRunSender(ctx context.Context, sc SenderContext) ScenarioResult {
	client, err := layr8.NewClient(layr8.Config{
		NodeURL:   sc.NodeURL,
		APIKey:    sc.APIKey,
		AgentDID:  sc.AgentDID,
		Protocols: []string{echoProtocol},
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		return ScenarioResult{Status: "fail", Scenario: "disconnected", Error: err.Error()}
	}

	connectCtx, cancel := context.WithTimeout(ctx, sc.Timeout)
	defer cancel()
	if err := client.Connect(connectCtx); err != nil {
		return ScenarioResult{Status: "fail", Scenario: "disconnected", Error: err.Error()}
	}
	defer client.Close()

	start := time.Now()
	reqCtx, reqCancel := context.WithTimeout(ctx, sc.Timeout)
	defer reqCancel()

	_, err = client.Request(reqCtx, &layr8.Message{
		Type: echoType,
		To:   []string{sc.ReceiverDID},
		Body: map[string]interface{}{"test_id": sc.TestID},
	})
	if err != nil {
		// Timeout or problem report means disconnected scenario worked
		return ScenarioResult{Status: "pass", Scenario: "disconnected", DurationMs: ElapsedMs(start)}
	}

	return ScenarioResult{
		Status:     "fail",
		Scenario:   "disconnected",
		DurationMs: ElapsedMs(start),
		Error:      "expected timeout but got response",
	}
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd <repo>/compat && go build ./scenarios/...`

Expected: Compiles with no errors.

- [ ] **Step 3: Commit**

```bash
cd <repo>
git add compat/scenarios/disconnected.go
git commit -m "Add disconnected compat scenario

Tests that sending to an offline DID results in a
clean timeout, not a crash or hang."
```

---

## Task 8: CLI adapter (Layer 2)

**Files:**
- Create: `compat/cmd/compat/main.go`

- [ ] **Step 1: Create the CLI adapter**

```go
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/layr8/go-sdk/compat/scenarios"
)

var scenarioRegistry = map[string]struct {
	receiver func(ctx context.Context, sc scenarios.ScenarioContext, onReady func(string)) error
	sender   func(ctx context.Context, sc scenarios.SenderContext) scenarios.ScenarioResult
}{
	"echo": {
		receiver: scenarios.EchoRunReceiver,
		sender:   scenarios.EchoRunSender,
	},
	"pass": {
		receiver: scenarios.PassRunReceiver,
		sender:   scenarios.PassRunSender,
	},
	"wildcard": {
		receiver: scenarios.WildcardRunReceiver,
		sender:   scenarios.WildcardRunSender,
	},
	"disconnected": {
		receiver: scenarios.DisconnectedRunReceiver,
		sender:   scenarios.DisconnectedRunSender,
	},
}

func main() {
	listScenarios := flag.Bool("list-scenarios", false, "Print available scenarios and exit")
	mode := flag.String("mode", "", "receiver or sender")
	scenario := flag.String("scenario", "", "Scenario name")
	node := flag.String("node", "", "Cloud-node WebSocket URL")
	apiKey := flag.String("api-key", envOrDefault("LAYR8_API_KEY", "test-key"), "API key")
	did := flag.String("did", "", "DID (receiver DID in sender mode)")
	testID := flag.String("test-id", "cli", "Test ID for correlation")
	timeoutMs := flag.Int("timeout", 10000, "Timeout in milliseconds")
	flag.Parse()

	if *listScenarios {
		names := make([]string, 0, len(scenarioRegistry))
		for name := range scenarioRegistry {
			names = append(names, name)
		}
		data, _ := json.Marshal(names)
		fmt.Println(string(data))
		return
	}

	if *mode == "" || *scenario == "" {
		fmt.Fprintln(os.Stderr, "--mode and --scenario are required")
		os.Exit(2)
	}

	entry, ok := scenarioRegistry[*scenario]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown scenario: %s\n", *scenario)
		os.Exit(2)
	}

	timeout := time.Duration(*timeoutMs) * time.Millisecond

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	switch *mode {
	case "receiver":
		sc := scenarios.ScenarioContext{
			NodeURL:  *node,
			APIKey:   *apiKey,
			TestID:   *testID,
			Timeout:  timeout,
			AgentDID: *did,
		}
		err := entry.receiver(ctx, sc, func(did string) {
			data, _ := json.Marshal(map[string]string{"status": "ready", "did": did})
			fmt.Println(string(data))
		})
		if err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "receiver error: %v\n", err)
			os.Exit(1)
		}

	case "sender":
		if *did == "" {
			fmt.Fprintln(os.Stderr, "--did is required in sender mode")
			os.Exit(2)
		}
		sc := scenarios.SenderContext{
			ScenarioContext: scenarios.ScenarioContext{
				NodeURL: *node,
				APIKey:  *apiKey,
				TestID:  *testID,
				Timeout: timeout,
			},
			ReceiverDID: *did,
		}
		result := entry.sender(ctx, sc)
		data, _ := json.Marshal(result)
		fmt.Println(string(data))
		if result.Status != "pass" {
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown mode: %s (want receiver or sender)\n", *mode)
		os.Exit(2)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd <repo>/compat && go build ./cmd/compat/`

Expected: Produces a `compat` binary.

- [ ] **Step 3: Verify --list-scenarios works**

Run: `cd <repo>/compat && go run ./cmd/compat/ --list-scenarios`

Expected: JSON array containing `echo`, `pass`, `wildcard`, `disconnected` (order may vary).

- [ ] **Step 4: Commit**

```bash
cd <repo>
git add compat/cmd/compat/main.go
git commit -m "Add compat CLI adapter (Layer 2)

Implements the compatibility orchestrator contract:
--mode, --scenario, --node, --did, --list-scenarios.
Receiver emits ready signal JSON to stdout.
Sender prints ScenarioResult JSON to stdout."
```

---

## Task 9: Dockerfile

**Files:**
- Create: `compat/Dockerfile`

- [ ] **Step 1: Create Dockerfile**

```dockerfile
FROM golang:1.25-alpine AS builder
WORKDIR /app

# Copy SDK source (replace directive resolves to ../)
COPY go.mod go.sum ./
COPY *.go ./
COPY examples/ ./examples/
COPY tests/ ./tests/

# Copy compat module
COPY compat/ ./compat/

WORKDIR /app/compat
RUN go mod download
RUN CGO_ENABLED=0 go build -o /compat ./cmd/compat/

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /compat /usr/local/bin/compat

LABEL org.opencontainers.image.source=https://github.com/layr8/go-sdk

ENTRYPOINT ["compat"]
```

- [ ] **Step 2: Verify Docker build**

Run: `cd <repo> && docker build -f compat/Dockerfile -t go-sdk-compat:local .`

Expected: Image builds successfully.

- [ ] **Step 3: Verify --list-scenarios in container**

Run: `docker run --rm go-sdk-compat:local --list-scenarios`

Expected: JSON array with the four scenario names.

- [ ] **Step 4: Commit**

```bash
cd <repo>
git add compat/Dockerfile
git commit -m "Add compat Dockerfile

Builds from local SDK source via replace directive.
Multi-stage: Go builder + minimal Alpine runtime.
OCI source label for ghcr.io auto-linking."
```

---

## Task 10: CI workflow updates

**Files:**
- Modify: `.github/workflows/ci.yaml`

- [ ] **Step 1: Add compat build job to CI**

Replace the contents of `.github/workflows/ci.yaml` with:

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

env:
  GOMAXPROCS: "4"

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: Build
        run: go build ./...

      - name: Test
        run: go test -count=1 -parallel=4 -v ./...

  compat-build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: Build compat scenarios
        run: cd compat && go build ./...

      - name: Verify compat CLI
        run: cd compat && go run ./cmd/compat/ --list-scenarios
```

- [ ] **Step 2: Commit**

```bash
cd <repo>
git add .github/workflows/ci.yaml
git commit -m "Add compat build check to CI

Verifies the compat module compiles and the CLI
adapter responds to --list-scenarios on every PR."
```

---

## Task 11: Update README and docs

**Files:**
- Modify: `README.md`
- Modify: `notes/features/prd-go-sdk-compat.md`

- [ ] **Step 1: Add Config.Protocols to README**

In the Config table (around line 67-73), add a row after `Persistent`:

```
| `Protocols` | -- | No | Additional protocol URIs to advertise on join (for sender-only actors) |
```

- [ ] **Step 2: Add compat section to README**

After the Development section (around line 285), add:

```markdown
## Compat Suite

The `compat/` directory contains integration scenarios that test the SDK against real cloud-nodes. It's a separate Go module.

```bash
cd compat && go build ./...              # Build scenarios
cd compat && go run ./cmd/compat/ --list-scenarios  # List available scenarios
```

Scenarios: `echo`, `pass`, `wildcard`, `disconnected`. See [notes/features/prd-go-sdk-compat.md](notes/features/prd-go-sdk-compat.md) for architecture details.

The CI pipeline builds the compat module on every PR. On release, a Docker image is published to `ghcr.io/layr8/go-sdk/compat:{version}` and the compatibility orchestrator is triggered.
```

- [ ] **Step 3: Commit**

```bash
cd <repo>
git add README.md
git commit -m "Document Config.Protocols and compat suite"
```
