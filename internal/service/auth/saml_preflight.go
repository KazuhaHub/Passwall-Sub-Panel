package auth

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/url"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/panelpath"
)

// Preflight statuses. A CLOSED set, because they are part of the contract the
// admin UI and the release notes read:
//
//	passed      — checked, and it holds
//	failed      — checked, and a known element is missing or wrong
//	not_checked — not checked, and cannot be from here
//
// The distinction between the last two is the whole point. Reporting a known
// missing certificate as "not checked" would tell an operator to go looking;
// reporting an unverifiable browser behaviour as "passed" would tell them to
// stop looking.
const (
	PreflightPassed     = "passed"
	PreflightFailed     = "failed"
	PreflightNotChecked = "not_checked"
)

// SupportedTopology is what this release actually supports, and it rides in the
// preflight response rather than in a footnote so that "configuration_valid:
// true" cannot be read as "safe to run several instances" (ADR 0036 D8).
const SupportedTopology = "single_instance"

// The public routes every derived URL is built from.
const (
	SAMLLoginPath = "/api/auth/saml/login"
	SAMLACSPath   = "/api/auth/saml/acs"
)

// PreflightCheck is one named, closed-set outcome.
type PreflightCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// PreflightReport is what the admin self-check returns, and what the save path
// inspects before failing a request.
type PreflightReport struct {
	// ConfigurationValid means the STATIC checks hold. It says nothing about
	// whether the runtime can serve requests — that is RuntimeReady.
	ConfigurationValid bool `json:"configuration_valid"`
	// RuntimeReady means the provider is built and both durable stores are wired.
	RuntimeReady bool   `json:"runtime_ready"`
	LoginURL     string `json:"login_url,omitempty"`
	ACSURL       string `json:"acs_url,omitempty"`
	// SupportedTopology is always populated; see the constant.
	SupportedTopology string           `json:"supported_topology"`
	Checks            []PreflightCheck `json:"checks"`
}

// FailedChecks returns the names of every failed check, in report order.
func (r PreflightReport) FailedChecks() []string {
	var out []string
	for _, c := range r.Checks {
		if c.Status == PreflightFailed {
			out = append(out, c.Name)
		}
	}
	return out
}

// FailureDetail returns the first failure's detail, or "" when nothing failed.
// The save path quotes this rather than the whole report: an admin should get the
// one thing to fix, not a wall of passing checks.
func (r PreflightReport) FailureDetail() string {
	for _, c := range r.Checks {
		if c.Status == PreflightFailed {
			if c.Detail != "" {
				return c.Detail
			}
			return c.Name
		}
	}
	return ""
}

// PreflightInput is everything the static checks need. PublicBase comes from the
// CONFIGURED public base URL, deliberately not from the request: an admin
// reaching the panel by some other name must not change what the validation
// concludes about the entry point their users will actually use.
type PreflightInput struct {
	Config     *config.SAMLConfig
	PanelPath  string
	PublicBase string
}

// StaticPreflight evaluates everything the configuration alone can decide.
//
// It performs no I/O beyond parsing what it is given: no metadata fetch, no
// database access, no write of any kind. That is what lets the same function
// back both the admin endpoint and the save-time gate.
func StaticPreflight(in PreflightInput) PreflightReport {
	rep := PreflightReport{SupportedTopology: SupportedTopology}

	pass := func(name string) {
		rep.Checks = append(rep.Checks, PreflightCheck{Name: name, Status: PreflightPassed})
	}
	fail := func(name, detail string) {
		rep.Checks = append(rep.Checks, PreflightCheck{Name: name, Status: PreflightFailed, Detail: detail})
	}
	unchecked := func(name, detail string) {
		rep.Checks = append(rep.Checks, PreflightCheck{Name: name, Status: PreflightNotChecked, Detail: detail})
	}

	cfg := in.Config
	base := strings.TrimSpace(in.PublicBase)
	if base == "" {
		fail("public_base", "the panel's public base URL (sub_base_url) is not configured, so the sign-in and callback URLs cannot be derived")
	} else {
		pass("public_base")
	}

	rep.LoginURL = panelpath.PanelURL(base, in.PanelPath, SAMLLoginPath)
	acsURL := ""
	if cfg != nil {
		acsURL = cfg.SP.ACSURL
	}
	rep.ACSURL = acsURL

	login, loginErr := parseAbsoluteURL(rep.LoginURL)
	acs, acsErr := parseAbsoluteURL(acsURL)

	switch {
	case loginErr != nil:
		fail("login_url", "the public sign-in URL could not be derived: "+loginErr.Error())
	case acsErr != nil:
		fail("login_url", "the configured ACS URL is not an absolute URL")
	default:
		pass("login_url")
	}

	switch {
	case loginErr != nil || acsErr != nil:
		fail("https", "both the panel's public base URL and the ACS URL must be HTTPS")
	case !strings.EqualFold(login.Scheme, "https") || !strings.EqualFold(acs.Scheme, "https"):
		fail("https", "both the panel's public base URL and the ACS URL must be HTTPS; the browser binding cookie is Secure and SameSite=None, which an HTTP origin cannot use")
	default:
		pass("https")
	}

	switch {
	case loginErr != nil || acsErr != nil:
		fail("same_origin", "the sign-in URL and the ACS URL must share an origin")
	case !strings.EqualFold(login.Host, acs.Host):
		fail("same_origin", "the sign-in URL and the ACS URL are on different hosts; a host-only binding cookie is not sent to another host, so this login could never complete")
	default:
		pass("same_origin")
	}

	switch {
	case acsErr != nil:
		fail("acs_url_shape", "the configured ACS URL is not an absolute URL")
	case acs.User != nil:
		fail("acs_url_shape", "the ACS URL must not carry user information")
	case acs.Fragment != "":
		fail("acs_url_shape", "the ACS URL must not carry a fragment")
	case acs.Path == "":
		fail("acs_url_shape", "the ACS URL must have a path")
	default:
		pass("acs_url_shape")
	}

	if cfg == nil {
		fail("sp_entity_id", "no SAML configuration")
		fail("sp_keypair", "no SAML configuration")
		fail("idp_metadata_url", "no SAML configuration")
	} else {
		if strings.TrimSpace(cfg.SP.EntityID) == "" {
			fail("sp_entity_id", "the SP entity ID is empty")
		} else {
			pass("sp_entity_id")
		}
		if detail := spKeypairProblem(cfg); detail != "" {
			fail("sp_keypair", detail)
		} else {
			pass("sp_keypair")
		}
		if detail := metadataURLProblem(cfg.IDP.MetadataURL); detail != "" {
			fail("idp_metadata_url", detail)
		} else {
			pass("idp_metadata_url")
		}
	}

	rep.ConfigurationValid = len(rep.FailedChecks()) == 0

	// The two things no static check can establish. They are listed rather than
	// omitted so a green report cannot be mistaken for a complete one.
	unchecked("database_write", "a read-only database surfaces at the first login; this check does not write, and must not, to find out")
	unchecked("browser_cookie", "whether a real browser stores and returns the binding cookie needs a browser; the deployment checklist's browser verification covers it")

	return rep
}

