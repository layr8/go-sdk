package layr8

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

// --- fixtures ---

func b64Segment(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

// grantJWT builds a compact JWS whose payload is claims. The signature segment
// is what the wallet falls back to for an attachment id, so it varies per
// fixture.
func grantJWT(t *testing.T, claims map[string]any, sig string) string {
	t.Helper()
	return b64Segment(t, map[string]string{"alg": "EdDSA"}) + "." + b64Segment(t, claims) + "." + sig
}

type grantOpts struct {
	scope      []map[string]any
	id         string
	noID       bool
	tools      []string
	sig        string
	validUntil string
}

func grantRecord(t *testing.T, o grantOpts) map[string]json.RawMessage {
	t.Helper()

	scope := o.scope
	if scope == nil {
		scope = []map[string]any{{"protocol": "*", "messageTypes": []string{"*"}}}
	}
	subject := map[string]any{"scope": scope}
	if o.tools != nil {
		subject["grant"] = map[string]any{"tools": o.tools}
	}

	claims := map[string]any{"credentialSubject": subject}
	if !o.noID {
		id := o.id
		if id == "" {
			id = "urn:uuid:grant-1"
		}
		claims["id"] = id
	}

	sig := o.sig
	if sig == "" {
		sig = "sig"
	}

	rec := map[string]json.RawMessage{
		"credential_jwt": json.RawMessage(fmt.Sprintf("%q", grantJWT(t, claims, sig))),
	}
	if o.validUntil != "" {
		rec["valid_until"] = json.RawMessage(fmt.Sprintf("%q", o.validUntil))
	}
	return rec
}

func heldFrom(t *testing.T, o grantOpts) HeldCredential {
	t.Helper()
	cred, ok := parseCredential(grantRecord(t, o))
	if !ok {
		t.Fatalf("fixture did not parse as a grant")
	}
	return cred
}

// --- splitTypeURI / toolNameOf ---

func TestSplitTypeURI_SplitsOnTheLastSlash(t *testing.T) {
	// What the node's own parser does — protocol and message type match
	// separately in the policy.
	proto, msgType := splitTypeURI("https://layr8.io/protocols/mcp/1.0/tools-call")
	if proto != "https://layr8.io/protocols/mcp/1.0" || msgType != "tools-call" {
		t.Fatalf("got (%q, %q)", proto, msgType)
	}
}

func TestSplitTypeURI_NoSlashIsAllProtocol(t *testing.T) {
	proto, msgType := splitTypeURI("bare")
	if proto != "bare" || msgType != "" {
		t.Fatalf("got (%q, %q)", proto, msgType)
	}
}

func TestToolNameOf(t *testing.T) {
	got := toolNameOf(map[string]any{"params": map[string]any{"name": "send_email"}}, nil)
	if got != "send_email" {
		t.Fatalf("got %q", got)
	}
	if n := toolNameOf(map[string]any{"text": "hi"}, nil); n != "" {
		t.Fatalf("body with no tool: got %q", n)
	}
	if n := toolNameOf(nil, nil); n != "" {
		t.Fatalf("nil body: got %q", n)
	}
	if n := toolNameOf(nil, json.RawMessage(`{"params":{"name":"from_raw"}}`)); n != "from_raw" {
		t.Fatalf("raw body: got %q", n)
	}
}

// --- parseCredential ---

func TestParseCredential_TopLevelClaims(t *testing.T) {
	cred := heldFrom(t, grantOpts{id: "urn:uuid:g1", tools: []string{"a"}})
	if cred.ID != "urn:uuid:g1" {
		t.Errorf("ID = %q", cred.ID)
	}
	if len(cred.Tools) != 1 || cred.Tools[0] != "a" {
		t.Errorf("Tools = %v", cred.Tools)
	}
	if len(cred.Scope) != 1 || cred.Scope[0].Protocol != "*" {
		t.Errorf("Scope = %+v", cred.Scope)
	}
}

func TestParseCredential_StandardVCEnvelope(t *testing.T) {
	claims := map[string]any{
		"vc": map[string]any{
			"id": "urn:uuid:wrapped",
			"credentialSubject": map[string]any{
				"scope": []map[string]any{{"protocol": "*", "messageTypes": []string{"*"}}},
			},
		},
	}
	rec := map[string]json.RawMessage{
		"credential_jwt": json.RawMessage(fmt.Sprintf("%q", grantJWT(t, claims, "sig"))),
	}
	cred, ok := parseCredential(rec)
	if !ok || cred.ID != "urn:uuid:wrapped" {
		t.Fatalf("ok=%v id=%q", ok, cred.ID)
	}
}

func TestParseCredential_VRTCIsNotAGrant(t *testing.T) {
	// A VRTC has `grantable` instead of `scope` and belongs in the node's
	// control chain, not here.
	claims := map[string]any{
		"credentialSubject": map[string]any{"grantable": []map[string]any{{"protocol": "*"}}},
	}
	rec := map[string]json.RawMessage{
		"credential_jwt": json.RawMessage(fmt.Sprintf("%q", grantJWT(t, claims, "sig"))),
	}
	if _, ok := parseCredential(rec); ok {
		t.Fatal("a VRTC must not parse as a grant")
	}
}

func TestParseCredential_RefusesAnythingButAThreeSegmentJWS(t *testing.T) {
	// Anything else cannot be verified by the node, so putting it on the wire
	// only costs a denial that names the wrong problem.
	for _, raw := range []string{"not.a.jwt.at.all", "two.parts", "head.payload.", ""} {
		rec := map[string]json.RawMessage{
			"credential_jwt": json.RawMessage(fmt.Sprintf("%q", raw)),
		}
		if _, ok := parseCredential(rec); ok {
			t.Errorf("%q must not parse as a grant", raw)
		}
	}
	if _, ok := parseCredential(map[string]json.RawMessage{}); ok {
		t.Error("an empty record must not parse as a grant")
	}
}

func TestParseCredential_IDFallsBackToTheSignatureSegment(t *testing.T) {
	// Every credential from one issuer shares a header, so falling back to the
	// head of the JWT gave them all the same attachment id — and a frame
	// carrying two attachments with one id is a frame whose second attachment
	// may not survive.
	a := heldFrom(t, grantOpts{noID: true, sig: "signature-a"})
	b := heldFrom(t, grantOpts{noID: true, sig: "signature-b"})

	if a.ID == b.ID {
		t.Fatalf("two grants collided on id %q", a.ID)
	}
	if got := a.ID[:8]; got != "urn:jws:" {
		t.Fatalf("fallback id = %q", a.ID)
	}
}

// --- scope matching ---

func selectOne(t *testing.T, scope []map[string]any, recipient, typeURI string) []Attachment {
	t.Helper()
	return selectGrants(
		[]HeldCredential{heldFrom(t, grantOpts{scope: scope})},
		grantSelection{Recipients: []string{recipient}, TypeURI: typeURI},
		nil,
	)
}

func TestScopeMatching(t *testing.T) {
	// selectGrants mirrors the node's authorization policy.
	wildcard := []map[string]any{{"protocol": "*", "messageTypes": []string{"*"}}}

	tests := []struct {
		name      string
		scope     []map[string]any
		recipient string
		typeURI   string
		want      bool
	}{
		{
			name: "wildcard protocol and message type cover anything", scope: wildcard,
			recipient: "did:web:bob", typeURI: "https://layr8.io/protocols/mcp/1.0/tools-call", want: true,
		},
		{
			name:      "a non-matching protocol covers nothing",
			scope:     []map[string]any{{"protocol": "https://other.example/proto/1.0", "messageTypes": []string{"*"}}},
			recipient: "did:web:bob", typeURI: "https://layr8.io/protocols/mcp/1.0/tools-call", want: false,
		},
		{
			name:      "a non-matching message type covers nothing",
			scope:     []map[string]any{{"protocol": "https://layr8.io/protocols/mcp/1.0", "messageTypes": []string{"ping"}}},
			recipient: "did:web:bob", typeURI: "https://layr8.io/protocols/mcp/1.0/tools-call", want: false,
		},
		{
			name:      "an exact resource matches",
			scope:     []map[string]any{{"protocol": "*", "messageTypes": []string{"*"}, "resource": "did:web:bob"}},
			recipient: "did:web:bob", typeURI: "p/t", want: true,
		},
		{
			// The rego strips only the `*`, so the trailing slash is part of the prefix.
			name:      "foo/* covers under foo/",
			scope:     []map[string]any{{"protocol": "*", "messageTypes": []string{"*"}, "resource": "tables/*"}},
			recipient: "tables/customers", typeURI: "p/t", want: true,
		},
		{
			name:      "foo/* does not cover foobar",
			scope:     []map[string]any{{"protocol": "*", "messageTypes": []string{"*"}, "resource": "tables/*"}},
			recipient: "tablesarchive", typeURI: "p/t", want: false,
		},
		{
			// The clause whose absence points the wrong way: this side withholding
			// a grant the policy would have honoured, which costs a working call.
			name:      "a bare resource is a segment prefix",
			scope:     []map[string]any{{"protocol": "*", "messageTypes": []string{"*"}, "resource": "tables"}},
			recipient: "tables/customers", typeURI: "p/t", want: true,
		},
		{
			name:      "a bare resource is not a substring prefix",
			scope:     []map[string]any{{"protocol": "*", "messageTypes": []string{"*"}, "resource": "tables"}},
			recipient: "tables_archive", typeURI: "p/t", want: false,
		},
		{
			name:      "a bare resource matches itself",
			scope:     []map[string]any{{"protocol": "*", "messageTypes": []string{"*"}, "resource": "tables"}},
			recipient: "tables", typeURI: "p/t", want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := len(selectOne(t, tc.scope, tc.recipient, tc.typeURI)) > 0
			if got != tc.want {
				t.Errorf("covered = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSelectGrants_CoveringAnyRecipientGoesOnTheWire(t *testing.T) {
	// The node evaluates one decision per recipient.
	cred := heldFrom(t, grantOpts{
		scope: []map[string]any{{"protocol": "*", "messageTypes": []string{"*"}, "resource": "did:web:bob"}},
	})
	got := selectGrants([]HeldCredential{cred}, grantSelection{
		Recipients: []string{"did:web:alice", "did:web:bob"},
		TypeURI:    "p/t",
	}, nil)
	if len(got) != 1 {
		t.Fatalf("got %d attachments", len(got))
	}
}

// --- the attachment shape ---

func TestSelectGrants_AttachmentShapeIsLoadBearing(t *testing.T) {
	// media_type is the ONLY thing the node's credential extractor filters on,
	// by exact string equality; everything else is dropped silently and the
	// denial is byte-for-byte the one for attaching nothing.
	rec := grantRecord(t, grantOpts{})
	cred, _ := parseCredential(rec)

	atts := selectGrants([]HeldCredential{cred}, grantSelection{
		Recipients: []string{"x"}, TypeURI: "p/t",
	}, nil)
	if len(atts) != 1 {
		t.Fatalf("got %d attachments", len(atts))
	}
	if atts[0].MediaType != "application/vc+jwt" {
		t.Errorf("MediaType = %q", atts[0].MediaType)
	}
	if atts[0].Data.JWS != cred.RawJWT {
		t.Errorf("Data.JWS = %v", atts[0].Data.JWS)
	}
	if atts[0].Data.Base64 != "" {
		t.Errorf("Data.Base64 should be empty, got %q", atts[0].Data.Base64)
	}

	// ...and it survives marshalling onto the wire.
	data, err := marshalDIDComm(&Message{ID: "m", Type: "p/t", Attachments: atts})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var env struct {
		Attachments []struct {
			MediaType string         `json:"media_type"`
			Data      map[string]any `json:"data"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.Attachments) != 1 || env.Attachments[0].MediaType != "application/vc+jwt" {
		t.Fatalf("wire attachments = %+v", env.Attachments)
	}
	if env.Attachments[0].Data["jws"] != cred.RawJWT {
		t.Errorf("wire data = %+v", env.Attachments[0].Data)
	}
}

// --- the cap ---

func TestSelectGrants_CapAnnouncesWhatWasLeftOff(t *testing.T) {
	creds := make([]HeldCredential, 0, 20)
	for i := 0; i < 20; i++ {
		creds = append(creds, heldFrom(t, grantOpts{
			id: fmt.Sprintf("urn:uuid:g%d", i), sig: fmt.Sprintf("sig%d", i),
		}))
	}

	var covering, attached int
	got := selectGrants(creds, grantSelection{Recipients: []string{"did:web:bob"}, TypeURI: "p/t"},
		func(c, a int) { covering, attached = c, a })

	if len(got) != MaxAttachedGrants {
		t.Fatalf("attached %d, want %d", len(got), MaxAttachedGrants)
	}
	if covering != 20 || attached != MaxAttachedGrants {
		t.Errorf("onCapped(%d, %d)", covering, attached)
	}
}

func TestSelectGrants_NoCapCallbackWhenEverythingFits(t *testing.T) {
	called := false
	selectGrants([]HeldCredential{heldFrom(t, grantOpts{})},
		grantSelection{Recipients: []string{"x"}, TypeURI: "p/t"},
		func(int, int) { called = true })
	if called {
		t.Error("onCapped fired with nothing left off")
	}
}

func TestSelectGrants_TheNamedToolKeepsItsSlot(t *testing.T) {
	// `grant.tools` is not a policy input anywhere, so it never filters — it
	// only decides who keeps a slot when the cap bites.
	creds := make([]HeldCredential, 0, MaxAttachedGrants+1)
	for i := 0; i < MaxAttachedGrants; i++ {
		creds = append(creds, heldFrom(t, grantOpts{
			id: fmt.Sprintf("urn:uuid:other%d", i), sig: fmt.Sprintf("s%d", i), tools: []string{"other"},
		}))
	}
	creds = append(creds, heldFrom(t, grantOpts{id: "urn:uuid:wanted", sig: "sw", tools: []string{"send_email"}}))

	got := selectGrants(creds, grantSelection{
		Recipients: []string{"did:web:bob"}, TypeURI: "p/t", Tool: "send_email",
	}, nil)

	if got[0].ID != "urn:uuid:wanted" {
		t.Fatalf("first attachment = %q", got[0].ID)
	}
}

func TestSelectGrants_ALapsedGrantLosesItsSlotToALiveOne(t *testing.T) {
	creds := make([]HeldCredential, 0, MaxAttachedGrants+1)
	for i := 0; i < MaxAttachedGrants; i++ {
		creds = append(creds, heldFrom(t, grantOpts{
			id: fmt.Sprintf("urn:uuid:old%d", i), sig: fmt.Sprintf("o%d", i),
			validUntil: "2020-01-01T00:00:00Z",
		}))
	}
	creds = append(creds, heldFrom(t, grantOpts{id: "urn:uuid:live", sig: "lv"}))

	got := selectGrants(creds, grantSelection{Recipients: []string{"did:web:bob"}, TypeURI: "p/t"}, nil)
	if got[0].ID != "urn:uuid:live" {
		t.Fatalf("first attachment = %q", got[0].ID)
	}
}

func TestSelectGrants_AnExpiredGrantIsStillAttachedWhenThereIsRoom(t *testing.T) {
	// Validity is the PDP's call, made against a clock this side cannot see.
	// Withholding because a local clock thought it was dead costs a working
	// call, and that failure is silent.
	expired := heldFrom(t, grantOpts{validUntil: "2020-01-01T00:00:00Z"})
	got := selectGrants([]HeldCredential{expired},
		grantSelection{Recipients: []string{"did:web:bob"}, TypeURI: "p/t"}, nil)
	if len(got) != 1 {
		t.Fatalf("got %d attachments", len(got))
	}
}

// --- the cache ---

type countingReader struct {
	calls   int
	records []map[string]json.RawMessage
	err     error
}

func (r *countingReader) read(_ context.Context, _ string) ([]map[string]json.RawMessage, error) {
	r.calls++
	return r.records, r.err
}

func TestWallet_CachesASuccessfulReadThenReReads(t *testing.T) {
	reader := &countingReader{records: []map[string]json.RawMessage{grantRecord(t, grantOpts{})}}
	w := newWallet(reader.read, time.Second, time.Second)

	base := time.Now()
	w.now = func() time.Time { return base }
	if creds, err := w.heldBy(context.Background(), "did:web:alice"); err != nil || len(creds) != 1 {
		t.Fatalf("creds=%d err=%v", len(creds), err)
	}

	w.now = func() time.Time { return base.Add(999 * time.Millisecond) }
	w.heldBy(context.Background(), "did:web:alice")
	if reader.calls != 1 {
		t.Fatalf("reads = %d, want 1 (cached)", reader.calls)
	}

	w.now = func() time.Time { return base.Add(time.Second) }
	w.heldBy(context.Background(), "did:web:alice")
	if reader.calls != 2 {
		t.Fatalf("reads = %d, want 2 (TTL lapsed)", reader.calls)
	}
}

func TestWallet_RefreshDropsTheEntry(t *testing.T) {
	reader := &countingReader{records: []map[string]json.RawMessage{grantRecord(t, grantOpts{})}}
	w := newWallet(reader.read, time.Minute, time.Second)

	w.heldBy(context.Background(), "did:web:alice")
	w.refresh("did:web:alice")
	w.heldBy(context.Background(), "did:web:alice")

	if reader.calls != 2 {
		t.Fatalf("reads = %d, want 2", reader.calls)
	}
}

func TestWallet_RefreshWithNoDIDDropsEverything(t *testing.T) {
	reader := &countingReader{records: []map[string]json.RawMessage{grantRecord(t, grantOpts{})}}
	w := newWallet(reader.read, time.Minute, time.Second)

	w.heldBy(context.Background(), "did:web:alice")
	w.heldBy(context.Background(), "did:web:bob")
	w.refresh("")
	w.heldBy(context.Background(), "did:web:alice")
	w.heldBy(context.Background(), "did:web:bob")

	if reader.calls != 4 {
		t.Fatalf("reads = %d, want 4", reader.calls)
	}
}

func TestWallet_CachesAFailureSoABadAPIKeyIsNotAPerMessageRoundTrip(t *testing.T) {
	reader := &countingReader{err: errors.New("unauthorized")}
	w := newWallet(reader.read, time.Minute, 2*time.Second)

	base := time.Now()
	w.now = func() time.Time { return base }
	if _, err := w.heldBy(context.Background(), "did:web:alice"); err == nil {
		t.Fatal("expected the read error")
	}

	w.now = func() time.Time { return base.Add(time.Second) }
	if _, err := w.heldBy(context.Background(), "did:web:alice"); err == nil {
		t.Fatal("expected the cached read error")
	}
	if reader.calls != 1 {
		t.Fatalf("reads = %d, want 1 (failure cached)", reader.calls)
	}

	// ...and it lapses, because the fix for whatever broke it should take effect
	// without a restart. Default failure TTL here is 5s.
	w.now = func() time.Time { return base.Add(5 * time.Second) }
	w.heldBy(context.Background(), "did:web:alice")
	if reader.calls != 2 {
		t.Fatalf("reads = %d, want 2 (failure TTL lapsed)", reader.calls)
	}
}

func TestWallet_FailureTTLIsNeverShorterThanTheReadDeadline(t *testing.T) {
	// The entry is stamped with the time the read STARTED, so a shorter one
	// would already be lapsed the moment a timeout recorded it, and a hung node
	// would cost EVERY send the full deadline.
	w := newWallet(nil, time.Minute, 20*time.Second)
	if w.failureTTL != 20*time.Second {
		t.Fatalf("failureTTL = %v", w.failureTTL)
	}
}

func TestWallet_FailureTTLNeverOutlivesTheSuccessTTL(t *testing.T) {
	w := newWallet(nil, time.Second, 2*time.Second)
	if w.failureTTL != time.Second {
		t.Fatalf("failureTTL = %v", w.failureTTL)
	}
}

func TestWallet_TheReadDeadlineBoundsAHungNode(t *testing.T) {
	// The read sits in front of every send; an unbounded one stalls the send
	// itself.
	blocked := func(ctx context.Context, _ string) ([]map[string]json.RawMessage, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	w := newWallet(blocked, time.Minute, 100*time.Millisecond)

	started := time.Now()
	if _, err := w.heldBy(context.Background(), "did:web:alice"); err == nil {
		t.Fatal("expected a deadline error")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("read was not bounded (took %v)", elapsed)
	}
}

func TestWallet_AttachmentsForIsEmptyWhenNothingCovers(t *testing.T) {
	// Most DIDComm traffic — discovery, trust-ping, problem reports — rides the
	// node's allow rules with no grant at all. Not an error.
	reader := &countingReader{}
	w := newWallet(reader.read, time.Minute, time.Second)

	atts, err := w.attachmentsFor(context.Background(), "did:web:alice", &Message{
		Type: "https://didcomm.org/trust-ping/2.0/ping",
		To:   []string{"did:web:bob"},
	}, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(atts) != 0 {
		t.Fatalf("got %d attachments", len(atts))
	}
}

func TestWallet_AttachmentsForSurfacesAReadFailure(t *testing.T) {
	reader := &countingReader{err: errors.New("nope")}
	w := newWallet(reader.read, time.Minute, time.Second)

	if _, err := w.attachmentsFor(context.Background(), "did:web:alice", &Message{
		Type: "p/t", To: []string{"did:web:bob"},
	}, nil); err == nil {
		t.Fatal("a read failure must surface, not read as an empty wallet")
	}
}

func TestWallet_AttachmentsForReadsTheToolFromTheBody(t *testing.T) {
	records := make([]map[string]json.RawMessage, 0, MaxAttachedGrants+1)
	for i := 0; i < MaxAttachedGrants; i++ {
		records = append(records, grantRecord(t, grantOpts{
			id: fmt.Sprintf("urn:uuid:o%d", i), sig: fmt.Sprintf("o%d", i), tools: []string{"other"},
		}))
	}
	records = append(records, grantRecord(t, grantOpts{
		id: "urn:uuid:wanted", sig: "w", tools: []string{"send_email"},
	}))

	reader := &countingReader{records: records}
	w := newWallet(reader.read, time.Minute, time.Second)

	atts, err := w.attachmentsFor(context.Background(), "did:web:alice", &Message{
		Type: "https://layr8.io/protocols/mcp/1.0/tools-call",
		To:   []string{"did:web:bob"},
		Body: map[string]any{"jsonrpc": "2.0", "method": "tools/call", "params": map[string]any{"name": "send_email"}},
	}, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if atts[0].ID != "urn:uuid:wanted" {
		t.Fatalf("first attachment = %q", atts[0].ID)
	}
}
