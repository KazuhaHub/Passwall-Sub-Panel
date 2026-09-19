package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"html"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crewjam/saml"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// This file builds a real, fully-functional crewjam/saml IdentityProvider so
// "a legitimate assertion must validate" and "a tampered one must not" are
// exercised against genuine XML-DSig signatures rather than hand-typed fixtures.
//
// Two things about it are load-bearing:
//
//   - The IdP signs with rsa-sha256 explicitly. crewjam defaults to rsa-sha1,
//     which the pre-check refuses (ADR 0036 D3), so leaving it unset would make
//     every "must accept" test fail for a reason unrelated to its subject.
//   - The SP is constructed DIRECTLY rather than through NewSAML/buildSP: the
//     real constructor fetches IdP metadata over safehttp, which refuses
//     loopback addresses by design, so a local test IdP could never be reached
//     through it. Injecting s.sp is what keeps this harness off the network.
//
// All hostnames use RFC 2606 reserved domains and nothing here refers to a real
// IdP or SP.

const testSPACSURL = "https://panel.example.com/panel/api/auth/saml/acs"
const testIDPSSOURL = "https://idp.example.com/saml/sso"

// testKeyPair returns a fresh RSA-2048 self-signed keypair for cn.
func testKeyPair(t *testing.T, cn string) (*rsa.PrivateKey, *x509.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("generate serial: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return key, cert
}

// testIdP wraps a crewjam IdentityProvider configured for one fixed SP, issuing
// a fixed session on demand. Fields are exported to the test so a case can vary
// exactly one thing (the name ID, the groups, the signature algorithm).
type testIdP struct {
	idp        *saml.IdentityProvider
	entityID   string
	spEntityID string
	spACSURL   string
	spCert     *x509.Certificate

	nameID    string
	nameIDFmt string
	userEmail string
	groups    []string
	// additionalAttributes are emitted as extra <Attribute> elements, which is
	// how the attribute-mapping cases get a claim to look up.
	additionalAttributes map[string][]string
}

func newTestIdP(t *testing.T, spEntityID, spACSURL string, spCert *x509.Certificate) *testIdP {
	t.Helper()
	key, cert := testKeyPair(t, "idp.example.com")
	idpEntityID := "https://idp.example.com/saml/metadata"

	tp := &testIdP{
		entityID:   idpEntityID,
		spEntityID: spEntityID,
		spACSURL:   spACSURL,
		spCert:     spCert,
		nameID:     "subject-001@idp.example.com",
		nameIDFmt:  string(saml.EmailAddressNameIDFormat),
		userEmail:  "subject-001@idp.example.com",
		groups:     []string{"engineering", "sso-test"},
	}
	tp.idp = &saml.IdentityProvider{
		Key:                     key,
		Certificate:             cert,
		MetadataURL:             mustParseURL(t, idpEntityID),
		SSOURL:                  mustParseURL(t, testIDPSSOURL),
		SignatureMethod:         sha256RSASignatureMethod,
		ServiceProviderProvider: tp,
		SessionProvider:         tp,
	}
	return tp
}

// Signature/digest URIs, spelled out rather than imported from goxmldsig: the
// values are fixed by the W3C recommendations, and importing that module here
// would turn an indirect dependency into a direct one for two string constants.
const (
	sha256RSASignatureMethod = "http://www.w3.org/2001/04/xmldsig-more#rsa-sha256"
	rsaSHA1SignatureMethod   = "http://www.w3.org/2000/09/xmldsig#rsa-sha1"
)

func mustParseURL(t *testing.T, raw string) url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url %q: %v", raw, err)
	}
	return *u
}

// GetServiceProvider implements saml.ServiceProviderProvider.
func (tp *testIdP) GetServiceProvider(_ *http.Request, serviceProviderID string) (*saml.EntityDescriptor, error) {
	if serviceProviderID != tp.spEntityID {
		return nil, os.ErrNotExist
	}
	isDefault := true
	return &saml.EntityDescriptor{
		EntityID: tp.spEntityID,
		SPSSODescriptors: []saml.SPSSODescriptor{
			{
				SSODescriptor: saml.SSODescriptor{
					RoleDescriptor: saml.RoleDescriptor{
						ProtocolSupportEnumeration: "urn:oasis:names:tc:SAML:2.0:protocol",
						KeyDescriptors: []saml.KeyDescriptor{
							{
								Use: "signing",
								KeyInfo: saml.KeyInfo{
									X509Data: saml.X509Data{
										X509Certificates: []saml.X509Certificate{
											{Data: base64.StdEncoding.EncodeToString(tp.spCert.Raw)},
										},
									},
								},
							},
						},
					},
				},
				AssertionConsumerServices: []saml.IndexedEndpoint{
					{Binding: saml.HTTPPostBinding, Location: tp.spACSURL, Index: 0, IsDefault: &isDefault},
				},
			},
		},
	}, nil
}