// RuntimeChecks appends the checks that need the live service: whether the
// provider was built and whether both durable stores are wired. They are separate
// from ConfigurationValid because an operator fixing a broken configuration and
// an operator investigating a broken deployment are looking at different problems.
func (s *SAMLService) RuntimeChecks() []PreflightCheck {
	var out []PreflightCheck

	snap := s.snapshot()
	if snap != nil && snap.sp != nil {
		out = append(out, PreflightCheck{Name: "provider_built", Status: PreflightPassed})
	} else {
		out = append(out, PreflightCheck{
			Name: "provider_built", Status: PreflightFailed,
			Detail: "no usable provider is built; the IdP metadata fetch or the SP key material failed at boot — see the panel log",
		})
	}

	s.mu.RLock()
	replay, requests := s.replayStore, s.requestStore
	s.mu.RUnlock()
	for _, c := range []struct {
		name  string
		wired bool
		what  string
	}{
		{"replay_store", replay != nil, "the durable assertion-replay set"},
		{"request_store", requests != nil, "the durable login-request set"},
	} {
		if c.wired {
			out = append(out, PreflightCheck{Name: c.name, Status: PreflightPassed})
			continue
		}
		out = append(out, PreflightCheck{
			Name: c.name, Status: PreflightFailed,
			Detail: fmt.Sprintf("%s is not wired, so every SAML login would be refused", c.what),
		})
	}
	return out
}

// Preflight is the full report: the static checks plus the live runtime ones.
func (s *SAMLService) Preflight(in PreflightInput) PreflightReport {
	rep := StaticPreflight(in)
	rep.Checks = append(rep.Checks, s.RuntimeChecks()...)

	rep.RuntimeReady = true
	for _, c := range rep.Checks {
		if c.Status == PreflightFailed && isRuntimeCheck(c.Name) {
			rep.RuntimeReady = false
			break
		}
	}
	return rep
}

// runtimeCheckNames is the boundary between the two verdicts. It is a set rather
// than a prefix test so that adding a check forces a decision about which verdict
// it belongs to.
var runtimeCheckNames = map[string]bool{
	"provider_built": true,
	"replay_store":   true,
	"request_store":  true,
}

func isRuntimeCheck(name string) bool { return runtimeCheckNames[name] }

func parseAbsoluteURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("no URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("not an absolute URL")
	}
	return u, nil
}

// spKeypairProblem reports why the SP key material is unusable, or "" if it is
// fine. The same parsing the provider build does, so the check cannot disagree
// with the thing it is predicting.
func spKeypairProblem(cfg *config.SAMLConfig) string {
	certPEM := strings.TrimSpace(cfg.SP.CertPEM)
	keyPEM := strings.TrimSpace(cfg.SP.KeyPEM)
	if certPEM == "" || keyPEM == "" {
		return "the SP certificate and private key must both be provided"
	}
	keyPair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return "the SP certificate and private key do not parse or do not match: " + err.Error()
	}
	if len(keyPair.Certificate) == 0 {
		return "the SP certificate contains no certificates"
	}
	if _, err := x509.ParseCertificate(keyPair.Certificate[0]); err != nil {
		return "the SP certificate does not parse: " + err.Error()
	}
	return ""
}

// metadataURLProblem reports why the IdP metadata URL is unusable, or "".
func metadataURLProblem(raw string) string {
	u, err := parseAbsoluteURL(raw)
	if err != nil {
		return "the IdP metadata URL must be an absolute URL"
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return "the IdP metadata URL must be HTTPS"
	}
	return ""
}
