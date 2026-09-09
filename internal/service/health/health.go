// Package health probes each enabled node and persists the outcome on the Node
// row so the admin UI / user portal can show a live status dot without hitting
// 3X-UI directly.
//
// The health verdict is pure reachability — "is the proxy port open?" — not a
// 3X-UI control-plane check:
//   - TCP protocols (VLESS/VMess/Trojan/Shadowsocks/AnyTLS/Naive): a TCP connect to
//     ServerAddress:Port. Connect succeeds → up; refused/timeout → down.
//   - UDP-only protocols (Hysteria2/TUIC): a QUIC version-negotiation probe.
//     A reply proves a QUIC server is listening; ICMP port-unreachable proves
//     nothing is; silence is INCONCLUSIVE and is reported as such, never as up.
//     See quicVersionNegotiationProbe for why silence is a real third answer.
//
// v3.5: port / protocol are read directly from the Node row — they're written
// by the inbound write-through paths (CreateInbound / ImportExisting /
// UpdateInboundConfig) and kept aligned by reconcile axis A (see
// docs/inbound-ownership.md). Health no longer calls 3X-UI at all; cutting the
// per-cycle ListInbounds also means a panel-API outage no longer affects the
// data-plane probe. Inbound-existence drift is covered by reconcile §9.4.3 #6.
package health

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safego"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

const (
	// healthProbeTimeout bounds each TCP/UDP probe.
	healthProbeTimeout = 5 * time.Second
	// healthProbeConcurrency caps simultaneous probes so a fleet of slow /
	// timing-out endpoints can't make one pass take nodeCount × timeout.
	healthProbeConcurrency = 8
)

type Service struct {
	nodes ports.NodeRepo
	// probe reports whether the proxy port is open. network is "tcp" or "udp".
	// Injectable so tests drive the up/down branches without real sockets.
	probe func(ctx context.Context, network, host string, port int) error
}

func New(nodes ports.NodeRepo) *Service {
	return &Service{nodes: nodes, probe: portOpen}
}

// isUDPProtocol reports whether a proxy protocol carries its traffic over UDP
// (so the port must be probed with a UDP, not TCP, check). Currently just the
// Hysteria2 / TUIC QUIC family.
func isUDPProtocol(proto string) bool {
	p := strings.ToLower(strings.TrimSpace(proto))
	return p == string(domain.ProtoHysteria2) || p == string(domain.ProtoTUIC) ||
		strings.Contains(p, "hysteria") || p == "hy2"
}

// errProbeInconclusive is returned when the probe RAN but could not decide.
// Callers must map it to NodeHealthInconclusive — never to ok and never to
// unreachable. It is a sentinel rather than a third return value so the
// injectable probe signature (and every existing test double) stays unchanged.
var errProbeInconclusive = errors.New("probe inconclusive")

// quicProbeDatagramSize is the datagram length the probe pads to. RFC 9000
// §14.1 sets 1200 bytes as the smallest datagram a QUIC server must accept,
// and servers deliberately ignore SHORTER unknown-version datagrams to avoid
// becoming a reflection amplifier. A probe under this size gets silence from a
// perfectly healthy server, which would be indistinguishable from a dead one —
// so this constant is load-bearing, not a round number.
const quicProbeDatagramSize = 1200

// quicGreaseVersion is a version number reserved by RFC 9000 §15 for exercising
// version negotiation. Every 0x?a?a?a?a value is guaranteed never to be a real
// QUIC version, so a conforming server MUST answer it with a Version
// Negotiation packet instead of trying to speak it.
const quicGreaseVersion = 0x0a0a0a0a

