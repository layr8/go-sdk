package layr8

import (
	"errors"
	"fmt"
	"log"
	"time"
)

// Sentinel errors for client state.
var (
	ErrNotConnected     = errors.New("client is not connected")
	ErrAlreadyConnected = errors.New("client is already connected")
	ErrClientClosed     = errors.New("client is closed")
)

// ProblemReportError represents a DIDComm problem report received from a remote agent.
// See: https://identity.foundation/didcomm-messaging/spec/#problem-reports
type ProblemReportError struct {
	Code    string `json:"code"`
	Comment string `json:"comment"`
}

func (e *ProblemReportError) Error() string {
	return fmt.Sprintf("problem report [%s]: %s", e.Code, e.Comment)
}

// ConnectionError represents a failure to connect or maintain connection to the cloud-node.
type ConnectionError struct {
	URL    string
	Reason string
}

func (e *ConnectionError) Error() string {
	return fmt.Sprintf("connection error [%s]: %s", e.URL, e.Reason)
}

// ErrorKind classifies SDK-level errors that cannot be returned to a caller.
type ErrorKind int

const (
	ErrParseFailure   ErrorKind = iota // inbound message couldn't be parsed
	ErrNoHandler                       // no handler registered for message type
	ErrHandlerPanic                    // handler goroutine panicked
	ErrServerReject                    // server refused a sent message (authz, routing, etc.)
	ErrTransportWrite                  // failed to write to connection
)

var errorKindNames = [...]string{
	ErrParseFailure:   "ErrParseFailure",
	ErrNoHandler:      "ErrNoHandler",
	ErrHandlerPanic:   "ErrHandlerPanic",
	ErrServerReject:   "ErrServerReject",
	ErrTransportWrite: "ErrTransportWrite",
}

func (k ErrorKind) String() string {
	if int(k) >= 0 && int(k) < len(errorKindNames) {
		return errorKindNames[k]
	}
	return fmt.Sprintf("ErrorKind(%d)", k)
}

// SDKError represents an error that the SDK could not deliver to a direct caller.
// These errors are routed to the ErrorHandler provided at client creation.
type SDKError struct {
	Kind      ErrorKind
	MessageID string
	Type      string // DIDComm message type, if known
	From      string // sender DID, if known
	Cause     error
	Raw       []byte    // raw payload (for parse failures)
	Timestamp time.Time
}

func (e *SDKError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v (msg=%s type=%s from=%s)", e.Kind, e.Cause, e.MessageID, e.Type, e.From)
	}
	return fmt.Sprintf("%s (msg=%s type=%s from=%s)", e.Kind, e.MessageID, e.Type, e.From)
}

func (e *SDKError) Unwrap() error {
	return e.Cause
}

// ErrorHandler is called for every SDK-level error that cannot be returned
// to a direct caller. It MUST be provided when creating a client.
type ErrorHandler func(SDKError)

// LogErrors returns an ErrorHandler that logs all SDK errors to the given logger.
func LogErrors(logger *log.Logger) ErrorHandler {
	return func(e SDKError) {
		if e.Cause != nil {
			logger.Printf("[layr8] %s: %v (msg=%s type=%s from=%s)", e.Kind, e.Cause, e.MessageID, e.Type, e.From)
		} else {
			logger.Printf("[layr8] %s (msg=%s type=%s from=%s)", e.Kind, e.MessageID, e.Type, e.From)
		}
	}
}
