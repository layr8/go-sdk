package layr8

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

// readingOf is what parseDelegatedCredentials makes of one join reply's
// delegated_credentials value.
func readingOf(raw string) *DelegatedCredentialsReading {
	return parseDelegatedCredentials(json.RawMessage(raw))
}

// TestTheFourDelegationReadingsArePairwiseDistinct is the assertion this whole
// object exists for.
//
// The fourth reading is the ABSENT key — a node that never looked. It is not a
// fourth flavour of "read and found nothing", and an unreadable wallet is not
// an empty one: both of those collapses land on the reassuring answer, and a
// client acting on it sends its messages bare and gets back a denial naming a
// grant.
//
// Asserting that any three of them differ passes while the defect is present,
// so every pair is compared.
func TestTheFourDelegationReadingsArePairwiseDistinct(t *testing.T) {
	readings := map[string]*DelegatedCredentialsReading{
		"absent — the node never looked":      readingOf(``),
		"read, and the parent grants nothing": readingOf(`{"status":"complete","credentials":[]}`),
		"read, and some could not be delegated": readingOf(
			`{"status":"partial","credentials":[{"id":"urn:uuid:1","parent_capability":"urn:uuid:p","credential_jwt":"a.b.c"}]}`),
		"could not be read at all": readingOf(`{"status":"unread","credentials":[]}`),
	}

	names := make([]string, 0, len(readings))
	for name := range readings {
		names = append(names, name)
	}

	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			a, b := readings[names[i]], readings[names[j]]
			if reflect.DeepEqual(a, b) {
				t.Errorf("%q and %q are the same value (%v); one of them states something nobody measured",
					names[i], names[j], a)
			}
		}
	}

	// And each one is the value it claims to be, so "distinct" is not being
	// satisfied by four different wrong answers.
	if readings["absent — the node never looked"] != nil {
		t.Error("an absent delegated_credentials must be no reading at all, never an empty complete one")
	}
	if got := readings["read, and the parent grants nothing"]; got == nil || got.Status != DelegationComplete || len(got.Credentials) != 0 {
		t.Errorf(`{"status":"complete","credentials":[]} parsed as %v`, got)
	}
	if got := readings["could not be read at all"]; got == nil || got.Status != DelegationUnread {
		t.Errorf(`{"status":"unread"} parsed as %v`, got)
	}
}

func TestNothingButAWellFormedReadingIsAReading(t *testing.T) {
	cases := map[string]string{
		"absent":                            ``,
		"null":                              `null`,
		"a bare array — an older node":      `[{"id":"urn:uuid:1"}]`,
		"a status this build does not know": `{"status":"partially","credentials":[]}`,
		"credentials missing":               `{"status":"complete"}`,
		"credentials not a list":            `{"status":"complete","credentials":{}}`,
		"a string":                          `"complete"`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if got := parseDelegatedCredentials(json.RawMessage(raw)); got != nil {
				t.Errorf("parsed %s as %v; want no reading", raw, got)
			}
		})
	}
}

func TestAReadingCarriesTheCredentialsVerbatim(t *testing.T) {
	got := readingOf(`{"status":"complete","credentials":[{"id":"urn:uuid:1","parent_capability":"urn:uuid:p","credential_jwt":"a.b.c"}]}`)
	if got == nil {
		t.Fatal("want a reading")
	}
	want := DelegatedCredential{ID: "urn:uuid:1", ParentCapability: "urn:uuid:p", CredentialJWT: "a.b.c"}
	if len(got.Credentials) != 1 || got.Credentials[0] != want {
		t.Errorf("credentials = %v, want %v", got.Credentials, want)
	}
}

// --- the wallet -------------------------------------------------------------

// deliveredGrant builds a delegated credential whose JWT is a real grant, so
// the wallet's own parser accepts it exactly as it accepts one read over REST.
func deliveredGrant(t *testing.T, sig string) DelegatedCredential {
	t.Helper()
	claims := map[string]any{
		"id": "urn:uuid:delegated-" + sig,
		"credentialSubject": map[string]any{
			"scope": []map[string]any{{"protocol": "*", "messageTypes": []string{"*"}}},
		},
	}
	return DelegatedCredential{
		ID:               "urn:uuid:delegated-" + sig,
		ParentCapability: "urn:uuid:parent",
		CredentialJWT:    grantJWT(t, claims, sig),
	}
}

