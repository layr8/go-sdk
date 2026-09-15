package layr8

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"testing"
)

var urnUUIDPattern = regexp.MustCompile(`^urn:uuid:[0-9a-f-]{36}$`)

// signAndCaptureCredential calls SignCredential against a fake node and returns
// the "credential" object exactly as it was put on the wire.
func signAndCaptureCredential(t *testing.T, cred Credential, opts ...CredentialSignOption) map[string]any {
	t.Helper()
	var sent map[string]any
	client := newTestClientWithREST(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode body %s: %v", raw, err)
		}
		c, ok := body["credential"].(map[string]any)
		if !ok {
			t.Errorf("request body has no credential object: %s", raw)
		}
		sent = c
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"signed_credential": "x.y.z"})
	}))
	if _, err := client.SignCredential(context.Background(), cred, opts...); err != nil {
		t.Fatalf("SignCredential() error: %v", err)
	}
	return sent
}

func TestSignCredential_FillsMissingIDAndIssuer(t *testing.T) {
	const agentDID = "did:web:test.localhost:test-agent"
	const givenID = "urn:uuid:caller-chosen-id"
	const givenIssuer = "did:web:test.localhost:caller-issuer"

	cases := []struct {
		name       string
		cred       Credential
		wantID     string // "" means: a generated urn:uuid is expected
		wantIssuer string
	}{
		{"neither id nor issuer", Credential{}, "", agentDID},
		{"id only", Credential{ID: givenID}, givenID, agentDID},
		{"issuer only", Credential{Issuer: givenIssuer}, "", givenIssuer},
		{"both id and issuer", Credential{ID: givenID, Issuer: givenIssuer}, givenID, givenIssuer},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.cred.CredentialSubject = map[string]any{"id": "did:web:test.localhost:subject"}
			before := tc.cred
			before.CredentialSubject = map[string]any{"id": "did:web:test.localhost:subject"}

			sent := signAndCaptureCredential(t, tc.cred)

			gotID, _ := sent["id"].(string)
			if tc.wantID == "" {
				if !urnUUIDPattern.MatchString(gotID) {
					t.Errorf("sent id = %q, want a generated urn:uuid matching %s", gotID, urnUUIDPattern)
				}
			} else if gotID != tc.wantID {
				t.Errorf("sent id = %q, want caller's %q unchanged", gotID, tc.wantID)
			}
			if got := sent["issuer"]; got != tc.wantIssuer {
				t.Errorf("sent issuer = %v, want %q", got, tc.wantIssuer)
			}
			if !reflect.DeepEqual(tc.cred, before) {
				t.Errorf("caller's credential was modified: got %+v, want %+v", tc.cred, before)
			}
		})
	}
}

func TestSignCredential_FilledIssuerFollowsWithIssuerDID(t *testing.T) {
	const override = "did:web:other.localhost:other-agent"
	sent := signAndCaptureCredential(t,
		Credential{CredentialSubject: map[string]any{"test": true}},
		WithIssuerDID(override),
	)
	if got := sent["issuer"]; got != override {
		t.Errorf("sent issuer = %v, want the signing DID %q", got, override)
	}
}

func TestSignCredential_GeneratesDistinctIDs(t *testing.T) {
	cred := Credential{CredentialSubject: map[string]any{"test": true}}
	a := signAndCaptureCredential(t, cred)["id"]
	b := signAndCaptureCredential(t, cred)["id"]
	if a == b {
		t.Errorf("two signings produced the same id %v", a)
	}
}
