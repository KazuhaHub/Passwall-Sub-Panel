package safehttp

import (
	"net"
	"testing"
)

// AllowsIP exists so enrollment can filter addresses before dialing. If it
// ever disagreed with the dialer, enrollment would either drop a usable
// address or accept one that then fails with a confusing error — so the two
// are asserted to agree on the whole rule set rather than only on the
// happy path.
func TestAllowsIPAgreesWithTheDialGuard(t *testing.T) {
	for _, s := range []string{
		"8.8.8.8", "1.1.1.1", "203.0.113.7", // public + TEST-NET
		"10.0.0.1", "172.16.0.1", "192.168.1.1", // RFC1918, deliberately allowed
		"100.64.0.1",       // CGNAT, deliberately allowed
		"127.0.0.1", "::1", // loopback
		"169.254.169.254",         // cloud metadata
		"0.0.0.0",                 // unspecified
		"198.18.0.1", "240.0.0.1", // benchmarking, reserved
		"2001:db8::1", // IPv6 documentation
	} {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("bad test input %q", s)
		}
		dialOK := BlockNonPublicDial("", net.JoinHostPort(ip.String(), "443"), nil) == nil
		if got := AllowsIP(ip); got != dialOK {
			t.Fatalf("%s: AllowsIP=%v but the dial guard says %v", s, got, dialOK)
		}
	}
}

func TestAllowsIPRejectsNil(t *testing.T) {
	if AllowsIP(nil) {
		t.Fatal("a nil IP must not be treated as dialable")
	}
}
