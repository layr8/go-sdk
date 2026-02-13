package layr8

import (
	"errors"
	"fmt"
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
