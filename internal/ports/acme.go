package ports

import (
	"context"
	"time"
)

// ACMEIssuer obtains TLS certificates via the ACME protocol using a DNS-01
// challenge (the only challenge that works when PSP is not on the cert's host
// and the only one that yields wildcards). The lego implementation lives in
// internal/adapters/acme; domain/service code never imports lego.
type ACMEIssuer interface {
	// Obtain runs the full DNS-01 flow for req.Domains and returns the issued
	// certificate. It registers the ACME account on first use (when
	// AccountKeyPEM / RegistrationJSON are empty) and returns the (possibly
	// newly generated) account key + registration so the caller can persist
	// them for reuse across future certificates sharing the (Email, Directory).
	Obtain(ctx context.Context, req ACMERequest) (ACMEResult, error)
}

// ACMERequest is the input to one ACME issuance.
type ACMERequest struct {
	Domains          []string          // SAN list; may include a wildcard (e.g. "*.example.com")
	Email            string            // ACME account contact
	DirectoryURL     string            // ACME directory URL (Let's Encrypt prod/staging, ZeroSSL, ...)
	AccountKeyPEM    string            // empty => generate a new account key (returned in the result)
	RegistrationJSON string            // empty => register the account (returned in the result)
	DNSProvider      string            // lego provider code (e.g. "cloudflare", "alidns", "route53")
	DNSCredentials   map[string]string // provider env vars (e.g. {"CF_DNS_API_TOKEN": "..."})
}

// ACMEResult is the output of one ACME issuance. CertPEM/KeyPEM are handed to
// the caller to store encrypted and deploy; AccountKeyPEM/RegistrationJSON are
// persisted so the account is reused (staying under ACME rate limits).
type ACMEResult struct {
	CertPEM          string // fullchain (leaf + issuer), PEM
	KeyPEM           string // certificate private key, PEM
	AccountKeyPEM    string // account key (newly generated or echoed back), PEM
	RegistrationJSON string // account registration resource, JSON
	NotBefore        time.Time
	NotAfter         time.Time
	Fingerprint      string // leaf SHA-256, hex — drives content-diff-gated redeploy
}
