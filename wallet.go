package layr8

// Attaching Verifiable Grants to outbound messages.
//
// # Why this exists
//
// The cloud-node REQUIRES a Verifiable Grant for any message its policy does
// not allow outright, and until now nothing in this SDK attached one. There was
// no enforcement on outgoing requests — there was no *mechanism*. An agent that
// connected directly, on any protocol, sent nothing and was denied with "no
// grant covers this call": a message that reads as "your grant is
// misconfigured" when the truth is "no credential was ever put on the wire".
//
// That misreading is the expensive part. Two teams spent days on it — checking
// the grant, the Space policy, whether the PDP expanded messageTypes: ["*"].
// The sender is the only party that knows it attached nothing, so the sender is
// the only one that can say so: see Config.OnGrantMiss.
//
// Cross-language contract: contracts/sender-cn-vg-attachment.md. The Node SDK's
// src/wallet.ts is the same abstraction.
//
// # The attachment shape is load-bearing
//
// MediaType is the ONLY thing the node's credential extractor filters on: it
// keeps attachments whose media type is exactly "application/vc+jwt" and drops
// every other one SILENTLY, before looking at the data at all. A Verifiable
// Presentation ("application/vp+jwt") is discarded on that rule, and the denial
// that follows is byte-for-byte the one you get for attaching nothing — which is
// how a partner team spent a day looking at a grant that was fine.
//
// Data.JWS is the primary place the JWS is read from, and what this SDK writes.
// Data.Base64 is NOT dropped: the extractor falls back to it and base64url-
// decodes it. Data.JWS is still the right choice — it is the field the extractor
// reaches for first and the one the whole ecosystem writes — but the reason is
// "primary path", not "the alternative is discarded".
//
// # Over-attaching is free; under-attaching is not
//
// grant.rego allows on the FIRST passing grant and simply ignores the rest, so
// an extra credential on the wire costs nothing. A credential withheld costs a
// working call, and the failure is invisible — it presents as the same "no grant
// covers this call" this file exists to end.
//
// That asymmetry decides every judgement call here. Nothing filters on the
// grant's credentialSubject.grant.tools allowlist: no policy reads it — helix
// evaluates credentialSubject.constraints.rego keyed by grant id, which this
// side cannot reproduce and should not try to. tools only ranks candidates when
// the cap bites.
//
// # Selection mirrors the policy, and deliberately errs wide
//
// covers mirrors helix's structure_v2.rego: some scope entry must match the
// protocol, the message type and the resource. What this does NOT do is decide
// anything the PDP decides — revocation and validity windows are checked there,
// against sources this side cannot see. Attaching a revoked or expired grant
// costs one denial; withholding one because a local cache thought it was dead
// costs a working call, and that failure is silent.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// MaxAttachedGrants is the most credentials put on one message.
//
// Over-attaching is free at the policy, but not on the wire: a holder with
// per-tool grants can hold dozens, each a 1-2KB JWT, on every message. The cap
// is far above any real holding; when it bites, the entries kept are the most
// likely to matter (see selectGrants) and the caller is TOLD, because a
// credential dropped here produces the same indistinguishable denial as one
// never held.
const MaxAttachedGrants = 16

// HeldCredential is a grant this DID holds, decoded far enough to decide
// whether it covers a message. RawJWT is what actually goes on the wire.
type HeldCredential struct {
	ID     string
	RawJWT string
	Scope  []grantScope
	// Tools is the grant's tool allowlist (credentialSubject.grant.tools).
	// Empty means any tool.
	Tools []string
	// ExpiresAt is the node's valid_until, when it sent one.
	//
	// NOT used to withhold anything — validity is the PDP's decision, made
	// against a clock this side cannot see, and a skewed local clock dropping a
	// live grant fails silently. It only breaks ties under MaxAttachedGrants, so
	// a live grant never loses its slot to one that has certainly lapsed.
	ExpiresAt time.Time
}

type grantScope struct {
	Protocol     string   `json:"protocol"`
	MessageTypes []string `json:"messageTypes"`
	Resource     string   `json:"resource"`
}

// credentialReader reads the stored credential records for a holder DID.
//
// A function rather than a *restClient so the cache and its failure TTL are
// testable without a node.
type credentialReader func(ctx context.Context, did string) ([]map[string]json.RawMessage, error)

