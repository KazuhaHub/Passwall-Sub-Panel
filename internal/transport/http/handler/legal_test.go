package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/legal"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/registration"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

type legalFixture struct {
	t        *testing.T
	repos    ports.Repos
	store    *sqlstore.LegalRepo
	settings ports.SettingsRepo
	svc      *legal.Service
	router   *gin.Engine
}

func newLegalFixture(t *testing.T) *legalFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	sqlstore.ConfigureSecretKey("test-only-legal-key")
	t.Cleanup(func() { sqlstore.ConfigureSecretKey("") })
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "legal.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s, e := db.DB(); e == nil {
			_ = s.Close()
		}
	})
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	f := &legalFixture{t: t, repos: sqlstore.NewRepos(db), store: sqlstore.NewLegalRepo(db)}
	f.settings = f.repos.Settings
	f.svc = legal.New(f.store, f.settings, legal.NewCollector(nil))
	f.router = f.build(f.settings)
	return f
}

func (f *legalFixture) build(settings ports.SettingsRepo) *gin.Engine {
	r := gin.New()
	r.GET("/api/legal/:kind", NewLegalPublicHandler(f.svc).Get)
	admin := NewAdminLegalHandler(LegalWiring{Service: f.svc, Store: f.store}, settings)
	ag := r.Group("/api/admin", func(c *gin.Context) {
		c.Set(middleware.CtxClaims, &jwtutil.Claims{UserID: 1, Role: domain.RoleAdmin})
	})
	ag.GET("/legal/affected-users", admin.AffectedUsers)
	ag.GET("/legal/:kind", admin.Get)
	ag.POST("/legal/:kind", admin.Publish)
	return r
}

func (f *legalFixture) do(method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.doOn(f.router, method, path, body, hdr...)
}

func (f *legalFixture) doOn(r http.Handler, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func (f *legalFixture) set(mut func(*ports.UISettings)) {
	f.t.Helper()
	s, err := f.settings.Load(context.Background(), ports.UISettings{})
	if err != nil {
		f.t.Fatal(err)
	}
	mut(&s)
	if err := f.settings.Save(context.Background(), s); err != nil {
		f.t.Fatal(err)
	}
}

func (f *legalFixture) loaded() ports.UISettings {
	f.t.Helper()
	s, err := f.settings.Load(context.Background(), ports.UISettings{})
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

// seedDoc publishes one document straight into the store, so consent is
// reachable without going through the endpoint (which would move the version).
func (f *legalFixture) seedDoc() {
	f.t.Helper()
	if _, err := f.store.Publish(context.Background(), domain.LegalDocument{Kind: domain.LegalKindTerms, Locale: "zh-CN", Content: "terms"}); err != nil {
		f.t.Fatal(err)
	}
}

func publishBody(locale, content string, bump bool) string {
	b, _ := json.Marshal(map[string]any{"locale": locale, "content": content, "consent_bump": bump})
	return string(b)
}

func decodeMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode: %v: %s", err, rec.Body.String())
	}
	return m
}

// ---- publishing ----

