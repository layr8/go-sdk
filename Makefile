.PHONY: test test-race lint build examples integration-test clean

# Run unit tests
test:
	go test ./...

# Run unit tests with race detector
test-race:
	go test -race -count=1 ./...

# Run unit tests with verbose output
test-v:
	go test -race -count=1 -v .

# Run golangci-lint (install: https://golangci-lint.run/usage/install/)
lint:
	golangci-lint run ./...

# Build all examples (compilation check)
build:
	go build ./...

# Build examples individually
examples:
	go build ./examples/echo-agent
	go build ./examples/chat
	go build ./examples/http-agent

# Run integration test against live Layr8 nodes
# Prerequisites:
#   kubectl port-forward -n cust-alice-test svc/node 14000:4000
#   kubectl port-forward -n cust-bob-test svc/node 14001:4000
integration-test:
	go run ./cmd/integration-test

# Remove build artifacts
clean:
	go clean ./...
