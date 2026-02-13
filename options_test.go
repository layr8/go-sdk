package layr8

import "testing"

func TestWithManualAck(t *testing.T) {
	opts := handlerDefaults()
	WithManualAck()(&opts)
	if !opts.manualAck {
		t.Error("WithManualAck should set manualAck to true")
	}
}

func TestHandlerDefaults(t *testing.T) {
	opts := handlerDefaults()
	if opts.manualAck {
		t.Error("default manualAck should be false (auto-ack)")
	}
}

func TestWithParentThread(t *testing.T) {
	opts := requestDefaults()
	WithParentThread("parent-123")(&opts)
	if opts.parentThreadID != "parent-123" {
		t.Errorf("parentThreadID = %q, want %q", opts.parentThreadID, "parent-123")
	}
}

func TestRequestDefaults(t *testing.T) {
	opts := requestDefaults()
	if opts.parentThreadID != "" {
		t.Error("default parentThreadID should be empty")
	}
}
