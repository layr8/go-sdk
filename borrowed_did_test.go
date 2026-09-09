package layr8

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testParent = "did:web:acme.example:users:alice"

// --- the name, and who chose it ---------------------------------------------

func TestNoParentLeavesTheDIDAloneAndNamesNobody(t *testing.T) {
	got, err := resolveBorrowerDID("did:web:acme.example:agents:bot", "")
	if err != nil {
		t.Fatalf("resolveBorrowerDID: %v", err)
	}
	if got.did != "did:web:acme.example:agents:bot" {
		t.Errorf("did = %q, want it returned unchanged", got.did)
	}
	// This rule is about a relationship between two names, and there is only
	// one name here. Reporting "client" would claim a caller chose a
	// BORROWER's name when there is no borrower.
	if got.nameSource != "" {
		t.Errorf("nameSource = %q, want %q", got.nameSource, "")
	}
}

func TestAParentWithNoDIDDerivesOneExactlyOneSegmentBeneath(t *testing.T) {
	got, err := resolveBorrowerDID("", testParent)
	if err != nil {
		t.Fatalf("resolveBorrowerDID: %v", err)
	}
	if !IsBeneathParent(got.did, testParent) {
		t.Fatalf("derived %q is not beneath %q", got.did, testParent)
	}
	segment := strings.TrimPrefix(got.did, testParent+":")
	if strings.Contains(segment, ":") {
		t.Errorf("derived segment %q adds more than one segment", segment)
	}
	if len(segment) != ChildSegmentLength {
		t.Errorf("segment length = %d, want %d", len(segment), ChildSegmentLength)
	}
	if got.nameSource != ChildNameSourceSDK {
		t.Errorf("nameSource = %q, want %q", got.nameSource, ChildNameSourceSDK)
	}
}

func TestACallerSuppliedNameBeneathTheParentIsReportedAsTheCallers(t *testing.T) {
	child := testParent + ":handbuilt"
	got, err := resolveBorrowerDID(child, testParent)
	if err != nil {
		t.Fatalf("resolveBorrowerDID: %v", err)
	}
	if got.did != child {
		t.Errorf("did = %q, want %q", got.did, child)
	}
	if got.nameSource != ChildNameSourceClient {
		t.Errorf("nameSource = %q, want %q", got.nameSource, ChildNameSourceClient)
	}
}

func TestADIDNotBeneathItsParentFailsLocally(t *testing.T) {
	_, err := resolveBorrowerDID("did:web:acme.example:agents:bot", testParent)
	if err == nil {
		t.Fatal("want an error for a DID that is not beneath its parent")
	}
	var nbp *NotBeneathParentError
	if !errors.As(err, &nbp) {
		t.Fatalf("error = %T, want *NotBeneathParentError", err)
	}
	// The message has to carry the way out, or the reader is left with a rule
	// and no repair.
	if !strings.Contains(err.Error(), "leave AgentDID empty") {
		t.Errorf("error does not say how to get a conforming name: %v", err)
	}
}

func TestIsBeneathParent(t *testing.T) {
	cases := []struct {
		name  string
		child string
		want  bool
	}{
		{"exactly one segment beneath", testParent + ":k7m2q9x4h3bd", true},
		{"the parent itself", testParent, false},
		{"a sibling that merely starts with the parent's text", "did:web:acme.example:users:alicent", false},
		{"two segments deeper", testParent + ":k7m2:q9x4", false},
		{"an empty further segment", testParent + ":", false},
		{"an unrelated DID", "did:web:other.example:bot", false},
		{"empty child", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsBeneathParent(tc.child, testParent); got != tc.want {
				t.Errorf("IsBeneathParent(%q, %q) = %v, want %v", tc.child, testParent, got, tc.want)
			}
		})
	}
	if IsBeneathParent(testParent+":x", "") {
		t.Error("an empty parent is beneath nothing")
	}
}

