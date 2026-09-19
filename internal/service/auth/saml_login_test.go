package auth

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// --- request material -----------------------------------------------------

func TestNewSAMLLoginTicket(t *testing.T) {
	a, err := NewSAMLLoginTicket()
	if err != nil {
		t.Fatalf("NewSAMLLoginTicket: %v", err)
	}
	b, err := NewSAMLLoginTicket()
	if err != nil {
		t.Fatalf("NewSAMLLoginTicket: %v", err)
	}

	if a.Token == b.Token || a.Binding == b.Binding {
		t.Fatal("two tickets reused the same secret; the generator is not random")
	}
	if !ValidSAMLRelayToken(a.Token) {
		t.Fatalf("a freshly minted token fails its own shape check: %q", a.Token)
	}
	// The two values must be independent: the token round-trips through the IdP,
	// so a token alone must not be the binding.
	if a.Token == a.Binding {
		t.Fatal("token and binding are the same value")
	}
	if a.TokenHash != SHA256Hex(a.Token) || a.BindingHash != SHA256Hex(a.Binding) {
		t.Fatal("hashes do not match the values they are supposed to digest")
	}
	if a.CookieName != SAMLLoginCookieName(a.Token) {
		t.Fatalf("cookie name %q is not derived from the token", a.CookieName)
	}
	if !strings.HasPrefix(a.CookieName, "psp_saml_") {
		t.Fatalf("cookie name %q lacks the expected namespace", a.CookieName)
	}
}

// The token arrives in RelayState, which the IdP echoes and an attacker can
// therefore choose. Its shape must be checked BEFORE it is used as a cookie
// name, or a crafted value injects cookie-header syntax.
func TestValidSAMLRelayToken(t *testing.T) {
	good, err := NewSAMLLoginTicket()
	if err != nil {
		t.Fatalf("NewSAMLLoginTicket: %v", err)
	}
	valid43 := good.Token
	cases := map[string]struct {
		token string
		want  bool
	}{
		"minted token":             {valid43, true},
		"empty":                    {"", false},
		"one char short":           {valid43[:len(valid43)-1], false},
		"one char long":            {valid43 + "A", false},
		"cookie separator":         {strings.Repeat("a", 42) + ";", false},
		"cookie value separator":   {strings.Repeat("a", 42) + "=", false},
		"space":                    {strings.Repeat("a", 42) + " ", false},
		"newline":                  {strings.Repeat("a", 42) + "\n", false},
		"non-ascii":                {strings.Repeat("a", 42) + "é", false},
		"standard base64 alphabet": {strings.Repeat("a", 42) + "+", false},
		"shape-valid but unminted": {strings.Repeat("A", 43), true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := ValidSAMLRelayToken(c.token); got != c.want {
				t.Fatalf("ValidSAMLRelayToken(%q) = %v, want %v", c.token, got, c.want)
			}
		})
	}
}

// --- entry point -----------------------------------------------------------

func TestBeginLogin_RejectsTheWrongEntryPoint(t *testing.T) {
	cases := map[string]string{
		"plain http":      "http://panel.example.com",
		"another host":    "https://other.example.com",
		"another port":    "https://panel.example.com:8443",
		"unusable origin": "",
		"not a url":       "://nope",
	}
	for name, origin := range cases {
		t.Run(name, func(t *testing.T) {
			svc, _ := testSAML(t)
			_, err := svc.BeginLogin(context.Background(), BeginLoginOptions{
				RequestOrigin: origin, ReturnTo: "/user/me", Now: time.Now(),
			})
			if !errors.Is(err, ErrSAMLEntryPoint) {
				t.Fatalf("BeginLogin from %q = %v, want ErrSAMLEntryPoint", origin, err)
			}
		})
	}
}

// A login that cannot be recorded is one that could never be completed, so the
// browser must not be sent to the IdP to spend a real authentication on it.
// The panel's own ACS URL is where the comparison comes from, and an unusable
// one is a refusal rather than a silent skip.
func TestBeginLogin_RequiresAUsableACSURL(t *testing.T) {
	svc, _ := testSAML(t)
	cfg := config.CloneSAMLConfig(svc.snapshot().cfg)
	cfg.SP.ACSURL = ""
	svc.setConfigForTest(cfg)
	_, err := svc.BeginLogin(context.Background(), BeginLoginOptions{
		RequestOrigin: "https://panel.example.com", ReturnTo: "/user/me", Now: time.Now(),
	})
	if !errors.Is(err, ErrSAMLEntryPoint) {
		t.Fatalf("empty ACS URL = %v, want ErrSAMLEntryPoint", err)
	}
}

