package layr8

import (
	"os"
	"testing"
	"time"
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

func TestResolveConfig_NormalizeHTTPS(t *testing.T) {
	resolved, err := resolveConfig(Config{
		NodeURL: "https://mynode.layr8.cloud/plugin_socket/websocket",
		APIKey:  "key",
	})
	if err != nil {
		t.Fatalf("resolveConfig() error: %v", err)
	}
	if resolved.NodeURL != "wss://mynode.layr8.cloud/plugin_socket/websocket" {
		t.Errorf("NodeURL = %q, want https:// normalized to wss://", resolved.NodeURL)
	}
}

func TestResolveConfig_NormalizeHTTP(t *testing.T) {
	resolved, err := resolveConfig(Config{
		NodeURL: "http://localhost:4000/plugin_socket/websocket",
		APIKey:  "key",
	})
	if err != nil {
		t.Fatalf("resolveConfig() error: %v", err)
	}
	if resolved.NodeURL != "ws://localhost:4000/plugin_socket/websocket" {
		t.Errorf("NodeURL = %q, want http:// normalized to ws://", resolved.NodeURL)
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

func TestRestURLFromWebSocket(t *testing.T) {
	tests := []struct {
		name  string
		wsURL string
		want  string
	}{
		{
			name:  "ws with path",
			wsURL: "ws://alice-test.localhost/plugin_socket/websocket",
			want:  "http://alice-test.localhost",
		},
		{
			name:  "wss with path",
			wsURL: "wss://alice-test.localhost/plugin_socket/websocket",
			want:  "https://alice-test.localhost",
		},
		{
			name:  "ws with port and path",
			wsURL: "ws://localhost:4000/plugin_socket/websocket",
			want:  "http://localhost:4000",
		},
		{
			name:  "wss with port and path",
			wsURL: "wss://mynode.layr8.cloud:443/plugin_socket/websocket",
			want:  "https://mynode.layr8.cloud:443",
		},
		{
			name:  "ws no path",
			wsURL: "ws://localhost:4000",
			want:  "http://localhost:4000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := restURLFromWebSocket(tt.wsURL)
			if got != tt.want {
				t.Errorf("restURLFromWebSocket(%q) = %q, want %q", tt.wsURL, got, tt.want)
			}
		})
	}
}

func TestResolveConfig_ProtocolsPassthrough(t *testing.T) {
	cfg := Config{
		NodeURL:   "ws://localhost:4000",
		APIKey:    "test-key",
		Protocols: []string{"https://layr8.test/echo/1.0"},
	}
	resolved, err := resolveConfig(cfg)
	if err != nil {
		t.Fatalf("resolveConfig() error: %v", err)
	}
	if len(resolved.Protocols) != 1 || resolved.Protocols[0] != "https://layr8.test/echo/1.0" {
		t.Errorf("Protocols = %v, want [https://layr8.test/echo/1.0]", resolved.Protocols)
	}
}

func TestResolveConfig_NilProtocols(t *testing.T) {
	cfg := Config{
		NodeURL: "ws://localhost:4000",
		APIKey:  "test-key",
	}
	resolved, err := resolveConfig(cfg)
	if err != nil {
		t.Fatalf("resolveConfig() error: %v", err)
	}
	if resolved.Protocols != nil {
		t.Errorf("Protocols = %v, want nil", resolved.Protocols)
	}
}

// --- grant / REST deadlines ---

func TestResolveConfig_GrantDefaults(t *testing.T) {
	cfg, err := resolveConfig(Config{NodeURL: "ws://localhost:4000", APIKey: "k"})
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}
	// On by default: a grant the node requires and the SDK does not attach is
	// indistinguishable, from the caller's side, from one that was never issued.
	if cfg.AttachGrants == nil || !*cfg.AttachGrants {
		t.Error("AttachGrants should default to on")
	}
	if cfg.GrantCacheTTL != DefaultGrantCacheTTL {
		t.Errorf("GrantCacheTTL = %v", cfg.GrantCacheTTL)
	}
	if cfg.GrantReadTimeout != DefaultGrantReadTimeout {
		t.Errorf("GrantReadTimeout = %v", cfg.GrantReadTimeout)
	}
	if cfg.RESTTimeout != DefaultRESTTimeout {
		t.Errorf("RESTTimeout = %v", cfg.RESTTimeout)
	}
}

func TestResolveConfig_AttachGrantsFromEnv(t *testing.T) {
	t.Setenv("LAYR8_ATTACH_GRANTS", "false")
	cfg, _ := resolveConfig(Config{NodeURL: "ws://localhost:4000", APIKey: "k"})
	if cfg.AttachGrants == nil || *cfg.AttachGrants {
		t.Fatal("LAYR8_ATTACH_GRANTS=false should turn attachment off")
	}
}

func TestResolveConfig_UnrecognisedEnvBoolLeavesTheDefaultAlone(t *testing.T) {
	// Including the empty string an unset-but-exported variable produces —
	// which must not read as false.
	t.Setenv("LAYR8_ATTACH_GRANTS", "")
	cfg, _ := resolveConfig(Config{NodeURL: "ws://localhost:4000", APIKey: "k"})
	if cfg.AttachGrants == nil || !*cfg.AttachGrants {
		t.Fatal("an empty LAYR8_ATTACH_GRANTS should leave attachment on")
	}
}

func TestResolveConfig_MillisecondEnvVars(t *testing.T) {
	t.Setenv("LAYR8_GRANT_CACHE_MS", "5000")
	t.Setenv("LAYR8_GRANT_READ_TIMEOUT_MS", "500")
	t.Setenv("LAYR8_REST_TIMEOUT_MS", "1000")

	cfg, _ := resolveConfig(Config{NodeURL: "ws://localhost:4000", APIKey: "k"})
	if cfg.GrantCacheTTL != 5*time.Second {
		t.Errorf("GrantCacheTTL = %v", cfg.GrantCacheTTL)
	}
	if cfg.GrantReadTimeout != 500*time.Millisecond {
		t.Errorf("GrantReadTimeout = %v", cfg.GrantReadTimeout)
	}
	if cfg.RESTTimeout != time.Second {
		t.Errorf("RESTTimeout = %v", cfg.RESTTimeout)
	}
}

func TestResolveConfig_GarbageEnvFallsBackToTheDefault(t *testing.T) {
	// A typo must not become a load problem nobody would connect to it.
	t.Setenv("LAYR8_GRANT_CACHE_MS", "not-a-number")
	cfg, _ := resolveConfig(Config{NodeURL: "ws://localhost:4000", APIKey: "k"})
	if cfg.GrantCacheTTL != DefaultGrantCacheTTL {
		t.Fatalf("GrantCacheTTL = %v, want the default", cfg.GrantCacheTTL)
	}
}

func TestResolveConfig_GrantReadTimeoutCannotBeDisabled(t *testing.T) {
	// A zero deadline would abort every read before it started, turning a
	// mistyped variable into an agent that attaches nothing at all — the exact
	// failure the whole feature exists to end. Explicit and env alike.
	cfg, _ := resolveConfig(Config{
		NodeURL: "ws://localhost:4000", APIKey: "k", GrantReadTimeout: -1,
	})
	if cfg.GrantReadTimeout != DefaultGrantReadTimeout {
		t.Errorf("explicit negative: GrantReadTimeout = %v", cfg.GrantReadTimeout)
	}

	t.Setenv("LAYR8_GRANT_READ_TIMEOUT_MS", "0")
	cfg, _ = resolveConfig(Config{NodeURL: "ws://localhost:4000", APIKey: "k"})
	if cfg.GrantReadTimeout != DefaultGrantReadTimeout {
		t.Errorf("env zero: GrantReadTimeout = %v", cfg.GrantReadTimeout)
	}
}

func TestResolveConfig_RESTTimeoutCanBeDisabled(t *testing.T) {
	// The contrast with GrantReadTimeout is deliberate: here "no deadline" is
	// the pre-existing behaviour and a legitimate thing for an operator with a
	// slow node to ask for.
	cfg, _ := resolveConfig(Config{NodeURL: "ws://localhost:4000", APIKey: "k", RESTTimeout: -1})
	if cfg.RESTTimeout != 0 {
		t.Fatalf("RESTTimeout = %v, want 0 (unbounded)", cfg.RESTTimeout)
	}
}
