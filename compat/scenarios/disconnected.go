package scenarios

import (
	"context"
	"fmt"
	"log"
	"time"

	layr8 "github.com/layr8/go-sdk"
)

// DisconnectedRunReceiver connects, signals ready, then immediately disconnects.
func DisconnectedRunReceiver(ctx context.Context, sc ScenarioContext, onReady func(did string)) error {
	client, err := layr8.NewClient(layr8.Config{
		NodeURL:   sc.NodeURL,
		APIKey:    sc.APIKey,
		AgentDID:  sc.AgentDID,
		Protocols: []string{echoProtocol},
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	client.Handle(echoType, func(msg *layr8.Message) (*layr8.Message, error) {
		return &layr8.Message{
			Type: echoResponseType,
			Body: map[string]interface{}{"echo": "should not arrive"},
		}, nil
	})

	connectCtx, cancel := context.WithTimeout(ctx, sc.Timeout)
	defer cancel()
	if err := client.Connect(connectCtx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	if onReady != nil {
		onReady(client.DID())
	}

	// Disconnect immediately — the whole point is the receiver is offline
	return client.Close()
}

// DisconnectedRunSender sends to an offline DID and expects a timeout.
func DisconnectedRunSender(ctx context.Context, sc SenderContext) ScenarioResult {
	client, err := layr8.NewClient(layr8.Config{
		NodeURL:   sc.NodeURL,
		APIKey:    sc.APIKey,
		AgentDID:  sc.AgentDID,
		Protocols: []string{echoProtocol},
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		return ScenarioResult{Status: "fail", Scenario: "disconnected", Error: err.Error()}
	}

	connectCtx, cancel := context.WithTimeout(ctx, sc.Timeout)
	defer cancel()
	if err := client.Connect(connectCtx); err != nil {
		return ScenarioResult{Status: "fail", Scenario: "disconnected", Error: err.Error()}
	}
	defer client.Close()

	start := time.Now()
	reqCtx, reqCancel := context.WithTimeout(ctx, sc.Timeout)
	defer reqCancel()

	_, err = client.Request(reqCtx, &layr8.Message{
		Type: echoType,
		To:   []string{sc.ReceiverDID},
		Body: map[string]interface{}{"test_id": sc.TestID},
	})
	if err != nil {
		// Timeout or problem report means disconnected scenario worked
		return ScenarioResult{Status: "pass", Scenario: "disconnected", DurationMs: ElapsedMs(start)}
	}

	return ScenarioResult{
		Status:     "fail",
		Scenario:   "disconnected",
		DurationMs: ElapsedMs(start),
		Error:      "expected timeout but got response",
	}
}