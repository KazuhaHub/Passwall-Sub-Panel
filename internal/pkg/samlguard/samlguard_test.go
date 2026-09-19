package samlguard

import (
	"errors"
	"strings"
	"testing"
)

// These tests pin the EXACT semantics of authcore/saml's raw-XML scan
// (saml/rawxml.go + the shape part of saml/validate.go at
// 1d5b0ee6a6799cfe57c5d5ec57037bcefd0fc5f8), because the replacement line (M1)
// must not relax anything the hardening line (H1) enforces. Where the two could
// reasonably differ, the divergence is pinned by a test rather than left to
// drift.

const (
	nsProtocol = "urn:oasis:names:tc:SAML:2.0:protocol"
	nsAssert   = "urn:oasis:names:tc:SAML:2.0:assertion"
	nsDSig     = "http://www.w3.org/2000/09/xmldsig#"

	acsURL    = "https://panel.example.com/panel/api/auth/saml/acs"
	otherURL  = "https://evil.example.com/acs"
	sha256RSA = "http://www.w3.org/2001/04/xmldsig-more#rsa-sha256"
	sha512RSA = "http://www.w3.org/2001/04/xmldsig-more#rsa-sha512"
	sha1RSA   = "http://www.w3.org/2000/09/xmldsig#rsa-sha1"
	sha256X   = "http://www.w3.org/2001/04/xmlenc#sha256"
	sha512X   = "http://www.w3.org/2001/04/xmlenc#sha512"
	sha384XM  = "http://www.w3.org/2001/04/xmldsig-more#sha384"
	sha1X     = "http://www.w3.org/2000/09/xmlenc#sha1"
)

// response wraps body in a Response envelope. destination == "" omits the
// Destination attribute entirely (the case crewjam's own check skips when the
// Response is unsigned).
func response(destination, body string) []byte {
	dest := ""
	if destination != "" {
		dest = ` Destination="` + destination + `"`
	}
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>` +
		`<samlp:Response xmlns:samlp="` + nsProtocol + `" xmlns:saml="` + nsAssert +
		`" xmlns:ds="` + nsDSig + `"` + dest + `>` +
		`<saml:Issuer>https://idp.example.com</saml:Issuer>` + body +
		`</samlp:Response>`)
}

// sig renders a ds:Signature carrying one SignatureMethod and one
// DigestMethod, which is what the algorithm scan walks.
func sig(sigAlg, digestAlg string) string {
	m := `<ds:SignatureMethod/>`
	if sigAlg != "" {
		m = `<ds:SignatureMethod Algorithm="` + sigAlg + `"/>`
	}
	d := `<ds:DigestMethod/>`
	if digestAlg != "" {
		d = `<ds:DigestMethod Algorithm="` + digestAlg + `"/>`
	}
	return `<ds:Signature><ds:SignedInfo>` + m +
		`<ds:Reference>` + d + `</ds:Reference></ds:SignedInfo></ds:Signature>`
}

func plainAssertion(inner string) string {
	return `<saml:Assertion>` + inner + `</saml:Assertion>`
}

func strongBody() string { return sig(sha256RSA, sha256X) + plainAssertion(sig(sha256RSA, sha256X)) }

func check(t *testing.T, raw []byte, want error) {
	t.Helper()
	err := New(acsURL).Check(raw)
	if want == nil {
		if err != nil {
			t.Fatalf("expected acceptance, got %v", err)
		}
		return
	}
	if !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
}

// --- Destination -----------------------------------------------------------

func TestCheck_AcceptsMatchingDestination(t *testing.T) {
	check(t, response(acsURL, strongBody()), nil)
}

func TestCheck_RejectsMissingDestination(t *testing.T) {
	// crewjam validates Destination only when the Response is signed or the
	// attribute is present; an unsigned Response without one skips the check
	// entirely. This is the gap the guard exists to close.
	check(t, response("", strongBody()), ErrMissingDestination)
}

func TestCheck_RejectsEmptyDestination(t *testing.T) {
	// A present-but-empty Destination is as unusable as a missing one, and
	// authcore treats the two identically.
	raw := []byte(`<samlp:Response xmlns:samlp="` + nsProtocol + `" xmlns:saml="` + nsAssert +
		`" Destination="">` + `<saml:Assertion/>` + `</samlp:Response>`)
	check(t, raw, ErrMissingDestination)
}

func TestCheck_RejectsMismatchedDestination(t *testing.T) {
	check(t, response(otherURL, strongBody()), ErrDestinationMismatch)
}

func TestCheck_DestinationIsExact(t *testing.T) {
	// Trailing slash, case and surrounding whitespace all describe a different
	// URL than the one configured, so a near-miss must not be accepted.
	for _, dest := range []string{acsURL + "/", strings.ToUpper(acsURL), " " + acsURL} {
		check(t, response(dest, strongBody()), ErrDestinationMismatch)
	}
}

