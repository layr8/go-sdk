package layr8

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAPIKey is a made-up key. Tests assert that it reaches the node in the
// x-api-key header and nowhere else: not in the URL, not in an error, not in
// any formatted form of a Config or Client.
const fakeAPIKey = "fake_key_0123_not-a-real-key-do-not-log"

// upgradeRecorder wraps a mock node and records the WebSocket upgrade
// request exactly as the node's HTTP layer receives it.
type upgradeRecorder struct {
	mu      sync.Mutex
	header  http.Header
	rawURL  string
	handler http.HandlerFunc
}

func (u *upgradeRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	u.header = r.Header.Clone()
	u.rawURL = r.URL.String()
	u.mu.Unlock()
	u.handler(w, r)
}

func (u *upgradeRecorder) seen() (http.Header, string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.header, u.rawURL
}

func joinReplying(mock *mockPhoenixServer) {
	mock.onMsg = func(msg phoenixMessage) {
		if msg.Event == "phx_join" {
			mock.sendToClient(phoenixMessage{
				JoinRef: msg.Ref,
				Ref:     msg.Ref,
				Topic:   msg.Topic,
				Event:   "phx_reply",
				Payload: json.RawMessage(`{"status":"ok","response":{}}`),
			})
		}
	}
}

func TestPhoenixChannel_SendsAPIKeyInHeaderNotURL(t *testing.T) {
	mock := newMockServer()
	joinReplying(mock)
	rec := &upgradeRecorder{handler: mock.handler}
	server := httptest.NewServer(rec)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/plugin_socket/websocket"
	ch := newPhoenixChannel(wsURL, fakeAPIKey, "did:web:test", false, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ch.connect(ctx, nil); err != nil {
		t.Fatalf("connect() error: %v", err)
	}
	defer ch.close()

	header, rawURL := rec.seen()
	if got := header.Get("x-api-key"); got != fakeAPIKey {
		t.Errorf("upgrade request x-api-key header = %q, want the configured key", got)
	}
	if strings.Contains(rawURL, fakeAPIKey) || strings.Contains(rawURL, "api_key") {
		t.Errorf("upgrade request URL carries the key: %s", rawURL)
	}
	if !strings.Contains(rawURL, "vsn=2.0.0") {
		t.Errorf("upgrade request URL lost vsn=2.0.0: %s", rawURL)
	}
}

func TestPhoenixChannel_RejectedUpgradeErrorDoesNotCarryKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/plugin_socket/websocket"
	ch := newPhoenixChannel(wsURL, fakeAPIKey, "did:web:test", false, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := ch.connect(ctx, nil)
	if err == nil {
		t.Fatal("connect() to a node that refuses the upgrade should fail")
	}
	for _, form := range []string{err.Error(), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
		if strings.Contains(form, fakeAPIKey) {
			t.Errorf("connection error carries the API key: %s", form)
		}
	}
}

func TestConfigAndClient_FormattedFormsDoNotCarryKey(t *testing.T) {
	cfg := Config{NodeURL: "ws://node.localhost/plugin_socket/websocket", APIKey: fakeAPIKey, AgentDID: "did:web:test"}
	client, err := NewClient(cfg, discardErrors)
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}

	var logged bytes.Buffer
	text := slog.New(slog.NewTextHandler(&logged, nil))
	text.Info("config", "cfg", cfg, "cfgPtr", &cfg, "client", client)
	jsonLog := slog.New(slog.NewJSONHandler(&logged, nil))
	jsonLog.Info("config", "cfg", cfg, "client", client)

	forms := map[string]string{"slog": logged.String()}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
		forms["cfg "+verb] = fmt.Sprintf(verb, cfg)
		forms["&cfg "+verb] = fmt.Sprintf(verb, &cfg)
		forms["client "+verb] = fmt.Sprintf(verb, client)
		forms["[]any "+verb] = fmt.Sprintf(verb, []any{cfg, client})
	}
	for name, out := range forms {
		if strings.Contains(out, fakeAPIKey) {
			t.Errorf("%s prints the API key: %s", name, out)
		}
	}
	if !strings.Contains(fmt.Sprint(cfg), "did:web:test") {
		t.Errorf("redacted Config should still show the non-secret fields: %s", fmt.Sprint(cfg))
	}
}