// portOpen probes host:port. nil means open; errProbeInconclusive means the
// probe could not tell; any other error means closed/unreachable.
//
// TCP is definitive in both directions: a completed handshake is open, a
// refusal or timeout is not.
func portOpen(ctx context.Context, network, host string, port int) error {
	dctx, cancel := context.WithTimeout(ctx, healthProbeTimeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(dctx, network, net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if network != "udp" {
		return nil // TCP handshake completed → open.
	}
	return quicVersionNegotiationProbe(conn)
}

// quicVersionNegotiationProbe sends one QUIC long-header packet carrying a
// reserved ("greased") version and waits for the Version Negotiation packet a
// conforming server owes in reply.
//
// WHY THIS RATHER THAN A ONE-BYTE POKE: a connectionless socket gives the
// caller no signal at all unless the peer chooses to answer. The previous
// implementation wrote one 0x00 byte, which no QUIC server will ever reply to,
// and then reported the inevitable timeout as OPEN — so for every Hysteria2 and
// TUIC node the probe could only ever return "up", including when the host was
// gone entirely. Version negotiation is the one QUIC exchange that requires no
// keys, no ALPN and no valid connection ID, so it is the only thing we can ask
// a proxy server that it will answer without credentials.
//
// VERIFIED against a real quic-go server (2026-09-09) — the stack BOTH
// Hysteria2 and TUIC are built on. It answered this exact datagram with 35
// bytes whose version field was 0x00000000, i.e. a genuine Version Negotiation
// packet. So a healthy unobfuscated node reads as ok, not as inconclusive;
// without that check this probe could have reported the whole UDP fleet
// unprobeable and looked principled while doing it.
//
// The three outcomes are all real and all distinct:
//   - a reply            → a QUIC server is listening (open)
//   - ICMP unreachable   → nothing is listening (closed)
//   - silence            → INCONCLUSIVE. A filtered port, a dead host, and a
//     live Hysteria2 with salamander obfuscation enabled (which is *designed*
//     to look like nothing to an unauthenticated stranger) are the same
//     observation from here. Saying "up" would be a guess dressed as a fact.
func quicVersionNegotiationProbe(conn net.Conn) error {
	pkt, err := buildQUICVersionNegotiationTrigger()
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(healthProbeTimeout))
	if _, err := conn.Write(pkt); err != nil {
		return err
	}
	// A Version Negotiation packet carries the server's whole supported-version
	// list, so size the buffer generously rather than truncating the datagram.
	buf := make([]byte, 1500)
	if _, err := conn.Read(buf); err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return errProbeInconclusive
		}
		return err // ECONNREFUSED (ICMP port-unreachable) → closed
	}
	// Any datagram back means something on that port answered a QUIC packet.
	// The contents are deliberately not validated: a stricter check would buy
	// nothing (we already know only a listener could have replied) and would
	// risk calling a healthy non-conforming server dead.
	return nil
}

// buildQUICVersionNegotiationTrigger assembles the probe datagram: a QUIC long
// header (RFC 9000 §17.2) with a reserved version and random connection IDs,
// padded to quicProbeDatagramSize.
//
// Connection IDs come from crypto/rand, not math/rand: they are echoed back in
// the reply, so predictable ones would let an off-path observer forge a
// response and make a dead node look alive.
func buildQUICVersionNegotiationTrigger() ([]byte, error) {
	const cidLen = 8
	cids := make([]byte, 2*cidLen)
	if _, err := rand.Read(cids); err != nil {
		return nil, fmt.Errorf("generate probe connection ids: %w", err)
	}
	pkt := make([]byte, 0, quicProbeDatagramSize)
	// Header form (1) + fixed bit (1) + type/type-specific bits. 0xC0 sets the
	// two bits that mark this a long-header packet; the remaining bits are
	// version-specific and unread by a server that does not know the version.
	pkt = append(pkt, 0xC0)
	pkt = binary.BigEndian.AppendUint32(pkt, quicGreaseVersion)
	pkt = append(pkt, cidLen)
	pkt = append(pkt, cids[:cidLen]...)
	pkt = append(pkt, cidLen)
	pkt = append(pkt, cids[cidLen:]...)
	// Pad to the minimum datagram size; see quicProbeDatagramSize.
	for len(pkt) < quicProbeDatagramSize {
		pkt = append(pkt, 0)
	}
	return pkt, nil
}

