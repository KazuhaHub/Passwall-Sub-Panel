package samlguard

import "testing"

// These tests pin the assertion-shape rules that stand in for the missing
// upstream fix to GO-2026-6909 (crewjam/saml ≤ v0.5.1, no patched release):
//
//   - an assertion with no SAML-namespace <Subject> reaches a nil dereference
//     in crewjam after the signature is checked (a missing <Issuer> only fails
//     the issuer comparison, since that field is a value; it is required too);
//   - crewjam's Subject.NameID field has no tag and NameID has no XMLName, so
//     encoding/xml fills it from a <NameID> in ANY namespace. A signed
//     assertion carrying a foreign-namespace NameID can therefore set the
//     Subject PSP keys SSO accounts on.
//
// Each case is a plaintext assertion; the encrypted case is the stated limit
// pinned at the bottom.

const (
	issuerEl  = `<saml:Issuer>https://idp.example.com</saml:Issuer>`
	nameIDEl  = `<saml:NameID>alice@corp.example</saml:NameID>`
	subjectEl = `<saml:Subject>` + nameIDEl + `</saml:Subject>`
)

// bareAssertion wraps inner in an Assertion WITHOUT the Issuer and Subject that
// plainAssertion supplies, so a test states exactly which children exist.
func bareAssertion(inner string) string {
	return sig(sha256RSA, sha256X) + `<saml:Assertion>` + inner + `</saml:Assertion>`
}

func TestCheck_AcceptsAssertionWithIssuerAndSubject(t *testing.T) {
	check(t, response(acsURL, bareAssertion(issuerEl+subjectEl)), nil)
}

func TestCheck_AcceptsSubjectWithoutNameID(t *testing.T) {
	// A Subject carrying only SubjectConfirmation is legal SAML. PSP refuses a
	// missing NameID later with an explanatory message, so the guard must not
	// pre-empt that with a structural one.
	check(t, response(acsURL, bareAssertion(issuerEl+
		`<saml:Subject><saml:SubjectConfirmation/></saml:Subject>`)), nil)
}

func TestCheck_RejectsAssertionWithoutIssuer(t *testing.T) {
	check(t, response(acsURL, bareAssertion(subjectEl)), ErrAssertionShape)
}

func TestCheck_RejectsForeignNamespaceIssuerOnly(t *testing.T) {
	// A look-alike Issuer in another namespace is not an Issuer to crewjam,
	// whose field is namespace-qualified.
	check(t, response(acsURL, bareAssertion(
		`<evil:Issuer xmlns:evil="urn:example:evil">https://idp.example.com</evil:Issuer>`+subjectEl)),
		ErrAssertionShape)
}

func TestCheck_RejectsAssertionWithoutSubject(t *testing.T) {
	check(t, response(acsURL, bareAssertion(issuerEl)), ErrAssertionShape)
}

func TestCheck_RejectsTwoIssuers(t *testing.T) {
	check(t, response(acsURL, bareAssertion(issuerEl+issuerEl+subjectEl)), ErrAssertionShape)
}

func TestCheck_RejectsTwoSubjects(t *testing.T) {
	check(t, response(acsURL, bareAssertion(issuerEl+subjectEl+subjectEl)), ErrAssertionShape)
}

func TestCheck_RejectsForeignNamespaceNameID(t *testing.T) {
	// The GO-2026-6909 spoof: no saml:NameID, a foreign one in its place.
	check(t, response(acsURL, bareAssertion(issuerEl+
		`<saml:Subject><evil:NameID xmlns:evil="urn:example:evil">admin@corp.example</evil:NameID></saml:Subject>`)),
		ErrAssertionShape)
}

func TestCheck_RejectsForeignNameIDBesideARealOne(t *testing.T) {
	// encoding/xml would fill the same pointer from both elements, so which one
	// wins is a decoder detail. Neither order is accepted.
	foreign := `<evil:NameID xmlns:evil="urn:example:evil">admin@corp.example</evil:NameID>`
	for name, inner := range map[string]string{
		"foreign first": foreign + nameIDEl,
		"foreign last":  nameIDEl + foreign,
	} {
		t.Run(name, func(t *testing.T) {
			check(t, response(acsURL, bareAssertion(issuerEl+`<saml:Subject>`+inner+`</saml:Subject>`)),
				ErrAssertionShape)
		})
	}
}

