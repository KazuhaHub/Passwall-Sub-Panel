package auth

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/crewjam/saml"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/samlguard"
)

const (
	precheckACS = "https://panel.example.com/panel/api/auth/saml/acs"
	precheckXML = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ` +
		`xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ` +
		`xmlns:ds="http://www.w3.org/2000/09/xmldsig#" Destination="` + precheckACS + `">` +
		`<ds:Signature><ds:SignedInfo>` +
		`<ds:SignatureMethod Algorithm="http://www.w3.org/2001/04/xmldsig-more#rsa-sha256"/>` +
		`<ds:Reference><ds:DigestMethod Algorithm="http://www.w3.org/2001/04/xmlenc#sha256"/></ds:Reference>` +
		`</ds:SignedInfo></ds:Signature>` +
		`<saml:Assertion/>` +
		`</samlp:Response>`
)

// b64 encodes the way an IdP actually posts it: StdEncoding, then the MIME-style
// line wrapping Entra emits, which ParseACSResponse strips before decoding.
func b64(xml string) string {
	enc := base64.StdEncoding.EncodeToString([]byte(xml))
	var wrapped []byte
	for i := 0; i < len(enc); i += 76 {
		end := i + 76
		if end > len(enc) {
			end = len(enc)
		}
		wrapped = append(wrapped, enc[i:end]...)
		wrapped = append(wrapped, '\n')
	}
	return string(wrapped)
}

// stripWS mirrors the whitespace removal ParseACSResponse performs on the form
// value before handing it to both the pre-check and crewjam.
func stripWS(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\n', '\r', '\t', ' ':
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}

func TestPrecheckResponse_AcceptsAGoodResponse(t *testing.T) {
	if err := precheckResponse(precheckACS, stripWS(b64(precheckXML))); err != nil {
		t.Fatalf("expected acceptance, got %v", err)
	}
}

// TestPrecheckResponse_RejectsAfterBase64Wrapping pins that the pre-check sees
// the same bytes crewjam will: both decode the whitespace-stripped form value
// with base64.StdEncoding.
func TestPrecheckResponse_RejectsAfterBase64Wrapping(t *testing.T) {
	weak := `<?xml version="1.0"?><samlp:Response Destination="` + precheckACS +
		`" xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ` +
		`xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" xmlns:ds="http://www.w3.org/2000/09/xmldsig#">` +
		`<ds:Signature><ds:SignedInfo>` +
		`<ds:SignatureMethod Algorithm="http://www.w3.org/2000/09/xmldsig#rsa-sha1"/>` +
		`</ds:SignedInfo></ds:Signature><saml:Assertion/></samlp:Response>`
	err := precheckResponse(precheckACS, stripWS(b64(weak)))
	if !errors.Is(err, samlguard.ErrWeakSignatureAlgorithm) {
		t.Fatalf("expected the weak-algorithm rejection to survive base64 wrapping, got %v", err)
	}
}

// TestPrecheckResponse_RunsBeforeSignatureVerification is the point of the
// pre-check: it rejects on the document's shape alone, so an attacker does not
// need a valid signature to reach it — and cannot use one on a second assertion
// to smuggle the first past the verifier.
func TestPrecheckResponse_RunsBeforeSignatureVerification(t *testing.T) {
	// Unsigned, two assertions, strong algorithms declared. Nothing here has a
	// valid signature, and the pre-check must still refuse it.
	twoAssertions := `<?xml version="1.0"?><samlp:Response Destination="` + precheckACS +
		`" xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ` +
		`xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion">` +
		`<saml:Assertion/><saml:Assertion/></samlp:Response>`
	if err := precheckResponse(precheckACS, stripWS(b64(twoAssertions))); !errors.Is(err, samlguard.ErrTooManyAssertions) {
		t.Fatalf("expected ErrTooManyAssertions, got %v", err)
	}
}

