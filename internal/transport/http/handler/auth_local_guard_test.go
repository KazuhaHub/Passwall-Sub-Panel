package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/captcha"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/loginguard"
)

type stubSettings struct{ s ports.UISettings }

func (f stubSettings) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return f.s, nil
}
func (f stubSettings) Save(context.Context, ports.UISettings) error { return nil }

type stubEvents struct {
	count    int64
	lastAt   time.Time
	inserted int
}

func (f *stubEvents) RecentAuthFailures(context.Context, string, string, time.Time) (int64, time.Time, error) {
	return f.count, f.lastAt, nil
}
func (f *stubEvents) Insert(context.Context, *domain.AuthEvent) error { f.inserted++; return nil }
func (f *stubEvents) List(context.Context, ports.AuthEventFilter) ([]*domain.AuthEvent, int64, error) {
	return nil, 0, nil
}
func (f *stubEvents) DeleteBefore(context.Context, time.Time) (int64, error) { return 0, nil }
func (f *stubEvents) CountByReasonSince(context.Context, string, time.Time) (int64, error) {
	return 0, nil
}

func loginCtx(body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/auth/local/login", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.RemoteAddr = "1.2.3.4:5555"
	return c, rr
}

// The lock + captcha gates run before VerifyLocalPassword, so the user/auth
// services are never touched on these paths — nil is safe and keeps the test
// focused on the guard wiring.
func newGuardHandler(set ports.UISettings, ev *stubEvents) *AuthLocalHandler {
	return NewAuthLocalHandler(nil, nil, nil, nil, stubSettings{set}, ev, loginguard.New(ev), captcha.NewService(), nil, nil, nil)
}

func TestLogin_LockedReturns429(t *testing.T) {
	ev := &stubEvents{count: 10, lastAt: time.Now().Add(-1 * time.Minute)}
	set := ports.UISettings{
		LockoutEnabled: true, LockoutThreshold: 10,
		LockoutWindowMinutes: 15, LockoutDurationMinutes: 15, LockoutScope: "ip_upn",
	}
	c, rr := loginCtx(`{"upn":"a@x","password":"whatever"}`)
	newGuardHandler(set, ev).Login(c)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"locked":true`) {
		t.Fatalf("body must flag locked: %s", rr.Body.String())
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Fatal("locked response must set Retry-After")
	}
	if ev.inserted != 1 {
		t.Fatalf("a locked attempt should record one (uncounted) event, got %d", ev.inserted)
	}
}

// A captcha.Verify *error* (e.g. token provider with no secret, or a transient
// siteverify outage) must fail CLOSED — never fall through to the password
// check — matching captcha.Service's documented contract.
func TestLogin_CaptchaVerifyErrorFailsClosed(t *testing.T) {
	ev := &stubEvents{}
	// Token provider + empty secret → captcha.Verify returns an error.
	set := ports.UISettings{CaptchaEnabled: true, CaptchaTrigger: "always", CaptchaProvider: "turnstile", CaptchaSecretKey: ""}
	c, rr := loginCtx(`{"upn":"a@x","password":"whatever","captcha_token":"tok"}`)
	newGuardHandler(set, ev).Login(c)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("captcha verify error must fail closed (400), got %d; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"captcha_required":true`) {
		t.Fatalf("fail-closed body must demand a captcha: %s", rr.Body.String())
	}
	if ev.inserted != 0 {
		t.Fatalf("a captcha error must not feed the lockout count, inserted=%d", ev.inserted)
	}
}

func TestLogin_CaptchaRequiredButMissing(t *testing.T) {
	ev := &stubEvents{}
	set := ports.UISettings{CaptchaEnabled: true, CaptchaTrigger: "always", CaptchaProvider: "image"}
	c, rr := loginCtx(`{"upn":"a@x","password":"whatever"}`) // no captcha fields
	newGuardHandler(set, ev).Login(c)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"captcha_required":true`) {
		t.Fatalf("body must demand a captcha: %s", rr.Body.String())
	}
	// Must NOT record an invalid_credentials failure for a missing captcha.
	if ev.inserted != 0 {
		t.Fatalf("missing captcha must not feed the lockout count, inserted=%d", ev.inserted)
	}
}