func TestCheck_RejectsUnprefixedNameIDInDefaultNamespace(t *testing.T) {
	// An unprefixed <NameID> with no default namespace has Space "" — still not
	// the SAML assertion namespace.
	check(t, response(acsURL, bareAssertion(issuerEl+`<saml:Subject><NameID>admin</NameID></saml:Subject>`)),
		ErrAssertionShape)
}

func TestCheck_RejectsTwoSAMLNameIDs(t *testing.T) {
	check(t, response(acsURL, bareAssertion(issuerEl+`<saml:Subject>`+nameIDEl+nameIDEl+`</saml:Subject>`)),
		ErrAssertionShape)
}

func TestCheck_IgnoresNameIDOutsideSubject(t *testing.T) {
	// Only <Subject>'s own NameID becomes Subject.NameID. A NameID elsewhere —
	// here inside an attribute value — is ordinary content.
	check(t, response(acsURL, bareAssertion(issuerEl+subjectEl+
		`<saml:AttributeStatement><saml:Attribute Name="x"><saml:AttributeValue>`+
		`<evil:NameID xmlns:evil="urn:example:evil">admin</evil:NameID>`+
		`</saml:AttributeValue></saml:Attribute></saml:AttributeStatement>`)), nil)
}

func TestCheck_AcceptsDefaultNamespaceAssertion(t *testing.T) {
	// Prefixes are arbitrary: an assertion that declares the SAML namespace as
	// its default namespace carries unprefixed children in that namespace.
	check(t, response(acsURL, sig(sha256RSA, sha256X)+
		`<Assertion xmlns="`+nsAssert+`"><Issuer>https://idp.example.com</Issuer>`+
		`<Subject><NameID>alice</NameID></Subject></Assertion>`), nil)
}

func TestCheck_EncryptedAssertionIsNotInspected(t *testing.T) {
	// Stated limit: the shape rules read raw XML and cannot see inside an
	// EncryptedAssertion. parseACSResponse recovers a panic from the
	// decrypted path instead; the NameID spoof needs a plaintext element the
	// IdP itself signed and encrypted, which this check cannot reach.
	check(t, response(acsURL, sig(sha256RSA, sha256X)+
		`<saml:EncryptedAssertion><x/></saml:EncryptedAssertion>`), nil)
}

// --- prefix rebinding -------------------------------------------------------
//
// crewjam does not decode the bytes this guard reads. Before xml.Unmarshal it
// re-serialises the assertion (elementToBytes): it collects each prefix's URI
// from every element in the subtree, last one wins, and writes that single
// binding onto the copy's root, overwriting the Assertion's own declaration. A
// prefix bound to two URIs in the signed document can therefore resolve to the
// SAML namespace in what crewjam decodes while it is foreign to this scan. A
// document that binds any prefix to more than one URI is refused outright.

func TestCheck_RejectsPrefixReboundToSAMLNamespace(t *testing.T) {
	// The reproduced bypass: x:Subject is foreign here, but x:Pad's local
	// declaration makes crewjam rebind x to SAML on the copy's root, so the
	// second Subject — and its NameID "admin" — is what crewjam decodes.
	check(t, response(acsURL, sig(sha256RSA, sha256X)+
		`<saml:Assertion xmlns:x="urn:example:evil">`+issuerEl+subjectEl+
		`<x:Subject><saml:NameID>admin@corp.example</saml:NameID></x:Subject>`+
		`<x:Pad xmlns:x="`+nsAssert+`"/>`+
		`</saml:Assertion>`), ErrNamespaceRebound)
}

func TestCheck_RejectsPrefixReboundAnywhereInTheDocument(t *testing.T) {
	// Fail closed on the whole document rather than reasoning about which
	// subtree crewjam copies: no IdP needs one prefix for two namespaces.
	check(t, response(acsURL, `<samlp:Extensions xmlns:saml="urn:example:evil"/>`+strongBody()),
		ErrNamespaceRebound)
}

func TestCheck_AcceptsARepeatedIdenticalDeclaration(t *testing.T) {
	// IdPs commonly re-declare saml/ds on the Assertion with the same URI the
	// Response already bound; that is one binding, not two.
	check(t, response(acsURL, sig(sha256RSA, sha256X)+
		`<saml:Assertion xmlns:saml="`+nsAssert+`" xmlns:ds="`+nsDSig+`">`+issuerEl+subjectEl+
		sig(sha256RSA, sha256X)+`</saml:Assertion>`), nil)
}
