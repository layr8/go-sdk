package layr8

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestClientWithREST creates a client with a test REST server.
func newTestClientWithREST(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return &Client{
		rest:     newRestClient(srv.URL, "test-api-key"),
		agentDID: "did:web:test.localhost:test-agent",
	}
}

func TestSignCredential(t *testing.T) {
	client := newTestClientWithREST(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/credentials/sign" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if r.Header.Get("x-api-key") != "test-api-key" {
			t.Errorf("missing or wrong api key header")
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		if body["issuer_did"] != "did:web:test.localhost:test-agent" {
			t.Errorf("issuer_did = %v, want agent DID", body["issuer_did"])
		}
		if body["format"] != "compact_jwt" {
			t.Errorf("format = %v, want compact_jwt", body["format"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"signed_credential": "eyJhbGciOiJFZERTQSJ9.test.signature",
		})
	}))

	cred := Credential{
		ID:     "urn:uuid:test-123",
		Issuer: "did:web:test.localhost:test-agent",
		CredentialSubject: map[string]any{
			"id":  "customer-abc",
			"org": "testorg",
		},
	}

	signed, err := client.SignCredential(context.Background(), cred)
	if err != nil {
		t.Fatalf("SignCredential() error: %v", err)
	}
	if signed != "eyJhbGciOiJFZERTQSJ9.test.signature" {
		t.Errorf("signed = %q, want test JWT", signed)
	}
}

func TestSignCredential_WithOptions(t *testing.T) {
	client := newTestClientWithREST(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		if body["issuer_did"] != "did:web:other.localhost:other-agent" {
			t.Errorf("issuer_did = %v, want override DID", body["issuer_did"])
		}
		if body["format"] != "json" {
			t.Errorf("format = %v, want json", body["format"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"signed_credential": "{}",
		})
	}))

	cred := Credential{CredentialSubject: map[string]any{"test": true}}
	_, err := client.SignCredential(context.Background(), cred,
		WithIssuerDID("did:web:other.localhost:other-agent"),
		WithCredentialFormat(FormatJSON),
	)
	if err != nil {
		t.Fatalf("SignCredential() error: %v", err)
	}
}

func TestVerifyCredential(t *testing.T) {
	client := newTestClientWithREST(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/credentials/verify" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		if body["verifier_did"] != "did:web:test.localhost:test-agent" {
			t.Errorf("verifier_did = %v, want agent DID", body["verifier_did"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"credential": map[string]any{
				"id":                "urn:uuid:test-123",
				"credentialSubject": map[string]any{"id": "customer-abc", "org": "testorg"},
			},
			"headers": map[string]any{"alg": "EdDSA"},
		})
	}))

	result, err := client.VerifyCredential(context.Background(), "eyJhbGciOiJFZERTQSJ9.test.sig")
	if err != nil {
		t.Fatalf("VerifyCredential() error: %v", err)
	}
	if result.Credential["id"] != "urn:uuid:test-123" {
		t.Errorf("credential.id = %v, want test ID", result.Credential["id"])
	}
	if result.Headers["alg"] != "EdDSA" {
		t.Errorf("headers.alg = %v, want EdDSA", result.Headers["alg"])
	}
}

func TestVerifyCredential_WithVerifierDID(t *testing.T) {
	client := newTestClientWithREST(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		if body["verifier_did"] != "did:web:custom.localhost:agent" {
			t.Errorf("verifier_did = %v, want custom DID", body["verifier_did"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"credential": map[string]any{},
			"headers":    map[string]any{},
		})
	}))

	_, err := client.VerifyCredential(context.Background(), "jwt",
		WithVerifierDID("did:web:custom.localhost:agent"),
	)
	if err != nil {
		t.Fatalf("VerifyCredential() error: %v", err)
	}
}

