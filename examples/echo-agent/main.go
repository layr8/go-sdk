// Echo Agent — a deployable DIDComm echo service built with the Layr8 Go SDK.
//
// Configuration via environment variables:
//
//	LAYR8_NODE_URL  — WebSocket URL of the cloud-node
//	LAYR8_API_KEY   — API key for authentication
//	LAYR8_AGENT_DID — DID for this agent
//	PEER_DID        — (optional) DID of the peer agent to ping every 10s
//
// Usage:
//
//	LAYR8_NODE_URL=ws://localhost:4000/plugin_socket/websocket \
//	LAYR8_API_KEY=alice_abcd1234_testkeyalicetestkeyali24 \
//	LAYR8_AGENT_DID=did:web:alice-test.localhost:sdk-echo \
//	PEER_DID=did:web:bob-test.localhost:sdk-echo \
//	  go run ./examples/echo-agent
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
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

	if peerDID := os.Getenv("PEER_DID"); peerDID != "" {
		log.Printf("will ping %s every 10s", peerDID)
		go pingLoop(runCtx, client, peerDID)
	}

	select {
	case err := <-disconnected:
		return err
	case <-ctx.Done():
		return nil
	}
}

func pingLoop(ctx context.Context, client *layr8.Client, peerDID string) {
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

			reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			resp, err := client.Request(reqCtx, &layr8.Message{
				Type: echoRequestType,
				To:   []string{peerDID},
				Body: EchoRequest{Message: msg},
			})
			cancel()

			if err != nil {
				log.Printf("ping #%d failed: %v", seq, err)
				continue
			}

			var echo EchoResponse
			if err := resp.UnmarshalBody(&echo); err != nil {
				log.Printf("ping #%d bad response: %v", seq, err)
				continue
			}
			log.Printf("ping #%d reply: %q", seq, echo.Echo)
		}
	}
}
