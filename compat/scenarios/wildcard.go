package scenarios

import (
	"context"
	"fmt"
	"log"
	"time"

	layr8 "github.com/layr8/go-sdk"
)

const (
	pingType             = "https://didcomm.org/trust-ping/2.0/ping"
	pingResponseType     = "https://didcomm.org/trust-ping/2.0/ping-response"
	wildcardResponseType = "https://layr8.test/wildcard/1.0/response"
	trustPingProtocol    = "https://didcomm.org/trust-ping/2.0"
)

// WildcardRunReceiver connects with only a catch-all handler. Blocks until ctx done.
func WildcardRunReceiver(ctx context.Context, sc ScenarioContext, onReady func(did string)) error {
	client, err := layr8.NewClient(layr8.Config{
		NodeURL:  sc.NodeURL,
		APIKey:   sc.APIKey,
		AgentDID: sc.AgentDID,
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	client.HandleAll(func(msg *layr8.Message) (*layr8.Message, error) {
		var body map[string]interface{}
		msg.UnmarshalBody(&body)

		reply := map[string]interface{}{
			"received": body,
			"from":     client.DID(),
		}

		var replyType string
		switch msg.Type {
		case echoType:
			replyType = echoResponseType
		case pingType:
			replyType = pingResponseType
			reply["intercepted"] = true
		default:
			replyType = wildcardResponseType
		}

		return &layr8.Message{Type: replyType, Body: reply}, nil
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

// WildcardRunSender sends two messages (echo + trust-ping) to a catch-all receiver.
func WildcardRunSender(ctx context.Context, sc SenderContext) ScenarioResult {
	client, err := layr8.NewClient(layr8.Config{
		NodeURL:   sc.NodeURL,
		APIKey:    sc.APIKey,
		AgentDID:  sc.AgentDID,
		Protocols: []string{echoProtocol, trustPingProtocol},
	}, layr8.LogErrors(log.Default()))
	if err != nil {
		return ScenarioResult{Status: "fail", Scenario: "wildcard", Error: err.Error()}
	}

	connectCtx, cancel := context.WithTimeout(ctx, sc.Timeout)
	defer cancel()
	if err := client.Connect(connectCtx); err != nil {
		return ScenarioResult{Status: "fail", Scenario: "wildcard", Error: err.Error()}
	}
	defer client.Close()

	start := time.Now()

	// 1. Send echo request — proves catch-all handles custom protocols.
	reqCtx1, cancel1 := context.WithTimeout(ctx, sc.Timeout)
	defer cancel1()
	echoResp, err := client.Request(reqCtx1, &layr8.Message{
		Type: echoType,
		To:   []string{sc.ReceiverDID},
		Body: map[string]interface{}{"data": sc.TestID},
	})
	if err != nil {
		return ScenarioResult{Status: "fail", Scenario: "wildcard", DurationMs: ElapsedMs(start), Error: err.Error()}
	}

	var echoBody map[string]interface{}
	echoResp.UnmarshalBody(&echoBody)
	received, _ := echoBody["received"].(map[string]interface{})
	if received == nil || received["data"] != sc.TestID {
		return ScenarioResult{
			Status:     "fail",
			Scenario:   "wildcard",
			DurationMs: ElapsedMs(start),
			Error:      "echo reply missing expected data",
		}
	}

	// 2. Send trust-ping — proves catch-all intercepts standard protocols.
	reqCtx2, cancel2 := context.WithTimeout(ctx, sc.Timeout)
	defer cancel2()
	pingResp, err := client.Request(reqCtx2, &layr8.Message{
		Type: pingType,
		To:   []string{sc.ReceiverDID},
		Body: map[string]interface{}{"responseRequested": true},
	})
	if err != nil {
		return ScenarioResult{Status: "fail", Scenario: "wildcard", DurationMs: ElapsedMs(start), Error: err.Error()}
	}

	var pingBody map[string]interface{}
	pingResp.UnmarshalBody(&pingBody)
	if pingBody["intercepted"] != true {
		return ScenarioResult{
			Status:     "fail",
			Scenario:   "wildcard",
			DurationMs: ElapsedMs(start),
			Error:      "ping reply missing intercepted field",
		}
	}

	return ScenarioResult{Status: "pass", Scenario: "wildcard", DurationMs: ElapsedMs(start)}
}