func TestBeginLogin_RequiresTheRequestStore(t *testing.T) {
	svc, _ := testSAML(t)
	svc.SetSAMLRequestStore(nil)
	_, err := svc.BeginLogin(context.Background(), BeginLoginOptions{
		RequestOrigin: "https://panel.example.com", ReturnTo: "/user/me", Now: time.Now(),
	})
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("no store = %v, want ErrUnavailable", err)
	}
}

// --- end to end ------------------------------------------------------------

// beginLogin drives BeginLogin and returns the redirect URL, the RelayState
// token it carries, and the binding the browser would hold.
func beginLogin(t *testing.T, svc *SAMLService) (redirectURL, token, binding string) {
	t.Helper()
	res, err := svc.BeginLogin(context.Background(), BeginLoginOptions{
		RequestOrigin: "https://panel.example.com", ReturnTo: "/user/me", Now: time.Now(),
	})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	u, err := url.Parse(res.RedirectURL)
	if err != nil {
		t.Fatalf("parse redirect URL: %v", err)
	}
	token = u.Query().Get("RelayState")
	if !ValidSAMLRelayToken(token) {
		t.Fatalf("BeginLogin put a non-token in RelayState: %q", token)
	}
	if res.CookieName != SAMLLoginCookieName(token) {
		t.Fatalf("cookie name %q does not name the token", res.CookieName)
	}
	if res.CookieValue == "" {
		t.Fatal("no binding value to set")
	}
	return res.RedirectURL, token, res.CookieValue
}

func TestBeginAndCompleteLogin_EndToEnd(t *testing.T) {
	svc, idp := testSAML(t)
	claimsFor(idp, "alice@corp.example", "alice@corp.example", "Alice Example", "engineering")

	redirectURL, token, binding := beginLogin(t, svc)
	raw := idp.signedResponse(t, redirectURL)

	res, err := svc.CompleteLogin(context.Background(), acsRequest(t, raw, token, false), CompleteLoginOptions{
		RelayState: token, BindingCookie: binding, Now: time.Now(),
	})
	if err != nil {
		t.Fatalf("CompleteLogin: %v", err)
	}
	if res.Assertion.UPN != "alice@corp.example" {
		t.Fatalf("UPN = %q", res.Assertion.UPN)
	}
	if res.ReturnTo != "/user/me" {
		t.Fatalf("ReturnTo = %q, want the value recorded at Begin", res.ReturnTo)
	}
	if res.Config == nil {
		t.Fatal("no configuration snapshot returned; the caller would have to read mutable config")
	}
}

// The whole point of the binding: a token captured from the IdP round trip is
// not enough from another browser. And a refused binding must NOT spend the
// request, or an attacker who can reach the ACS could deny the real user's login
// with a single guessed cookie value.
func TestCompleteLogin_RequiresTheBindingCookie(t *testing.T) {
	svc, idp := testSAML(t)
	claimsFor(idp, "alice@corp.example", "alice@corp.example", "Alice Example", "engineering")
	redirectURL, token, binding := beginLogin(t, svc)
	raw := idp.signedResponse(t, redirectURL)
	ctx := context.Background()

	cases := map[string]string{
		"absent":    "",
		"wrong":     strings.Repeat("B", 43),
		"empty-ish": "x",
	}
	for name, cookie := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := svc.CompleteLogin(ctx, acsRequest(t, raw, token, false), CompleteLoginOptions{
				RelayState: token, BindingCookie: cookie, Now: time.Now(),
			})
			if !errors.Is(err, domain.ErrSAMLRequestInvalid) {
				t.Fatalf("binding %q = %v, want ErrSAMLRequestInvalid", name, err)
			}
		})
	}

	// The legitimate browser can still complete it afterwards.
	if _, err := svc.CompleteLogin(ctx, acsRequest(t, raw, token, false), CompleteLoginOptions{
		RelayState: token, BindingCookie: binding, Now: time.Now(),
	}); err != nil {
		t.Fatalf("a refused binding spent the request: %v", err)
	}
}