func TestPublish_FirstPublicationSetsConsentVersionOneEvenWithoutBump(t *testing.T) {
	f := newLegalFixture(t)
	rec := f.do("POST", "/api/admin/legal/privacy", publishBody("zh-CN", "# 隐私", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("publish = %d: %s", rec.Code, rec.Body)
	}
	if got := f.loaded().LegalConsentVersion; got != 1 {
		t.Fatalf("consent_version = %d after the first publication, want 1", got)
	}
	if body := decodeMap(t, rec); body["consent_version"] != float64(1) {
		t.Errorf("response consent_version = %v", body["consent_version"])
	}
}

func TestPublish_OnlyABumpMovesTheConsentVersion(t *testing.T) {
	f := newLegalFixture(t)
	post := func(kind, locale string, bump bool) {
		t.Helper()
		if rec := f.do("POST", "/api/admin/legal/"+kind, publishBody(locale, "text "+kind+locale, bump)); rec.Code != http.StatusOK {
			t.Fatalf("publish = %d: %s", rec.Code, rec.Body)
		}
	}
	post("terms", "zh-CN", false) // first: 1
	post("terms", "zh-CN", false) // typo fix: still 1
	post("privacy", "en-US", false)
	if got := f.loaded().LegalConsentVersion; got != 1 {
		t.Fatalf("after typo fixes consent_version = %d, want 1", got)
	}
	post("privacy", "zh-CN", true) // a material change in any kind or locale
	if got := f.loaded().LegalConsentVersion; got != 2 {
		t.Fatalf("after a bump consent_version = %d, want 2", got)
	}
	post("terms", "en-US", true)
	if got := f.loaded().LegalConsentVersion; got != 3 {
		t.Fatalf("consent_version = %d, want 3 (one counter for both kinds and all locales)", got)
	}
}

func TestPublish_RejectsOversizeContentAndChangesNothing(t *testing.T) {
	f := newLegalFixture(t)
	rec := f.do("POST", "/api/admin/legal/terms", publishBody("zh-CN", strings.Repeat("a", domain.LegalContentMaxBytes+1), true))
	if rec.Code != http.StatusBadRequest || decodeMap(t, rec)["code"] != "legal_content_too_large" {
		t.Fatalf("60001 bytes = %d %s, want 400 legal_content_too_large", rec.Code, rec.Body)
	}
	if f.loaded().LegalConsentVersion != 0 {
		t.Error("a refused publication moved the consent version")
	}
	if _, err := f.store.Latest(context.Background(), domain.LegalKindTerms, "zh-CN"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a refused publication stored a document: %v", err)
	}
	if rec := f.do("POST", "/api/admin/legal/terms", publishBody("zh-CN", strings.Repeat("a", domain.LegalContentMaxBytes), false)); rec.Code != http.StatusOK {
		t.Errorf("exactly 60000 bytes = %d: %s", rec.Code, rec.Body)
	}
}

func TestPublish_BadInput(t *testing.T) {
	f := newLegalFixture(t)
	for name, c := range map[string]struct {
		path, body string
		want       int
	}{
		"unknown kind":   {"/api/admin/legal/cookies", publishBody("zh-CN", "x", false), http.StatusNotFound},
		"bad locale":     {"/api/admin/legal/terms", publishBody("zh_CN", "x", false), http.StatusBadRequest},
		"missing locale": {"/api/admin/legal/terms", `{"content":"x"}`, http.StatusBadRequest},
		"blank content":  {"/api/admin/legal/terms", publishBody("zh-CN", "  ", false), http.StatusBadRequest},
		"not json":       {"/api/admin/legal/terms", `nope`, http.StatusBadRequest},
	} {
		if rec := f.do("POST", c.path, c.body); rec.Code != c.want {
			t.Errorf("%s = %d, want %d: %s", name, rec.Code, c.want, rec.Body)
		}
	}
	if f.loaded().LegalConsentVersion != 0 {
		t.Error("a refused publication moved the consent version")
	}
}

func TestAdminGet_ListsLatestPerLocaleAndTheHistory(t *testing.T) {
	f := newLegalFixture(t)
	f.do("POST", "/api/admin/legal/terms", publishBody("zh-CN", "one", false))
	f.do("POST", "/api/admin/legal/terms", publishBody("zh-CN", "two", true))
	f.do("POST", "/api/admin/legal/terms", publishBody("en-US", "english", false))
	rec := f.do("GET", "/api/admin/legal/terms", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rec.Code, rec.Body)
	}
	body := decodeMap(t, rec)
	docs, _ := body["documents"].([]any)
	vers, _ := body["versions"].([]any)
	if len(docs) != 2 || len(vers) != 3 {
		t.Fatalf("documents %d, versions %d; want 2 and 3: %s", len(docs), len(vers), rec.Body)
	}
	if body["consent_version"] != float64(2) {
		t.Errorf("consent_version = %v", body["consent_version"])
	}
	for _, v := range vers {
		if _, has := v.(map[string]any)["content"]; has {
			t.Error("the version list carries document text")
		}
	}
	if rec := f.do("GET", "/api/admin/legal/cookies", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown kind = %d", rec.Code)
	}
}

