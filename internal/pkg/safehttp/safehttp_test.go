package safehttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// BlockNonPublicDial must reject the SSRF-prone / never-a-real-host ranges and
// allow public + RFC1918 private (legit self-hosted panels) + CGNAT (Tailscale/
// Headscale overlay, which IS a legitimate way to reach a panel).
func TestBlockNonPublicDial(t *testing.T) {
	blocked := []string{
		"127.0.0.1:443",      // loopback
		"[::1]:443",          // IPv6 loopback
		"169.254.169.254:80", // link-local (cloud metadata)
		"0.0.0.0:443",        // unspecified
		"192.0.2.10:443",     // TEST-NET-1 (RFC 5737)
		"198.51.100.10:443",  // TEST-NET-2
		"203.0.113.10:443",   // TEST-NET-3
		"198.18.0.10:443",    // benchmarking (RFC 2544)
		"192.0.0.10:443",     // IETF protocol assignments (RFC 6890)
		"240.0.0.10:443",     // reserved / future use (RFC 1112)
	}
	for _, a := range blocked {
		if err := BlockNonPublicDial("tcp", a, nil); err == nil {
			t.Errorf("expected %s to be blocked", a)
		}
	}

	allowed := []string{
		"8.8.8.8:443",      // public
		"10.0.0.5:443",     // RFC1918 (legit self-hosted panel)
		"192.168.1.10:443", // RFC1918
		"172.16.0.10:443",  // RFC1918
		"100.64.0.5:443",   // CGNAT — deliberately allowed (Tailscale/Headscale reach a real panel here)
	}
	for _, a := range allowed {
		if err := BlockNonPublicDial("tcp", a, nil); err != nil {
			t.Errorf("expected %s to be allowed, got %v", a, err)
		}
	}
}

// Panel clients are shared per panel and fan out up to ~8 concurrent calls at
// ONE host; Go's default MaxIdleConnsPerHost of 2 closes the surplus
// connections after every burst and re-dials TCP+TLS on the next one.
func TestNewClientKeepsIdleConnsForFanOut(t *testing.T) {
	for _, insecure := range []bool{false, true} {
		tr, ok := NewClientTLS(time.Second, insecure).Transport.(*http.Transport)
		if !ok {
			t.Fatal("transport is not *http.Transport")
		}
		if tr.MaxIdleConnsPerHost < 16 {
			t.Errorf("MaxIdleConnsPerHost = %d, want >= 16", tr.MaxIdleConnsPerHost)
		}
		if tr.MaxIdleConns < tr.MaxIdleConnsPerHost {
			t.Errorf("MaxIdleConns = %d, below per-host %d", tr.MaxIdleConns, tr.MaxIdleConnsPerHost)
		}
		if tr.IdleConnTimeout <= 0 {
			t.Error("IdleConnTimeout must stay set")
		}
	}
}

// The SSRF guard must survive the pool change: a loopback server is refused.
func TestNewClientStillRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	if resp, err := NewClient(2 * time.Second).Get(srv.URL); err == nil {
		resp.Body.Close()
		t.Fatal("expected loopback dial to be refused")
	}
}
