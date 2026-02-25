// Example: HTTP REST Agent (Resource Side)
//
// Rewritten from the layr8-demos HTTP agent.
// The original manages WebSocket, Phoenix Channels, DIDComm envelopes,
// heartbeats, reconnection, and thread correlation manually.
// With the SDK, the agent is pure HTTP proxy logic.
//
// Demonstrates: Handle (request/response), error handling, MessageContext.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"

	layr8 "github.com/layr8/go-sdk"
)

// --- Protocol types ---

const (
	ProtocolBase       = "https://layr8.io/protocols/https-rest/1.0"
	ProtocolQueryType  = ProtocolBase + "/query"
	ProtocolResultType = ProtocolBase + "/result"
)

type HTTPQueryRequest struct {
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Headers     map[string]string `json:"headers"`
	QueryParams map[string]string `json:"query_params"`
	Body        string            `json:"body"`
	Action      string            `json:"action"`   // "read" or "write"
	Resource    string            `json:"resource"`
	Reason      string            `json:"reason"`
}

type HTTPQueryResponse struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	Decision   string            `json:"decision"`
	Explain    string            `json:"explain"`
	Error      string            `json:"error,omitempty"`
}

func main() {
	backendURL := os.Getenv("BACKEND_URL")
	if backendURL == "" {
		backendURL = "http://localhost:3000"
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}

	client, err := layr8.NewClient(layr8.Config{
		NodeURL:  "ws://localhost:4000/plugin_socket/websocket",
		AgentDID: "did:web:mycompany:http-agent",
		// APIKey loaded from LAYR8_API_KEY env
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		log.Fatal(err)
	}

	// Handle incoming HTTP requests
	client.Handle(ProtocolQueryType,
		func(msg *layr8.Message) (*layr8.Message, error) {
			var req HTTPQueryRequest
			if err := msg.UnmarshalBody(&req); err != nil {
				return nil, fmt.Errorf("invalid HTTP request: %w", err)
			}

			log.Printf("%s %s from %s (action=%s)", req.Method, req.Path, msg.From, req.Action)

			// Build upstream URL
			u, err := url.Parse(backendURL + req.Path)
			if err != nil {
				return nil, fmt.Errorf("invalid path: %w", err)
			}
			q := u.Query()
			for k, v := range req.QueryParams {
				q.Set(k, v)
			}
			u.RawQuery = q.Encode()

			// Build upstream request
			httpReq, err := http.NewRequestWithContext(
				context.Background(),
				req.Method,
				u.String(),
				strings.NewReader(req.Body),
			)
			if err != nil {
				return nil, fmt.Errorf("failed to create request: %w", err)
			}
			for k, v := range req.Headers {
				httpReq.Header.Set(k, v)
			}

			// Execute
			httpResp, err := httpClient.Do(httpReq)
			if err != nil {
				return nil, fmt.Errorf("backend request failed: %w", err)
			}
			defer httpResp.Body.Close()

			body, err := io.ReadAll(httpResp.Body)
			if err != nil {
				return nil, fmt.Errorf("failed to read response: %w", err)
			}

			// Collect response headers
			headers := make(map[string]string)
			for k, v := range httpResp.Header {
				headers[k] = v[0]
			}

			return &layr8.Message{
				Type: ProtocolResultType,
				Body: HTTPQueryResponse{
					StatusCode: httpResp.StatusCode,
					Headers:    headers,
					Body:       string(body),
					Decision:   "allow",
				},
			}, nil
		},
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := client.Connect(ctx); err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	log.Printf("http agent running, proxying to %s, press Ctrl+C to stop", backendURL)
	<-ctx.Done()
}
