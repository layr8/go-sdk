package layr8

import (
	"errors"
	"fmt"
	"testing"
)

func TestProblemReportError_Error(t *testing.T) {
	err := &ProblemReportError{
		Code:    "e.p.xfer.cant-process",
		Comment: "database unavailable",
	}
	want := "problem report [e.p.xfer.cant-process]: database unavailable"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestProblemReportError_ErrorsAs(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &ProblemReportError{
		Code:    "e.p.xfer.cant-process",
		Comment: "not found",
	})
	var probErr *ProblemReportError
	if !errors.As(err, &probErr) {
		t.Fatal("errors.As should match ProblemReportError")
	}
	if probErr.Code != "e.p.xfer.cant-process" {
		t.Errorf("Code = %q, want %q", probErr.Code, "e.p.xfer.cant-process")
	}
}

func TestConnectionError_Error(t *testing.T) {
	err := &ConnectionError{
		URL:    "ws://localhost:4000",
		Reason: "connection refused",
	}
	want := "connection error [ws://localhost:4000]: connection refused"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestConnectionError_ErrorsAs(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &ConnectionError{
		URL:    "ws://localhost:4000",
		Reason: "auth failed",
	})
	var connErr *ConnectionError
	if !errors.As(err, &connErr) {
		t.Fatal("errors.As should match ConnectionError")
	}
	if connErr.Reason != "auth failed" {
		t.Errorf("Reason = %q, want %q", connErr.Reason, "auth failed")
	}
}

func TestSentinelErrors(t *testing.T) {
	if !errors.Is(ErrNotConnected, ErrNotConnected) {
		t.Error("ErrNotConnected should match itself")
	}
	if !errors.Is(ErrAlreadyConnected, ErrAlreadyConnected) {
		t.Error("ErrAlreadyConnected should match itself")
	}
	if !errors.Is(ErrClientClosed, ErrClientClosed) {
		t.Error("ErrClientClosed should match itself")
	}
}
