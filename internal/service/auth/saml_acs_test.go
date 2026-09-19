package auth

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"errors"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/crewjam/saml"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/samlguard"
)

// These tests drive ParseACSResponse against a genuinely signed Response from a
// real crewjam IdentityProvider (saml_testidp_test.go), which is what the
// acceptance matrix means by exercising the real path rather than a fixture.

// claimsFor wires the IdP to emit the four claims the panel maps.
func claimsFor(idp *testIdP, upn, email, display, groups string) {
	idp.additionalAttributes = map[string][]string{
		"upn":         {upn},
		"email":       {email},
		"displayName": {display},
		"groups":      {groups, "sso-test"},
	}
}

// TestParseACSResponse_AcceptsARealSignedResponse is S01: a valid signed
// response completes with the claims mapped onto the panel's fields.
func TestParseACSResponse_AcceptsARealSignedResponse(t *testing.T) {
	svc, idp := testSAML(t)
	claimsFor(idp, "alice@corp.example", "alice@corp.example", "Alice Example", "engineering")

	raw, requestID := signedLogin(t, svc, idp)
	got, err := svc.ParseACSResponse(acsRequest(t, raw, "relaystate", false), []string{requestID})
	if err != nil {
		t.Fatalf("a genuinely signed response was refused: %v", err)
	}
	checks := []struct{ field, have, want string }{
		{"Subject", got.Subject, idp.nameID},
		{"UPN", got.UPN, "alice@corp.example"},
		{"Email", got.Email, "alice@corp.example"},
		{"DisplayName", got.DisplayName, "Alice Example"},
	}
	for _, c := range checks {
		if c.have != c.want {
			t.Errorf("%s = %q, want %q", c.field, c.have, c.want)
		}
	}
	if len(got.Groups) != 2 || got.Groups[0] != "engineering" {
		t.Errorf("Groups = %v, want [engineering sso-test]", got.Groups)
	}
	// Role rules and group rules read arbitrary IdP claims out of this map, so
	// it must carry the raw names the IdP actually sent.
	if len(got.Attributes["upn"]) != 1 || got.Attributes["upn"][0] != "alice@corp.example" {
		t.Errorf("raw attribute bag = %v", got.Attributes)
	}
}

// S02: a response altered in flight must not validate. The change is inside the
// signed content, so this is decided by signature verification, not the guard.
func TestParseACSResponse_RejectsTamperedResponse(t *testing.T) {
	svc, idp := testSAML(t)
	claimsFor(idp, "alice@corp.example", "alice@corp.example", "Alice Example", "engineering")

	raw, requestID := signedLogin(t, svc, idp)
	tampered := bytes.Replace(raw, []byte("alice@corp.example"), []byte("aliceXcorp.example"), 1)
	if bytes.Equal(tampered, raw) {
		t.Fatal("tamper did not change the document; the test would be vacuous")
	}
	if _, err := svc.ParseACSResponse(acsRequest(t, tampered, "relaystate", false), []string{requestID}); err == nil {
		t.Fatal("a tampered response was accepted")
	}
}

// S02: the signature stays valid, but the assertion is addressed to a different
// SP. Audience validation is crewjam's, and this pins that it is actually in
// play rather than assumed.
func TestParseACSResponse_RejectsWrongAudience(t *testing.T) {
	svc, idp := testSAML(t)
	claimsFor(idp, "alice@corp.example", "alice@corp.example", "Alice Example", "engineering")

	raw, requestID := signedLogin(t, svc, idp)
	// The IdP addressed the assertion to the SP entity ID it was built with;
	// point the SP at a different one.
	svc.withProviderForTest(func(p *saml.ServiceProvider) {
		p.EntityID = "https://other.example.org/saml/metadata"
	})
	if _, err := svc.ParseACSResponse(acsRequest(t, raw, "relaystate", false), []string{requestID}); err == nil {
		t.Fatal("an assertion for a different audience was accepted")
	}
}

// S15: the response is genuine but answers a different request.
func TestParseACSResponse_RejectsWrongInResponseTo(t *testing.T) {
	svc, idp := testSAML(t)
	claimsFor(idp, "alice@corp.example", "alice@corp.example", "Alice Example", "engineering")

	raw, _ := signedLogin(t, svc, idp)
	if _, err := svc.ParseACSResponse(acsRequest(t, raw, "relaystate", false), []string{"some-other-request-id"}); err == nil {
		t.Fatal("a response with a mismatched InResponseTo was accepted")
	}
}

