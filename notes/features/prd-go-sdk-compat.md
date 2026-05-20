# PRD: Go SDK — Compat Integration (Layer 1 + Layer 2 Image)

## Problem

The Go SDK (`layr8/go-sdk`) has no compat test infrastructure. The
compat-suite currently has a stub Dockerfile that just prints
`{"status":"fail","scenario":"unimplemented"}`. All compat scenarios
need to be implemented from scratch.

## Goal

Add a `compat/` directory to the Go SDK repo implementing the same
structure as the sibling SDKs: scenario core logic, Layer 1 tests
(go test), and Layer 2 CLI adapter + Dockerfile. CI publishes
`ghcr.io/layr8/go-sdk/compat:{version}` on release.

## Decisions (from grilling session 2026-05-20)

- **Reference implementation**: Python SDK — match its patterns
- **Module structure**: Separate `go.mod` with `replace` directive for
  dev/CI, built from local source in Docker (not published module ref)
- **Scenario context**: Flat struct with NodeURL/APIKey/TestID/Timeout/AgentDID.
  Scenarios create their own clients. No CreateClient factory.
- **Ready signal**: `onReady func(did string)` callback, matching all
  three sibling SDKs
- **Config.Protocols**: Must be added to Go SDK before compat work —
  sender-only actors need to declare protocols without registering handlers
- **Dockerfile**: Always builds from local source (option A). Tagged with
  release version. No chicken-and-egg version swap.
- **Terminology**: See CONTEXT.md — Actor (not Agent), Plugin (channel
  session), Protocol (not PIURI/payload_type)

## Target Structure

```
go-sdk/
└── compat/
    ├── cloud-nodes.json         # cloud-node version declaration
    ├── scenarios/               # core — pure domain logic
    │   ├── types.go             #   ScenarioContext, SenderContext, Result
    │   ├── echo.go              #   RunSender(ctx), RunReceiver(ctx)
    │   ├── pass.go
    │   ├── wildcard.go
    │   └── disconnected.go
    ├── tests/                   # adapter: Layer 1 (go test)
    │   ├── setup_test.go        #   testcontainers lifecycle
    │   ├── echo_test.go         #   parameterized over cloud-node versions
    │   ├── pass_test.go
    │   ├── wildcard_test.go
    │   └── disconnected_test.go
    ├── cmd/                     # adapter: Layer 2 (CLI)
    │   └── compat/
    │       └── main.go          #   --mode/--scenario/--node/--did/--list-scenarios
    ├── Dockerfile
    └── go.mod                   # depends on github.com/layr8/go-sdk
```

## Scenario Port (Go)

```go
package scenarios

import "github.com/layr8/go-sdk"

type ScenarioContext struct {
    CreateClient func(did string) *sdk.Client
    TestID       string
    Signal       context.Context  // cancellation via context, idiomatic Go
}

type SenderContext struct {
    ScenarioContext
    ReceiverDID string
}

type Result struct {
    Status    string `json:"status"`
    Scenario  string `json:"scenario"`
    DurationMs int64 `json:"duration_ms"`
    Error     string `json:"error,omitempty"`
}

func RunReceiver(ctx ScenarioContext) error { ... }
func RunSender(ctx SenderContext) Result { ... }
```

Note: Go uses `context.Context` instead of `AbortSignal` — idiomatic
per language, same semantics.

## Layer 1 (go test + testcontainers)

Uses `github.com/testcontainers/testcontainers-go`:

```go
// tests/setup_test.go
func TestMain(m *testing.M) {
    // Start cloud-node containers from cloud-nodes.json
    // Store URLs in package-level vars
    // Run tests
    // Tear down containers
}

// tests/echo_test.go
func TestEcho(t *testing.T) {
    for _, node := range cloudNodes {
        t.Run(node.Version, func(t *testing.T) {
            ctx := scenarios.ScenarioContext{
                CreateClient: func(did string) *sdk.Client {
                    return sdk.NewClient(node.URL, "test-key", did)
                },
                TestID: generateTestID(),
                Signal: context.Background(),
            }
            // Start receiver, run sender, assert pass
        })
    }
}
```