// CheckOnce probes every enabled node and updates its HealthState. Disabled
// nodes and separators are skipped. Errors per node / per panel are logged but
// don't abort the pass.
func (s *Service) CheckOnce(ctx context.Context) error {
	allNodes, err := s.nodes.List(ctx)
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}

	now := time.Now()
	type nodeProbe struct {
		node        *domain.Node
		directState domain.NodeHealthState
		directError string
		relays      []domain.RelayHealth
	}
	type target struct {
		owner     *nodeProbe
		relaySlot int // -1 = landing/direct endpoint
		host      string
		port      int
		network   string
	}
	var states []*nodeProbe
	var targets []target

	for _, n := range allNodes {
		if !n.Enabled || n.IsSeparator() {
			continue
		}
		state := &nodeProbe{node: n}
		states = append(states, state)
		network := "tcp"
		if isUDPProtocol(n.Protocol) {
			network = "udp"
		}
		// v3.5: port / protocol are the node row's own authoritative columns
		// — populated by the inbound write-through paths and aligned by
		// reconcile axis A. No ListInbounds, no per-panel grouping needed.
		if n.ServerAddress == "" || n.Port <= 0 {
			// Pre-v3.5 row that never got its port captured, or a freshly-
			// imported node before reconcile backfills it. Report unreachable
			// so the UI surfaces "no signal" rather than a stale green dot.
			state.directState = domain.NodeHealthUnreachable
			state.directError = "no known port to probe (awaiting inbound config capture)"
		} else {
			targets = append(targets, target{owner: state, relaySlot: -1, host: n.ServerAddress, port: n.Port, network: network})
		}

		// Relay probes are opt-in, except HideDirect forces them on. Only
		// enabled lines are user-visible/probed. Their snapshot is separate
		// from Relays so this worker never overwrites admin-owned config.
		//
		// Scope of a relay "ok": this measures FRONT-EDGE reachability only —
		// the relay's ServerAddress:Port accepts a connection. It does NOT
		// verify the relay actually tunnels to the landing, nor that TLS /
		// Reality / WS / credentials succeed. For CDN-fronted relays a healthy
		// dot can therefore mean "the edge is up", not "the transit works".
		if n.EffectiveShowRelayStatus() {
			for relayIndex, relay := range n.Relays {
				if !relay.Enabled {
					continue
				}
				port := relay.Port
				if port <= 0 {
					port = n.Port
				}
				state.relays = append(state.relays, domain.RelayHealth{
					Index: relayIndex, Address: relay.Address, Port: port, CheckedAt: &now,
				})
				slot := len(state.relays) - 1
				if strings.TrimSpace(relay.Address) == "" || port <= 0 {
					state.relays[slot].State = domain.NodeHealthUnreachable
					continue
				}
				targets = append(targets, target{owner: state, relaySlot: slot, host: relay.Address, port: port, network: network})
			}
		}
	}

	sem := make(chan struct{}, healthProbeConcurrency)
	var wg sync.WaitGroup
	for _, tg := range targets {
		tg := tg
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			defer safego.Recover("health.port-probe")
			addr := net.JoinHostPort(tg.host, strconv.Itoa(tg.port))
			err := s.probe(ctx, tg.network, tg.host, tg.port)
			state := domain.NodeHealthOK
			switch {
			case errors.Is(err, errProbeInconclusive):
				// "Could not tell" is its own answer. Folding it into ok would
				// re-create the exact defect this branch exists to fix; folding
				// it into unreachable would alert forever on an obfuscated node
				// nobody can probe.
				state = domain.NodeHealthInconclusive
			case err != nil:
				state = domain.NodeHealthUnreachable
			}
			if tg.relaySlot >= 0 {
				tg.owner.relays[tg.relaySlot].State = state
				return
			}
			tg.owner.directState = state
			if err != nil {
				detail := fmt.Sprintf("%s %s: %v", tg.network, addr, err)
				if errors.Is(err, errProbeInconclusive) {
					// Without this the admin sees a non-green dot and no way to
					// tell an unprobeable-by-design node from a broken one.
					detail += " (no QUIC version-negotiation reply: a filtered port, a dead host, or a server with obfuscation enabled — this probe cannot distinguish them)"
				}
				tg.owner.directError = detail
			}
		}()
	}
	wg.Wait()
	for _, state := range states {
		state.node.RelayHealth = state.relays
		s.persist(ctx, state.node, state.node.Port, state.node.Protocol,
			state.directState, state.directError, now)
	}
	return nil
}

func (s *Service) persist(ctx context.Context, n *domain.Node, port int, proto string, state domain.NodeHealthState, detail string, at time.Time) {
	n.HealthState = state
	n.HealthDetail = detail
	n.HealthCheckedAt = &at // always stamped so "last checked" reflects the real probe time
	n.Port = port
	n.Protocol = proto
	if err := s.nodes.UpdateHealth(ctx, n); err != nil {
		// Don't propagate — one stuck node row mustn't block updates for
		// the rest of the fleet.
		log.Warn("health checker persist", "node_id", n.ID, "err", err)
	}
}

// Loop runs CheckOnce on a fixed interval until ctx is cancelled. Designed
// to be launched as a background goroutine from app startup.
func (s *Service) Loop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		log.Warn("health checker disabled (interval <= 0)")
		return
	}
	// Run once immediately so admins don't have to wait a full interval
	// for the first dot to appear after panel boot.
	if err := s.CheckOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Warn("health checker initial run", "err", err)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.CheckOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				log.Warn("health checker tick", "err", err)
			}
		}
	}
}
