// Package samlguard inspects the raw, not-yet-trusted SAML Response XML before
// crewjam/saml sees it, closing three gaps that library leaves open by design:
//
//   - An absent Destination. crewjam checks Destination only when the Response
//     itself is signed OR the attribute is present, so an attacker can omit it
//     on an otherwise validly-signed-Assertion Response and skip the check.
//   - Multiple assertions. crewjam collects every Assertion/EncryptedAssertion
//     it can parse and returns the FIRST that validates; its own source calls
//     this "less than fully correct". That lets a Response smuggle a second,
//     differently-trusted assertion alongside a legitimate one.
//   - Weak signature algorithms. crewjam accepts whatever the IdP declares,
//     including rsa-sha1 (ADFS's and legacy Keycloak's historical default).
//
// Every check here is "shape of the document", never "content a verifier has
// already extracted", so a bug in this package cannot be masked by — or mask —
// the signature check that follows. Nothing here produces identity data: the
// caller still takes every claim from the verified assertion.
//
// # Parity with authcore
//
// The rules are authcore/saml's verbatim (saml/rawxml.go and the shape half of
// saml/validate.go), because the replacement line must not relax anything this
// hardening line enforces. Where the two implementations could plausibly drift
// — the digest allowlist, local-name matching, last-Destination-wins, an
// Algorithm-less declaration — the behaviour is pinned by a test in this
// package rather than left to chance.
//
// # Stated limit
//
// The algorithm scan reads the raw XML, so it cannot see inside an
// EncryptedAssertion: the enclosed assertion's own signature algorithms are
// opaque until decrypted, and are the decrypting library's business. Do not
// describe this check as covering "every signature in the message".
package samlguard

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
)

// Sentinel errors, matchable with errors.Is. They are deliberately distinct
// from each other so the caller can log a low-cardinality reason and so a
// rejection caused by a broken deployment (bad ACS URL) reads differently from
// one caused by hostile input.
var (
	// ErrMalformed means the bytes are not a single top-level SAML Response
	// carrying exactly one assertion (not valid XML, wrong root element, no
	// assertion at all).
	ErrMalformed = errors.New("samlguard: not a well-formed single-assertion SAML Response")

	// ErrMissingDestination means the Response declares no Destination (or an
	// empty one). SAML core requires it; crewjam would skip the check.
	ErrMissingDestination = errors.New("samlguard: Response has no Destination")

	// ErrDestinationMismatch means the Destination is present but does not
	// equal the configured ACS URL.
	ErrDestinationMismatch = errors.New("samlguard: Response Destination does not match the configured ACS URL")

	// ErrTooManyAssertions means the Response carries more than one top-level
	// Assertion/EncryptedAssertion element.
	ErrTooManyAssertions = errors.New("samlguard: Response carries more than one assertion")

	// ErrWeakSignatureAlgorithm means a declared SignatureMethod or
	// DigestMethod is outside the SHA-256-or-better allowlist.
	ErrWeakSignatureAlgorithm = errors.New("samlguard: weak signature or digest algorithm")
)

const (
	// nsAssertion is the SAML assertion namespace. Only elements in it count
	// towards the assertion total; the Response root is matched by local name
	// alone, mirroring authcore.
	nsAssertion = "urn:oasis:names:tc:SAML:2.0:assertion"

	// XMLDSig element and attribute names. Literals rather than goxmldsig's
	// constants so this package adds no dependency; the values are fixed by the
	// XML Signature Recommendation, not by any Go module.
	signatureMethodTag = "SignatureMethod"
	digestMethodTag    = "DigestMethod"
	algorithmAttr      = "Algorithm"
	destinationAttr    = "Destination"
)

// strongSignatureAlgorithms and strongDigestAlgorithms are ALLOWLISTS, not
// denylists: an identifier nobody has evaluated is refused by default rather
// than accepted by omission. That is the fail-closed direction, and it means
// adding a newly standardised algorithm is a deliberate edit here plus the
// matching upstream change in authcore.
//
// The digest list is authcore's exactly — three URIs, and deliberately not
// xmlenc#sha384. See the pinned test for why that asymmetry is preserved rather
// than quietly "fixed" on this side.
var (
	strongSignatureAlgorithms = map[string]bool{
		"http://www.w3.org/2001/04/xmldsig-more#rsa-sha256":   true,
		"http://www.w3.org/2001/04/xmldsig-more#rsa-sha384":   true,
		"http://www.w3.org/2001/04/xmldsig-more#rsa-sha512":   true,
		"http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha256": true,
		"http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha384": true,
		"http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha512": true,
	}
	strongDigestAlgorithms = map[string]bool{
		"http://www.w3.org/2001/04/xmlenc#sha256":       true,
		"http://www.w3.org/2001/04/xmldsig-more#sha384": true,
		"http://www.w3.org/2001/04/xmlenc#sha512":       true,
	}
)