// S11 (single instance): the same response presented twice must be refused the
// second time, by the durable set.
func TestParseACSResponse_RejectsReplayedAssertion(t *testing.T) {
	svc, idp := testSAML(t)
	claimsFor(idp, "alice@corp.example", "alice@corp.example", "Alice Example", "engineering")

	raw, requestID := signedLogin(t, svc, idp)
	if _, err := svc.ParseACSResponse(acsRequest(t, raw, "relaystate", false), []string{requestID}); err != nil {
		t.Fatalf("first presentation refused: %v", err)
	}
	_, err := svc.ParseACSResponse(acsRequest(t, raw, "relaystate", false), []string{requestID})
	if !errors.Is(err, ErrSAMLAssertionReplayed) {
		t.Fatalf("second presentation = %v, want ErrSAMLAssertionReplayed", err)
	}
}

// S06: Entra wraps the base64 payload at 76 characters.
func TestParseACSResponse_AcceptsMimeWrappedBase64(t *testing.T) {
	svc, idp := testSAML(t)
	claimsFor(idp, "alice@corp.example", "alice@corp.example", "Alice Example", "engineering")

	raw, requestID := signedLogin(t, svc, idp)
	if _, err := svc.ParseACSResponse(acsRequest(t, raw, "relaystate", true), []string{requestID}); err != nil {
		t.Fatalf("a MIME-wrapped payload was refused: %v", err)
	}
}

// S04: an IdP still signing with rsa-sha1 is refused by the guard, on a
// genuinely signed document — the algorithm check is exercised against real
// signature XML rather than a string that merely looks like an algorithm URI.
func TestParseACSResponse_RejectsWeakSignatureAtRealEntryPoint(t *testing.T) {
	svc, idp := testSAML(t)
	claimsFor(idp, "alice@corp.example", "alice@corp.example", "Alice Example", "engineering")
	idp.idp.SignatureMethod = rsaSHA1SignatureMethod

	raw, requestID := signedLogin(t, svc, idp)
	_, err := svc.ParseACSResponse(acsRequest(t, raw, "relaystate", false), []string{requestID})
	if !errors.Is(err, samlguard.ErrWeakSignatureAlgorithm) {
		t.Fatalf("rsa-sha1 response = %v, want ErrWeakSignatureAlgorithm", err)
	}
}

// S03: a second assertion smuggled alongside a legitimately signed one is
// refused BEFORE verification — which is the point, since the first assertion's
// signature is valid and crewjam would have accepted it.
func TestParseACSResponse_RejectsTwoAssertionsAtRealEntryPoint(t *testing.T) {
	svc, idp := testSAML(t)
	claimsFor(idp, "alice@corp.example", "alice@corp.example", "Alice Example", "engineering")

	raw, requestID := signedLogin(t, svc, idp)
	doubled, ok := duplicateElement(raw, "Assertion")
	if !ok {
		t.Fatalf("could not find an Assertion element to duplicate in:\n%s", raw)
	}
	_, err := svc.ParseACSResponse(acsRequest(t, doubled, "relaystate", false), []string{requestID})
	if !errors.Is(err, samlguard.ErrTooManyAssertions) {
		t.Fatalf("two-assertion response = %v, want ErrTooManyAssertions", err)
	}
}

// S03: crewjam skips the Destination check when the Response is unsigned and the
// attribute is absent. The guard does not.
func TestParseACSResponse_RejectsMissingDestinationAtRealEntryPoint(t *testing.T) {
	svc, idp := testSAML(t)
	claimsFor(idp, "alice@corp.example", "alice@corp.example", "Alice Example", "engineering")

	raw, requestID := signedLogin(t, svc, idp)
	stripped, ok := stripAttribute(raw, "Destination")
	if !ok {
		t.Fatalf("no Destination attribute to strip in:\n%s", raw)
	}
	_, err := svc.ParseACSResponse(acsRequest(t, stripped, "relaystate", false), []string{requestID})
	if !errors.Is(err, samlguard.ErrMissingDestination) {
		t.Fatalf("Destination-less response = %v, want ErrMissingDestination", err)
	}
}

