package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	layr8 "github.com/layr8/go-sdk"
)

const (
	protocolBase = "https://layr8.io/protocols/echo-test/1.0"
	requestType  = protocolBase + "/request"
	responseType = protocolBase + "/response"
)

type RequestBody struct {
	Content string `json:"content"`
}

type ResponseBody struct {
	Content string `json:"content"`
}

func main() {
	chatID := os.Getenv("CHAT_ID")
	if chatID == "" {
		log.Fatal("CHAT_ID environment variable is required")
	}

	bobDID := fmt.Sprintf("did:web:bob-test.localhost:chat-%s", chatID)
	aliceDID := fmt.Sprintf("did:web:alice-test.localhost:chat-%s", chatID)

	client, err := layr8.NewClient(layr8.Config{
		NodeURL:  "ws://bob-test.localhost/plugin_socket/websocket",
		APIKey:   "bob_efgh5678_testkeybobbtestkeybobt24",
		AgentDID: bobDID,
	})
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}

	// Register handler so the protocol is advertised on connect.
	// Bob doesn't expect inbound requests, but the protocol must be registered.
	client.Handle(requestType, func(msg *layr8.Message) (*layr8.Message, error) {
		return nil, nil
	})

	connectCtx, connectCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer connectCancel()

	if err := client.Connect(connectCtx); err != nil {
		log.Fatalf("failed to connect: %v", err)
	}
	defer client.Close()

	log.Printf("Bob connected as %s", client.DID())
	log.Printf("Sending message to Alice at %s", aliceDID)

	reqCtx, reqCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer reqCancel()

	resp, err := client.Request(reqCtx, &layr8.Message{
		Type: requestType,
		To:   []string{aliceDID},
		Body: RequestBody{Content: "Hey Alice, this is Bob!"},
	})
	if err != nil {
		var prob *layr8.ProblemReportError
		if errors.As(err, &prob) {
			log.Fatalf("problem report from Alice [%s]: %s", prob.Code, prob.Comment)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			log.Fatal("request timed out -- Alice may be offline")
		}
		log.Fatalf("request failed: %v", err)
	}

	var result ResponseBody
	if err := resp.UnmarshalBody(&result); err != nil {
		log.Fatalf("failed to parse response body: %v", err)
	}

	log.Printf("Alice replied: %s", result.Content)
}