func TestPrecheckResponse_Rejections(t *testing.T) {
	cases := map[string]struct {
		xml  string
		want error
	}{
		"missing destination": {
			xml: `<?xml version="1.0"?><samlp:Response ` +
				`xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ` +
				`xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion"><saml:Assertion/></samlp:Response>`,
			want: samlguard.ErrMissingDestination,
		},
		"wrong destination": {
			xml: `<?xml version="1.0"?><samlp:Response Destination="https://evil.example.com/acs" ` +
				`xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ` +
				`xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion"><saml:Assertion/></samlp:Response>`,
			want: samlguard.ErrDestinationMismatch,
		},
		"no assertion": {
			xml:  `<?xml version="1.0"?><samlp:Response Destination="` + precheckACS + `" xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol"/>`,
			want: samlguard.ErrMalformed,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := precheckResponse(precheckACS, stripWS(b64(tc.xml)))
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}
}

func TestPrecheckResponse_RejectsUnusableInput(t *testing.T) {
	// An artifact-binding request carries no SAMLResponse at all. PSP never
	// asks for that binding, so a missing payload is refused rather than handed
	// to the verifier.
	if err := precheckResponse(precheckACS, ""); !errors.Is(err, samlguard.ErrMalformed) {
		t.Fatalf("expected ErrMalformed for an empty payload, got %v", err)
	}
	if err := precheckResponse(precheckACS, "!!!not base64!!!"); err == nil {
		t.Fatal("expected an error for a non-base64 payload")
	}
}

// TestPrecheckResponse_UsesTheConfiguredACSURL pins that a guard built with an
// empty ACS URL cannot silently pass: it must not degrade into "skip the check".
func TestPrecheckResponse_UsesTheConfiguredACSURL(t *testing.T) {
	if err := precheckResponse("", stripWS(b64(precheckXML))); !errors.Is(err, samlguard.ErrDestinationMismatch) {
		t.Fatalf("expected a mismatch when no ACS URL is configured, got %v", err)
	}
}

// TestParseACSResponse_PreChecksBeforeVerifying proves the WIRING, not just the
// extracted function: it drives the real method with a Provider that cannot
// verify anything (no IdP metadata, no key), so a samlguard sentinel coming back
// can only mean the pre-check ran before crewjam ever saw the bytes.
//
// End-to-end coverage through the real HTTP ACS entry point with a test IdP is
// a separate requirement (acceptance S03) and is not claimed here.
func TestParseACSResponse_PreChecksBeforeVerifying(t *testing.T) {
	cfg := &config.SAMLConfig{SP: config.SPConf{ACSURL: precheckACS}}
	svc := &SAMLService{snap: &samlSnapshot{
		cfg: cfg, digest: SAMLConfigDigest(cfg), generation: 1, sp: &saml.ServiceProvider{},
	}}

	cases := map[string]struct {
		xml  string
		want error
	}{
		"weak algorithm": {
			xml: `<?xml version="1.0"?><samlp:Response Destination="` + precheckACS +
				`" xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ` +
				`xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" xmlns:ds="http://www.w3.org/2000/09/xmldsig#">` +
				`<ds:Signature><ds:SignedInfo>` +
				`<ds:SignatureMethod Algorithm="http://www.w3.org/2000/09/xmldsig#rsa-sha1"/>` +
				`</ds:SignedInfo></ds:Signature><saml:Assertion/></samlp:Response>`,
			want: samlguard.ErrWeakSignatureAlgorithm,
		},
		"two assertions": {
			xml: `<?xml version="1.0"?><samlp:Response Destination="` + precheckACS +
				`" xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ` +
				`xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion">` +
				`<saml:Assertion/><saml:Assertion/></samlp:Response>`,
			want: samlguard.ErrTooManyAssertions,
		},
		"missing destination": {
			xml: `<?xml version="1.0"?><samlp:Response ` +
				`xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" ` +
				`xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion"><saml:Assertion/></samlp:Response>`,
			want: samlguard.ErrMissingDestination,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			form := url.Values{"SAMLResponse": {stripWS(b64(tc.xml))}}
			req := httptest.NewRequest(http.MethodPost, "/api/auth/saml/acs", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			_, err := svc.ParseACSResponse(req, []string{"req-1"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v from the pre-check, got %v", tc.want, err)
			}
		})
	}
}
