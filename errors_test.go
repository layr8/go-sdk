package layr8

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"
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

func TestSDKError_Error(t *testing.T) {
	err := &SDKError{
		Kind:      ErrNoHandler,
		MessageID: "msg-1",
		Type:      "https://layr8.io/protocols/echo/1.0/request",
		From:      "did:web:bob",
		Cause:     fmt.Errorf("no handler registered"),
		Timestamp: time.Now(),
	}
	got := err.Error()
	if !strings.Contains(got, "no handler registered") {
		t.Errorf("Error() = %q, should contain cause message", got)
	}
	if !strings.Contains(got, "ErrNoHandler") {
		t.Errorf("Error() = %q, should contain error kind", got)
	}
}

func TestSDKError_Unwrap(t *testing.T) {
	cause := fmt.Errorf("underlying error")
	err := &SDKError{Kind: ErrParseFailure, Cause: cause}
	if !errors.Is(err, cause) {
		t.Error("SDKError should unwrap to its Cause")
	}
}

func TestSDKError_ErrorsAs(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &SDKError{
		Kind:  ErrServerReject,
		Cause: fmt.Errorf("unauthorized"),
	})
	var sdkErr *SDKError
	if !errors.As(err, &sdkErr) {
		t.Fatal("errors.As should match SDKError")
	}
	if sdkErr.Kind != ErrServerReject {
		t.Errorf("Kind = %v, want ErrServerReject", sdkErr.Kind)
	}
}

func TestErrorKind_String(t *testing.T) {
	tests := []struct {
		kind ErrorKind
		want string
	}{
		{ErrParseFailure, "ErrParseFailure"},
		{ErrNoHandler, "ErrNoHandler"},
		{ErrHandlerPanic, "ErrHandlerPanic"},
		{ErrServerReject, "ErrServerReject"},
		{ErrTransportWrite, "ErrTransportWrite"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("%d.String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestLogErrors(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)

	handler := LogErrors(logger)
	handler(SDKError{
		Kind:      ErrNoHandler,
		MessageID: "msg-1",
		Type:      "https://layr8.io/protocols/echo/1.0/request",
		From:      "did:web:bob",
		Cause:     fmt.Errorf("no handler"),
		Timestamp: time.Now(),
	})

	output := buf.String()
	if !strings.Contains(output, "ErrNoHandler") {
		t.Errorf("LogErrors output = %q, should contain error kind", output)
	}
	if !strings.Contains(output, "msg-1") {
		t.Errorf("LogErrors output = %q, should contain message ID", output)
	}
}