// splitTypeURI splits a DIDComm type into (protocol, messageType).
//
// A type URI is <protocol>/<messageType> and the policy matches the two
// separately. Splitting on the LAST slash is what the node's own parser does.
func splitTypeURI(typeURI string) (string, string) {
	cut := strings.LastIndex(typeURI, "/")
	if cut <= 0 {
		return typeURI, ""
	}
	return typeURI[:cut], typeURI[cut+1:]
}

// toolNameOf returns the tool name the policy will match, if this body carries
// one.
func toolNameOf(body any, bodyRaw json.RawMessage) string {
	var probe struct {
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}

	if len(bodyRaw) > 0 {
		if err := json.Unmarshal(bodyRaw, &probe); err == nil {
			return probe.Params.Name
		}
		return ""
	}
	if body == nil {
		return ""
	}
	data, err := json.Marshal(body)
	if err != nil {
		return ""
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return ""
	}
	return probe.Params.Name
}

// ── structure_v2.rego mirror ──

func protocolMatches(scopeProtocol, want string) bool {
	return scopeProtocol == "*" || scopeProtocol == want
}

func messageTypeMatches(types []string, want string) bool {
	for _, t := range types {
		if t == "*" || t == want {
			return true
		}
	}
	return false
}

// resourceMatches implements the three ways a scope's resource can cover a
// message's, in the order structure_v2.rego's _resource_ok states them:
//
//  1. equal;
//  2. "foo/*" covers anything under "foo/" — the rego strips only the "*", so
//     the trailing slash is part of the prefix and "foo/*" does not cover
//     "foobar";
//  3. a bare "foo" covers "foo/bar" — a SEGMENT prefix, requiring the next
//     character to be "/", so "tables" covers "tables/customers" but not
//     "tables_archive".
//
// Clause 3 is the one that points the wrong way when it's missing: this side
// withholds a grant the policy would have honoured, which is the failure that
// costs a working call and shows up as "no grant covers this call".
func resourceMatches(resource, want string) bool {
	if resource == "" || resource == "*" {
		return true
	}
	if strings.HasSuffix(resource, "/*") {
		return strings.HasPrefix(want, resource[:len(resource)-1])
	}
	if resource == want {
		return true
	}
	return strings.HasPrefix(want, resource) && len(want) > len(resource) && want[len(resource)] == '/'
}

func (c HeldCredential) covers(resource, protocol, messageType string) bool {
	for _, s := range c.Scope {
		if protocolMatches(s.Protocol, protocol) &&
			messageTypeMatches(s.MessageTypes, messageType) &&
			resourceMatches(s.Resource, resource) {
			return true
		}
	}
	return false
}

// grantSelection describes one outbound message to selectGrants.
type grantSelection struct {
	Recipients []string
	TypeURI    string
	Tool       string
	Now        time.Time
}