func TestAffectedUsers(t *testing.T) {
	f := newLegalFixture(t)
	ctx := context.Background()
	for i, role := range []domain.Role{domain.RoleUser, domain.RoleUser, domain.RoleOperator} {
		n := string(rune('a'+i)) + "user"
		u := &domain.User{UPN: n + "@example.test", Email: n + "@example.test", SSOProvider: domain.SSOProviderLocal, SSOSubject: n, Role: role, Enabled: true,
			UUID: "77777777-7777-4777-7777-77777777777" + string(rune('0'+i)), SubToken: "legal-affected-" + n, TrafficResetPeriod: domain.ResetMonthly}
		if err := f.repos.User.Create(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	rec := f.do("GET", "/api/admin/legal/affected-users", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d: %s", rec.Code, rec.Body)
	}
	if got := decodeMap(t, rec)["affected_users"]; got != float64(2) {
		t.Errorf("affected_users = %v, want 2 (staff are never asked)", got)
	}
}

// ---- the public document ----

func TestPublicGet_404WhileDisabled(t *testing.T) {
	f := newLegalFixture(t)
	f.do("POST", "/api/admin/legal/privacy", publishBody("zh-CN", "x", false))
	if rec := f.do("GET", "/api/legal/privacy?lang=zh-CN", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("disabled = %d, want 404", rec.Code)
	}
}

func TestPublicGet_ServesTheDocumentWithTheLiveDataCollection(t *testing.T) {
	f := newLegalFixture(t)
	f.do("POST", "/api/admin/legal/privacy", publishBody("zh-CN", "# 隐私\n\n[[data-collection]]\n", false))
	f.set(func(s *ports.UISettings) { s.LegalEnabled = true; s.SubLogRetentionDays = 21 })
	rec := f.do("GET", "/api/legal/privacy?lang=zh-TW", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d: %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-cache" || rec.Header().Get("ETag") == "" {
		t.Errorf("headers: %v", rec.Header())
	}
	body := decodeMap(t, rec)
	if body["locale"] != "zh-CN" || body["fallback_from"] != "zh-TW" || body["version"] != float64(1) || body["consent_version"] != float64(1) {
		t.Errorf("body = %v", body)
	}
	if !strings.Contains(body["content"].(string), "[[data-collection]]") || body["published_at"] == "" {
		t.Errorf("content/published_at: %v", body)
	}
	dc := body["data_collection"].(map[string]any)
	if sl := dc["sub_log"].(map[string]any); sl["retention_days"] != float64(21) || sl["enabled"] != true {
		t.Errorf("sub_log = %v, want the stored 21 days", sl)
	}
	if _, usage := dc["dest_usage"]; usage {
		t.Error("usage recording is disclosed without the admin's switch")
	}
}

func TestPublicGet_ETag(t *testing.T) {
	f := newLegalFixture(t)
	f.do("POST", "/api/admin/legal/privacy", publishBody("zh-CN", "x", false))
	f.set(func(s *ports.UISettings) { s.LegalEnabled = true })
	first := f.do("GET", "/api/legal/privacy?lang=zh-CN", "")
	etag := first.Header().Get("ETag")
	second := f.do("GET", "/api/legal/privacy?lang=zh-CN", "", "If-None-Match", etag)
	if second.Code != http.StatusNotModified || second.Body.Len() != 0 {
		t.Fatalf("revalidation = %d with %d body bytes, want 304 and none", second.Code, second.Body.Len())
	}
	if second.Header().Get("ETag") != etag || second.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("a 304 must carry the validator and the same Cache-Control: %v", second.Header())
	}
	// A retention the page lists changes: the same document is a different page.
	f.set(func(s *ports.UISettings) { s.SubLogRetentionDays = 99 })
	third := f.do("GET", "/api/legal/privacy?lang=zh-CN", "", "If-None-Match", etag)
	if third.Code != http.StatusOK || third.Header().Get("ETag") == etag {
		t.Fatalf("after a settings change = %d, etag unchanged = %v; want a fresh 200", third.Code, third.Header().Get("ETag") == etag)
	}
	// A new version is a different page too.
	f.do("POST", "/api/admin/legal/privacy", publishBody("zh-CN", "y", false))
	if fourth := f.do("GET", "/api/legal/privacy?lang=zh-CN", "", "If-None-Match", third.Header().Get("ETag")); fourth.Code != http.StatusOK {
		t.Errorf("after a new version = %d, want 200", fourth.Code)
	}
}

func TestPublicGet_UnknownKindAndNothingPublished(t *testing.T) {
	f := newLegalFixture(t)
	f.set(func(s *ports.UISettings) { s.LegalEnabled = true; s.LegalConsentVersion = 1 })
	for _, p := range []string{"/api/legal/cookies", "/api/legal/terms?lang=zh-CN"} {
		if rec := f.do("GET", p, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", p, rec.Code)
		}
	}
}

func TestPublicGet_NothingWired(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/legal/:kind", NewLegalPublicHandler(nil).Get)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/legal/terms", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("= %d, want 404", rec.Code)
	}
}