// Guard enforces the constraints above for one deployment's ACS URL.
type Guard struct {
	acsURL string
}

// New returns a Guard that requires every Response's Destination to equal
// acsURL exactly. An empty acsURL is not special-cased into "skip the check" —
// it simply cannot match, so a misconfigured deployment fails closed.
func New(acsURL string) *Guard { return &Guard{acsURL: acsURL} }

// shape is what one tokenizing pass over the document yields.
type shape struct {
	rootIsResponse bool
	destination    string
	hasDestination bool
	assertionCount int
}

// Check rejects a raw SAML Response that is structurally unusable or declares
// weak algorithms. A nil return means only "the verifier may proceed"; it is
// never an acceptance, and it carries no identity data.
func (g *Guard) Check(raw []byte) error {
	sh := scanShape(raw)
	if !sh.rootIsResponse {
		return fmt.Errorf("%w: no top-level Response element", ErrMalformed)
	}
	// The count is settled before Destination and algorithms so that a response
	// smuggling a second assertion is reported as such rather than surfacing a
	// downstream symptom.
	switch {
	case sh.assertionCount == 0:
		return fmt.Errorf("%w: no Assertion or EncryptedAssertion element", ErrMalformed)
	case sh.assertionCount > 1:
		return fmt.Errorf("%w: found %d", ErrTooManyAssertions, sh.assertionCount)
	}
	if !sh.hasDestination || sh.destination == "" {
		return ErrMissingDestination
	}
	if sh.destination != g.acsURL {
		return fmt.Errorf("%w: got %q, want %q", ErrDestinationMismatch, sh.destination, g.acsURL)
	}
	if el, alg := firstWeakAlgorithm(raw); el != "" {
		return fmt.Errorf("%w: %s uses %q", ErrWeakSignatureAlgorithm, el, alg)
	}
	return nil
}

// scanShape reports the document's top-level structure. It is best-effort in
// exactly the way authcore's is: a decoding error ends the scan, and the
// caller's subsequent checks decide the outcome on whatever was read. Only a
// missing Response root is reported here, because that is the one condition
// that makes the rest of the judgement unsound.
//
// The count looks at the root's IMMEDIATE children only — the same scope
// crewjam's findChildren uses, so this cannot reject a document the verifier
// would have read differently.
func scanShape(raw []byte) shape {
	var sh shape
	dec := xml.NewDecoder(bytes.NewReader(raw))
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch el := tok.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 {
				if el.Name.Local != "Response" {
					return sh
				}
				sh.rootIsResponse = true
				for _, a := range el.Attr {
					// Last match wins, mirroring authcore. Attribute names in
					// SAML are unprefixed, but matching on the local name keeps
					// a namespaced variant from slipping past the check.
					if a.Name.Local == destinationAttr {
						sh.hasDestination = true
						sh.destination = a.Value
					}
				}
			}
			if depth == 2 && el.Name.Space == nsAssertion &&
				(el.Name.Local == "Assertion" || el.Name.Local == "EncryptedAssertion") {
				sh.assertionCount++
			}
		case xml.EndElement:
			depth--
		}
	}
	return sh
}

// firstWeakAlgorithm reports the element name and value of the first
// disallowed SignatureMethod or DigestMethod found ANYWHERE in the document —
// the outer Response signature, the assertion's own signature, and every
// Reference digest — so a strong outer algorithm can never vouch for a weak
// inner one. It returns "" when everything found is allowed, including when the
// document declares no algorithm at all: an absent signature is a different
// failure, caught by the verifier.
//
// Matching is on the element's LOCAL name rather than its namespace (authcore
// does the same). A namespaced look-alike is therefore still scanned, which is
// the fail-closed direction.
func firstWeakAlgorithm(raw []byte) (element, algorithm string) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", ""
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		var allowed map[string]bool
		switch el.Name.Local {
		case signatureMethodTag:
			allowed = strongSignatureAlgorithms
		case digestMethodTag:
			allowed = strongDigestAlgorithms
		default:
			continue
		}
		for _, a := range el.Attr {
			if a.Name.Local == algorithmAttr && !allowed[a.Value] {
				return el.Name.Local, a.Value
			}
		}
	}
}