// selectGrants returns the covering set for one outbound message, as
// ready-to-send attachments.
//
// Recipients is the message's To: the node evaluates one decision per
// recipient, so a credential covering ANY of them belongs on the wire.
//
// An empty result is a legitimate outcome, not an error. Most DIDComm traffic —
// discovery, trust-ping, problem reports — rides the node's allow rules with no
// grant at all.
//
// onCapped is told when the cap left credentials off. Silence there is the same
// class of failure this file exists to end: the holder is the only party that
// knows a covering credential never reached the wire.
func selectGrants(creds []HeldCredential, sel grantSelection, onCapped func(covering, attached int)) []Attachment {
	protocol, messageType := splitTypeURI(sel.TypeURI)
	now := sel.Now
	if now.IsZero() {
		now = time.Now()
	}

	covering := make([]HeldCredential, 0, len(creds))
	for _, c := range creds {
		for _, r := range sel.Recipients {
			if c.covers(r, protocol, messageType) {
				covering = append(covering, c)
				break
			}
		}
	}

	// Ordering only matters when the cap bites. It decides which credentials are
	// LEFT OFF, so it ranks by how likely each one is to have been the one that
	// mattered — it is not a filter, and nothing here withholds anything the cap
	// has room for.
	//
	// Live beats lapsed by more than everything else combined: a certainly
	// expired grant cannot be the one that would have worked. Then the tool:
	// naming THIS tool first, naming no tool at all (unrestricted) second,
	// naming only OTHER tools last — last, not excluded, since grant.tools is
	// not a policy input anywhere. Named resource before wildcard as the
	// finest-grained tiebreak.
	rank := func(c HeldCredential) int {
		score := 0
		if !c.ExpiresAt.IsZero() && !c.ExpiresAt.After(now) {
			score += 8
		}
		switch {
		case len(c.Tools) == 0:
			score += 1 * 2
		case sel.Tool != "" && contains(c.Tools, sel.Tool):
			score += 0
		default:
			score += 2 * 2
		}
		named := false
		for _, s := range c.Scope {
			if s.Resource != "" && s.Resource != "*" && !strings.HasSuffix(s.Resource, "/*") {
				named = true
				break
			}
		}
		if !named {
			score++
		}
		return score
	}

	idx := make([]int, len(covering))
	for i := range idx {
		idx[i] = i
	}
	// Index as the tiebreak: a stable order, so the same message does not carry
	// a different set on each send.
	sort.SliceStable(idx, func(a, b int) bool {
		return rank(covering[idx[a]]) < rank(covering[idx[b]])
	})

	n := len(idx)
	if n > MaxAttachedGrants {
		n = MaxAttachedGrants
	}
	if n < len(covering) && onCapped != nil {
		onCapped(len(covering), n)
	}

	out := make([]Attachment, 0, n)
	for _, i := range idx[:n] {
		out = append(out, Attachment{
			ID:        covering[i].ID,
			MediaType: "application/vc+jwt",
			Data:      AttachmentData{JWS: covering[i].RawJWT},
		})
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func decodeJWTPayload(jwt string) map[string]json.RawMessage {
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	return payload
}

// parseCredential decodes one stored credential record into a HeldCredential,
// or reports false if it is not a Verifiable Grant.
func parseCredential(rec map[string]json.RawMessage) (HeldCredential, bool) {
	rawJWT := firstString(rec, "credential_jwt", "raw_jwt", "jwt")
	if rawJWT == "" {
		return HeldCredential{}, false
	}

	// A compact JWS has exactly three segments. Anything else cannot be verified
	// by the node, so putting it on the wire only costs a denial that names the
	// wrong problem.
	parts := strings.Split(rawJWT, ".")
	if len(parts) != 3 || parts[2] == "" {
		return HeldCredential{}, false
	}

	payload := decodeJWTPayload(rawJWT)
	// Claims are at the TOP LEVEL of the payload on this node; the "vc" wrapper
	// is the standard alternative and both are accepted.
	vc := payload
	if wrapped, ok := payload["vc"]; ok {
		var inner map[string]json.RawMessage
		if json.Unmarshal(wrapped, &inner) == nil && inner != nil {
			vc = inner
		}
	}

	var subject struct {
		Scope []grantScope `json:"scope"`
		Grant struct {
			Tools []string `json:"tools"`
		} `json:"grant"`
	}
	if raw, ok := vc["credentialSubject"]; ok {
		_ = json.Unmarshal(raw, &subject)
	}
	// A VRTC has "grantable" instead of "scope" and belongs in the node's
	// control chain, not here. No scope, not a grant.
	if len(subject.Scope) == 0 {
		return HeldCredential{}, false
	}

	// "id" is what the REST contract calls it; "credential_id" is the column
	// name, accepted because the two have been confused at this boundary before.
	id := firstString(vc, "id")
	if id == "" {
		id = firstString(payload, "jti")
	}
	if id == "" {
		id = firstString(rec, "id", "credential_id")
	}
	if id == "" {
		// The SIGNATURE segment as the fallback, not the head of the JWT: every
		// credential from one issuer shares a header, so the first bytes gave
		// them all the SAME attachment id — and a frame carrying two attachments
		// with one id is a frame whose second attachment may not survive.
		sig := parts[2]
		if len(sig) > 32 {
			sig = sig[:32]
		}
		id = "urn:jws:" + sig
	}

	validUntil := firstString(rec, "valid_until", "validUntil")
	if validUntil == "" {
		validUntil = firstString(vc, "validUntil")
	}
	expiresAt, _ := time.Parse(time.RFC3339, validUntil)

	return HeldCredential{
		ID:        id,
		RawJWT:    rawJWT,
		Scope:     subject.Scope,
		Tools:     subject.Grant.Tools,
		ExpiresAt: expiresAt,
	}, true
}

func firstString(m map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		raw, ok := m[k]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			return s
		}
	}
	return ""
}

