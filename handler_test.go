package layr8

import (
	"testing"
)

func TestHandlerRegistry_Register(t *testing.T) {
	r := newHandlerRegistry()
	handler := func(msg *Message) (*Message, error) { return nil, nil }

	err := r.register("https://layr8.io/protocols/echo/1.0/request", handler)
	if err != nil {
		t.Fatalf("register() error: %v", err)
	}

	entry, ok := r.lookup("https://layr8.io/protocols/echo/1.0/request")
	if !ok {
		t.Fatal("lookup() should find registered handler")
	}
	if entry.fn == nil {
		t.Error("handler function should not be nil")
	}
}

func TestHandlerRegistry_RegisterWithManualAck(t *testing.T) {
	r := newHandlerRegistry()
	handler := func(msg *Message) (*Message, error) { return nil, nil }

	r.register("https://layr8.io/protocols/echo/1.0/request", handler, WithManualAck())

	entry, _ := r.lookup("https://layr8.io/protocols/echo/1.0/request")
	if !entry.manualAck {
		t.Error("handler should have manualAck=true")
	}
}

func TestHandlerRegistry_DuplicateRegistration(t *testing.T) {
	r := newHandlerRegistry()
	handler := func(msg *Message) (*Message, error) { return nil, nil }

	r.register("https://layr8.io/protocols/echo/1.0/request", handler)
	err := r.register("https://layr8.io/protocols/echo/1.0/request", handler)
	if err == nil {
		t.Fatal("register() should error on duplicate message type")
	}
}

func TestHandlerRegistry_LookupMissing(t *testing.T) {
	r := newHandlerRegistry()
	_, ok := r.lookup("https://layr8.io/protocols/echo/1.0/unknown")
	if ok {
		t.Error("lookup() should return false for unregistered type")
	}
}

func TestHandlerRegistry_DeriveProtocols(t *testing.T) {
	r := newHandlerRegistry()
	handler := func(msg *Message) (*Message, error) { return nil, nil }

	r.register("https://layr8.io/protocols/echo/1.0/request", handler)
	r.register("https://layr8.io/protocols/echo/1.0/response", handler)
	r.register("https://layr8.io/protocols/postgres/1.0/query", handler)

	protocols := r.protocols()

	// Should deduplicate: echo/1.0 appears once, postgres/1.0 once
	if len(protocols) != 2 {
		t.Fatalf("protocols() len = %d, want 2, got %v", len(protocols), protocols)
	}

	has := func(p string) bool {
		for _, proto := range protocols {
			if proto == p {
				return true
			}
		}
		return false
	}

	if !has("https://layr8.io/protocols/echo/1.0") {
		t.Error("protocols should include echo/1.0")
	}
	if !has("https://layr8.io/protocols/postgres/1.0") {
		t.Error("protocols should include postgres/1.0")
	}
}

func TestHandlerRegistry_DeriveProtocols_DIDComm(t *testing.T) {
	r := newHandlerRegistry()
	handler := func(msg *Message) (*Message, error) { return nil, nil }

	r.register("https://didcomm.org/basicmessage/2.0/message", handler)
	r.register("https://didcomm.org/report-problem/2.0/problem-report", handler)

	protocols := r.protocols()
	if len(protocols) != 2 {
		t.Fatalf("protocols() len = %d, want 2, got %v", len(protocols), protocols)
	}
}

func TestDeriveProtocol(t *testing.T) {
	tests := []struct {
		msgType string
		want    string
	}{
		{
			"https://layr8.io/protocols/echo/1.0/request",
			"https://layr8.io/protocols/echo/1.0",
		},
		{
			"https://layr8.io/protocols/postgres/1.0/query",
			"https://layr8.io/protocols/postgres/1.0",
		},
		{
			"https://didcomm.org/basicmessage/2.0/message",
			"https://didcomm.org/basicmessage/2.0",
		},
	}

	for _, tt := range tests {
		got := deriveProtocol(tt.msgType)
		if got != tt.want {
			t.Errorf("deriveProtocol(%q) = %q, want %q", tt.msgType, got, tt.want)
		}
	}
}
