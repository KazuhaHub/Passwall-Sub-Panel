package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
)

// This validator's whole value is the distance it removes. Every condition it
// catches would otherwise surface at a login — a different person, later, with
// an error that names none of this. So each test states the login-time symptom
// it is replacing, not just the return value.

type slugGroupRepo struct {
	ports.GroupRepo
	known map[string]bool
	calls int
}

func (r *slugGroupRepo) GetBySlug(_ context.Context, slug string) (*domain.Group, error) {
	r.calls++
	if r.known[slug] {
		return &domain.Group{Slug: slug}, nil
	}
	return nil, domain.ErrNotFound
}

func knownGroups(slugs ...string) *slugGroupRepo {
	m := map[string]bool{}
	for _, s := range slugs {
		m[s] = true
	}
	return &slugGroupRepo{known: m}
}

func TestValidateSSOGroupRules_AcceptsResolvableRules(t *testing.T) {
	rules := []config.SSOGroupRule{
		{Attribute: "", Value: "idp-vip", Group: "vip"},
		{Attribute: "department", Value: "finance", Group: "staff"},
	}
	if err := validateSSOGroupRules(context.Background(), knownGroups("vip", "staff"), rules); err != nil {
		t.Fatalf("valid rules rejected: %v", err)
	}
}

// Without this the admin saves happily and finds out when a new principal
// cannot be provisioned at all — an error on somebody else's login screen.
func TestValidateSSOGroupRules_RejectsUnknownSlug(t *testing.T) {
	rules := []config.SSOGroupRule{{Attribute: "", Value: "idp-vip", Group: "typo"}}
	err := validateSSOGroupRules(context.Background(), knownGroups("vip"), rules)
	if err == nil {
		t.Fatal("a rule naming a nonexistent group must not save")
	}
	// The message has to carry both the row and the slug: a rule list is
	// edited as a table, and "one of these is wrong" is not actionable.
	if !strings.Contains(err.Error(), "typo") || !strings.Contains(err.Error(), "1") {
		t.Fatalf("error must name the offending row and slug, got %q", err)
	}
}

// A blank value is the guard the matcher already applies, surfaced as a
// message. Otherwise the rule saves, never fires, and looks identical to a
// directory that is not sending the claim.
func TestValidateSSOGroupRules_RejectsBlankValue(t *testing.T) {
	rules := []config.SSOGroupRule{{Attribute: "", Value: "  ", Group: "vip"}}
	if err := validateSSOGroupRules(context.Background(), knownGroups("vip"), rules); err == nil {
		t.Fatal("a rule with no value can never match and must not save")
	}
}

// A half-written row is skipped by the matcher, which silently shortens the
// rule set. Refusing the save is how the admin learns the row is unfinished.
func TestValidateSSOGroupRules_RejectsBlankGroup(t *testing.T) {
	rules := []config.SSOGroupRule{{Attribute: "", Value: "idp-vip", Group: ""}}
	if err := validateSSOGroupRules(context.Background(), knownGroups("vip"), rules); err == nil {
		t.Fatal("a rule with no group must not save")
	}
}

// An empty rule set is the state every deployment is in before using the
// feature; validating it must not require a group repository or a round trip.
func TestValidateSSOGroupRules_EmptyIsFineWithoutARepo(t *testing.T) {
	if err := validateSSOGroupRules(context.Background(), nil, nil); err != nil {
		t.Fatalf("no rules should validate trivially: %v", err)
	}
}

// If the repo is missing but rules exist, the check cannot be performed. It
// must say so rather than pass: a guarantee that silently downgrades to
// "no guarantee" depending on wiring is worse than none, because the admin
// still believes the form checked.
func TestValidateSSOGroupRules_MissingRepoIsAnErrorNotASkip(t *testing.T) {
	rules := []config.SSOGroupRule{{Attribute: "", Value: "idp-vip", Group: "vip"}}
	if err := validateSSOGroupRules(context.Background(), nil, rules); err == nil {
		t.Fatal("unvalidatable rules must not be accepted as valid")
	}
}