// S08: the configured UPN claim is absent. The login is refused with a message
// naming the missing claim — never silently completed from NameID or email.
func TestParseACSResponse_RejectsMissingUPNClaim(t *testing.T) {
	svc, idp := testSAML(t)
	// Emit every claim except the one configured as the UPN source.
	idp.additionalAttributes = map[string][]string{"email": {"alice@corp.example"}}

	raw, requestID := signedLogin(t, svc, idp)
	_, err := svc.ParseACSResponse(acsRequest(t, raw, "relaystate", false), []string{requestID})
	if err == nil {
		t.Fatal("a response with no UPN claim was accepted")
	}
	if !strings.Contains(err.Error(), "upn") {
		t.Fatalf("the refusal does not name the missing claim: %v", err)
	}
}

// S07: the SSO account key is the NameID, so a UPN rename in the IdP does not
// reroute the assertion to a different panel row.
func TestParseACSResponse_SubjectIsNameIDNotUPN(t *testing.T) {
	svc, idp := testSAML(t)
	idp.nameID = "stable-nameid-001"
	claimsFor(idp, "renamed@corp.example", "renamed@corp.example", "Renamed Person", "engineering")

	raw, requestID := signedLogin(t, svc, idp)
	got, err := svc.ParseACSResponse(acsRequest(t, raw, "relaystate", false), []string{requestID})
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	if got.Subject != "stable-nameid-001" {
		t.Fatalf("Subject = %q, want the NameID", got.Subject)
	}
	if got.UPN != "renamed@corp.example" {
		t.Fatalf("UPN = %q, want the configured claim's value", got.UPN)
	}
}

// The AuthnRequest must not force a transient NameID: Entra honours the
// SP-requested format over its own admin setting, so a transient request makes
// every login arrive with a fresh unlinkable hash and no account ever matches.
func TestAuthnRequest_DoesNotForceTransientNameID(t *testing.T) {
	svc, _ := testSAML(t)
	redirectURL, _ := svc.authnRequest(t, "relaystate")

	u, err := url.Parse(redirectURL)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	xml := inflateSAMLRequest(t, u.Query().Get("SAMLRequest"))
	if strings.Contains(xml, "nameid-format:transient") {
		t.Fatalf("AuthnRequest forces a transient NameID:\n%s", xml)
	}
}

// duplicateElement returns raw with one extra copy of the first occurrence of
// the named element. Used to build responses that are structurally hostile
// rather than cryptographically forged.
func duplicateElement(raw []byte, local string) ([]byte, bool) {
	open := []byte("<saml:" + local)
	closeTag := []byte("</saml:" + local + ">")
	start := bytes.Index(raw, open)
	end := bytes.Index(raw, closeTag)
	if start < 0 || end < 0 || end < start {
		return nil, false
	}
	end += len(closeTag)
	element := raw[start:end]
	out := make([]byte, 0, len(raw)+len(element))
	out = append(out, raw[:end]...)
	out = append(out, element...)
	out = append(out, raw[end:]...)
	return out, true
}

// stripAttribute removes the first name="value" attribute from a document,
// leaving an otherwise intact (now incorrectly signed) Response.
func stripAttribute(raw []byte, name string) ([]byte, bool) {
	open := []byte(" " + name + `="`)
	i := bytes.Index(raw, open)
	if i < 0 {
		return nil, false
	}
	rest := raw[i+len(open):]
	j := bytes.IndexByte(rest, '"')
	if j < 0 {
		return nil, false
	}
	end := i + len(open) + j + 1
	out := make([]byte, 0, len(raw))
	out = append(out, raw[:i]...)
	out = append(out, raw[end:]...)
	return out, true
}

// inflateSAMLRequest decodes the Redirect-binding query parameter: base64 of a
// raw DEFLATE stream.
func inflateSAMLRequest(t *testing.T, raw string) string {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("base64 decode SAMLRequest: %v", err)
	}
	out, err := io.ReadAll(flate.NewReader(bytes.NewReader(decoded)))
	if err != nil {
		t.Fatalf("inflate SAMLRequest: %v", err)
	}
	return string(out)
}