func TestStoreCredential(t *testing.T) {
	client := newTestClientWithREST(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/credentials" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		if body["holder_did"] != "did:web:test.localhost:test-agent" {
			t.Errorf("holder_did = %v, want agent DID", body["holder_did"])
		}

		w.WriteHeader(http.StatusCreated)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":             "urn:uuid:stored-123",
			"holder_did":     body["holder_did"],
			"credential_jwt": body["credential_jwt"],
		})
	}))

	result, err := client.StoreCredential(context.Background(), "eyJ0ZXN0.jwt.here")
	if err != nil {
		t.Fatalf("StoreCredential() error: %v", err)
	}
	if result.ID != "urn:uuid:stored-123" {
		t.Errorf("stored.ID = %q, want stored ID", result.ID)
	}
}

func TestStoreCredential_WithMeta(t *testing.T) {
	validUntil := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)

	client := newTestClientWithREST(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		if body["holder_did"] != "did:web:custom.localhost:holder" {
			t.Errorf("holder_did = %v, want custom holder", body["holder_did"])
		}
		if body["issuer_did"] != "did:web:issuer.localhost:agent" {
			t.Errorf("issuer_did = %v, want issuer DID", body["issuer_did"])
		}
		if body["valid_until"] == nil {
			t.Error("valid_until should be set")
		}

		w.WriteHeader(http.StatusCreated)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":         "urn:uuid:stored-456",
			"holder_did": body["holder_did"],
		})
	}))

	_, err := client.StoreCredential(context.Background(), "jwt",
		WithHolderDID("did:web:custom.localhost:holder"),
		WithStoreMeta("did:web:issuer.localhost:agent", validUntil),
	)
	if err != nil {
		t.Fatalf("StoreCredential() error: %v", err)
	}
}

func TestListCredentials(t *testing.T) {
	client := newTestClientWithREST(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if r.URL.Query().Get("holder_did") != "did:web:test.localhost:test-agent" {
			t.Errorf("holder_did query = %v, want agent DID", r.URL.Query().Get("holder_did"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"credentials": []map[string]any{
				{"id": "cred-1", "holder_did": "did:web:test.localhost:test-agent", "credential_jwt": "jwt1"},
				{"id": "cred-2", "holder_did": "did:web:test.localhost:test-agent", "credential_jwt": "jwt2"},
			},
		})
	}))

	creds, err := client.ListCredentials(context.Background())
	if err != nil {
		t.Fatalf("ListCredentials() error: %v", err)
	}
	if len(creds) != 2 {
		t.Errorf("len(creds) = %d, want 2", len(creds))
	}
}

func TestGetCredential(t *testing.T) {
	client := newTestClientWithREST(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/credentials/urn:uuid:test-123" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":             "urn:uuid:test-123",
			"holder_did":     "did:web:test.localhost:test-agent",
			"credential_jwt": "jwt-data",
		})
	}))

	cred, err := client.GetCredential(context.Background(), "urn:uuid:test-123")
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}
	if cred.ID != "urn:uuid:test-123" {
		t.Errorf("cred.ID = %q, want test-123", cred.ID)
	}
}

func TestSignCredential_RESTError(t *testing.T) {
	client := newTestClientWithREST(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"error": "No assertion key found for issuer DID",
		})
	}))

	_, err := client.SignCredential(context.Background(), Credential{
		CredentialSubject: map[string]any{"test": true},
	})
	if err == nil {
		t.Fatal("SignCredential() should error on 404")
	}

	var restErr *RESTError
	if !containsRESTError(err, &restErr) {
		t.Fatalf("error should wrap RESTError, got: %v", err)
	}
	if restErr.StatusCode != 404 {
		t.Errorf("status = %d, want 404", restErr.StatusCode)
	}
}

// containsRESTError unwraps the error chain to find a *RESTError.
func containsRESTError(err error, target **RESTError) bool {
	for err != nil {
		if re, ok := err.(*RESTError); ok {
			*target = re
			return true
		}
		if u, ok := err.(interface{ Unwrap() error }); ok {
			err = u.Unwrap()
		} else {
			return false
		}
	}
	return false
}
