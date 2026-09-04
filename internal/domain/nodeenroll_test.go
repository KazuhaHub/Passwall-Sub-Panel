package domain

import (
	"errors"
	"net"
	"reflect"
	"testing"
)

// Enrollment exists to remove the one question that has proven impossible to
// answer reliably by hand: which address does PSP put in. So the ordering and
// the filtering here are the feature, not plumbing around it.

// allowPublic mirrors the dial policy closely enough for these tests: refuse
// loopback and link-local, allow RFC1918. The production caller passes the
// real safehttp predicate so the two cannot drift.
func allowPublic(ip net.IP) bool {
	return !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
}

func report() NodeEnrollReport {
	return NodeEnrollReport{Scheme: "http", Port: 54321, BasePath: "/", Addresses: nil}
}

// The address PSP saw the callback arrive from is the only one it has positive
// evidence about — it just received a packet from it — so it is tried first.
func TestNodeEnrollCandidates_ObservedAddressComesFirst(t *testing.T) {
	r := report()
	r.Addresses = []string{"10.0.0.9"}
	got, err := NodeEnrollCandidates(r, "203.0.113.7", allowPublic)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	want := []string{"http://203.0.113.7:54321", "http://10.0.0.9:54321"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// The reason the node's own addresses are kept at all: behind NAT, or when the
// callback egresses a different interface than the panel listens on, the
// observed address is simply the wrong machine-facing address.
func TestNodeEnrollCandidates_KeepsSelfReportedForNAT(t *testing.T) {
	r := report()
	r.Addresses = []string{"192.168.1.50", "10.8.0.2"}
	got, err := NodeEnrollCandidates(r, "198.51.100.4", allowPublic)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d candidates, want 3: %v", len(got), got)
	}
	for _, want := range []string{"http://192.168.1.50:54321", "http://10.8.0.2:54321"} {
		var found bool
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("%q missing from %v — a NAT'd node would be unenrollable", want, got)
		}
	}
}

// Dropped here rather than left to fail at probe time. A loopback candidate
// comes back as "refusing connection to non-public address", which reads like
// a bug in PSP instead of "that address was never usable from here".
func TestNodeEnrollCandidates_DropsAddressesTheDialGuardRefuses(t *testing.T) {
	r := report()
	r.Addresses = []string{"127.0.0.1", "169.254.169.254", "0.0.0.0", "10.0.0.9"}
	got, err := NodeEnrollCandidates(r, "", allowPublic)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"http://10.0.0.9:54321"}) {
		t.Fatalf("got %v, want only the routable one", got)
	}
}

// A node that can only offer addresses PSP may not dial has to fail loudly at
// enrollment. Registering it anyway would produce a panel row that can never
// work, and the operator would debug the panel instead of the topology.
func TestNodeEnrollCandidates_FailsWhenNothingIsReachable(t *testing.T) {
	r := report()
	r.Addresses = []string{"127.0.0.1", "::1"}
	_, err := NodeEnrollCandidates(r, "127.0.0.1", allowPublic)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

// The adapter builds requests as baseURL + "/panel/api/...", so a stored base
// path with a trailing slash yields a double slash and a 404 that looks like a
// version problem.
func TestNodeEnrollCandidates_NormalisesBasePath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/", "http://10.0.0.9:54321"},
		{"", "http://10.0.0.9:54321"},
		{"secret", "http://10.0.0.9:54321/secret"},
		{"/secret", "http://10.0.0.9:54321/secret"},
		{"/secret/", "http://10.0.0.9:54321/secret"},
		{"/deep/path/", "http://10.0.0.9:54321/deep/path"},
	} {
		r := report()
		r.BasePath = tc.in
		r.Addresses = []string{"10.0.0.9"}
		got, err := NodeEnrollCandidates(r, "", allowPublic)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if got[0] != tc.want {
			t.Fatalf("basePath %q -> %q, want %q", tc.in, got[0], tc.want)
		}
	}
}

func TestNodeEnrollCandidates_BracketsIPv6(t *testing.T) {
	r := report()
	r.Addresses = []string{"2001:4860:4860::8888"}
	got, err := NodeEnrollCandidates(r, "", allowPublic)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if got[0] != "http://[2001:4860:4860::8888]:54321" {
		t.Fatalf("got %q, want a bracketed IPv6 host", got[0])
	}
}

// A hostname cannot be judged against the dial policy here: that guard runs
// post-DNS inside the dialer. Keeping it as a candidate is right; silently
// dropping it would make a DNS-named panel unenrollable.
func TestNodeEnrollCandidates_KeepsHostnamesForTheProbeToJudge(t *testing.T) {
	r := report()
	r.Addresses = []string{"panel.example.com"}
	got, err := NodeEnrollCandidates(r, "", allowPublic)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if got[0] != "http://panel.example.com:54321" {
		t.Fatalf("got %q", got[0])
	}
}

func TestNodeEnrollCandidates_Deduplicates(t *testing.T) {
	r := report()
	r.Addresses = []string{"10.0.0.9", "10.0.0.9"}
	got, err := NodeEnrollCandidates(r, "10.0.0.9", allowPublic)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %v, want one entry", got)
	}
}

func TestNodeEnrollCandidates_RejectsBadSchemeAndPort(t *testing.T) {
	r := report()
	r.Addresses = []string{"10.0.0.9"}
	r.Scheme = "ftp"
	if _, err := NodeEnrollCandidates(r, "", allowPublic); !errors.Is(err, ErrValidation) {
		t.Fatal("a non-http scheme must be rejected")
	}
	r = report()
	r.Addresses = []string{"10.0.0.9"}
	r.Port = 0
	if _, err := NodeEnrollCandidates(r, "", allowPublic); !errors.Is(err, ErrValidation) {
		t.Fatal("port 0 must be rejected")
	}
}

// The hostname arrives from a machine PSP has not authenticated and lands in a
// uniquely-indexed column, so it is sanitised rather than stored verbatim.
func TestNodeEnrollName(t *testing.T) {
	cases := []struct{ host, fallback, want string }{
		{"tokyo-node-1", "10.0.0.9:54321", "tokyo-node-1"},
		{"  spaced  ", "10.0.0.9:54321", "spaced"},
		{"bad/../name", "10.0.0.9:54321", "bad..name"},
		{"", "10.0.0.9:54321", "10.0.0.9"},
		{"!!!", "10.0.0.9:54321", "10.0.0.9"},
		{"", "", "node"},
	}
	for _, tc := range cases {
		if got := NodeEnrollName(tc.host, tc.fallback); got != tc.want {
			t.Fatalf("NodeEnrollName(%q,%q) = %q, want %q", tc.host, tc.fallback, got, tc.want)
		}
	}
	long := NodeEnrollName(string(make([]byte, 0))+"a-very-long-hostname-that-keeps-going-and-going-and-going", "x")
	if len(long) > 48 {
		t.Fatalf("name not truncated: %d chars", len(long))
	}
}