// ---- the system settings page ----

func TestSettingsPut_PreservesLegalConsentVersion(t *testing.T) {
	stored := storedEverywhere()
	stored.LegalEnabled = false
	stored.LegalDiscloseUsage = false
	stored.LegalConsentVersion = 5
	repo := &policySettingsRepo{settings: stored}

	body := map[string]any{
		"login_mode": "local_only", "site_title": "after",
		"legal_enabled": true, "legal_disclose_usage": true,
		"legal_consent_version": 99, // a stale or hostile tab
	}
	payload, _ := json.Marshal(body)
	rec := requestRiskPolicy(destSettingsRouter(repo), http.MethodPut, "/api/admin/settings/ui", string(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("settings PUT = %d: %s", rec.Code, rec.Body.String())
	}
	got, saves, _ := repo.snapshot()
	if saves != 1 {
		t.Fatalf("saves = %d", saves)
	}
	if got.LegalConsentVersion != 5 {
		t.Errorf("consent_version = %d, want the stored 5: only the publish endpoint writes it", got.LegalConsentVersion)
	}
	if !got.LegalEnabled || !got.LegalDiscloseUsage {
		t.Errorf("the two switches the page owns were not saved: enabled %v, disclose_usage %v", got.LegalEnabled, got.LegalDiscloseUsage)
	}
	if v := decodeMap(t, rec)["legal_consent_version"]; v != float64(5) {
		t.Errorf("answer legal_consent_version = %v, want the kept 5", v)
	}
}

func TestSettingsGet_ServesTheLegalKeys(t *testing.T) {
	repo := &policySettingsRepo{settings: ports.UISettings{LegalEnabled: true, LegalConsentVersion: 4, LegalDiscloseUsage: true}}
	r := gin.New()
	r.GET("/s", NewAdminSettingsHandler(repo, nil, nil, nil).Get)
	rec := requestRiskPolicy(r, http.MethodGet, "/s", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d: %s", rec.Code, rec.Body)
	}
	m := decodeMap(t, rec)
	if m["legal_enabled"] != true || m["legal_consent_version"] != float64(4) || m["legal_disclose_usage"] != true {
		t.Errorf("legal keys = %v %v %v", m["legal_enabled"], m["legal_consent_version"], m["legal_disclose_usage"])
	}
}

// ---- auth: methods, register ----

func TestPublicGet_IfNoneMatchAcceptsWeakValidatorsAndLists(t *testing.T) {
	f := newLegalFixture(t)
	f.do("POST", "/api/admin/legal/privacy", publishBody("zh-CN", "x", false))
	f.set(func(s *ports.UISettings) { s.LegalEnabled = true })
	etag := f.do("GET", "/api/legal/privacy?lang=zh-CN", "").Header().Get("ETag")
	for name, header := range map[string]string{
		"weak":          "W/" + etag,
		"a list":        `"nope", ` + etag + `, "other"`,
		"a weak list":   `W/"nope",W/` + etag,
		"star":          "*",
		"padded spaces": "  " + etag + "  ",
	} {
		if rec := f.do("GET", "/api/legal/privacy?lang=zh-CN", "", "If-None-Match", header); rec.Code != http.StatusNotModified {
			t.Errorf("%s: %q = %d, want 304", name, header, rec.Code)
		}
	}
	if rec := f.do("GET", "/api/legal/privacy?lang=zh-CN", "", "If-None-Match", `"nope", "other"`); rec.Code != http.StatusOK {
		t.Errorf("no match = %d, want 200", rec.Code)
	}
}

// With the service wired, the methods answer says "enabled" only when consent
// can actually be asked: a document is reachable.
func TestAuthMethods_LegalIsNotEnabledWithoutAReachableDocument(t *testing.T) {
	f := newLegalFixture(t)
	f.set(func(s *ports.UISettings) { s.LegalEnabled = true; s.LegalConsentVersion = 2 })
	call := func() map[string]any {
		h := &AuthLocalHandler{settings: stubScoped{f.loaded()}, legal: f.svc}
		r := gin.New()
		r.GET("/m", h.Methods)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", "/m", nil))
		blk, _ := decodeMap(t, rec)["legal"].(map[string]any)
		return blk
	}
	if blk := call(); blk["enabled"] != false || blk["consent_version"] != float64(2) {
		t.Errorf("nothing published: %v", blk)
	}
	f.do("POST", "/api/admin/legal/terms", publishBody("en-US", "x", false))
	if blk := call(); blk["enabled"] != true {
		t.Errorf("a document is published: %v", blk)
	}
}

type stubScoped struct{ s ports.UISettings }

func (s stubScoped) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return s.s, nil
}
func (s stubScoped) LoadForGroup(context.Context, int64, ports.UISettings) (ports.UISettings, error) {
	return s.s, nil
}
func (s stubScoped) LoadForUser(context.Context, *domain.User, ports.UISettings) (ports.UISettings, error) {
	return s.s, nil
}

