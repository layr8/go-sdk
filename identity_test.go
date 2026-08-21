package layr8

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// Boundary test for the sender -> cloud-node identity-credential contract.
//
// The node routes an attachment on credentialSubject.scope alone, so the two
// claims this SDK must keep straight are "no scope, so identity" and "has a
// scope, so grant".

// identityJWT is an identity credential: the same claims shape as a grant, and
// NO credentialSubject.scope. That absence is the entire discriminator, on this
// side and on the node's.
func identityJWT(t *testing.T, id string) string {
	t.Helper()
	claims := map[string]any{
		"id":     id,
		"type":   []string{"VerifiableCredential", "EmploymentCredential"},
		"issuer": "did:web:issuer.localhost",
		"credentialSubject": map[string]any{
			"id":       "did:web:alice.localhost",
			"employer": "Example Incorporated",
			"role":     "buyer",
		},
	}
	return grantJWT(t, claims, "identity-sig")
}

func TestIdentity_GoesOutExactlyAsTheCallerBuiltIt(t *testing.T) {
	node, wsURL := setupGrantNode(t)
	raw := identityJWT(t, "urn:uuid:idc-1")

	att, err := IdentityAttachment(raw)
	if err != nil {
		t.Fatalf("IdentityAttachment: %v", err)
	}

	client, ctx := connectedClient(t, wsURL, Config{})
	msg := toolCall()
	msg.Attachments = []Attachment{att}
	if err := client.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	atts := sentAttachments(t, node)
	if len(atts) == 0 {
		t.Fatal("nothing reached the wire")
	}
	if atts[0]["id"] != "urn:uuid:idc-1" {
		t.Errorf("id = %v, want urn:uuid:idc-1", atts[0]["id"])
	}
	if atts[0]["media_type"] != CredentialMediaType {
		t.Errorf("media_type = %v", atts[0]["media_type"])
	}
	data, _ := atts[0]["data"].(map[string]any)
	if data["jws"] != raw {
		t.Errorf("the credential was altered on the way out: %v", data["jws"])
	}
}

func TestIdentity_DoesNotCostTheMessageItsGrant(t *testing.T) {
	// The half that would be silently wrong. Under the old rule ANY
	// caller-supplied attachment made the wallet stand aside, so saying who you
	// are meant sending nothing that says what you may do — and the node's
	// denial then read "no grant covers this call", which is exactly the message
	// that sends people looking at their grant configuration.
	node, wsURL := setupGrantNode(t)
	rec := coveringRecord(t)
	node.credentials = []map[string]json.RawMessage{rec}

	att, err := IdentityAttachment(identityJWT(t, "urn:uuid:idc-1"))
	if err != nil {
		t.Fatalf("IdentityAttachment: %v", err)
	}

	client, ctx := connectedClient(t, wsURL, Config{})
	msg := toolCall()
	msg.Attachments = []Attachment{att}
	if err := client.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	atts := sentAttachments(t, node)
	if len(atts) != 2 {
		t.Fatalf("attachments = %d, want 2 (the caller's identity credential, then the grant)", len(atts))
	}
	// The caller's stays FIRST and unmodified; the wallet's selection follows.
	if atts[0]["id"] != "urn:uuid:idc-1" {
		t.Errorf("first attachment = %v, want the caller's", atts[0]["id"])
	}
	data, _ := atts[1]["data"].(map[string]any)
	if data["jws"] != rawJWTOf(t, rec) {
		t.Errorf("second attachment is not the wallet's grant: %v", data["jws"])
	}
}

func TestIdentity_ACredentialWithAScopeIsRefused(t *testing.T) {
	// Not a taste call. The node would route it to the policy's "credentials"
	// input, where it can never satisfy a senderCredentials requirement, and the
	// denial that follows is byte-for-byte the one for attaching nothing at all.
	// The check is local and exact, so the choice is between an error at the
	// call site and a misroute diagnosed at the far end.
	grant := rawJWTOf(t, coveringRecord(t))

	if _, err := IdentityAttachment(grant); !errors.Is(err, ErrCredentialIsGrant) {
		t.Fatalf("IdentityAttachment(grant) err = %v, want ErrCredentialIsGrant", err)
	}

	if IsIdentityAttachment(Attachment{MediaType: CredentialMediaType, Data: AttachmentData{JWS: grant}}) {
		t.Error("a grant was read as an identity credential")
	}
	identity := identityJWT(t, "urn:uuid:idc-1")
	if !IsIdentityAttachment(Attachment{MediaType: CredentialMediaType, Data: AttachmentData{JWS: identity}}) {
		t.Error("an identity credential was not read as one")
	}
}

