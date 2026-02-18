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

// Request is the body of an incoming echo-test request.
type Request struct {
	Content string `json:"content"`
}

// Response is the body of an outgoing echo-test response.
type Response struct {
	Content string `json:"content"`
}

func main() {
	// Determine the chat ID from the environment or generate one from a timestamp.
	chatID := os.Getenv("CHAT_ID")
	if chatID == "" {
		chatID = fmt.Sprintf("%d", time.Now().UnixMilli())
	}

	agentDID := fmt.Sprintf("did:web:alice-test.localhost:chat-%s", chatID)

	client, err := layr8.NewClient(layr8.Config{
		NodeURL:  "ws://alice-test.localhost/plugin_socket/websocket",
		APIKey:   "alice_abcd1234_testkeyalicetestkeyali24",
		AgentDID: agentDID,
	})
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}

	// Register the handler for echo-test requests before connecting.
	client.Handle("https://layr8.io/protocols/echo-test/1.0/request",
		func(msg *layr8.Message) (*layr8.Message, error) {
			var req Request
			if err := msg.UnmarshalBody(&req); err != nil {
				return nil, err
			}

			log.Printf("Message from %s: %s", msg.From, req.Content)

			return &layr8.Message{
				Type: "https://layr8.io/protocols/echo-test/1.0/response",
				Body: Response{
					Content: fmt.Sprintf("Hello! I got your message: %q", req.Content),
				},
			}, nil
		},
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := client.Connect(ctx); err != nil {
		log.Fatalf("failed to connect: %v", err)
	}
	defer client.Close()

	log.Printf("Alice agent running as %s", client.DID())
	log.Println("Waiting for messages... Press Ctrl+C to stop.")
	<-ctx.Done()
	log.Println("Shutting down.")
}
