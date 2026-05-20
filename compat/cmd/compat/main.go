package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/google/uuid"
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
				NodeURL:  *node,
				APIKey:   *apiKey,
				TestID:   *testID,
				Timeout:  timeout,
				AgentDID: senderDID(*node),
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

// senderDID generates a unique did:web DID from the node URL.
// The cloud-node rejects empty DIDs in the join topic, so senders
// must provide one even though they don't register handlers.
// Uses port 9000 (HTTP/DID-resolution port) regardless of the
// WebSocket port in the URL, so cross-node DID resolution works.
func senderDID(nodeURL string) string {
	u, err := url.Parse(nodeURL)
	if err != nil {
		return fmt.Sprintf("did:web:localhost%%3A9000:compat:sender-%s", uuid.New())
	}
	return fmt.Sprintf("did:web:%s%%3A9000:compat:sender-%s", u.Hostname(), uuid.New())
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}