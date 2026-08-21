package layr8

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Attaching an identity credential — a credential about WHO THE SENDER IS, not
// about what it may do.
//
// An identity credential rides in the same Attachments slice, with the same
// media type, as a Verifiable Grant. The cloud-node tells the two apart on one
// test — credentialSubject.scope: present and non-empty is a grant and feeds
// the policy's "credentials" input; absent or empty is an identity credential
// and feeds "sender_credentials", where a grant's constraints.senderCredentials
// requirement can see it.
//
// # Why this is a separate path, not a wallet feature
//
// The wallet SELECTS grants: it ranks candidates by how well their scope covers
// the outbound message. An identity credential has no scope, so it cannot be
// ranked — and there is nothing here for the wallet to select BY either. The
// requirement it would have to satisfy lives in the grant held by the RECIPIENT
// and never reaches the sender before the call.
//
// So an SDK that chose identity credentials automatically would have exactly
// one implementable behaviour: attach everything the holder has. That is a
// disclosure decision wearing the costume of a convenience feature. Which
// claims about a person or an organisation a counterparty gets to see is the
// holder's call, made per message.
//
// The caller names the credential. This file only builds the envelope.

// CredentialMediaType is the only media type the node's credential extractor
// keeps, matched by exact string equality. Everything else — including
// "application/vp+jwt", the Verifiable Presentation envelope — is dropped in
// silence, and the denial that follows is byte-for-byte the one for attaching
// nothing at all.
const CredentialMediaType = "application/vc+jwt"

// ErrNotCompactJWS is returned for an argument that is not a compact JWS
// (three non-empty dot-separated segments). The node can verify nothing else,
// so putting it on the wire only buys a denial that names the wrong problem.
var ErrNotCompactJWS = errors.New("layr8: not a compact JWS")

// ErrCredentialIsGrant is returned for a credential carrying a non-empty
// credentialSubject.scope. That is a GRANT: the node would route it to the
// policy's "credentials" input, it would not satisfy a senderCredentials
// requirement, and the resulting denial is indistinguishable from having
// attached nothing. The check is local, exact and free — refusing here is the
// difference between an error at the call site and a silent misroute diagnosed
// at the far end. Grants belong to the wallet, which selects and caps them;
// this path does neither.
var ErrCredentialIsGrant = errors.New("layr8: credential has a scope, so it is a Verifiable Grant, not an identity credential")

