// Echo Agent — a deployable DIDComm echo service built with the Layr8 Go SDK.
//
// Configuration via environment variables:
//
//	LAYR8_NODE_URL  — WebSocket URL of the cloud-node
//	LAYR8_API_KEY   — API key for authentication
//	LAYR8_AGENT_DID — DID for this agent
//
// Usage:
//
//	LAYR8_NODE_URL=ws://localhost:4000/plugin_socket/websocket \
//	LAYR8_API_KEY=alice_abcd1234_testkeyalicetestkeyali24 \
//	LAYR8_AGENT_DID=did:web:alice-test.localhost:sdk-echo \
//	  go run ./cmd/echo-agent
package main

import (
	"context"
	"log"
	"os"
	"os/signal"

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

	client, err := layr8.NewClient(layr8.Config{
		// All fields read from LAYR8_* env vars by default
	})
	if err != nil {
		log.Fatalf("NewClient: %v", err)
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := client.Connect(ctx); err != nil {
		log.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	log.Printf("echo agent running (DID=%s)", client.DID())
	<-ctx.Done()
	log.Println("shutting down")
}
