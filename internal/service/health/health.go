// Package health probes each enabled node and persists the outcome on the Node
// row so the admin UI / user portal can show a live status dot without hitting
// 3X-UI directly.
//
// The health verdict is pure reachability — "is the proxy port open?" — not a
// 3X-UI control-plane check:
//   - TCP protocols (VLESS/VMess/Trojan/Shadowsocks): a TCP connect to
//     ServerAddress:Port. Connect succeeds → up; refused/timeout → down.
//   - UDP-only protocols (Hysteria2): a best-effort UDP probe. UDP is
//     connectionless, so this is "open|filtered" — we only call it down when
//     the OS surfaces an ICMP port-unreachable; otherwise it's treated as up.
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
// Hysteria2 / QUIC family.
func isUDPProtocol(proto string) bool {
	p := strings.ToLower(strings.TrimSpace(proto))
	return p == string(domain.ProtoHysteria2) || strings.Contains(p, "hysteria") || p == "hy2"
}

// portOpen probes host:port and returns nil when the port is open. For TCP a
// successful connect is definitive. For UDP it's a best-effort "open|filtered":
// a connectionless socket can only prove closed when the OS reports an ICMP
// port-unreachable (surfaced as a write/read error); silence is treated as open.
func portOpen(ctx context.Context, network, host string, port int) error {
	dctx, cancel := context.WithTimeout(ctx, healthProbeTimeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(dctx, network, net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	defer conn.Close()
	if network != "udp" {
		return nil // TCP handshake completed → open.
	}
	// UDP: poke the port and watch for an ICMP refusal. No reply within the
	// deadline = open or filtered, which we report as open.
	_ = conn.SetDeadline(time.Now().Add(healthProbeTimeout))
	if _, err := conn.Write([]byte{0}); err != nil {
		return err
	}
	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return nil // open|filtered
		}
		return err // ECONNREFUSED (ICMP port-unreachable) → closed
	}
	return nil
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
	type target struct {
		n       *domain.Node
		host    string
		network string
	}
	var targets []target

	for _, n := range allNodes {
		if !n.Enabled || n.IsSeparator() {
			continue
		}
		// v3.5: port / protocol are the node row's own authoritative columns
		// — populated by the inbound write-through paths and aligned by
		// reconcile axis A. No ListInbounds, no per-panel grouping needed.
		if n.ServerAddress == "" || n.Port <= 0 {
			// Pre-v3.5 row that never got its port captured, or a freshly-
			// imported node before reconcile backfills it. Report unreachable
			// so the UI surfaces "no signal" rather than a stale green dot.
			s.persist(ctx, n, n.Port, n.Protocol, domain.NodeHealthUnreachable,
				"no known port to probe (awaiting inbound config capture)", now)
			continue
		}
		network := "tcp"
		if isUDPProtocol(n.Protocol) {
			network = "udp"
		}
		targets = append(targets, target{n: n, host: n.ServerAddress, network: network})
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
			addr := net.JoinHostPort(tg.host, strconv.Itoa(tg.n.Port))
			if err := s.probe(ctx, tg.network, tg.host, tg.n.Port); err != nil {
				s.persist(ctx, tg.n, tg.n.Port, tg.n.Protocol, domain.NodeHealthUnreachable,
					fmt.Sprintf("%s %s: %v", tg.network, addr, err), now)
				return
			}
			s.persist(ctx, tg.n, tg.n.Port, tg.n.Protocol, domain.NodeHealthOK, "", now)
		}()
	}
	wg.Wait()
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