func TestCredentialsFromTheJoinReplyAreAttachedThoughTheNodeStoresNone(t *testing.T) {
	// GET /api/v1/credentials returns nothing for a borrowed DID, forever:
	// the node stores nothing about a credential that belongs to a connection.
	w := newWallet(func(context.Context, string) ([]map[string]json.RawMessage, error) {
		return nil, nil
	}, time.Minute, time.Second)

	w.seedDelivered("did:web:child", []DelegatedCredential{deliveredGrant(t, "sigA")})

	att, err := w.attachmentsFor(context.Background(), "did:web:child",
		&Message{To: []string{"did:web:peer"}, Type: "https://layr8.io/protocols/echo/1.0/ping"}, nil)
	if err != nil {
		t.Fatalf("attachmentsFor: %v", err)
	}
	if len(att) != 1 {
		t.Fatalf("attached %d credentials, want 1", len(att))
	}
}

func TestDeliveredCredentialsSurviveAFailedReadOfASourceThatWillNeverHaveThem(t *testing.T) {
	readErr := errors.New("credentials endpoint unavailable")
	w := newWallet(func(context.Context, string) ([]map[string]json.RawMessage, error) {
		return nil, readErr
	}, time.Minute, time.Second)

	w.seedDelivered("did:web:child", []DelegatedCredential{deliveredGrant(t, "sigA")})

	att, err := w.attachmentsFor(context.Background(), "did:web:child",
		&Message{To: []string{"did:web:peer"}, Type: "https://layr8.io/protocols/echo/1.0/ping"}, nil)
	if err != nil {
		t.Fatalf("a failed read must not withhold what the join reply already delivered: %v", err)
	}
	if len(att) != 1 {
		t.Fatalf("attached %d credentials, want 1", len(att))
	}

	// With nothing delivered, the same failure IS the only answer this DID has
	// and the caller is told.
	if _, err := w.attachmentsFor(context.Background(), "did:web:other",
		&Message{To: []string{"did:web:peer"}, Type: "https://layr8.io/protocols/echo/1.0/ping"}, nil); !errors.Is(err, readErr) {
		t.Errorf("error = %v, want the read failure", err)
	}
}

func TestARejoinReplacesWhatThePreviousJoinDelivered(t *testing.T) {
	w := newWallet(func(context.Context, string) ([]map[string]json.RawMessage, error) {
		return nil, nil
	}, time.Minute, time.Second)

	w.seedDelivered("did:web:child", []DelegatedCredential{deliveredGrant(t, "sigA")})
	w.seedDelivered("did:web:child", []DelegatedCredential{deliveredGrant(t, "sigB")})

	held := w.deliveredTo("did:web:child")
	if len(held) != 1 {
		t.Fatalf("held %d credentials after a rejoin, want 1 — a rejoin replaces, it does not merge", len(held))
	}
	if held[0].ID != "urn:uuid:delegated-sigB" {
		t.Errorf("held %q, want the credential from the LATEST join", held[0].ID)
	}
}

// TestAJoinWithNoReadingClearsWhatThePreviousOneDelivered is the case the
// unconditional callback exists for: the node mints a fresh set per join, so a
// rejoin that carries no reading is a rejoin after which the previous set must
// go. Keeping it leaves the wallet attaching last join's credentials while
// DelegatedCredentials() reports there are none.
func TestAJoinWithNoReadingClearsWhatThePreviousOneDelivered(t *testing.T) {
	w := newWallet(func(context.Context, string) ([]map[string]json.RawMessage, error) {
		return nil, nil
	}, time.Minute, time.Second)

	c := &Client{wallet: w, agentDID: "did:web:child"}

	c.applyDelegated("did:web:child", &DelegatedCredentialsReading{
		Status:      DelegationComplete,
		Credentials: []DelegatedCredential{deliveredGrant(t, "sigA")},
	})
	if len(w.deliveredTo("did:web:child")) != 1 {
		t.Fatal("the first join's credentials were not seeded")
	}

	c.applyDelegated("did:web:child", nil)
	if held := w.deliveredTo("did:web:child"); len(held) != 0 {
		t.Errorf("held %d credentials after a join that carried no reading, want 0", len(held))
	}
}

