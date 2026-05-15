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

func TestHandlerRegistry_PayloadTypes(t *testing.T) {
	r := newHandlerRegistry()
	handler := func(msg *Message) (*Message, error) { return nil, nil }

	r.register("https://layr8.io/protocols/echo/1.0/request", handler)
	r.register("https://layr8.io/protocols/echo/1.0/response", handler)
	r.register("https://layr8.io/protocols/postgres/1.0/query", handler)

	protocols := r.payloadTypes()

	// Should deduplicate: echo/1.0 appears once, postgres/1.0 once,
	// plus the always-included report-problem/2.0
	if len(protocols) != 3 {
		t.Fatalf("protocols() len = %d, want 3, got %v", len(protocols), protocols)
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

func TestHandlerRegistry_PayloadTypes_DIDComm(t *testing.T) {
	r := newHandlerRegistry()
	handler := func(msg *Message) (*Message, error) { return nil, nil }

	r.register("https://didcomm.org/basicmessage/2.0/message", handler)
	r.register("https://didcomm.org/report-problem/2.0/problem-report", handler)

	protocols := r.payloadTypes()
	if len(protocols) != 2 {
		t.Fatalf("protocols() len = %d, want 2, got %v", len(protocols), protocols)
	}
}

func TestHandlerRegistry_PayloadTypesAlwaysIncludesProblemReport(t *testing.T) {
	r := newHandlerRegistry()
	protocols := r.payloadTypes()

	if protocols == nil {
		t.Fatal("protocols() should return non-nil slice, got nil")
	}
	// Even with no handlers, report-problem is always included
	if len(protocols) != 1 {
		t.Fatalf("protocols() len = %d, want 1, got %v", len(protocols), protocols)
	}
	if protocols[0] != "https://didcomm.org/report-problem/2.0" {
		t.Fatalf("protocols()[0] = %s, want report-problem/2.0", protocols[0])
	}
}

func TestHandlerRegistry_RegisterCatchAll(t *testing.T) {
	r := newHandlerRegistry()
	handler := func(msg *Message) (*Message, error) { return nil, nil }

	err := r.registerCatchAll(handler)
	if err != nil {
		t.Fatalf("registerCatchAll() error: %v", err)
	}

	// Catch-all should match any message type
	entry, ok := r.lookup("https://layr8.io/protocols/anything/1.0/whatever")
	if !ok {
		t.Fatal("lookup() should fall back to catch-all for unregistered type")
	}
	if entry.fn == nil {
		t.Error("handler function should not be nil")
	}
}

func TestHandlerRegistry_SpecificOverCatchAll(t *testing.T) {
	r := newHandlerRegistry()
	specificCalled := false
	catchAllCalled := false

	r.register("https://layr8.io/protocols/echo/1.0/request",
		func(msg *Message) (*Message, error) {
			specificCalled = true
			return nil, nil
		})
	r.registerCatchAll(func(msg *Message) (*Message, error) {
		catchAllCalled = true
		return nil, nil
	})

	// Specific handler should win
	entry, _ := r.lookup("https://layr8.io/protocols/echo/1.0/request")
	entry.fn(nil)
	if !specificCalled {
		t.Error("specific handler should be called for exact match")
	}
	if catchAllCalled {
		t.Error("catch-all should not be called when specific handler matches")
	}
}

func TestHandlerRegistry_DuplicateCatchAll(t *testing.T) {
	r := newHandlerRegistry()
	handler := func(msg *Message) (*Message, error) { return nil, nil }

	r.registerCatchAll(handler)
	err := r.registerCatchAll(handler)
	if err == nil {
		t.Fatal("registerCatchAll() should error on duplicate")
	}
}

func TestHandlerRegistry_CatchAllInPayloadTypes(t *testing.T) {
	r := newHandlerRegistry()
	handler := func(msg *Message) (*Message, error) { return nil, nil }

	r.register("https://layr8.io/protocols/echo/1.0/request", handler)
	r.registerCatchAll(handler)

	types := r.payloadTypes()

	has := func(s string) bool {
		for _, t := range types {
			if t == s {
				return true
			}
		}
		return false
	}

	if !has("*") {
		t.Error("payloadTypes() should include '*' when catch-all is registered")
	}
	if !has("https://layr8.io/protocols/echo/1.0") {
		t.Error("payloadTypes() should include specific protocols")
	}
}

func TestHandlerRegistry_PayloadTypes_NoCatchAll(t *testing.T) {
	r := newHandlerRegistry()
	handler := func(msg *Message) (*Message, error) { return nil, nil }

	r.register("https://layr8.io/protocols/echo/1.0/request", handler)

	types := r.payloadTypes()

	for _, pt := range types {
		if pt == "*" {
			t.Error("payloadTypes() should not include '*' without catch-all")
		}
	}
}

func TestHandlerRegistry_CatchAllWithManualAck(t *testing.T) {
	r := newHandlerRegistry()
	handler := func(msg *Message) (*Message, error) { return nil, nil }

	r.registerCatchAll(handler, WithManualAck())

	entry, _ := r.lookup("https://layr8.io/protocols/anything/1.0/x")
	if !entry.manualAck {
		t.Error("catch-all handler should have manualAck=true")
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
