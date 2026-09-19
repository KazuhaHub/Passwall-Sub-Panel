package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/samlkey"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
)

type fakeSAMLConfigRepo struct {
	cfg *config.SAMLConfig
	err error
}

func (f fakeSAMLConfigRepo) Load(context.Context) (*config.SAMLConfig, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.cfg, nil
}

func (f fakeSAMLConfigRepo) Save(context.Context, *config.SAMLConfig) error { return nil }

type fakeSettingsRepo struct{ ui ports.UISettings }

func (f fakeSettingsRepo) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return f.ui, nil
}

func (f fakeSettingsRepo) Save(context.Context, ports.UISettings) error { return nil }

func runPreflight(t *testing.T, repo ports.SAMLConfigRepo, ui ports.UISettings, svc *auth.SAMLService) (int, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/admin/settings/saml/preflight", nil)

	NewAdminSAMLHandler(repo, svc, fakeSettingsRepo{ui: ui}, nil).Preflight(c)
	return rec.Code, rec.Body.String()
}

// A service built from a disabled configuration performs no network I/O, which
// makes it the right stand-in for "the process is up but nothing is wired".
func disabledSAMLService(t *testing.T) *auth.SAMLService {
	t.Helper()
	svc, err := auth.NewSAML(&config.SAMLConfig{})
	if err != nil {
		t.Fatalf("NewSAML: %v", err)
	}
	return svc
}

func TestPreflightEndpoint_ReportsBothVerdictsSeparately(t *testing.T) {
	cert, key, err := samlkey.GenerateSelfSigned("https://panel.example.com/api/auth/saml/metadata")
	if err != nil {
		t.Fatalf("generate SP keypair: %v", err)
	}
	cfg := &config.SAMLConfig{
		Enabled: true,
		SP:      config.SPConf{EntityID: "https://panel.example.com/api/auth/saml/metadata", ACSURL: "https://panel.example.com/api/auth/saml/acs", CertPEM: cert, KeyPEM: key},
		IDP:     config.IDPConf{MetadataURL: "https://idp.example.com/saml/metadata"},
	}

	code, body := runPreflight(t, fakeSAMLConfigRepo{cfg: cfg},
		ports.UISettings{SubBaseURL: "https://panel.example.com"}, disabledSAMLService(t))
	if code != http.StatusOK {
		t.Fatalf("status = %d, body %s", code, body)
	}

	var rep auth.PreflightReport
	if err := json.Unmarshal([]byte(body), &rep); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if rep.SupportedTopology != auth.SupportedTopology {
		t.Errorf("supported_topology = %q", rep.SupportedTopology)
	}
	if rep.LoginURL == "" || rep.ACSURL == "" {
		t.Errorf("the report must carry the public URLs it judged: %+v", rep)
	}
	// The two verdicts answer different questions, and this is the case that
	// shows they are not one boolean twice: these stored values are fine, while
	// the process — a stand-in service with no provider and no stores — is not.
	if !rep.ConfigurationValid {
		t.Fatalf("a valid configuration was not reported valid: %+v", rep.Checks)
	}
	if rep.RuntimeReady {
		t.Fatal("the stand-in service has no provider and no stores, so runtime_ready must be false")
	}
}

func TestPreflightEndpoint_NamesTheFailedCheck(t *testing.T) {
	cfg := &config.SAMLConfig{
		Enabled: true,
		SP:      config.SPConf{EntityID: "https://panel.example.com/api/auth/saml/metadata", ACSURL: "https://panel.example.com/api/auth/saml/acs"},
		IDP:     config.IDPConf{MetadataURL: "https://idp.example.com/saml/metadata"},
	} // no SP key material

	code, body := runPreflight(t, fakeSAMLConfigRepo{cfg: cfg},
		ports.UISettings{SubBaseURL: "https://panel.example.com"}, disabledSAMLService(t))
	if code != http.StatusOK {
		t.Fatalf("status = %d, body %s", code, body)
	}

	var rep auth.PreflightReport
	if err := json.Unmarshal([]byte(body), &rep); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if rep.ConfigurationValid {
		t.Fatal("a configuration with no SP key material was reported valid")
	}
	failed := rep.FailedChecks()
	if len(failed) == 0 {
		t.Fatal("the report names no failed check")
	}
	found := false
	for _, name := range failed {
		if name == "sp_keypair" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the missing key material was not identified: %v", failed)
	}
}

// If the stored configuration cannot be read, readiness is unknown — and "valid"
// is the one wrong answer, because it sends the operator looking somewhere else.
func TestPreflightEndpoint_UnreadableConfigurationIsNotGreen(t *testing.T) {
	code, body := runPreflight(t, fakeSAMLConfigRepo{err: errors.New("db down")},
		ports.UISettings{}, disabledSAMLService(t))

	if code == http.StatusOK {
		t.Fatalf("an unreadable configuration answered 200: %s", body)
	}
	if strings.Contains(body, `"configuration_valid":true`) {
		t.Fatalf("an unreadable configuration was reported valid: %s", body)
	}
}

// The endpoint is a diagnostic, not a disclosure: it must not echo key material.
func TestPreflightEndpoint_ReturnsNoSecrets(t *testing.T) {
	const secret = "-----BEGIN PRIVATE KEY-----super-secret-key-material-----END PRIVATE KEY-----"
	cfg := &config.SAMLConfig{
		Enabled: true,
		SP: config.SPConf{
			EntityID: "https://panel.example.com/api/auth/saml/metadata",
			ACSURL:   "https://panel.example.com/api/auth/saml/acs",
			CertPEM:  secret,
			KeyPEM:   secret,
		},
		IDP: config.IDPConf{MetadataURL: "https://idp.example.com/saml/metadata"},
	}

	_, body := runPreflight(t, fakeSAMLConfigRepo{cfg: cfg},
		ports.UISettings{SubBaseURL: "https://panel.example.com"}, disabledSAMLService(t))

	if strings.Contains(body, secret) || strings.Contains(body, "super-secret") {
		t.Fatalf("the preflight response echoed key material: %s", body)
	}
}