// Several IdP groups pointing at one OU is the ordinary shape of a rule set;
// it must not cost one query per row.
func TestValidateSSOGroupRules_ResolvesEachSlugOnce(t *testing.T) {
	repo := knownGroups("vip")
	rules := []config.SSOGroupRule{
		{Attribute: "", Value: "idp-vip-eu", Group: "vip"},
		{Attribute: "", Value: "idp-vip-us", Group: "vip"},
		{Attribute: "", Value: "idp-vip-ap", Group: "vip"},
	}
	if err := validateSSOGroupRules(context.Background(), repo, rules); err != nil {
		t.Fatalf("valid rules rejected: %v", err)
	}
	if repo.calls != 1 {
		t.Fatalf("resolved the same slug %d times, want 1", repo.calls)
	}
}

// Whether the SAVE PATHS actually call the validator.
//
// The tests above prove validateSSOGroupRules works. They say nothing about
// either handler invoking it, and that is a separate failure with the same
// symptom: a bad rule set stores cleanly and only misbehaves at somebody's
// login. Deleting the call from a Put still compiles and still passes every
// other test in this package — verified by mutation — so these exist to make
// the call site itself load-bearing.
//
// Asserted as "rejected AND nothing was written", not just the status code: a
// handler that returns 400 after persisting would be worse than one that
// never checked, because the form would report failure while the rules went
// live.

type recordingSAMLRepo struct {
	saves int
}

func (r *recordingSAMLRepo) Load(context.Context) (*config.SAMLConfig, error) {
	return &config.SAMLConfig{}, nil
}

func (r *recordingSAMLRepo) Save(context.Context, *config.SAMLConfig) error {
	r.saves++
	return nil
}

type recordingOIDCRepo struct {
	saves int
}

func (r *recordingOIDCRepo) Load(context.Context) (*config.OIDCConfig, error) {
	return &config.OIDCConfig{}, nil
}

func (r *recordingOIDCRepo) Save(context.Context, *config.OIDCConfig) error {
	r.saves++
	return nil
}

func putJSON(t *testing.T, h func(*gin.Context), body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h(c)
	return rec
}

const badRuleBody = `{"enabled":false,"group_rules":[{"attribute":"","value":"idp-vip","group":"does-not-exist"}]}`

func TestAdminSAMLPutRejectsUnresolvableGroupRule(t *testing.T) {
	repo := &recordingSAMLRepo{}
	// Real (zero-value) services rather than nil, for both handlers: see
	// the note above the OIDC case. With the config disabled, Reload is
	// pure state and touches nothing external, and the difference only
	// shows when the validator is ABSENT — which is precisely the run
	// that has to produce a readable failure.
	h := NewAdminSAMLHandler(repo, &auth.SAMLService{}, nil, knownGroups("vip"))

	rec := putJSON(t, h.Put, badRuleBody)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 — the save path did not validate group rules", rec.Code)
	}
	if repo.saves != 0 {
		t.Fatalf("rejected config was still persisted (%d saves)", repo.saves)
	}
}

func TestAdminOIDCPutRejectsUnresolvableGroupRule(t *testing.T) {
	repo := &recordingOIDCRepo{}
	// A real (zero-value) service rather than nil: with the config
	// disabled, Reload is pure state and touches no network. Without it
	// this handler nil-panics once past the validator, which would make
	// a missing validator abort the TEST BINARY — taking every later
	// test with it — instead of failing this test cleanly. That is a
	// signal reading as infrastructure noise exactly when it should read
	// as "the save path stopped checking".
	h := NewAdminOIDCHandler(repo, &auth.OIDCService{}, knownGroups("vip"))

	rec := putJSON(t, h.Put, badRuleBody)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 — the save path did not validate group rules", rec.Code)
	}
	if repo.saves != 0 {
		t.Fatalf("rejected config was still persisted (%d saves)", repo.saves)
	}
}