func TestDIDNamespaceOfIsTheKeyEntryCoveringEveryBorrower(t *testing.T) {
	if got := DIDNamespaceOf(testParent); got != testParent+":*" {
		t.Errorf("DIDNamespaceOf = %q, want %q", got, testParent+":*")
	}
}

func TestRandomChildSegmentUsesTheCrockfordAlphabetAndVaries(t *testing.T) {
	seen := make(map[string]struct{}, 64)
	for i := 0; i < 64; i++ {
		seg := RandomChildSegment()
		if len(seg) != ChildSegmentLength {
			t.Fatalf("segment %q has length %d, want %d", seg, len(seg), ChildSegmentLength)
		}
		for _, r := range seg {
			if !strings.ContainsRune(childSegmentAlphabet, r) {
				t.Fatalf("segment %q contains %q, which is outside the alphabet", seg, r)
			}
		}
		// i, l, o and u are omitted so the value survives being read off a
		// screen and typed back.
		if strings.ContainsAny(seg, "ilou") {
			t.Fatalf("segment %q contains a character the alphabet omits", seg)
		}
		seen[seg] = struct{}{}
	}
	// A counter or a constant would collide here, and a collision is one
	// connection joining onto another's identity.
	if len(seen) != 64 {
		t.Errorf("64 segments produced %d distinct values", len(seen))
	}
}

// TestTheThreeChildNameSourcesArePairwiseDistinct asserts the THREE values,
// not that a field was set. A generated name and a hand-built conforming one
// are identical bytes on the socket; folding "not stated" into "client" would
// claim a caller chose a name when nothing measured that.
func TestTheThreeChildNameSourcesArePairwiseDistinct(t *testing.T) {
	notStated := ChildNameSource("")
	pairs := [][2]ChildNameSource{
		{ChildNameSourceSDK, ChildNameSourceClient},
		{ChildNameSourceSDK, notStated},
		{ChildNameSourceClient, notStated},
	}
	for _, p := range pairs {
		if p[0] == p[1] {
			t.Errorf("%q and %q are the same value", p[0], p[1])
		}
	}
}

// --- the configuration ------------------------------------------------------

func TestResolveConfigSettlesTheBorrowerDIDSoAgentDIDIsTheOneThatJoins(t *testing.T) {
	cfg, err := resolveConfig(Config{
		NodeURL:   "ws://node.example/plugin_socket/websocket",
		APIKey:    "k",
		ParentDID: testParent,
	})
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}
	if !IsBeneathParent(cfg.AgentDID, testParent) {
		t.Errorf("AgentDID = %q, want a name beneath %q", cfg.AgentDID, testParent)
	}
	if cfg.childNameSource != ChildNameSourceSDK {
		t.Errorf("childNameSource = %q, want %q", cfg.childNameSource, ChildNameSourceSDK)
	}
}

func TestNewClientRefusesADIDNotBeneathItsParentBeforeAnythingIsWritten(t *testing.T) {
	_, err := NewClient(Config{
		NodeURL:   "ws://node.example/plugin_socket/websocket",
		APIKey:    "k",
		AgentDID:  "did:web:acme.example:agents:bot",
		ParentDID: testParent,
	}, func(SDKError) {})
	if err == nil {
		t.Fatal("want NewClient to refuse a DID that is not beneath its parent")
	}
	var nbp *NotBeneathParentError
	if !errors.As(err, &nbp) {
		t.Fatalf("error = %T, want *NotBeneathParentError", err)
	}
}

// --- the wire ---------------------------------------------------------------

// joinPayloadFor connects one channel to a mock node and returns the raw bytes
// of the phx_join payload it wrote.
func joinPayloadFor(t *testing.T, agentDID, parentDID string, source ChildNameSource, reply string) []byte {
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
	ch := newPhoenixChannel(wsURL, "test-key", agentDID, false, nil)
	ch.setBorrower(parentDID, source)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ch.connect(ctx, []string{"https://layr8.io/protocols/echo/1.0"}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { ch.close() })

	received := mock.getReceived()
	if len(received) == 0 || received[0].Event != "phx_join" {
		t.Fatal("no phx_join reached the server")
	}
	return received[0].Payload
}

