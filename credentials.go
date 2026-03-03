package layr8

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// CredentialFormat controls the signed credential output encoding.
type CredentialFormat string

const (
	FormatCompactJWT CredentialFormat = "compact_jwt" // default
	FormatJSON       CredentialFormat = "json"
	FormatJWT        CredentialFormat = "jwt"
	FormatEnveloped  CredentialFormat = "enveloped"
)

// Credential represents a W3C Verifiable Credential for signing.
type Credential struct {
	Context           []string       `json:"@context,omitempty"`
	ID                string         `json:"id,omitempty"`
	Type              []string       `json:"type,omitempty"`
	Issuer            string         `json:"issuer,omitempty"`
	CredentialSubject map[string]any `json:"credentialSubject"`
	ValidFrom         string         `json:"validFrom,omitempty"`
	ValidUntil        string         `json:"validUntil,omitempty"`
}

// VerifiedCredential is returned by VerifyCredential.
type VerifiedCredential struct {
	Credential map[string]any `json:"credential"`
	Headers    map[string]any `json:"headers"`
}

// StoredCredential represents a credential in the node's credential store.
type StoredCredential struct {
	ID            string `json:"id"`
	HolderDID     string `json:"holder_did"`
	CredentialJWT string `json:"credential_jwt"`
	IssuerDID     string `json:"issuer_did,omitempty"`
	ValidUntil    string `json:"valid_until,omitempty"`
}

// --- Credential Sign ---

// CredentialSignOption configures SignCredential behavior.
type CredentialSignOption func(*credentialSignOpts)

type credentialSignOpts struct {
	issuerDID string
	format    CredentialFormat
}

// WithIssuerDID overrides the default issuer DID (client.DID()) for signing.
func WithIssuerDID(did string) CredentialSignOption {
	return func(o *credentialSignOpts) { o.issuerDID = did }
}

// WithCredentialFormat sets the output format (defaults to compact_jwt).
func WithCredentialFormat(f CredentialFormat) CredentialSignOption {
	return func(o *credentialSignOpts) { o.format = f }
}

// SignCredential signs a W3C Verifiable Credential using the issuer's assertion key.
// Defaults: issuer = client.DID(), format = compact_jwt.
//
// Note: The cloud-node signs using the issuer DID's assertion key from the local wallet.
func (c *Client) SignCredential(ctx context.Context, cred Credential, opts ...CredentialSignOption) (string, error) {
	o := credentialSignOpts{
		issuerDID: c.agentDID,
		format:    FormatCompactJWT,
	}
	for _, opt := range opts {
		opt(&o)
	}

	body := map[string]any{
		"credential": cred,
		"issuer_did": o.issuerDID,
		"format":     string(o.format),
	}

	var result struct {
		SignedCredential string `json:"signed_credential"`
	}
	if err := c.rest.post(ctx, "/api/v1/credentials/sign", body, &result); err != nil {
		return "", fmt.Errorf("sign credential: %w", err)
	}
	return result.SignedCredential, nil
}

// --- Credential Verify ---

// CredentialVerifyOption configures VerifyCredential behavior.
type CredentialVerifyOption func(*credentialVerifyOpts)

type credentialVerifyOpts struct {
	verifierDID string
}

// WithVerifierDID overrides the default verifier DID (client.DID()) for verification.
func WithVerifierDID(did string) CredentialVerifyOption {
	return func(o *credentialVerifyOpts) { o.verifierDID = did }
}

// VerifyCredential verifies a signed credential using the verifier DID's assertion key.
// Defaults: verifier = client.DID().
//
// Note: The verifier DID must have keys in the local node's wallet. Cross-node
// verification (VCs signed by DIDs on other nodes) is not currently supported.
func (c *Client) VerifyCredential(ctx context.Context, signedCredential string, opts ...CredentialVerifyOption) (*VerifiedCredential, error) {
	o := credentialVerifyOpts{
		verifierDID: c.agentDID,
	}
	for _, opt := range opts {
		opt(&o)
	}

	body := map[string]any{
		"signed_credential": signedCredential,
		"verifier_did":      o.verifierDID,
	}

	var result VerifiedCredential
	if err := c.rest.post(ctx, "/api/v1/credentials/verify", body, &result); err != nil {
		return nil, fmt.Errorf("verify credential: %w", err)
	}
	return &result, nil
}

// --- Credential Store ---

// CredentialStoreOption configures StoreCredential behavior.
type CredentialStoreOption func(*credentialStoreOpts)

type credentialStoreOpts struct {
	holderDID  string
	issuerDID  string
	validUntil *time.Time
}

// WithHolderDID overrides the default holder DID (client.DID()) for storage.
func WithHolderDID(did string) CredentialStoreOption {
	return func(o *credentialStoreOpts) { o.holderDID = did }
}

// WithStoreMeta sets optional metadata when storing a credential.
func WithStoreMeta(issuerDID string, validUntil time.Time) CredentialStoreOption {
	return func(o *credentialStoreOpts) {
		o.issuerDID = issuerDID
		o.validUntil = &validUntil
	}
}

// StoreCredential stores a signed credential JWT for a holder.
// Defaults: holder = client.DID().
func (c *Client) StoreCredential(ctx context.Context, credentialJWT string, opts ...CredentialStoreOption) (*StoredCredential, error) {
	o := credentialStoreOpts{
		holderDID: c.agentDID,
	}
	for _, opt := range opts {
		opt(&o)
	}

	body := map[string]any{
		"holder_did":     o.holderDID,
		"credential_jwt": credentialJWT,
	}
	if o.issuerDID != "" {
		body["issuer_did"] = o.issuerDID
	}
	if o.validUntil != nil {
		body["valid_until"] = o.validUntil.Format(time.RFC3339)
	}

	var result StoredCredential
	if err := c.rest.post(ctx, "/api/v1/credentials", body, &result); err != nil {
		return nil, fmt.Errorf("store credential: %w", err)
	}
	return &result, nil
}

// --- Credential List ---

// CredentialListOption configures ListCredentials behavior.
type CredentialListOption func(*credentialListOpts)

type credentialListOpts struct {
	holderDID string
}

// WithListHolderDID overrides the default holder DID (client.DID()) for listing.
func WithListHolderDID(did string) CredentialListOption {
	return func(o *credentialListOpts) { o.holderDID = did }
}

// ListCredentials lists all stored credentials for a holder.
// Defaults: holder = client.DID().
func (c *Client) ListCredentials(ctx context.Context, opts ...CredentialListOption) ([]StoredCredential, error) {
	o := credentialListOpts{
		holderDID: c.agentDID,
	}
	for _, opt := range opts {
		opt(&o)
	}

	path := "/api/v1/credentials?holder_did=" + url.QueryEscape(o.holderDID)

	var result struct {
		Credentials []StoredCredential `json:"credentials"`
	}
	if err := c.rest.get(ctx, path, &result); err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}
	return result.Credentials, nil
}

// GetCredential retrieves a stored credential by ID.
func (c *Client) GetCredential(ctx context.Context, credentialID string) (*StoredCredential, error) {
	path := "/api/v1/credentials/" + url.PathEscape(credentialID)

	var result StoredCredential
	if err := c.rest.get(ctx, path, &result); err != nil {
		return nil, fmt.Errorf("get credential: %w", err)
	}
	return &result, nil
}
