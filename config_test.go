package layr8

import (
	"os"
	"testing"
)

func TestResolveConfig_ExplicitValues(t *testing.T) {
	cfg := Config{
		NodeURL:  "ws://localhost:4000/plugin_socket/websocket",
		APIKey:   "test-key",
		AgentDID: "did:web:test",
	}
	resolved, err := resolveConfig(cfg)
	if err != nil {
		t.Fatalf("resolveConfig() error: %v", err)
	}
	if resolved.NodeURL != "ws://localhost:4000/plugin_socket/websocket" {
		t.Errorf("NodeURL = %q, want explicit value", resolved.NodeURL)
	}
	if resolved.APIKey != "test-key" {
		t.Errorf("APIKey = %q, want %q", resolved.APIKey, "test-key")
	}
	if resolved.AgentDID != "did:web:test" {
		t.Errorf("AgentDID = %q, want %q", resolved.AgentDID, "did:web:test")
	}
}

func TestResolveConfig_EnvFallback(t *testing.T) {
	os.Setenv("LAYR8_NODE_URL", "ws://env-host:4000")
	os.Setenv("LAYR8_API_KEY", "env-key")
	os.Setenv("LAYR8_AGENT_DID", "did:web:env-agent")
	defer func() {
		os.Unsetenv("LAYR8_NODE_URL")
		os.Unsetenv("LAYR8_API_KEY")
		os.Unsetenv("LAYR8_AGENT_DID")
	}()

	resolved, err := resolveConfig(Config{})
	if err != nil {
		t.Fatalf("resolveConfig() error: %v", err)
	}
	if resolved.NodeURL != "ws://env-host:4000" {
		t.Errorf("NodeURL = %q, want env value", resolved.NodeURL)
	}
	if resolved.APIKey != "env-key" {
		t.Errorf("APIKey = %q, want env value", resolved.APIKey)
	}
	if resolved.AgentDID != "did:web:env-agent" {
		t.Errorf("AgentDID = %q, want env value", resolved.AgentDID)
	}
}

func TestResolveConfig_ExplicitOverridesEnv(t *testing.T) {
	os.Setenv("LAYR8_API_KEY", "env-key")
	defer os.Unsetenv("LAYR8_API_KEY")

	resolved, err := resolveConfig(Config{
		NodeURL: "ws://localhost:4000",
		APIKey:  "explicit-key",
	})
	if err != nil {
		t.Fatalf("resolveConfig() error: %v", err)
	}
	if resolved.APIKey != "explicit-key" {
		t.Errorf("APIKey = %q, want explicit value over env", resolved.APIKey)
	}
}

func TestResolveConfig_MissingNodeURL(t *testing.T) {
	_, err := resolveConfig(Config{APIKey: "key"})
	if err == nil {
		t.Fatal("resolveConfig() should error when NodeURL is missing")
	}
}

func TestResolveConfig_MissingAPIKey(t *testing.T) {
	_, err := resolveConfig(Config{NodeURL: "ws://localhost:4000"})
	if err == nil {
		t.Fatal("resolveConfig() should error when APIKey is missing")
	}
}

func TestResolveConfig_EmptyAgentDID_IsAllowed(t *testing.T) {
	cfg := Config{
		NodeURL: "ws://localhost:4000",
		APIKey:  "key",
	}
	resolved, err := resolveConfig(cfg)
	if err != nil {
		t.Fatalf("resolveConfig() should allow empty AgentDID: %v", err)
	}
	if resolved.AgentDID != "" {
		t.Errorf("AgentDID should remain empty, got %q", resolved.AgentDID)
	}
}