func TestIdentity_ACallerAttachedGrantStillDisplacesTheWallet(t *testing.T) {
	// The other side of the narrowing: only identity credentials keep the wallet
	// running. A caller attaching a grant is still saying "use mine".
	node, wsURL := setupGrantNode(t)
	node.credentials = []map[string]json.RawMessage{coveringRecord(t)}

	client, ctx := connectedClient(t, wsURL, Config{})
	msg := toolCall()
	msg.Attachments = []Attachment{{
		ID:        "mine",
		MediaType: CredentialMediaType,
		Data:      AttachmentData{JWS: rawJWTOf(t, grantRecord(t, grantOpts{id: "mine", sig: "other"}))},
	}}
	if err := client.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	atts := sentAttachments(t, node)
	if len(atts) != 1 || atts[0]["id"] != "mine" {
		t.Fatalf("attachments = %v, want only the caller's", atts)
	}
}

func TestIdentity_AnythingThatIsNotACompactJWSIsRefused(t *testing.T) {
	// The node can verify nothing else, so attaching it only buys a denial that
	// names the wrong problem.
	for _, bad := range []string{"not-a-jws", "a.b.", "", "a.b.c.d"} {
		if _, err := IdentityAttachment(bad); !errors.Is(err, ErrNotCompactJWS) {
			t.Errorf("IdentityAttachment(%q) err = %v, want ErrNotCompactJWS", bad, err)
		}
	}
}

func TestIdentity_AnUndecodableAttachmentIsNotAnIdentityCredential(t *testing.T) {
	// Counting three segments is not reading a credential. Each of these has
	// three of them and decodes to nothing usable, so nothing here can say
	// whether it carries a credentialSubject.scope.
	//
	// "I could not read a scope" must not collapse into "there is no scope, so
	// this is identity". Identity is the ONE attachment shape that leaves the
	// wallet running, so that collapse hands a caller who attached garbage the
	// wallet's grants, appended silently, while every other foreign attachment
	// stands the wallet aside. The caller chose nothing and got a disclosure.
	seg := func(raw string) string { return base64.RawURLEncoding.EncodeToString([]byte(raw)) }

	undecodable := []string{
		"..",
		"a.b.c",
		seg("{}") + "." + seg("not json at all") + ".c2ln",
		// Valid JSON, but a scalar: it parses, and then has no
		// credentialSubject to read — which looked exactly like a scope-free
		// credential.
		seg("{}") + "." + seg("42") + ".c2ln",
	}

	for _, bad := range undecodable {
		att := Attachment{MediaType: CredentialMediaType, Data: AttachmentData{JWS: bad}}
		if IsIdentityAttachment(att) {
			t.Errorf("IsIdentityAttachment(%q) = true, want false", bad)
		}
		if _, err := IdentityAttachment(bad); !errors.Is(err, ErrNotCompactJWS) {
			t.Errorf("IdentityAttachment(%q) err = %v, want ErrNotCompactJWS", bad, err)
		}
	}
}

func TestIdentity_MixedWithAGrantStillDisplacesTheWallet(t *testing.T) {
	// The narrowing is "the caller's attachments are ALL identity credentials",
	// not "at least one of them is". A caller that supplied a grant of its own
	// has said which grant to use, and the wallet appending its own selection
	// behind that would be overriding an explicit choice — the same silent
	// substitution the whole path exists to avoid. Mixing the two is the case
	// where both rules apply at once, and nothing pinned which one wins.
	node, wsURL := setupGrantNode(t)
	node.credentials = []map[string]json.RawMessage{coveringRecord(t)}

	identity, err := IdentityAttachment(identityJWT(t, "urn:uuid:idc-1"))
	if err != nil {
		t.Fatalf("IdentityAttachment: %v", err)
	}

	client, ctx := connectedClient(t, wsURL, Config{})
	msg := toolCall()
	msg.Attachments = []Attachment{identity, {
		ID:        "mine",
		MediaType: CredentialMediaType,
		Data:      AttachmentData{JWS: rawJWTOf(t, grantRecord(t, grantOpts{id: "mine", sig: "other"}))},
	}}
	if err := client.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Both of the caller's survive, in order, and the wallet adds nothing.
	atts := sentAttachments(t, node)
	if len(atts) != 2 {
		t.Fatalf("attachments = %d, want 2 (only the caller's two)", len(atts))
	}
	if atts[0]["id"] != "urn:uuid:idc-1" || atts[1]["id"] != "mine" {
		t.Errorf("attachments = %v, want the caller's identity credential then its grant", atts)
	}
}