// TestAJoinThatNamesNoParentIsUnchangedOnTheWire pins the EXACT bytes, not
// "parentDid is absent". The promise this feature makes to every existing
// caller is that a join naming no parent puts the payload on the socket it put
// there before the field existed, so the golden literal is the assertion.
func TestAJoinThatNamesNoParentIsUnchangedOnTheWire(t *testing.T) {
	const want = `{"did_spec":{"mode":"Create","storage":"ephemeral","type":"plugin",` +
		`"verificationMethods":[{"purpose":"authentication"},{"purpose":"assertionMethod"},` +
		`{"purpose":"keyAgreement"}]},"payload_types":["https://layr8.io/protocols/echo/1.0"],` +
		`"reply_protocol":true}`

	got := joinPayloadFor(t, "did:web:test", "", "", `{"status":"ok","response":{}}`)
	if string(got) != want {
		t.Errorf("join payload changed.\n got: %s\nwant: %s", got, want)
	}
}

func TestNamingAParentAddsExactlyTwoKeysToTheDIDSpec(t *testing.T) {
	child := testParent + ":k7m2q9x4h3bd"
	got := joinPayloadFor(t, child, testParent, ChildNameSourceSDK, `{"status":"ok","response":{}}`)

	var payload struct {
		DidSpec map[string]json.RawMessage `json:"did_spec"`
	}
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("unmarshal join payload: %v", err)
	}

	if string(payload.DidSpec["parentDid"]) != `"`+testParent+`"` {
		t.Errorf("did_spec.parentDid = %s, want %q", payload.DidSpec["parentDid"], testParent)
	}
	if string(payload.DidSpec["childNameSource"]) != `"sdk"` {
		t.Errorf("did_spec.childNameSource = %s, want %q", payload.DidSpec["childNameSource"], "sdk")
	}
	// Only a temporary identity may borrow authority; the node refuses
	// persistent + parentDid with e.join.plugin.child.storage-not-ephemeral.
	if string(payload.DidSpec["storage"]) != `"ephemeral"` {
		t.Errorf("did_spec.storage = %s, want %q", payload.DidSpec["storage"], "ephemeral")
	}
}

// TestChildNameSourceIsAbsentRatherThanEmptyWhenNobodyChoseABorrowersName is
// the third answer of the three. "" on the wire, or the key defaulting to
// "client", would both say something nobody measured.
func TestChildNameSourceIsAbsentRatherThanEmptyWhenNobodyChoseABorrowersName(t *testing.T) {
	got := joinPayloadFor(t, "did:web:test", "", "", `{"status":"ok","response":{}}`)
	var payload struct {
		DidSpec map[string]json.RawMessage `json:"did_spec"`
	}
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("unmarshal join payload: %v", err)
	}
	if _, present := payload.DidSpec["childNameSource"]; present {
		t.Errorf("did_spec carries childNameSource = %s when nobody chose a name",
			payload.DidSpec["childNameSource"])
	}
	if _, present := payload.DidSpec["parentDid"]; present {
		t.Error("did_spec carries parentDid when no parent was named")
	}
}

func TestACallerBuiltNameIsReportedAsTheCallersOnTheWire(t *testing.T) {
	child := testParent + ":handbuilt"
	got := joinPayloadFor(t, child, testParent, ChildNameSourceClient, `{"status":"ok","response":{}}`)
	var payload struct {
		DidSpec map[string]json.RawMessage `json:"did_spec"`
	}
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatalf("unmarshal join payload: %v", err)
	}
	if string(payload.DidSpec["childNameSource"]) != `"client"` {
		t.Errorf("did_spec.childNameSource = %s, want %q", payload.DidSpec["childNameSource"], "client")
	}
}