// decodeIdentityPayload is deliberately a local copy of decodeJWTPayload rather
// than a call to it.
//
// The identity path runs BESIDE the grant wallet, not through it. Reusing the
// wallet's decoder would quietly make this path depend on code whose job is
// grant selection; a few lines of base64url is the cheaper coupling to avoid.
func decodeIdentityPayload(jws string) map[string]json.RawMessage {
	parts := strings.Split(jws, ".")
	if len(parts) < 2 {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	// nil for everything unreadable, and that includes a payload that is valid
	// JSON but not an object: a scalar or an array has no credentialSubject to
	// read, which is indistinguishable downstream from a scope-free credential.
	// json.Unmarshal into a map rejects both, and leaves the map nil for the
	// JSON literal null, which it accepts without error.
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	return payload
}

// credentialShape is what a compact JWS is, by the same test the node routes on.
//
// THREE outcomes, not two. credentialSubject.scope present and non-empty is a
// grant; absent or empty is an identity credential; a payload that does not
// decode is NEITHER.
//
// The third one is why this is not a bool. A helper that answers "scope length
// 0" both for a credential that has no scope and for a string it could not
// decode makes an undecodable attachment indistinguishable from an identity
// credential. That is not cosmetic: identity credentials are the one caller
// attachment shape that does NOT stand the wallet aside, so a caller who
// attached three dots and a shrug would get the wallet's grants appended to its
// message — a disclosure it never asked for, chosen silently, which is the exact
// thing the explicit-selection rule exists to forbid. Every other foreign
// attachment displaces the wallet; something unreadable has to behave like the
// rest of them, not like the privileged case.
type credentialShape int

const (
	// shapeUndecodable is the zero value on purpose: decodeIdentityPayload
	// returns nil for everything it cannot read, and nil must not be able to
	// fall through into the identity case by accident.
	shapeUndecodable credentialShape = iota
	shapeIdentity
	shapeGrant
)

// identityClaims returns the credential's shape and its own id, as the node
// reads them.
//
// Claims are at the TOP LEVEL of the payload on this node; the "vc" wrapper is
// the standard alternative and both are accepted — same as parseCredential.
func identityClaims(jws string) (shape credentialShape, id string) {
	payload := decodeIdentityPayload(jws)
	if payload == nil {
		return shapeUndecodable, ""
	}

	vc := payload
	if wrapped, ok := payload["vc"]; ok {
		var inner map[string]json.RawMessage
		if json.Unmarshal(wrapped, &inner) == nil && inner != nil {
			vc = inner
		}
	}

	var subject struct {
		Scope []json.RawMessage `json:"scope"`
	}
	if raw, ok := vc["credentialSubject"]; ok {
		_ = json.Unmarshal(raw, &subject)
	}

	id = firstString(vc, "id")
	if id == "" {
		id = firstString(payload, "jti")
	}
	if len(subject.Scope) > 0 {
		return shapeGrant, id
	}
	return shapeIdentity, id
}

// IdentityAttachment builds the attachment that carries one identity
// credential.
//
// credentialJWS is the credential itself — the compact JWS, which
// ListCredentials returns as CredentialJWT. It is not a credential id: an id
// would mean a read from the node, and this runs inside the send path where an
// unbounded read stalls the channel (the reason Config.GrantReadTimeout
// exists). A JWS also lets a caller attach a credential the node's store has
// never seen.
//
// It returns ErrNotCompactJWS or ErrCredentialIsGrant rather than an attachment
// the far end will misread.
//
//	creds, _ := client.ListCredentials(ctx)
//	att, err := layr8.IdentityAttachment(creds[0].CredentialJWT)
//	if err != nil {
//	    return err
//	}
//	err = client.Send(ctx, &layr8.Message{
//	    To:          []string{peer},
//	    Type:        "https://layr8.io/protocols/mcp/1.0/tools-call",
//	    Body:        body,
//	    Attachments: []layr8.Attachment{att},
//	})
//
// Attaching one does NOT cost the message its grants: withGrants appends the
// wallet's selection after attachments that are all identity credentials.
func IdentityAttachment(credentialJWS string) (Attachment, error) {
	parts := strings.Split(credentialJWS, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return Attachment{}, fmt.Errorf(
			"%w: expected three non-empty dot-separated segments", ErrNotCompactJWS)
	}

	shape, id := identityClaims(credentialJWS)
	if shape == shapeUndecodable {
		return Attachment{}, fmt.Errorf(
			"%w: three segments is not the same as three READABLE segments. The payload "+
				"segment must be base64url-encoded JSON object; one that does not decode says "+
				"nothing about credentialSubject.scope, so nothing here can show it to be an "+
				"identity credential rather than a grant", ErrNotCompactJWS)
	}
	if shape == shapeGrant {
		return Attachment{}, fmt.Errorf(
			"%w: the node would route it to the policy's \"credentials\" input and it would "+
				"never satisfy a senderCredentials requirement. Let the wallet attach grants, "+
				"or put it in Attachments yourself", ErrCredentialIsGrant)
	}

	if id == "" {
		// The SIGNATURE segment as the fallback, not the head of the JWT: every
		// credential from one issuer shares a header, so a head-derived id gives
		// them all the SAME attachment id — and a frame carrying two attachments
		// with one id is a frame whose second attachment may not survive.
		sig := parts[2]
		if len(sig) > 32 {
			sig = sig[:32]
		}
		id = "urn:jws:" + sig
	}

	return Attachment{
		ID:        id,
		MediaType: CredentialMediaType,
		Data:      AttachmentData{JWS: credentialJWS},
	}, nil
}

// IsIdentityAttachment reports whether att is an identity credential — the same
// test the node routes on, applied to what is actually on the message.
//
// Used by withGrants to decide whether caller-supplied attachments should still
// displace the wallet. Nothing here trusts how the attachment was built: a
// hand-assembled one counts exactly the same.
//
// False for an attachment whose JWS does not decode, as well as for one that
// carries a scope. Only a credential this can actually READ as scope-free is an
// identity credential; see credentialShape.
func IsIdentityAttachment(att Attachment) bool {
	if att.MediaType != CredentialMediaType {
		return false
	}
	jws, ok := att.Data.JWS.(string)
	if !ok || len(strings.Split(jws, ".")) != 3 {
		return false
	}
	shape, _ := identityClaims(jws)
	return shape == shapeIdentity
}
