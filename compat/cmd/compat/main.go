package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/layr8/go-sdk/compat/scenarios"
)

var scenarioRegistry = map[string]struct {
	receiver func(ctx context.Context, sc scenarios.ScenarioContext, onReady func(string)) error
	sender   func(ctx context.Context, sc scenarios.SenderContext) scenarios.ScenarioResult
}{
	"echo": {
		receiver: scenarios.EchoRunReceiver,
		sender:   scenarios.EchoRunSender,
	},
	"pass": {
		receiver: scenarios.PassRunReceiver,
		sender:   scenarios.PassRunSender,
	},
	"wildcard": {
		receiver: scenarios.WildcardRunReceiver,
		sender:   scenarios.WildcardRunSender,
	},
	"disconnected": {
		receiver: scenarios.DisconnectedRunReceiver,
		sender:   scenarios.DisconnectedRunSender,
	},
}

func main() {
	listScenarios := flag.Bool("list-scenarios", false, "Print available scenarios and exit")
	mode := flag.String("mode", "", "receiver or sender")
	scenario := flag.String("scenario", "", "Scenario name")
	node := flag.String("node", "", "Cloud-node WebSocket URL")
	apiKey := flag.String("api-key", envOrDefault("LAYR8_API_KEY", "test-key"), "API key")
	did := flag.String("did", "", "DID (receiver DID in sender mode)")
	testID := flag.String("test-id", "cli", "Test ID for correlation")
	timeoutMs := flag.Int("timeout", 10000, "Timeout in milliseconds")
	flag.Parse()

	if *listScenarios {
		names := make([]string, 0, len(scenarioRegistry))
		for name := range scenarioRegistry {
			names = append(names, name)
		}
		data, _ := json.Marshal(names)
		fmt.Println(string(data))
		return
	}

	if *mode == "" || *scenario == "" {
		fmt.Fprintln(os.Stderr, "--mode and --scenario are required")
		os.Exit(2)
	}

	entry, ok := scenarioRegistry[*scenario]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown scenario: %s\n", *scenario)
		os.Exit(2)
	}

	timeout := time.Duration(*timeoutMs) * time.Millisecond

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	switch *mode {
	case "receiver":
		sc := scenarios.ScenarioContext{
			NodeURL:  *node,
			APIKey:   *apiKey,
			TestID:   *testID,
			Timeout:  timeout,
			AgentDID: *did,
		}
		err := entry.receiver(ctx, sc, func(did string) {
			data, _ := json.Marshal(map[string]string{"status": "ready", "did": did})
			fmt.Println(string(data))
		})
		if err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "receiver error: %v\n", err)
			os.Exit(1)
		}

	case "sender":
		if *did == "" {
			fmt.Fprintln(os.Stderr, "--did is required in sender mode")
			os.Exit(2)
		}
		sc := scenarios.SenderContext{
			ScenarioContext: scenarios.ScenarioContext{
				NodeURL: *node,
				APIKey:  *apiKey,
				TestID:  *testID,
				Timeout: timeout,
			},
			ReceiverDID: *did,
		}
		result := entry.sender(ctx, sc)
		data, _ := json.Marshal(result)
		fmt.Println(string(data))
		if result.Status != "pass" {
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown mode: %s (want receiver or sender)\n", *mode)
		os.Exit(2)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}