// GetSession implements saml.SessionProvider. There is no login form here: every
// request gets the same canned session, which is what a case varies when it
// wants a different NameID or group set.
//
// The claims the panel maps (UPN / email / display name / groups) are emitted as
// CustomAttributes rather than through crewjam's built-in fields, because those
// carry OID-style Names that no admin would type into AttributeMapping. Keys are
// sorted so the generated XML is byte-stable across runs.
func (tp *testIdP) GetSession(_ http.ResponseWriter, _ *http.Request, _ *saml.IdpAuthnRequest) *saml.Session {
	s := &saml.Session{
		ID:           "session-001",
		CreateTime:   time.Now(),
		ExpireTime:   time.Now().Add(time.Hour),
		Index:        "session-index-001",
		NameID:       tp.nameID,
		NameIDFormat: tp.nameIDFmt,
		UserEmail:    tp.userEmail,
		Groups:       tp.groups,
	}
	names := make([]string, 0, len(tp.additionalAttributes))
	for name := range tp.additionalAttributes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		attr := saml.Attribute{Name: name}
		for _, v := range tp.additionalAttributes[name] {
			attr.Values = append(attr.Values, saml.AttributeValue{Value: v})
		}
		s.CustomAttributes = append(s.CustomAttributes, attr)
	}
	return s
}

// testSPMaterial is the SP half of the pair.
type testSPMaterial struct {
	entityID string
	acsURL   string
	key      *rsa.PrivateKey
	cert     *x509.Certificate
}

func newTestSPMaterial(t *testing.T) testSPMaterial {
	t.Helper()
	key, cert := testKeyPair(t, "panel.example.com")
	return testSPMaterial{
		entityID: "https://panel.example.com/saml/metadata",
		acsURL:   testSPACSURL,
		key:      key,
		cert:     cert,
	}
}

// memReplayStore is a real, in-memory SAMLReplayRepo for tests whose subject
// includes replay detection. The fakeReplayStore stub in
// saml_replay_store_test.go deliberately returns a fixed answer, so using it
// here would make every replay assertion vacuously pass.
type memReplayStore struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func (m *memReplayStore) SeenOrAdd(_ context.Context, id string, expiresAt, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seen == nil {
		m.seen = map[string]time.Time{}
	}
	if exp, ok := m.seen[id]; ok && now.Before(exp) {
		return true, nil
	}
	m.seen[id] = expiresAt
	return false, nil
}

func (m *memReplayStore) DeleteExpired(context.Context, time.Time) (int64, error) { return 0, nil }

// memRequestStore is an in-memory ports.SAMLRequestRepo with the same claim
// semantics as the SQL one: a single conditional claim, and a failed claim that
// leaves the row untouched. The real repo's atomicity is proven against
// SQLite/MySQL/PostgreSQL in the sqlstore package; this exists so the login flow
// can be driven end to end without a database.
type memRequestStore struct {
	mu      sync.Mutex
	records map[string]*domain.SAMLLoginRequest
}

func (m *memRequestStore) Create(_ context.Context, req *domain.SAMLLoginRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.records == nil {
		m.records = map[string]*domain.SAMLLoginRequest{}
	}
	if _, exists := m.records[req.TokenHash]; exists {
		return errors.New("duplicate token hash")
	}
	cp := *req
	m.records[req.TokenHash] = &cp
	return nil
}

func (m *memRequestStore) Consume(_ context.Context, tokenHash, browserHash, configDigest string, now time.Time) (*domain.SAMLLoginRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.records[tokenHash]
	if !ok {
		return nil, domain.ErrSAMLRequestInvalid
	}
	if rec.BrowserHash != browserHash || rec.ConfigDigest != configDigest ||
		rec.ConsumedAt != nil || !now.Before(rec.ExpiresAt) {
		return nil, domain.ErrSAMLRequestInvalid
	}
	consumed := now
	rec.ConsumedAt = &consumed
	cp := *rec
	return &cp, nil
}

func (m *memRequestStore) DeleteExpired(_ context.Context, now time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for k, rec := range m.records {
		if !now.Before(rec.ExpiresAt) {
			delete(m.records, k)
			n++
		}
	}
	return n, nil
}

