package layr8

import (
	"fmt"
	"os"
	"strings"
)

// Config holds the configuration for a Layr8 client.
type Config struct {
	// NodeURL is the WebSocket URL of the Layr8 cloud-node.
	// Fallback: LAYR8_NODE_URL environment variable.
	NodeURL string

	// APIKey is the authentication key for the cloud-node.
	// Fallback: LAYR8_API_KEY environment variable.
	APIKey string

	// AgentDID is the DID identity of this agent.
	// If empty, an ephemeral DID is created on Connect().
	// Fallback: LAYR8_AGENT_DID environment variable.
	AgentDID string
}

// resolveConfig fills empty fields from environment variables and validates required fields.
func resolveConfig(cfg Config) (Config, error) {
	if cfg.NodeURL == "" {
		cfg.NodeURL = os.Getenv("LAYR8_NODE_URL")
	}
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("LAYR8_API_KEY")
	}
	if cfg.AgentDID == "" {
		cfg.AgentDID = os.Getenv("LAYR8_AGENT_DID")
	}

	if cfg.NodeURL == "" {
		return cfg, fmt.Errorf("NodeURL is required (set in Config or LAYR8_NODE_URL env)")
	}

	// Normalize HTTP(S) URLs to WebSocket scheme.
	// In production, the /plugin_socket endpoint serves WebSocket over HTTPS.
	if rest, ok := strings.CutPrefix(cfg.NodeURL, "https://"); ok {
		cfg.NodeURL = "wss://" + rest
	} else if rest, ok := strings.CutPrefix(cfg.NodeURL, "http://"); ok {
		cfg.NodeURL = "ws://" + rest
	}
	if cfg.APIKey == "" {
		return cfg, fmt.Errorf("APIKey is required (set in Config or LAYR8_API_KEY env)")
	}

	return cfg, nil
}
