package health

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The defect these tests exist for: until 2026-09-09 the UDP branch wrote one
// 0x00 byte, which no QUIC server answers, and reported the inevitable timeout
// as OPEN. So for every Hysteria2 / TUIC node the probe could only ever return
// "up" — including when the host was gone. "Cannot tell" and "fine" were the
// same value.

// udpEcho stands in for a QUIC server: it answers whatever it receives. A real
// server replies with a Version Negotiation packet; the probe deliberately
// does not parse the reply, so echoing is a faithful stand-in for "something
// on this port answered a QUIC packet".
func udpEcho(t *testing.T) (host string, port int) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(buf[:n], addr)
		}
	}()
	return splitHostPort(t, pc.LocalAddr().String())
}

// udpSilent accepts datagrams and never answers — a filtered port, a dead host
// behind a DROP rule, or a Hysteria2 server with obfuscation enabled.
func udpSilent(t *testing.T) (host string, port int) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			if _, _, err := pc.ReadFrom(buf); err != nil {
				return
			}
		}
	}()
	return splitHostPort(t, pc.LocalAddr().String())
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		t.Fatalf("port %q: %v", p, err)
	}
	return h, n
}

// closedUDPPort returns a loopback port with nothing bound, so the kernel
// answers with ICMP port-unreachable (surfaced as ECONNREFUSED on read).
func closedUDPPort(t *testing.T) (string, int) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	host, port := splitHostPort(t, pc.LocalAddr().String())
	_ = pc.Close() // free it again: the port is now known-closed
	return host, port
}

func TestUDPProbeAnsweringServerIsOpen(t *testing.T) {
	host, port := udpEcho(t)
	if err := portOpen(context.Background(), "udp", host, port); err != nil {
		t.Fatalf("a server that answers must read as open, got %v", err)
	}
}

// The whole point of the change. Before it, this returned nil (= up).
func TestUDPProbeSilenceIsInconclusiveNotOpen(t *testing.T) {
	host, port := udpSilent(t)
	err := portOpen(context.Background(), "udp", host, port)
	if err == nil {
		t.Fatal("silence read as OPEN — this is the defect: an unprobeable or dead UDP endpoint must never report healthy")
	}
	if !errors.Is(err, errProbeInconclusive) {
		t.Fatalf("silence must be inconclusive, not a hard failure; got %v", err)
	}
}

// A dead host times out exactly like a filtered one. It must not be "up".
func TestUDPProbeUnroutableHostIsNotOpen(t *testing.T) {
	// 203.0.113.0/24 is TEST-NET-3 (RFC 5737) — reserved for documentation and
	// guaranteed not to carry real traffic, so this cannot hit a live host.
	err := portOpen(context.Background(), "udp", "203.0.113.1", 8443)
	if err == nil {
		t.Fatal("an unroutable host read as OPEN — the exact shape of the fixed defect")
	}
}

func TestUDPProbeClosedPortIsUnreachableNotInconclusive(t *testing.T) {
	host, port := closedUDPPort(t)
	err := portOpen(context.Background(), "udp", host, port)
	if err == nil {
		t.Fatal("a closed port must not read as open")
	}
	if errors.Is(err, errProbeInconclusive) {
		t.Fatalf("ICMP port-unreachable is PROOF of closed, not an inconclusive result; got %v", err)
	}
}

func TestTCPProbeStaysDefinitiveInBothDirections(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	host, port := splitHostPort(t, ln.Addr().String())
	if err := portOpen(context.Background(), "tcp", host, port); err != nil {
		t.Fatalf("open tcp port: %v", err)
	}
	_ = ln.Close()
	err = portOpen(context.Background(), "tcp", host, port)
	if err == nil {
		t.Fatal("closed tcp port read as open")
	}
	if errors.Is(err, errProbeInconclusive) {
		t.Fatal("TCP is definitive; it must never produce an inconclusive verdict")
	}
}

