package safehttp

import "testing"

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
