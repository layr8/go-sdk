package scenarios

import (
	"context"
	"fmt"
	"log"
	"time"

	layr8 "github.com/layr8/go-sdk"
)

const (
	echoType         = "https://layr8.test/echo/1.0/request"
	echoResponseType = "https://layr8.test/echo/1.0/response"
	echoProtocol     = "https://layr8.test/echo/1.0"
)

// EchoRunReceiver connects, registers an echo handler, and blocks.
func EchoRunReceiver(ctx context.Context, sc ScenarioContext, onReady func(did string)) error {
	client, err := layr8.NewClient(layr8.Config{
		NodeURL:  sc.NodeURL,
		APIKey:   sc.APIKey,
		AgentDID: sc.AgentDID,
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	client.Handle(echoType, func(msg *layr8.Message) (*layr8.Message, error) {
		var body map[string]interface{}
		msg.UnmarshalBody(&body)
		return &layr8.Message{
			Type: echoResponseType,
			Body: map[string]interface{}{"echo": body, "from": client.DID()},
		}, nil
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

// EchoRunSender sends an echo request and verifies the response.
func EchoRunSender(ctx context.Context, sc SenderContext) ScenarioResult {
	client, err := layr8.NewClient(layr8.Config{
		NodeURL:   sc.NodeURL,
		APIKey:    sc.APIKey,
		AgentDID:  sc.AgentDID,
		Protocols: []string{echoProtocol},
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		return ScenarioResult{Status: "fail", Scenario: "echo", Error: err.Error()}
	}

	connectCtx, cancel := context.WithTimeout(ctx, sc.Timeout)
	defer cancel()
	if err := client.Connect(connectCtx); err != nil {
		return ScenarioResult{Status: "fail", Scenario: "echo", Error: err.Error()}
	}
	defer client.Close()

	start := time.Now()
	reqCtx, reqCancel := context.WithTimeout(ctx, sc.Timeout)
	defer reqCancel()

	resp, err := client.Request(reqCtx, &layr8.Message{
		Type: echoType,
		To:   []string{sc.ReceiverDID},
		Body: map[string]interface{}{"ping": sc.TestID},
	})
	if err != nil {
		return ScenarioResult{Status: "fail", Scenario: "echo", DurationMs: ElapsedMs(start), Error: err.Error()}
	}

	var body map[string]interface{}
	resp.UnmarshalBody(&body)
	echo, _ := body["echo"].(map[string]interface{})
	if echo == nil || echo["ping"] != sc.TestID {
		return ScenarioResult{
			Status:     "fail",
			Scenario:   "echo",
			DurationMs: ElapsedMs(start),
			Error:      fmt.Sprintf("unexpected echo: %v", body),
		}
	}

	return ScenarioResult{Status: "pass", Scenario: "echo", DurationMs: ElapsedMs(start)}
}