func TestAnUnreadWalletDeliversNothingRatherThanAnEmptyMeasurement(t *testing.T) {
	w := newWallet(func(context.Context, string) ([]map[string]json.RawMessage, error) {
		return nil, nil
	}, time.Minute, time.Second)
	c := &Client{wallet: w, agentDID: "did:web:child"}

	c.applyDelegated("did:web:child", &DelegatedCredentialsReading{
		Status:      DelegationUnread,
		Credentials: []DelegatedCredential{},
	})
	if held := w.deliveredTo("did:web:child"); len(held) != 0 {
		t.Errorf("held %d credentials from an unread wallet, want 0", len(held))
	}
}

func TestAnEntryThatIsNotAGrantIsDroppedAtSeedTimeNotSendTime(t *testing.T) {
	w := newWallet(func(context.Context, string) ([]map[string]json.RawMessage, error) {
		return nil, nil
	}, time.Minute, time.Second)

	w.seedDelivered("did:web:child", []DelegatedCredential{
		{ID: "urn:uuid:1", CredentialJWT: "not-a-compact-jws"},
		deliveredGrant(t, "sigA"),
	})

	if held := w.deliveredTo("did:web:child"); len(held) != 1 {
		t.Errorf("held %d credentials, want 1 — the malformed one belongs nowhere near the wire", len(held))
	}
}

// --- the join reply, end to end ---------------------------------------------

func TestTheJoinReplyReadingReachesTheChannelAndTheCallback(t *testing.T) {
	reply := `{"status":"ok","response":{"capabilities":["ephemeral_delegation/1"],` +
		`"delegated_credentials":{"status":"partial","credentials":[` +
		`{"id":"urn:uuid:1","parent_capability":"urn:uuid:p","credential_jwt":"a.b.c"}]}}}`

	var seenDID string
	var seenReading *DelegatedCredentialsReading
	ch := connectedChannelWithReply(t, reply, func(did string, r *DelegatedCredentialsReading) {
		seenDID, seenReading = did, r
	})

	if !ch.supportsEphemeralDelegation() {
		t.Error("ephemeral_delegation/1 was advertised and not recorded")
	}
	got := ch.delegatedCredentials()
	if got == nil || got.Status != DelegationPartial || len(got.Credentials) != 1 {
		t.Fatalf("delegatedCredentials() = %v, want one partial credential", got)
	}
	if seenReading == nil || seenReading.Status != DelegationPartial {
		t.Errorf("callback got %v, want the same partial reading", seenReading)
	}
	if seenDID != "did:web:test" {
		t.Errorf("callback got did %q, want the DID the channel speaks as", seenDID)
	}
}

// TestACallbackFiresEvenWhenTheNodeSentNoReading pins the unconditional call:
// the reply carries no delegated_credentials, and the wallet still has to be
// told, because that is exactly when the previous set is most likely wrong.
func TestACallbackFiresEvenWhenTheNodeSentNoReading(t *testing.T) {
	fired := false
	var seenReading *DelegatedCredentialsReading
	ch := connectedChannelWithReply(t, `{"status":"ok","response":{}}`,
		func(_ string, r *DelegatedCredentialsReading) {
			fired, seenReading = true, r
		})

	if !fired {
		t.Fatal("the callback did not fire for a reply carrying no reading")
	}
	if seenReading != nil {
		t.Errorf("callback got %v, want no reading", seenReading)
	}
	if ch.supportsEphemeralDelegation() {
		t.Error("ephemeral_delegation/1 was not advertised and was recorded anyway")
	}
}

func connectedChannelWithReply(t *testing.T, reply string, onDelegated func(string, *DelegatedCredentialsReading)) *phoenixChannel {
	t.Helper()

	mock := newMockServer()
	mock.onMsg = func(msg phoenixMessage) {
		if msg.Event == "phx_join" {
			mock.sendToClient(phoenixMessage{
				JoinRef: msg.Ref,
				Ref:     msg.Ref,
				Topic:   msg.Topic,
				Event:   "phx_reply",
				Payload: json.RawMessage(reply),
			})
		}
	}

	server := httptest.NewServer(http.HandlerFunc(mock.handler))
	t.Cleanup(server.Close)
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/plugin_socket/websocket"

	ch := newPhoenixChannel(wsURL, "test-key", "did:web:test", false, nil)
	ch.setBorrower(testParent, ChildNameSourceSDK)
	ch.onDelegatedCredentials(onDelegated)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ch.connect(ctx, []string{}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { ch.close() })
	return ch
}