func TestCompleteLogin_IsSingleUse(t *testing.T) {
	svc, idp := testSAML(t)
	claimsFor(idp, "alice@corp.example", "alice@corp.example", "Alice Example", "engineering")
	redirectURL, token, binding := beginLogin(t, svc)
	raw := idp.signedResponse(t, redirectURL)
	ctx := context.Background()

	if _, err := svc.CompleteLogin(ctx, acsRequest(t, raw, token, false), CompleteLoginOptions{
		RelayState: token, BindingCookie: binding, Now: time.Now(),
	}); err != nil {
		t.Fatalf("first completion: %v", err)
	}
	_, err := svc.CompleteLogin(ctx, acsRequest(t, raw, token, false), CompleteLoginOptions{
		RelayState: token, BindingCookie: binding, Now: time.Now(),
	})
	if !errors.Is(err, domain.ErrSAMLRequestInvalid) {
		t.Fatalf("second completion = %v, want ErrSAMLRequestInvalid", err)
	}
}

// A shape-valid token nobody minted is refused by the store, not by the shape
// check — that ordering is why the shape check can stay cheap.
func TestCompleteLogin_RejectsAnUnmintedToken(t *testing.T) {
	svc, idp := testSAML(t)
	claimsFor(idp, "alice@corp.example", "alice@corp.example", "Alice Example", "engineering")
	redirectURL, _, binding := beginLogin(t, svc)
	raw := idp.signedResponse(t, redirectURL)

	_, err := svc.CompleteLogin(context.Background(), acsRequest(t, raw, strings.Repeat("A", 43), false), CompleteLoginOptions{
		RelayState: strings.Repeat("A", 43), BindingCookie: binding, Now: time.Now(),
	})
	if !errors.Is(err, domain.ErrSAMLRequestInvalid) {
		t.Fatalf("unminted token = %v, want ErrSAMLRequestInvalid", err)
	}
}

// Changing the trust or mapping configuration between Begin and Complete must
// invalidate the login: the whole reason the digest is stored is that a login
// begun under one set of rules must not be completed under another.
func TestCompleteLogin_RejectsAConfigurationChangedMidFlight(t *testing.T) {
	svc, idp := testSAML(t)
	claimsFor(idp, "alice@corp.example", "alice@corp.example", "Alice Example", "engineering")
	redirectURL, token, binding := beginLogin(t, svc)

	// An admin saves a new role rule while the browser is at the IdP.
	changed := config.CloneSAMLConfig(svc.snapshot().cfg)
	changed.RoleRules = append(changed.RoleRules, config.SSORoleRule{
		Attribute: "groups", Value: "ops", Role: "operator",
	})
	svc.setConfigForTest(changed)

	raw := idp.signedResponse(t, redirectURL)
	_, err := svc.CompleteLogin(context.Background(), acsRequest(t, raw, token, false), CompleteLoginOptions{
		RelayState: token, BindingCookie: binding, Now: time.Now(),
	})
	if !errors.Is(err, domain.ErrSAMLRequestInvalid) {
		t.Fatalf("config changed mid-flight = %v, want ErrSAMLRequestInvalid", err)
	}
}

// An expired request is refused even with perfect binding material.
func TestCompleteLogin_RejectsAnExpiredRequest(t *testing.T) {
	svc, idp := testSAML(t)
	claimsFor(idp, "alice@corp.example", "alice@corp.example", "Alice Example", "engineering")

	res, err := svc.BeginLogin(context.Background(), BeginLoginOptions{
		RequestOrigin: "https://panel.example.com", ReturnTo: "/user/me", Now: time.Now().Add(-2 * SAMLLoginTTL),
	})
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	u, _ := url.Parse(res.RedirectURL)
	token := u.Query().Get("RelayState")
	raw := idp.signedResponse(t, res.RedirectURL)

	_, err = svc.CompleteLogin(context.Background(), acsRequest(t, raw, token, false), CompleteLoginOptions{
		RelayState: token, BindingCookie: res.CookieValue, Now: time.Now(),
	})
	if !errors.Is(err, domain.ErrSAMLRequestInvalid) {
		t.Fatalf("expired request = %v, want ErrSAMLRequestInvalid", err)
	}
}
