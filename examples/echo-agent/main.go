// Example: Echo Agent
//
// A minimal agent that echoes back any message it receives.
// Demonstrates: Handle (request/response), auto-ack, auto-thread correlation.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	layr8 "github.com/layr8/layr8-go-sdk"
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
		// APIKey loaded from LAYR8_API_KEY env
	})
	if err != nil {
		log.Fatal(err)
	}

	client.Handle("https://layr8.io/protocols/echo/1.0/request",
		func(msg *layr8.Message) (*layr8.Message, error) {
			var req EchoRequest
			if err := msg.UnmarshalBody(&req); err != nil {
				return nil, err // sends problem report
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

	log.Println("echo agent running, press Ctrl+C to stop")
	<-ctx.Done()
}
