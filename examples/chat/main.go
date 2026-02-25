// Example: Chat Client
//
// A simple chat client rewritten from the go_chat plugin example.
// The original go_chat demo is ~900+ lines across multiple files managing
// WebSocket, Phoenix Channel protocol, heartbeats, and reconnection.
// With the SDK, the core logic is ~60 lines.
//
// Demonstrates: Send (fire-and-forget), Handle (inbound), MessageContext,
// multi-recipient, graceful shutdown.
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"

	layr8 "github.com/layr8/go-sdk"
)

type ChatMessage struct {
	Content string `json:"content"`
	Locale  string `json:"locale"`
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: chat <recipient-did> [recipient-did...]")
	}
	recipients := os.Args[1:]

	client, err := layr8.NewClient(layr8.Config{
		// All fields fall back to env vars: LAYR8_NODE_URL, LAYR8_API_KEY, LAYR8_AGENT_DID
	})
	if err != nil {
		log.Fatal(err)
	}

	// Receive chat messages
	client.Handle("https://didcomm.org/basicmessage/2.0/message",
		func(msg *layr8.Message) (*layr8.Message, error) {
			var chat ChatMessage
			if err := msg.UnmarshalBody(&chat); err != nil {
				return nil, err
			}

			// Use sender credentials from cloud-node context
			sender := msg.From
			if msg.Context != nil && len(msg.Context.SenderCredentials) > 0 {
				sender = msg.Context.SenderCredentials[0].Name
			}

			fmt.Printf("[%s] %s\n", sender, chat.Content)
			return nil, nil // no response for chat messages
		},
	)

	// Handle problem reports (server notifications)
	client.Handle("https://didcomm.org/report-problem/2.0/problem-report",
		func(msg *layr8.Message) (*layr8.Message, error) {
			var prob layr8.ProblemReportError
			if err := msg.UnmarshalBody(&prob); err != nil {
				return nil, err
			}
			log.Printf("server: [%s] %s\n", prob.Code, prob.Comment)
			return nil, nil
		},
	)

	// Connection status
	client.OnDisconnect(func(err error) {
		fmt.Println("--- disconnected ---")
	})
	client.OnReconnect(func() {
		fmt.Println("--- reconnected ---")
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := client.Connect(ctx); err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	fmt.Printf("chatting with %s\n", strings.Join(recipients, ", "))
	fmt.Println("type a message and press enter (Ctrl+C to quit)")

	// Read from stdin, send to all recipients
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		text := scanner.Text()
		if text == "" {
			continue
		}

		err := client.Send(ctx, &layr8.Message{
			Type: "https://didcomm.org/basicmessage/2.0/message",
			To:   recipients,
			Body: ChatMessage{Content: text, Locale: "en"},
		})
		if err != nil {
			log.Printf("send error: %v\n", err)
		}
	}
}