func TestAuthMethods_CarriesTheLegalBlock(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, c := range []struct {
		s       ports.UISettings
		enabled bool
		version float64
	}{
		{ports.UISettings{LegalEnabled: true, LegalConsentVersion: 3}, true, 3},
		{ports.UISettings{LegalEnabled: false, LegalConsentVersion: 3}, false, 3},
		{ports.UISettings{}, false, 0},
	} {
		h := &AuthLocalHandler{settings: stubScoped{c.s}}
		r := gin.New()
		r.GET("/m", h.Methods)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", "/m", nil))
		blk, _ := decodeMap(t, rec)["legal"].(map[string]any)
		if blk == nil || blk["enabled"] != c.enabled || blk["consent_version"] != c.version {
			t.Errorf("legal block for %+v = %v", c.s, blk)
		}
	}
}

type stubRegistrar struct {
	calls int
	id    int64
	err   error
}

func (s *stubRegistrar) Register(context.Context, registration.RegisterInput) (*registration.RegisterResult, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &registration.RegisterResult{UserID: s.id}, nil
}
func (s *stubRegistrar) ResendVerification(context.Context, string) {}
func (s *stubRegistrar) Verify(context.Context, registration.VerifyInput) error {
	return nil
}

func registerRouter(h *AuthRegisterHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/register", h.Register)
	return r
}

