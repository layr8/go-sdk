package layr8

import (
	"encoding/json"
	"strconv"
)

// DelegatedCredential is one credential the node signed for this DID out of
// what its parent holds.
//
// CredentialJWT is a compact JWS, ready to attach to an outbound message as
// application/vc+jwt — the same shape GET /api/v1/credentials returns, so the
// wallet parses it with no special case.
//
// It exists NOWHERE BUT the join reply. The node stores nothing about it: a
// credential belonging to a connection has the lifetime of that connection.
// There is no endpoint that will hand it back, and losing the join reply means
// rejoining to be issued a new one.
type DelegatedCredential struct {
	// ID is the credential's own id.
	ID string `json:"id"`
	// ParentCapability is the parent credential it cites in
	// credentialSubject.delegation.parentCapability.
	ParentCapability string `json:"parent_capability"`
	// CredentialJWT is the signed credential, as a compact JWS.
	CredentialJWT string `json:"credential_jwt"`
}

// DelegationStatus says how completely the node read the parent's wallet.
//
//   - DelegationComplete — it was read and every grant in it was delegated.
//     Credentials is the whole answer, and an empty list here is the measured
//     statement that the parent holds no grants.
//   - DelegationPartial — it was read and at least one grant could NOT be
//     delegated. Credentials holds the rest, and there is authority the parent
//     has that this connection will never get. The node's log says why.
//   - DelegationUnread — it could not be read at all. Credentials is empty and
//     that emptiness measures nothing.
type DelegationStatus string

const (
	DelegationComplete DelegationStatus = "complete"
	DelegationPartial  DelegationStatus = "partial"
	DelegationUnread   DelegationStatus = "unread"
)

// DelegatedCredentialsReading is what one join learned about the parent's
// wallet.
//
// The node sends an object rather than a bare array precisely so that
// "unread" has a value of its own. When it was an array, an unreadable wallet
// arrived as [] — the same value that means "read, and it grants nothing" —
// and that is the reassuring one of the two: a client acting on it sends its
// messages bare and gets back a denial naming a grant.
type DelegatedCredentialsReading struct {
	Status      DelegationStatus      `json:"status"`
	Credentials []DelegatedCredential `json:"credentials"`
}

// parseDelegatedCredentials returns a join reply's delegated_credentials, or
// nil if it is not a reading.
//
// Anything that is not a well-formed reading — absent, an array (an older
// node, before status existed), a status this build does not know — is nil,
// which means "no reading". It is never coerced into
// {Status: complete, Credentials: []}: that would state that a wallet was read
// and grants nothing, which is the one thing none of those inputs says.
func parseDelegatedCredentials(raw json.RawMessage) *DelegatedCredentialsReading {
	if len(raw) == 0 {
		return nil
	}

	var probe struct {
		Status      *string           `json:"status"`
		Credentials []json.RawMessage `json:"credentials"`
	}
	// An array, a string, null or a number all fail to unmarshal into a struct,
	// or leave Status nil — every one of them is "no reading".
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil
	}
	if probe.Status == nil || probe.Credentials == nil {
		return nil
	}

	status := DelegationStatus(*probe.Status)
	switch status {
	case DelegationComplete, DelegationPartial, DelegationUnread:
	default:
		return nil
	}

	var reading DelegatedCredentialsReading
	if err := json.Unmarshal(raw, &reading); err != nil {
		return nil
	}
	if reading.Credentials == nil {
		reading.Credentials = []DelegatedCredential{}
	}
	return &reading
}

// DelegationRefreshCapability is the capability a node announces when it
// pushes a replacement reading to a live borrowed child whose join asked for
// it with delegation_refresh: true.
const DelegationRefreshCapability = "ephemeral_delegation_refresh/1"

// parseDelegationPush returns a pushed delegated_credentials reading and its
// revision, or ok=false if it is not one to apply.
//
// The payload is the join reply's reading plus revision, so it goes through
// the same parser. On top of that:
//
//   - revision must be a non-negative integer. Without it a consumer cannot
//     tell a late push from a new one.
//   - status "unread" is never pushed by the node: a refresh whose read failed
//     sends nothing, and the last reading stands. A push that says unread
//     anyway is dropped rather than applied, because applying it would replace
//     a set that came from a real read with an empty list that measures
//     nothing, and take working authority away.
func parseDelegationPush(raw json.RawMessage) (reading *DelegatedCredentialsReading, revision int64, ok bool) {
	reading = parseDelegatedCredentials(raw)
	if reading == nil || reading.Status == DelegationUnread {
		return nil, 0, false
	}
	revision, ok = revisionOf(raw)
	if !ok {
		return nil, 0, false
	}
	return reading, revision, true
}

// joinRevision is a join reply's delegated_credentials.revision, or 0 when an
// older node sent none.
func joinRevision(raw json.RawMessage) int64 {
	if rev, ok := revisionOf(raw); ok {
		return rev
	}
	return 0
}

// revisionOf reads a non-negative JSON integer "revision". A quoted number, a
// fraction or a negative value is not a revision.
func revisionOf(raw json.RawMessage) (int64, bool) {
	var probe struct {
		Revision json.RawMessage `json:"revision"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil || len(probe.Revision) == 0 {
		return 0, false
	}
	for _, b := range probe.Revision {
		if b < '0' || b > '9' {
			return 0, false
		}
	}
	rev, err := strconv.ParseInt(string(probe.Revision), 10, 64)
	if err != nil {
		return 0, false
	}
	return rev, true
}
