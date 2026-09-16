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

	// replyMode returns true if the server supports the reply protocol.
	// When true, the client sends dispatch_reply events instead of acks.
	replyMode() bool

	// setBorrower records the parent whose authority this connection's DID
	// borrows, and who chose the borrower's name.
	setBorrower(parentDID string, source ChildNameSource)

	// onDelegatedCredentials registers a callback that fires after EVERY
	// successful join and rejoin, with the reading the node returned or nil.
	onDelegatedCredentials(fn func(did string, reading *DelegatedCredentialsReading))

	// delegatedCredentials reports what the last join learned about the
	// parent's wallet, or nil when it learned nothing.
	delegatedCredentials() *DelegatedCredentialsReading

	// supportsEphemeralDelegation reports whether the node advertised
	// ephemeral_delegation/1 at join.
	supportsEphemeralDelegation() bool

	// onDelegationRefreshed registers a callback that fires after a pushed
	// replacement reading was applied.
	onDelegationRefreshed(fn func(did string, reading *DelegatedCredentialsReading, revision int64))

	// supportsEphemeralDelegationRefresh reports whether the node advertised
	// ephemeral_delegation_refresh/1 at join.
	supportsEphemeralDelegationRefresh() bool
}