func TestRegister_ConsentVersionMismatchIs409AndCreatesNothing(t *testing.T) {
	f := newLegalFixture(t)
	f.set(func(s *ports.UISettings) { s.LegalEnabled = true; s.LegalConsentVersion = 2 })
	f.seedDoc()
	reg := &stubRegistrar{id: 41}
	h := &AuthRegisterHandler{reg: reg, settings: f.settings, legal: f.svc}
	r := registerRouter(h)
	for name, body := range map[string]string{
		"stale":   `{"email":"a@b.test","password":"x1y2z3w4","accepted_consent_version":1}`,
		"missing": `{"email":"a@b.test","password":"x1y2z3w4"}`,
		"future":  `{"email":"a@b.test","password":"x1y2z3w4","accepted_consent_version":3}`,
	} {
		rec := f.doOn(r, "POST", "/register", body)
		if rec.Code != http.StatusConflict || decodeMap(t, rec)["code"] != "legal_consent_outdated" {
			t.Errorf("%s = %d %s, want 409 legal_consent_outdated", name, rec.Code, rec.Body)
		}
		if decodeMap(t, rec)["consent_version"] != float64(2) {
			t.Errorf("%s: the answer must carry the current consent_version so the page can refresh: %s", name, rec.Body)
		}
	}
	if reg.calls != 0 {
		t.Fatalf("a refused registration reached the service %d times", reg.calls)
	}
}

