// Echo Agent — a deployable DIDComm echo service built with the Layr8 Go SDK.
//
// Configuration via environment variables:
//
//	LAYR8_NODE_URL  — WebSocket URL of the cloud-node
//	LAYR8_API_KEY   — API key for authentication
//	LAYR8_AGENT_DID — DID for this agent
//	PEER_DIDS       — (optional) comma-separated DIDs to ping every 10s
//	PEER_DID        — (optional) single DID to ping (backward compat, merged into PEER_DIDS)
//
// Usage:
//
//	LAYR8_NODE_URL=ws://localhost:4000/plugin_socket/websocket \
//	LAYR8_API_KEY=alice_abcd1234_testkeyalicetestkeyali24 \
//	LAYR8_AGENT_DID=did:web:alice-test.localhost:sdk-echo-go \
//	PEER_DIDS=did:web:bob-test.localhost:sdk-echo-node,did:web:alice-test.localhost:sdk-echo-py \
//	  go run ./examples/echo-agent
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	layr8 "github.com/layr8/go-sdk"
)

const (
	echoProtocolBase = "https://layr8.io/protocols/echo/1.0"
	echoRequestType  = echoProtocolBase + "/request"
	echoResponseType = echoProtocolBase + "/response"
)

type EchoRequest struct {
	Message string `json:"message"`
}

type EchoResponse struct {
	Echo string `json:"echo"`
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	for {
		err := runAgent(ctx)
		if ctx.Err() != nil {
			break
		}
		log.Printf("disconnected: %v — reconnecting in 3s", err)
		select {
		case <-time.After(3 * time.Second):
		case <-ctx.Done():
		}
	}
	log.Println("shutting down")
}

func runAgent(ctx context.Context) error {
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	client, err := layr8.NewClient(layr8.Config{})
	if err != nil {
		return fmt.Errorf("NewClient: %w", err)
	}

	client.Handle(echoRequestType, func(msg *layr8.Message) (*layr8.Message, error) {
		var req EchoRequest
		if err := msg.UnmarshalBody(&req); err != nil {
			return nil, err
		}
		log.Printf("echo request from %s: %q", msg.From, req.Message)

		return &layr8.Message{
			Type: echoResponseType,
			Body: EchoResponse{Echo: req.Message},
		}, nil
	})

	disconnected := make(chan error, 1)
	client.OnDisconnect(func(err error) {
		runCancel()
		select {
		case disconnected <- err:
		default:
		}
	})

	if err := client.Connect(runCtx); err != nil {
		return fmt.Errorf("Connect: %w", err)
	}
	defer client.Close()

	log.Printf("echo agent running (DID=%s)", client.DID())

	for _, peer := range parsePeerDIDs() {
		log.Printf("will ping %s every 10s", peer)
		go pingLoop(runCtx, client, peer)
	}

	select {
	case err := <-disconnected:
		return err
	case <-ctx.Done():
		return nil
	}
}

func parsePeerDIDs() []string {
	var peers []string
	if dids := os.Getenv("PEER_DIDS"); dids != "" {
		for _, d := range strings.Split(dids, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				peers = append(peers, d)
			}
		}
	}
	// Backward compat: merge PEER_DID if not already present
	if single := os.Getenv("PEER_DID"); single != "" {
		found := false
		for _, p := range peers {
			if p == single {
				found = true
				break
			}
		}
		if !found {
			peers = append(peers, single)
		}
	}
	return peers
}

// shortDID returns the last segment of a DID for compact log output.
func shortDID(did string) string {
	parts := strings.Split(did, ":")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return did
}

func pingLoop(ctx context.Context, client *layr8.Client, peerDID string) {
	// Wait for DID propagation across nodes before first ping
	select {
	case <-time.After(30 * time.Second):
	case <-ctx.Done():
		return
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	seq := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			seq++
			msg := fmt.Sprintf("ping #%d from %s", seq, client.DID())

			start := time.Now()
			reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			resp, err := client.Request(reqCtx, &layr8.Message{
				Type: echoRequestType,
				To:   []string{peerDID},
				Body: EchoRequest{Message: msg},
			})
			cancel()
			rtt := time.Since(start)

			if err != nil {
				log.Printf("[→ %s] ping #%d failed (%s): %v", shortDID(peerDID), seq, rtt.Round(time.Millisecond), err)
				continue
			}

			var echo EchoResponse
			if err := resp.UnmarshalBody(&echo); err != nil {
				log.Printf("[→ %s] ping #%d bad response: %v", shortDID(peerDID), seq, err)
				continue
			}
			log.Printf("[→ %s] ping #%d reply (%s): %q", shortDID(peerDID), seq, rtt.Round(time.Millisecond), echo.Echo)
		}
	}
}