func TestCheck_DestinationKeepsLastAttribute(t *testing.T) {
	// Parity pin: authcore's scanResponseShape assigns on every Destination
	// match, so the LAST attribute wins. Two Destination attributes can only
	// coexist by differing in namespace. Pinned so that "tidying" this rule in
	// one implementation but not the other fails here instead of becoming a
	// silent H/M behavioural drift.
	build := func(first, second string) []byte {
		return []byte(`<?xml version="1.0"?>` +
			`<samlp:Response xmlns:samlp="` + nsProtocol + `" xmlns:saml="` + nsAssert +
			`" xmlns:evil="urn:example:evil" Destination="` + first +
			`" evil:Destination="` + second + `">` +
			`<saml:Assertion/>` +
			`</samlp:Response>`)
	}
	// Correct value last → accepted.
	check(t, build(otherURL, acsURL), nil)
	// Wrong value last → rejected.
	check(t, build(acsURL, otherURL), ErrDestinationMismatch)
}

// --- assertion count ------------------------------------------------------

func TestCheck_AcceptsSinglePlainAssertion(t *testing.T) {
	check(t, response(acsURL, strongBody()), nil)
}

func TestCheck_AcceptsSingleEncryptedAssertion(t *testing.T) {
	// Encrypted assertions are allowed (the SP holds a decryption key), and one
	// encrypted assertion is a count of one. Note the algorithm scan cannot see
	// inside it — see the package doc for that stated limit.
	body := sig(sha256RSA, sha256X) + `<saml:EncryptedAssertion><x/></saml:EncryptedAssertion>`
	check(t, response(acsURL, body), nil)
}

func TestCheck_RejectsNoAssertion(t *testing.T) {
	// authcore reports a zero count as ErrMalformed rather than a dedicated
	// sentinel; the guard keeps the same taxonomy.
	check(t, response(acsURL, sig(sha256RSA, sha256X)), ErrMalformed)
}

func TestCheck_RejectsTwoPlainAssertions(t *testing.T) {
	body := sig(sha256RSA, sha256X) + plainAssertion("") + plainAssertion("")
	check(t, response(acsURL, body), ErrTooManyAssertions)
}

func TestCheck_RejectsPlainPlusEncrypted(t *testing.T) {
	// The smuggling shape: a legitimate assertion alongside a second,
	// differently-trusted one. crewjam returns the first that validates.
	body := sig(sha256RSA, sha256X) + plainAssertion("") +
		`<saml:EncryptedAssertion><x/></saml:EncryptedAssertion>`
	check(t, response(acsURL, body), ErrTooManyAssertions)
}

func TestCheck_CountsOnlyDirectChildren(t *testing.T) {
	// An Assertion nested deeper is not a Response child. crewjam's own
	// findChildren uses the same scope, so counting at any depth would reject
	// documents the verifier would never have read.
	body := sig(sha256RSA, sha256X) +
		`<samlp:Extensions><saml:Assertion/></samlp:Extensions>` +
		plainAssertion("")
	check(t, response(acsURL, body), nil)
}

func TestCheck_IgnoresForeignNamespaceAssertion(t *testing.T) {
	// Only the SAML assertion namespace counts towards the total.
	body := sig(sha256RSA, sha256X) +
		`<ns:Assertion xmlns:ns="urn:example:not-saml"/>` +
		plainAssertion("")
	check(t, response(acsURL, body), nil)
}

func TestCheck_RejectsForeignNamespaceOnlyAssertion(t *testing.T) {
	body := sig(sha256RSA, sha256X) + `<ns:Assertion xmlns:ns="urn:example:not-saml"/>`
	check(t, response(acsURL, body), ErrMalformed)
}

// --- algorithms -----------------------------------------------------------

func TestCheck_AcceptsStrongAlgorithms(t *testing.T) {
	sigAlgs := []string{
		"http://www.w3.org/2001/04/xmldsig-more#rsa-sha256",
		"http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha256",
		"http://www.w3.org/2001/04/xmldsig-more#rsa-sha384",
		"http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha384",
		"http://www.w3.org/2001/04/xmldsig-more#rsa-sha512",
		"http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha512",
	}
	digestAlgs := []string{sha256X, sha384XM, sha512X}
	for _, s := range sigAlgs {
		for _, d := range digestAlgs {
			check(t, response(acsURL, sig(s, d)+plainAssertion("")), nil)
		}
	}
}

func TestCheck_RejectsWeakAlgorithm(t *testing.T) {
	cases := map[string]string{
		"rsa-sha1":    sha1RSA,
		"dsa-sha1":    "http://www.w3.org/2000/09/xmldsig#dsa-sha1",
		"hmac-sha1":   "http://www.w3.org/2000/09/xmldsig#hmac-sha1",
		"ecdsa-sha1":  "http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha1",
		"sha1 digest": sha1X,
		"md5":         "http://www.w3.org/2001/04/xmldsig-more#md5",
		"sha1 dsig":   "http://www.w3.org/2000/09/xmldsig#sha1",
		"unknown URI": "http://example.com/not-a-real-algorithm",
	}
	for name, alg := range cases {
		t.Run(name, func(t *testing.T) {
			// Weak on the Response signature.
			check(t, response(acsURL, sig(alg, sha256X)+plainAssertion("")), ErrWeakSignatureAlgorithm)
			// Weak as a digest.
			check(t, response(acsURL, sig(sha256RSA, alg)+plainAssertion("")), ErrWeakSignatureAlgorithm)
			// Weak on the assertion's own signature.
			check(t, response(acsURL, sig(sha256RSA, sha256X)+plainAssertion(sig(alg, sha256X))), ErrWeakSignatureAlgorithm)
		})
	}
}