func TestRegister_RecordsTheConsentOfTheNewAccount(t *testing.T) {
	f := newLegalFixture(t)
	ctx := context.Background()
	u := &domain.User{UPN: "reg@example.test", Email: "reg@example.test", SSOProvider: domain.SSOProviderLocal, SSOSubject: "reg", Role: domain.RoleUser, Enabled: true,
		UUID: "66666666-6666-4666-6666-666666666666", SubToken: "legal-register-token", TrafficResetPeriod: domain.ResetMonthly}
	if err := f.repos.User.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	f.set(func(s *ports.UISettings) { s.LegalEnabled = true; s.LegalConsentVersion = 2 })
	f.seedDoc()
	h := &AuthRegisterHandler{reg: &stubRegistrar{id: u.ID}, settings: f.settings, legal: f.svc}
	rec := f.doOn(registerRouter(h), "POST", "/register", `{"email":"reg@example.test","password":"x1y2z3w4","accepted_consent_version":2}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d: %s", rec.Code, rec.Body)
	}
	if ok, _ := f.store.HasConsentAtLeast(ctx, u.ID, 2); !ok {
		t.Error("the registration did not record the consent")
	}
}

func TestRegister_IgnoresTheFieldWhileLegalIsOff(t *testing.T) {
	f := newLegalFixture(t)
	reg := &stubRegistrar{id: 1}
	h := &AuthRegisterHandler{reg: reg, settings: f.settings, legal: f.svc}
	rec := f.doOn(registerRouter(h), "POST", "/register", `{"email":"a@b.test","password":"x1y2z3w4"}`)
	if rec.Code != http.StatusOK || reg.calls != 1 {
		t.Fatalf("= %d after %d service calls: %s", rec.Code, reg.calls, rec.Body)
	}
}

// ---- the signed-in user ----

func claimsRouter(role domain.Role, uid int64, h gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/accept", func(c *gin.Context) {
		c.Set(middleware.CtxClaims, &jwtutil.Claims{UserID: uid, Role: role})
		h(c)
	})
	return r
}

func TestUserAcceptLegal(t *testing.T) {
	f := newLegalFixture(t)
	ctx := context.Background()
	u := &domain.User{UPN: "acc@example.test", Email: "acc@example.test", SSOProvider: domain.SSOProviderLocal, SSOSubject: "acc", Role: domain.RoleUser, Enabled: true,
		UUID: "55555555-5555-4555-5555-555555555555", SubToken: "legal-accept-token", TrafficResetPeriod: domain.ResetMonthly}
	if err := f.repos.User.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	f.set(func(s *ports.UISettings) { s.LegalEnabled = true; s.LegalConsentVersion = 3 })
	f.seedDoc()
	h := &UserMeHandler{legal: f.svc}
	r := claimsRouter(domain.RoleUser, u.ID, h.AcceptLegal)

	if rec := f.doOn(r, "POST", "/accept", `{"consent_version":2}`); rec.Code != http.StatusConflict || decodeMap(t, rec)["code"] != "legal_consent_outdated" {
		t.Fatalf("stale = %d %s, want 409 legal_consent_outdated", rec.Code, rec.Body)
	}
	if pending, _ := h.legalPending(ctx, &domain.User{ID: u.ID, Role: domain.RoleUser}); !pending {
		t.Error("a refused accept cleared the prompt")
	}
	if rec := f.doOn(r, "POST", "/accept", `{"consent_version":3}`); rec.Code != http.StatusNoContent {
		t.Fatalf("current = %d: %s", rec.Code, rec.Body)
	}
	if pending, _ := h.legalPending(ctx, &domain.User{ID: u.ID, Role: domain.RoleUser}); pending {
		t.Error("still pending after accepting")
	}
	if rec := f.doOn(r, "POST", "/accept", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("no version = %d, want 400", rec.Code)
	}
}

func TestUserMeLegalPending_StaffNeverAndNothingWiredNever(t *testing.T) {
	f := newLegalFixture(t)
	f.set(func(s *ports.UISettings) { s.LegalEnabled = true; s.LegalConsentVersion = 1 })
	f.seedDoc()
	h := &UserMeHandler{legal: f.svc}
	ctx := context.Background()
	if p, _ := h.legalPending(ctx, &domain.User{ID: 8, Role: domain.RoleAdmin}); p {
		t.Error("an admin is pending")
	}
	if p, _ := h.legalPending(ctx, &domain.User{ID: 9, Role: domain.RoleUser}); !p {
		t.Error("a user with no consent is not pending")
	}
	if p, _ := (&UserMeHandler{}).legalPending(ctx, &domain.User{ID: 9, Role: domain.RoleUser}); p {
		t.Error("pending without the legal service wired")
	}
}

// brokenLegalStore fails the reachability lookup.
type brokenLegalStore struct{ legal.Store }

func (brokenLegalStore) HasDocumentIn(context.Context, []string) (bool, error) {
	return false, errors.New("db down")
}

// When the consent state cannot be read, the methods answer agrees with what
// Required() would have said of an unreadable state: not enabled. Reporting the
// raw setting would put a checkbox on the register form that registration then
// would not ask for.
func TestAuthMethods_LegalIsNotEnabledWhenTheStateCannotBeRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := ports.UISettings{LegalEnabled: true, LegalConsentVersion: 2}
	svc := legal.New(brokenLegalStore{}, stubScoped{s}, nil)
	h := &AuthLocalHandler{settings: stubScoped{s}, legal: svc}
	r := gin.New()
	r.GET("/m", h.Methods)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/m", nil))
	blk, _ := decodeMap(t, rec)["legal"].(map[string]any)
	if blk["enabled"] != false || blk["consent_version"] != float64(2) {
		t.Errorf("legal block = %v, want enabled false", blk)
	}
}

// The editor previews the "what we collect" list before anything is published
// or the feature is on, so the administration read carries it too: built from
// the same settings as the public page, whatever the enabled switch says.
func TestAdminGet_CarriesTheDataCollectionList(t *testing.T) {
	f := newLegalFixture(t)
	rec := f.do("GET", "/api/admin/legal/privacy", "")
	dc, _ := decodeMap(t, rec)["data_collection"].(map[string]any)
	sub, _ := dc["sub_log"].(map[string]any)
	if sub == nil || sub["enabled"] != true {
		t.Fatalf("data_collection = %v, want the sub_log item even while nothing is published and the feature is off", dc)
	}
}
