package scenarios

import (
	"context"
	"fmt"
	"log"
	"time"

	layr8 "github.com/layr8/go-sdk"
)

// PassRunReceiver connects with a handler that returns ErrPass. Blocks until ctx done.
func PassRunReceiver(ctx context.Context, sc ScenarioContext, onReady func(did string)) error {
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
		return nil, layr8.ErrPass
	})

	connectCtx, cancel := context.WithTimeout(ctx, sc.Timeout)
	defer cancel()
	if err := client.Connect(connectCtx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Close()

	if onReady != nil {
		onReady(client.DID())
	}

	<-ctx.Done()
	return nil
}

// PassRunSender sends a request and expects a timeout (receiver PASSes).
func PassRunSender(ctx context.Context, sc SenderContext) ScenarioResult {
	client, err := layr8.NewClient(layr8.Config{
		NodeURL:   sc.NodeURL,
		APIKey:    sc.APIKey,
		AgentDID:  sc.AgentDID,
		Protocols: []string{echoProtocol},
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		return ScenarioResult{Status: "fail", Scenario: "pass", Error: err.Error()}
	}

	connectCtx, cancel := context.WithTimeout(ctx, sc.Timeout)
	defer cancel()
	if err := client.Connect(connectCtx); err != nil {
		return ScenarioResult{Status: "fail", Scenario: "pass", Error: err.Error()}
	}
	defer client.Close()

	start := time.Now()
	reqCtx, reqCancel := context.WithTimeout(ctx, sc.Timeout)
	defer reqCancel()

	_, err = client.Request(reqCtx, &layr8.Message{
		Type: echoType,
		To:   []string{sc.ReceiverDID},
		Body: map[string]interface{}{"ping": sc.TestID},
	})
	if err != nil {
		// Timeout or error means PASS behavior worked
		return ScenarioResult{Status: "pass", Scenario: "pass", DurationMs: ElapsedMs(start)}
	}

	return ScenarioResult{
		Status:     "fail",
		Scenario:   "pass",
		DurationMs: ElapsedMs(start),
		Error:      "expected timeout but received a response",
	}
}