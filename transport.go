package layr8

import "context"

// serverReply is the parsed Phoenix phx_reply for a sent message.
type serverReply struct {
	Status string // "ok" or "error"
	Reason string // server's rejection reason (e.g., "unauthorized")
}

// transport is the internal interface for communication with the cloud-node.
// The current implementation uses WebSocket/Phoenix Channel (channel.go).
// Future implementations may use QUIC (quic.go).
type transport interface {
	// connect establishes the connection and joins the channel with the given protocols.
	connect(ctx context.Context, protocols []string) error

	// send writes a raw Phoenix Channel message and waits for the server's phx_reply.
	// The context controls the timeout for waiting on the reply.
	send(ctx context.Context, event string, payload []byte) (serverReply, error)

	// sendFireAndForget writes a raw Phoenix Channel message without waiting for a reply.
	sendFireAndForget(event string, payload []byte) error

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
