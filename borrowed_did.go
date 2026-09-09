package layr8

import (
	"crypto/rand"
	"fmt"
	"strings"
)

// Naming a DID that borrows a parent's authority.
//
// A join may name the parent whose authority its DID borrows
// (Config.ParentDID). The node requires such a DID to be named BENEATH that
// parent — the parent, then exactly one further segment:
//
//	parent  did:web:acme.example:users:alice
//	child   did:web:acme.example:users:alice:k7m2q9x4h3bd
//
// and refuses a join whose DID is not, with the problem code
// e.join.plugin.child.not-beneath-parent.
//
// # Why the shape is fixed rather than free
//
// A cloud-node API key can restrict which DIDs it may bind. An entry is either
// an exact DID or a literal prefix with a trailing "*", so a key can admit a
// whole FAMILY of DIDs only when that family is a namespace. While a
// borrower's name was unrelated to its parent, no entry shorter than the
// borrower's whole DID covered it — and since the name is generated per
// connection, that entry cannot be written in advance. The only key that
// admitted a borrower was one with no restrictions at all, which admits every
// DID on the node.
//
// Named beneath its parent, the family is "<parent>:*", and a key carrying the
// parent plus that one namespace admits the parent and its borrowers and
// nothing else.
//
// The node is the control, not this file. A client that builds its own name
// reaches the same socket, so the rule is enforced at the join; deriving a
// conforming name here is what stops a caller having to know the rule.
//
// # The segment: random, and why not the alternatives
//
// RandomChildSegment returns 12 characters of Crockford base32 — 60 bits from
// a cryptographic source, in an alphabet that omits "i", "l", "o" and "u" so
// the value survives being read off a screen and typed back.
//
// It appears in the node's audit rows, so a person reads it. Two alternatives
// were considered and both fail on something a reader would care about:
//
//   - A counter. There is no shared state that owns one. Two processes
//     borrowing from the same parent would allocate the same number, and a
//     collision here is one connection joining onto another's identity.
//   - A name the operator supplies. That is the thing this removes: a caller
//     that has to hand-build a conforming DID is a caller that can get it
//     wrong, and the resulting refusal happens at connect time in production.
//
// Twelve characters is far more than collision needs (a single parent would
// need on the order of a billion simultaneous borrowers before a repeat became
// likely) and short enough to sit in a log line. There is no readable prefix
// on it: under this rule EVERY segment beneath a parent is a borrower, so a
// marker saying so would be true of every value it could ever have.
//
// The value is generated once, when the configuration is resolved — not per
// join. A reconnect therefore returns under the same DID, which is what lets
// the node re-mint the same delegated credentials for it.

// childSegmentAlphabet is Crockford base32: the digits and lower-case letters,
// less "i", "l", "o" and "u".
const childSegmentAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// ChildSegmentLength is the number of characters in a generated segment.
// 12 × 5 bits = 60 bits.
const ChildSegmentLength = 12

// ChildNameSource says who chose the segment of a borrower's DID.
//
// The empty value is a THIRD answer — "this client does not report it" — and
// is never folded into ChildNameSourceClient. A generated name and a
// hand-built one that conforms are identical bytes on the socket, so without
// this the node's log could not say whether a malformed borrower DID came from
// this library or from a caller's typo.
type ChildNameSource string

const (
	// ChildNameSourceSDK means this library generated the segment.
	ChildNameSourceSDK ChildNameSource = "sdk"
	// ChildNameSourceClient means the caller supplied the whole DID.
	ChildNameSourceClient ChildNameSource = "client"
)

// NotBeneathParentError reports a DID that names a parent it is not named
// beneath. Returned by resolveBorrowerDID, so the join is never written: the
// node refuses it anyway, and a refusal at connect time in production is the
// expensive way to learn this.
type NotBeneathParentError struct {
	DID       string
	ParentDID string
}

func (e *NotBeneathParentError) Error() string {
	return fmt.Sprintf(
		"AgentDID %s is not named beneath its parent %s. A DID that borrows a parent's "+
			"authority must be %q (exactly one further segment), and the node refuses a join "+
			"that is not. Set ParentDID and leave AgentDID empty to have one generated.",
		e.DID, e.ParentDID, e.ParentDID+":<segment>",
	)
}

// RandomChildSegment returns a fresh segment for a borrower's DID.
//
// Each character consumes exactly five bits of one random byte, so every
// character is uniformly distributed — a modulo over a 31- or 36-character
// alphabet would not be.
//
// crypto/rand.Read never returns an error as of Go 1.24; it panics if the
// system's randomness source fails, which is not a condition a caller could
// meaningfully handle here either.
func RandomChildSegment() string {
	buf := make([]byte, ChildSegmentLength)
	rand.Read(buf)

	out := make([]byte, ChildSegmentLength)
	for i, b := range buf {
		out[i] = childSegmentAlphabet[b&0x1f]
	}
	return string(out)
}

// DIDNamespaceOf returns the API-key entry covering every DID that may borrow
// parentDID's authority.
//
// Exported because a key is written by hand from it, and a key written with a
// different pattern is one the node's rule and the key disagree about.
func DIDNamespaceOf(parentDID string) string {
	return parentDID + ":*"
}

// IsBeneathParent reports whether childDID is named beneath parentDID — the
// parent, then exactly one further non-empty segment.
//
// False for the parent itself, for a sibling that merely starts with the
// parent's text (…:users:alicent), and for a name two segments deeper.
func IsBeneathParent(childDID, parentDID string) bool {
	if childDID == "" || parentDID == "" {
		return false
	}
	segment, ok := strings.CutPrefix(childDID, parentDID+":")
	if !ok {
		return false
	}
	return segment != "" && !strings.Contains(segment, ":")
}

// borrowerDID is a borrower's DID, and who chose its segment.
type borrowerDID struct {
	did string
	// nameSource is "" when no parent was named — there is no borrower, so
	// there is nobody who chose a borrower's name. Never folded into
	// ChildNameSourceClient.
	nameSource ChildNameSource
}

// resolveBorrowerDID settles the DID a join will use.
//
// Three inputs, three outcomes, and the three are kept apart on the wire:
//
//   - No parent named. did is returned unchanged and nothing is claimed about
//     who named it. This rule is about a relationship between two names and
//     there is only one name here.
//   - A parent, and no DID. The caller passes nothing but the parent; a
//     segment is generated and the result is reported as "sdk".
//   - A parent and a DID. The caller named the borrower itself, and the result
//     is reported as "client". A name that is not beneath the parent is an
//     ERROR here, rather than travelling to the node and coming back as a join
//     refusal at connect time — the node still refuses it, for every client
//     that is not this one.
func resolveBorrowerDID(did, parentDID string) (borrowerDID, error) {
	if parentDID == "" {
		return borrowerDID{did: did}, nil
	}

	if did == "" {
		return borrowerDID{
			did:        parentDID + ":" + RandomChildSegment(),
			nameSource: ChildNameSourceSDK,
		}, nil
	}

	if !IsBeneathParent(did, parentDID) {
		return borrowerDID{}, &NotBeneathParentError{DID: did, ParentDID: parentDID}
	}

	return borrowerDID{did: did, nameSource: ChildNameSourceClient}, nil
}
