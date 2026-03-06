package layr8

import (
	"context"
	"fmt"
)

// VerifiedPresentation is returned by VerifyPresentation.
type VerifiedPresentation struct {
	Presentation map[string]any `json:"presentation"`
	Headers      map[string]any `json:"headers"`
}

// --- Presentation Sign ---

// PresentationSignOption configures SignPresentation behavior.
type PresentationSignOption func(*presentationSignOpts)

type presentationSignOpts struct {
	holderDID string
	format    CredentialFormat
	nonce     string
}

// WithPresentationHolderDID overrides the default holder DID (client.DID()) for signing.
func WithPresentationHolderDID(did string) PresentationSignOption {
	return func(o *presentationSignOpts) { o.holderDID = did }
}

// WithPresentationFormat sets the output format (defaults to compact_jwt).
func WithPresentationFormat(f CredentialFormat) PresentationSignOption {
	return func(o *presentationSignOpts) { o.format = f }
}

// WithNonce sets the nonce for the presentation.
func WithNonce(nonce string) PresentationSignOption {
	return func(o *presentationSignOpts) { o.nonce = nonce }
}

// SignPresentation signs a W3C Verifiable Presentation wrapping one or more signed credentials.
// Uses the holder's authentication key (not assertion key).
// Defaults: holder = client.DID(), format = compact_jwt.
func (c *Client) SignPresentation(ctx context.Context, credentials []string, opts ...PresentationSignOption) (string, error) {
	o := presentationSignOpts{
		holderDID: c.agentDID,
		format:    FormatCompactJWT,
	}
	for _, opt := range opts {
		opt(&o)
	}

	body := map[string]any{
		"credentials": credentials,
		"holder_did":  o.holderDID,
		"format":      string(o.format),
	}
	if o.nonce != "" {
		body["nonce"] = o.nonce
	}

	var result struct {
		SignedPresentation string `json:"signed_presentation"`
	}
	if err := c.rest.post(ctx, "/api/v1/presentations/sign", body, &result); err != nil {
		return "", fmt.Errorf("sign presentation: %w", err)
	}
	return result.SignedPresentation, nil
}

// --- Presentation Verify ---

// PresentationVerifyOption configures VerifyPresentation behavior.
type PresentationVerifyOption func(*presentationVerifyOpts)

type presentationVerifyOpts struct {
	verifierDID string
}

// WithPresentationVerifierDID overrides the default verifier DID (client.DID()) for verification.
func WithPresentationVerifierDID(did string) PresentationVerifyOption {
	return func(o *presentationVerifyOpts) { o.verifierDID = did }
}

// VerifyPresentation verifies a signed presentation using the verifier DID's authentication key.
// Defaults: verifier = client.DID().
//
// Note: The verifier DID must have keys in the local node's wallet. Cross-node
// verification (presentations signed by DIDs on other nodes) is not currently supported.
func (c *Client) VerifyPresentation(ctx context.Context, signedPresentation string, opts ...PresentationVerifyOption) (*VerifiedPresentation, error) {
	o := presentationVerifyOpts{
		verifierDID: c.agentDID,
	}
	for _, opt := range opts {
		opt(&o)
	}

	body := map[string]any{
		"signed_presentation": signedPresentation,
		"verifier_did":        o.verifierDID,
	}

	var result VerifiedPresentation
	if err := c.rest.post(ctx, "/api/v1/presentations/verify", body, &result); err != nil {
		return nil, fmt.Errorf("verify presentation: %w", err)
	}
	return &result, nil
}