## Layer 2 (CLI)

```go
// cmd/compat/main.go
func main() {
    // Parse flags: --mode, --scenario, --node, --did, --list-scenarios
    // If --list-scenarios: print available scenarios, exit
    // Construct ScenarioContext with factory wired to --node
    // Call RunSender or RunReceiver
    // Print JSON result to stdout
}
```

## Dockerfile

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY compat/go.mod compat/go.sum ./
RUN go mod download
COPY compat/ ./
RUN go build -o /compat ./cmd/compat/

FROM alpine:3.19
COPY --from=builder /compat /usr/local/bin/compat
LABEL org.opencontainers.image.source=https://github.com/layr8/go-sdk
ENTRYPOINT ["compat"]
```

The compat module depends on the Go SDK via `go.mod`:
```
require github.com/layr8/go-sdk v0.1.0
```

On release, update the version in go.mod to the releasing version,
build, and push.

**Image**: `ghcr.io/layr8/go-sdk/compat:{version}` (e.g., `v0.1.0`)

## Cloud-Node Declaration

```json
{
  "image": "ghcr.io/layr-8/cloud-node",
  "min": "4.13.0",
  "exclude": {
    "4.14.0": "Accepts reply_protocol from join but doesn't advertise capability"
  }
}
```

## CI Workflow

```yaml
jobs:
  build:
    # go build, go test, go vet

  compat-layer1:
    needs: build
    steps:
      - run: cd compat && go test ./tests/...

  publish-sdk:
    # git tag push (Go modules are tag-based)
    needs: [build, compat-layer1]

  publish-compat-image:
    needs: publish-sdk
    steps:
      - run: |
          cd compat
          # Update go.mod to reference the released version
          go get github.com/layr8/go-sdk@$VERSION
          docker build -t ghcr.io/layr8/go-sdk/compat:$VERSION .
          docker push ghcr.io/layr8/go-sdk/compat:$VERSION

  compat-gate:
    needs: [publish-compat-image, validate-version]
    runs-on: ubuntu-latest
    steps:
      - name: Trigger compat-suite gate
        run: |
          gh api repos/layr8/compat-suite/dispatches \
            -f event_type=gate \
            -f "client_payload[sdk]=go" \
            -f "client_payload[version]=${{ needs.validate-version.outputs.version }}"
        env:
          GH_TOKEN: ${{ secrets.COMPAT_GATE_PAT }}
```

### Compat-Suite Trigger

The `compat-gate` job fires a `repository_dispatch` event (type `gate`)
to `layr8/compat-suite`. This triggers the gate workflow which pulls
the freshly-published compat image and runs the cross-language matrix.

**Required secret**: `COMPAT_GATE_PAT` — a PAT (or fine-grained token)
with `repo` scope on `layr8/compat-suite`. Same token used by all SDK
repos.

### GOPROXY Indexing

Go modules are tag-based — no registry upload needed. The
`index-goproxy` job triggers `proxy.golang.org` to index the new
version so `go get` works immediately after release.

## README Update

Update the Go SDK README.md to document:
- The `compat/` directory structure and hexagonal architecture
- How to run Layer 1 locally (`cd compat && go test ./tests/...`)
- Cloud-node version declaration (`compat/cloud-nodes.json`)
- CI ordering: build → Layer 1 → publish tag → compat image → Layer 2
- That Layer 2 gate failures are informational (SDK already published)
- How to add a new scenario
- How to add support for a new cloud-node version

## Implementation Steps

1. Initialize `compat/go.mod` with dependency on `github.com/layr8/go-sdk`
2. Create `compat/scenarios/types.go` with context and result types
3. Implement scenarios (echo first, then pass, wildcard, disconnected)
4. Create `compat/tests/` with testcontainers setup and test files
5. Create `compat/cmd/compat/main.go` with CLI adapter
6. Create `compat/Dockerfile`
7. Create `compat/cloud-nodes.json`
8. Add CI workflow steps
9. Verify Layer 1 passes, build and test compat image locally