type walletEntry struct {
	at    time.Time
	creds []HeldCredential
	err   error
}

// wallet holds the grants a DID holds, read from the node and cached.
//
// Cached for ttl because a send should not cost a round trip. The TTL is the
// whole freshness story: a grant minted seconds ago is invisible until it
// lapses, which is why it is short and why refresh exists for a caller that has
// just been told it was granted something.
type wallet struct {
	read        credentialReader
	ttl         time.Duration
	readTimeout time.Duration
	// failureTTL is how long a FAILED read is remembered.
	//
	// A failure is cached at all because only caching successes meant an agent
	// whose API key cannot read credentials paid a full failing round trip on
	// EVERY outbound message, forever — turning a config mistake into a
	// permanent latency tax. Short, because the fix for that mistake should take
	// effect without a restart; never longer than a success is cached; and never
	// shorter than the read deadline, because the entry is stamped with the time
	// the read STARTED, so a shorter one is already lapsed the moment a timeout
	// records it.
	failureTTL time.Duration

	mu    sync.Mutex
	cache map[string]walletEntry
	now   func() time.Time // injectable for tests
}

func newWallet(read credentialReader, ttl, readTimeout time.Duration) *wallet {
	failureTTL := 5 * time.Second
	if readTimeout > failureTTL {
		failureTTL = readTimeout
	}
	if ttl < failureTTL {
		failureTTL = ttl
	}
	return &wallet{
		read:        read,
		ttl:         ttl,
		readTimeout: readTimeout,
		failureTTL:  failureTTL,
		cache:       make(map[string]walletEntry),
		now:         time.Now,
	}
}

// refresh drops the cached grants for did (or all, when did is empty), forcing
// the next send to re-read.
func (w *wallet) refresh(did string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if did == "" {
		w.cache = make(map[string]walletEntry)
		return
	}
	delete(w.cache, did)
}

func (w *wallet) heldBy(ctx context.Context, did string) ([]HeldCredential, error) {
	now := w.now()

	w.mu.Lock()
	if hit, ok := w.cache[did]; ok {
		ttl := w.ttl
		if hit.err != nil {
			ttl = w.failureTTL
		}
		if now.Sub(hit.at) < ttl {
			w.mu.Unlock()
			return hit.creds, hit.err
		}
	}
	w.mu.Unlock()

	// The deadline is the reason a hung node cannot stall every send behind it.
	readCtx := ctx
	if w.readTimeout > 0 {
		var cancel context.CancelFunc
		readCtx, cancel = context.WithTimeout(ctx, w.readTimeout)
		defer cancel()
	}

	records, err := w.read(readCtx, did)

	var creds []HeldCredential
	if err == nil {
		for _, rec := range records {
			if cred, ok := parseCredential(rec); ok {
				creds = append(creds, cred)
			}
		}
	}

	w.mu.Lock()
	w.cache[did] = walletEntry{at: now, creds: creds, err: err}
	w.mu.Unlock()

	return creds, err
}

// attachmentsFor returns the attachments for one outbound message, or nil if
// nothing covers it.
func (w *wallet) attachmentsFor(ctx context.Context, did string, msg *Message, onCapped func(covering, attached int)) ([]Attachment, error) {
	creds, err := w.heldBy(ctx, did)
	if err != nil {
		return nil, err
	}
	return selectGrants(creds, grantSelection{
		Recipients: msg.To,
		TypeURI:    msg.Type,
		Tool:       toolNameOf(msg.Body, msg.bodyRaw),
	}, onCapped), nil
}

// restCredentialReader reads a holder's credentials over the SDK's REST client.
//
// The REST client rather than a bare http.Client, deliberately: it carries the
// *.localhost dialer that makes local development work, and the x-api-key
// header.
func restCredentialReader(rest *restClient) credentialReader {
	return func(ctx context.Context, did string) ([]map[string]json.RawMessage, error) {
		path := "/api/v1/credentials?holder_did=" + url.QueryEscape(did)

		var raw json.RawMessage
		if err := rest.get(ctx, path, &raw); err != nil {
			return nil, err
		}

		var list []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &list); err == nil {
			return list, nil
		}
		var wrapper struct {
			Credentials []map[string]json.RawMessage `json:"credentials"`
		}
		if err := json.Unmarshal(raw, &wrapper); err != nil {
			return nil, err
		}
		return wrapper.Credentials, nil
	}
}
