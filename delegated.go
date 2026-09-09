package layr8

import "encoding/json"

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
