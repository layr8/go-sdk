package layr8

import "context"

// transport is the internal interface for communication with the cloud-node.
// The current implementation uses WebSocket/Phoenix Channel (channel.go).
// Future implementations may use QUIC (quic.go).
type transport interface {
	// connect establishes the connection and joins the channel with the given protocols.
	connect(ctx context.Context, protocols []string) error

	// send writes a raw Phoenix Channel message to the connection.
	send(event string, payload []byte) error

	// sendAck acknowledges message IDs to the cloud-node.
	sendAck(ids []string) error

	// setMessageHandler registers the callback for inbound "message" events.
	// The callback receives the raw payload bytes.
	setMessageHandler(fn func(payload []byte))

	// close gracefully shuts down the connection.
	close() error

	// onDisconnect registers a callback for when the connection drops.
	onDisconnect(fn func(error))

	// onReconnect registers a callback for when the connection is restored.
	onReconnect(fn func())

	// assignedDID returns the DID assigned by the cloud-node on join (for ephemeral DIDs).
	assignedDID() string
}
