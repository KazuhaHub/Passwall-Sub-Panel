package xui

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"os"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Live verification of the concurrent-IP enforcement probe against a REAL
// 3X-UI panel.
//
// The stub tests beside this file assert what the adapter does with a response
// shape THIS REPOSITORY wrote down. That is exactly the weak spot for this
// feature: its whole premise is that a node which enforces and a node which
// does not are indistinguishable from PSP's side, so "it matches my own stub"
// is close to no evidence at all. These run the same code against a panel
// binary built from upstream, with the node flipped between the real gate
// states.
//
// Run (one state per invocation):
//
//	PSP_LIVE_F2B_URL='http://host:54321' \
//	PSP_LIVE_F2B_USER='psp' PSP_LIVE_F2B_PASS='...' \
//	PSP_LIVE_F2B_EXPECT='enforced' \
//	go test ./internal/adapters/xui/ -run TestLive_Fail2ban -count=1 -v
//
// PSP_LIVE_F2B_EXPECT is optional: without it the test reports what it saw and
// passes, which is what you want when pointing it at an unknown node.
func liveFail2banClient(t *testing.T) *Client {
	t.Helper()
	base := os.Getenv("PSP_LIVE_F2B_URL")
	if base == "" {
		t.Skip("set PSP_LIVE_F2B_URL (plus _USER/_PASS or _TOKEN) to run the live fail2ban probe")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	// Built by hand rather than through New(), which wraps safehttp's SSRF dial
	// guard. That guard correctly refuses loopback and TEST-NET, which is all a
	// throwaway test host usually has — so the transport is swapped out and the
	// guard is NOT what these tests cover. Everything above it is real: the
	// login, the endpoint, upstream's JSON, the error mapping, the
	// classification.
	return &Client{
		baseURL:  base,
		apiToken: os.Getenv("PSP_LIVE_F2B_TOKEN"),
		username: os.Getenv("PSP_LIVE_F2B_USER"),
		password: os.Getenv("PSP_LIVE_F2B_PASS"),
		http:     &http.Client{Timeout: 30 * time.Second, Jar: jar},
		jar:      jar,
	}
}

func TestLive_Fail2banStatus(t *testing.T) {
	c := liveFail2banClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := c.GetFail2banStatus(ctx)
	if err != nil {
		t.Fatalf("GetFail2banStatus against the live panel: %v", err)
	}
	state := domain.ClassifyIPLimit(*st)
	t.Logf("gates: enabled=%v installed=%v usable=%v windows=%v  ->  %s (enforcing=%v actionable=%v)",
		st.Enabled, st.Installed, st.Usable, st.Windows,
		state, state.Enforcing(), state.Actionable())

	if want := os.Getenv("PSP_LIVE_F2B_EXPECT"); want != "" && string(state) != want {
		t.Fatalf("state = %q, want %q (gates: %+v)", state, want, *st)
	}
}

// A panel that has no such route must reach the caller as
// ErrXUIEndpointUnsupported, or a pre-3.7.0 node gets recorded as a fault
// instead of as "this version cannot answer". Asserted against a REAL 3X-UI
// 404 rather than a hand-written one: the mapping keys off the status code,
// but the body and headers gin actually sends are not something this
// repository gets to decide.
func TestLive_Fail2banStatusUnknownRouteIsUnsupported(t *testing.T) {
	c := liveFail2banClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var out struct{}
	err := c.doJSON(ctx, http.MethodGet, "/panel/api/server/fail2banStatusNoSuchRoute", nil, &out)
	if err == nil {
		t.Fatal("a route that does not exist answered successfully")
	}
	t.Logf("live 404 mapped to: %v", err)
	if !errors.Is(err, ports.ErrXUIEndpointUnsupported) {
		t.Fatalf("err = %v, want ErrXUIEndpointUnsupported", err)
	}
}