// The datagram's shape is load-bearing: a server ignores a short unknown-version
// datagram (anti-amplification), and answers a reserved version with Version
// Negotiation. Get either wrong and a healthy server looks silent.
func TestProbeDatagramIsAConformingVersionNegotiationTrigger(t *testing.T) {
	pkt, err := buildQUICVersionNegotiationTrigger()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// The literal 1200 on purpose, NOT quicProbeDatagramSize: asserting a
	// constant against itself is a tautology that survives any change to it.
	// (Caught by mutation — shrinking the constant to 64 left this test green.)
	// RFC 9000 §14.1 fixes this number; servers ignore shorter unknown-version
	// datagrams to avoid becoming reflection amplifiers, so a short probe reads
	// as silence from a perfectly healthy server.
	if len(pkt) != 1200 {
		t.Errorf("datagram is %d bytes, want 1200 (RFC 9000 §14.1 minimum) — a shorter probe is ignored by a healthy server and reads as silence", len(pkt))
	}
	if pkt[0]&0x80 == 0 {
		t.Error("header form bit unset — this is not a long-header packet and a server will not treat it as a version-negotiation trigger")
	}
	if pkt[0]&0x40 == 0 {
		t.Error("fixed bit unset — RFC 9000 §17.2 requires it; servers drop packets without it")
	}
	version := binary.BigEndian.Uint32(pkt[1:5])
	if version&0x0f0f0f0f != 0x0a0a0a0a {
		t.Errorf("version %#08x is not a reserved 0x?a?a?a?a value — a real version would make the server try to SPEAK it instead of answering with version negotiation", version)
	}
}

// Predictable connection IDs would let an off-path observer forge a reply and
// make a dead node look alive, so two probes must never be identical.
func TestProbeConnectionIDsAreFresh(t *testing.T) {
	a, err := buildQUICVersionNegotiationTrigger()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	b, err := buildQUICVersionNegotiationTrigger()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// bytes 5.. carry DCID len + DCID + SCID len + SCID
	if string(a[5:22]) == string(b[5:22]) {
		t.Fatal("two probes carried identical connection IDs — they must come from crypto/rand")
	}
}

func TestProbeRespectsDeadline(t *testing.T) {
	host, port := udpSilent(t)
	start := time.Now()
	_ = portOpen(context.Background(), "udp", host, port)
	if elapsed := time.Since(start); elapsed > healthProbeTimeout+2*time.Second {
		t.Fatalf("probe took %v, well past the %v budget", elapsed, healthProbeTimeout)
	}
}

// --- Caller-level coverage -------------------------------------------------
//
// The probe returning errProbeInconclusive is only half the fix; CheckOnce has
// to MAP it to its own state. Mutation-caught: collapsing that mapping to
// NodeHealthOK left every probe-level test above green, because none of them
// went through CheckOnce. Same shape as the markConfigSyncGaveUp gap — a
// correct helper whose only caller was untested.

func TestCheckOnceMapsInconclusiveToItsOwnState(t *testing.T) {
	repo := &fakeNodeRepo{nodes: []*domain.Node{
		{ID: 1, PanelID: 10, InboundID: 1, Enabled: true, ServerAddress: "h.example", Port: 8443, Protocol: "hysteria2"},
	}}
	s := New(repo)
	s.probe = func(context.Context, string, string, int) error { return errProbeInconclusive }
	if err := s.CheckOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := repo.nodes[0].HealthState
	if got == domain.NodeHealthOK {
		t.Fatal("an inconclusive probe was recorded as OK — the fleet would look healthy while the probe is blind")
	}
	if got == domain.NodeHealthUnreachable {
		t.Fatal("an inconclusive probe was recorded as UNREACHABLE — an obfuscated node would alarm forever")
	}
	if got != domain.NodeHealthInconclusive {
		t.Fatalf("HealthState = %q, want %q", got, domain.NodeHealthInconclusive)
	}
	if repo.nodes[0].HealthDetail == "" {
		t.Error("an inconclusive verdict with no detail leaves the admin unable to tell unprobeable-by-design from broken")
	}
}

func TestCheckOnceStillSeparatesUpFromDown(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want domain.NodeHealthState
	}{
		{"probe succeeds", nil, domain.NodeHealthOK},
		{"probe fails hard", errors.New("connection refused"), domain.NodeHealthUnreachable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeNodeRepo{nodes: []*domain.Node{
				{ID: 1, Enabled: true, ServerAddress: "h.example", Port: 443, Protocol: "vless"},
			}}
			s := New(repo)
			s.probe = func(context.Context, string, string, int) error { return tc.err }
			if err := s.CheckOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := repo.nodes[0].HealthState; got != tc.want {
				t.Fatalf("HealthState = %q, want %q", got, tc.want)
			}
		})
	}
}