func TestCheck_RejectsDigestOutsideAuthcoreAllowlist(t *testing.T) {
	// H's allowlist is authcore's list verbatim (saml/rawxml.go): it carries
	// xmldsig-more#sha384 but NOT xmlenc#sha384. Pinned deliberately — widening
	// H without the matching upstream change would make M relax a check H
	// enforces, which ADR 0036 and the migration plan forbid. Widening is an
	// upstream (A1) change with its own test, not a local edit here.
	check(t, response(acsURL, sig(sha256RSA, "http://www.w3.org/2001/04/xmlenc#sha384")+plainAssertion("")),
		ErrWeakSignatureAlgorithm)
}

func TestCheck_RejectsWhenAnySignatureIsWeak(t *testing.T) {
	// A strong outer signature must not launder a weak assertion signature.
	body := sig(sha512RSA, sha512X) + plainAssertion(sig(sha1RSA, sha256X))
	check(t, response(acsURL, body), ErrWeakSignatureAlgorithm)
}

func TestCheck_IgnoresUnrelatedElements(t *testing.T) {
	// The scan is keyed on the element's local name; a sha1 value sitting in
	// some other element is not an algorithm declaration.
	body := sig(sha256RSA, sha256X) +
		`<samlp:Extensions><Note Algorithm="` + sha1RSA + `"/></samlp:Extensions>` +
		plainAssertion("")
	check(t, response(acsURL, body), nil)
}

func TestCheck_ScansSignatureMethodByLocalName(t *testing.T) {
	// Parity pin: the scan matches the element's LOCAL name, not its namespace
	// (authcore does the same). So a weak algorithm is refused even when
	// declared in a foreign namespace, and a strong one there is not a failure.
	weak := `<evil:SignatureMethod xmlns:evil="urn:example:evil" Algorithm="` + sha1RSA + `"/>`
	check(t, response(acsURL, sig(sha256RSA, sha256X)+plainAssertion(weak)), ErrWeakSignatureAlgorithm)

	strong := `<evil:SignatureMethod xmlns:evil="urn:example:evil" Algorithm="` + sha256RSA + `"/>`
	check(t, response(acsURL, sig(sha256RSA, sha256X)+plainAssertion(strong)), nil)
}

func TestCheck_IgnoresAlgorithmlessDeclaration(t *testing.T) {
	// Parity pin: an element with no Algorithm attribute carries nothing the
	// scan can judge. authcore skips it and lets signature verification fail
	// instead, so the guard must not invent a rejection here.
	body := `<ds:Signature><ds:SignedInfo><ds:SignatureMethod/>` +
		`<ds:Reference><ds:DigestMethod/></ds:Reference></ds:SignedInfo></ds:Signature>` +
		plainAssertion("")
	check(t, response(acsURL, body), nil)
}

// --- envelope -------------------------------------------------------------

func TestCheck_RejectsMalformed(t *testing.T) {
	full := response(acsURL, strongBody())
	for name, raw := range map[string][]byte{
		"empty":      {},
		"not xml":    []byte("hello"),
		"unclosed":   []byte(`<samlp:Response>`),
		"wrong root": []byte(`<NotAResponse/>`),
		"truncated":  full[:40],
	} {
		t.Run(name, func(t *testing.T) {
			check(t, raw, ErrMalformed)
		})
	}
}

func TestCheck_AcceptsAlternateNamespacePrefixes(t *testing.T) {
	// Prefixes are arbitrary; the assertion namespace URI is what is matched.
	raw := []byte(`<?xml version="1.0"?>` +
		`<ns1:Response xmlns:ns1="` + nsProtocol + `" xmlns:ns2="` + nsAssert +
		`" xmlns:ds="` + nsDSig + `" Destination="` + acsURL + `">` +
		`<ds:Signature><ds:SignedInfo><ds:SignatureMethod Algorithm="` + sha256RSA + `"/>` +
		`<ds:Reference><ds:DigestMethod Algorithm="` + sha256X + `"/></ds:Reference>` +
		`</ds:SignedInfo></ds:Signature>` +
		`<ns2:Assertion/>` +
		`</ns1:Response>`)
	check(t, raw, nil)
}

func TestCheck_RejectsEmptyACSURLConfiguration(t *testing.T) {
	// A guard with no configured ACS URL cannot judge a Destination: accepting
	// anything would silently disable the check.
	if err := New("").Check(response(acsURL, strongBody())); !errors.Is(err, ErrDestinationMismatch) {
		t.Fatalf("expected rejection when the configured ACS URL is empty, got %v", err)
	}
}