// testSAML wires a service pointed at a test IdP without touching the network.
// The returned IdP can be mutated before a response is produced.
func testSAML(t *testing.T) (*SAMLService, *testIdP) {
	t.Helper()
	sp := newTestSPMaterial(t)
	idp := newTestIdP(t, sp.entityID, sp.acsURL, sp.cert)
	svc := &SAMLService{
		cfg: &config.SAMLConfig{
			Enabled:          true,
			SP:               config.SPConf{EntityID: sp.entityID, ACSURL: sp.acsURL},
			AttributeMapping: config.SAMLAttributeMap{UPN: "upn", Email: "email", DisplayName: "displayName", Groups: "groups"},
		},
		sp: &saml.ServiceProvider{
			EntityID:          sp.entityID,
			Key:               sp.key,
			Certificate:       sp.cert,
			AcsURL:            mustParseURL(t, sp.acsURL),
			IDPMetadata:       idp.idp.Metadata(),
			AuthnNameIDFormat: saml.UnspecifiedNameIDFormat,
		},
	}
	svc.SetReplayStore(&memReplayStore{})
	svc.SetSAMLRequestStore(&memRequestStore{})
	return svc, idp
}

// authnRequest builds a real AuthnRequest the way the Login handler does, so the
// Response the IdP produces carries a genuine InResponseTo, and returns both the
// redirect URL and that request ID. Tests that expect to reach verification must
// pass the ID as a possible request ID: crewjam refuses a Response whose
// InResponseTo is not in the list, which is exactly the check the current
// RelayState-only binding leans on.
func (s *SAMLService) authnRequest(t *testing.T, relayState string) (redirectURL, requestID string) {
	t.Helper()
	s.mu.RLock()
	sp := s.sp
	s.mu.RUnlock()
	req, err := sp.MakeAuthenticationRequest(testIDPSSOURL, saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	if err != nil {
		t.Fatalf("make authn request: %v", err)
	}
	u, err := req.Redirect(relayState, sp)
	if err != nil {
		t.Fatalf("redirect: %v", err)
	}
	return u.String(), req.ID
}

// signedResponse drives the IdP for the given AuthnRequest URL and returns the
// raw, decoded <Response> XML — a genuinely signed document.
func (tp *testIdP) signedResponse(t *testing.T, authnRequestURL string) []byte {
	t.Helper()
	u, err := url.Parse(authnRequestURL)
	if err != nil {
		t.Fatalf("parse authn request url: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, testIDPSSOURL+"?"+u.RawQuery, nil)
	rec := httptest.NewRecorder()
	tp.idp.ServeSSO(rec, req)
	if rec.Code != http.StatusOK && rec.Code != 0 {
		t.Fatalf("ServeSSO: status %d body %s", rec.Code, rec.Body.String())
	}
	return extractSAMLResponse(t, rec.Body.String())
}

// signedLogin produces a real, validly signed Response for a fresh
// AuthnRequest and returns it with the request ID it answers. Tests that expect
// to reach verification must pass that ID as the allowed request ID — crewjam
// refuses a Response whose InResponseTo is not in the list.
func signedLogin(t *testing.T, svc *SAMLService, idp *testIdP) (raw []byte, requestID string) {
	t.Helper()
	redirectURL, requestID := svc.authnRequest(t, "relaystate")
	return idp.signedResponse(t, redirectURL), requestID
}

// extractSAMLResponse pulls the SAMLResponse hidden-input value out of
// crewjam's auto-submit form and base64-decodes it.
func extractSAMLResponse(t *testing.T, page string) []byte {
	t.Helper()
	const marker = `name="SAMLResponse" value="`
	i := strings.Index(page, marker)
	if i < 0 {
		t.Fatalf("no SAMLResponse field in IdP output: %s", page)
	}
	rest := page[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("unterminated SAMLResponse field: %s", page)
	}
	// The form renders through html/template, which entity-escapes the value
	// (notably '+' -> "&#43;"), so undo that before base64-decoding.
	b64 := html.UnescapeString(rest[:j])
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decode SAMLResponse: %v", err)
	}
	return decoded
}

// acsRequest builds the ACS POST an IdP would send: the response as a
// form-encoded SAMLResponse, optionally MIME-wrapped the way Entra wraps it.
func acsRequest(t *testing.T, responseXML []byte, relayState string, wrap bool) *http.Request {
	t.Helper()
	b64 := base64.StdEncoding.EncodeToString(responseXML)
	if wrap {
		var wrapped strings.Builder
		for i := 0; i < len(b64); i += 76 {
			end := i + 76
			if end > len(b64) {
				end = len(b64)
			}
			wrapped.WriteString(b64[i:end])
			wrapped.WriteString("\n")
		}
		b64 = wrapped.String()
	}
	form := url.Values{}
	form.Set("SAMLResponse", b64)
	if relayState != "" {
		form.Set("RelayState", relayState)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/saml/acs", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}
