package main

import (
	"net/url"
	"strings"
	"testing"
)

func TestSenderDIDUsesPort9000(t *testing.T) {
	nodeURL := "ws://node-4-15-0:4040/plugin_socket/websocket"
	did := senderDID(nodeURL)

	if did == "" {
		t.Fatal("senderDID returned empty string")
	}
	// DID must use port 9000 (HTTP/DID resolution port), not
	// 4040 (WebSocket port), so cross-node DID resolution works.
	if !strings.HasPrefix(did, "did:web:node-4-15-0%3A9000:compat:sender-") {
		t.Fatalf("unexpected DID format: %s", did)
	}

	// Verify uniqueness
	did2 := senderDID(nodeURL)
	if did == did2 {
		t.Fatal("senderDID should return unique values")
	}
}

func TestSenderDIDHostExtraction(t *testing.T) {
	tests := []struct {
		nodeURL    string
		wantPrefix string
	}{
		{"ws://alice:4040/plugin_socket/websocket", "did:web:alice%3A9000:compat:sender-"},
		{"ws://node-4-13-40:4040/path", "did:web:node-4-13-40%3A9000:compat:sender-"},
		{"wss://prod.example.com:443/ws", "did:web:prod.example.com%3A9000:compat:sender-"},
	}

	for _, tt := range tests {
		t.Run(tt.nodeURL, func(t *testing.T) {
			did := senderDID(tt.nodeURL)
			if !strings.HasPrefix(did, tt.wantPrefix) {
				t.Fatalf("got %s, want prefix %s", did, tt.wantPrefix)
			}
		})
	}
}

func TestSenderDIDIsValidDIDWeb(t *testing.T) {
	did := senderDID("ws://myhost:4040/plugin_socket/websocket")

	// did:web DIDs use %3A for port separator
	if !strings.Contains(did, "%3A9000") {
		t.Fatalf("DID should contain %%3A9000 port: %s", did)
	}

	// Should be parseable — strip did:web: prefix and decode
	rest := strings.TrimPrefix(did, "did:web:")
	decoded, err := url.PathUnescape(rest)
	if err != nil {
		t.Fatalf("DID path should be URL-decodable: %v", err)
	}
	if !strings.Contains(decoded, "myhost:9000") {
		t.Fatalf("decoded DID should contain host:9000: %s", decoded)
	}
}
