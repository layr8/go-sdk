package layr8

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestSignPresentation(t *testing.T) {
	client := newTestClientWithREST(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/presentations/sign" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		if body["holder_did"] != "did:web:test.localhost:test-agent" {
			t.Errorf("holder_did = %v, want agent DID", body["holder_did"])
		}
		if body["format"] != "compact_jwt" {
			t.Errorf("format = %v, want compact_jwt", body["format"])
		}
		creds, ok := body["credentials"].([]any)
		if !ok || len(creds) != 2 {
			t.Errorf("credentials = %v, want 2-element array", body["credentials"])
		}
		if _, hasNonce := body["nonce"]; hasNonce {
			t.Error("nonce should not be set when not provided")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"signed_presentation": "eyJhbGciOiJFZERTQSJ9.vp.sig",
		})
	}))

	signed, err := client.SignPresentation(context.Background(), []string{"jwt1", "jwt2"})
	if err != nil {
		t.Fatalf("SignPresentation() error: %v", err)
	}
	if signed != "eyJhbGciOiJFZERTQSJ9.vp.sig" {
		t.Errorf("signed = %q, want test VP JWT", signed)
	}
}

func TestSignPresentation_WithOptions(t *testing.T) {
	client := newTestClientWithREST(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		if body["holder_did"] != "did:web:custom.localhost:holder" {
			t.Errorf("holder_did = %v, want custom holder", body["holder_did"])
		}
		if body["format"] != "json" {
			t.Errorf("format = %v, want json", body["format"])
		}
		if body["nonce"] != "challenge-123" {
			t.Errorf("nonce = %v, want challenge-123", body["nonce"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"signed_presentation": "{}",
		})
	}))

	_, err := client.SignPresentation(context.Background(), []string{"jwt1"},
		WithPresentationHolderDID("did:web:custom.localhost:holder"),
		WithPresentationFormat(FormatJSON),
		WithNonce("challenge-123"),
	)
	if err != nil {
		t.Fatalf("SignPresentation() error: %v", err)
	}
}

func TestVerifyPresentation(t *testing.T) {
	client := newTestClientWithREST(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/presentations/verify" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		if body["verifier_did"] != "did:web:test.localhost:test-agent" {
			t.Errorf("verifier_did = %v, want agent DID", body["verifier_did"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"presentation": map[string]any{
				"type":                 []string{"VerifiablePresentation"},
				"verifiableCredential": []string{"jwt1"},
			},
			"headers": map[string]any{"alg": "EdDSA"},
		})
	}))

	result, err := client.VerifyPresentation(context.Background(), "eyJ.vp.sig")
	if err != nil {
		t.Fatalf("VerifyPresentation() error: %v", err)
	}
	if result.Headers["alg"] != "EdDSA" {
		t.Errorf("headers.alg = %v, want EdDSA", result.Headers["alg"])
	}
}

func TestVerifyPresentation_WithVerifierDID(t *testing.T) {
	client := newTestClientWithREST(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		if body["verifier_did"] != "did:web:custom.localhost:verifier" {
			t.Errorf("verifier_did = %v, want custom verifier", body["verifier_did"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"presentation": map[string]any{},
			"headers":      map[string]any{},
		})
	}))

	_, err := client.VerifyPresentation(context.Background(), "jwt",
		WithPresentationVerifierDID("did:web:custom.localhost:verifier"),
	)
	if err != nil {
		t.Fatalf("VerifyPresentation() error: %v", err)
	}
}
