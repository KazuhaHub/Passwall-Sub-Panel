// Package linkprobe runs the panel's side of link quality self-tests
// (transit.probe.v1): it decides what an entry server is asked to probe, takes
// what it measured, and turns the measurements into a state per link or forward
// with hysteresis for withholding.
//
// The agent probes the links it is the entry of (end to end through the exit,
// and transport only) and, optionally, its forwards. PSP's rules:
//
//   - The block is sent only to an agent that declared transit.probe.v1, only
//     while probing is on, and only to a server that has a link or a forward to
//     probe. Otherwise the config is byte-for-byte what it was.
//   - A result is accepted only for an object in the config PSP sent that
//     server, on the side that probes it: links in LinksOut (e2e, transport) and
//     forwards (forward). Anything else is dropped.
//   - A result is a window of attempts. All of at least three failing is down,
//     a fifth or more failing is degraded; the flag that withholds an object
//     needs N consecutive down windows and clears after N ok ones, and only
//     while withholding is on. Unknown (no attempts, or stale) never withholds.
package linkprobe

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Method decides how a forward is probed. It is the one place the choice is
// made, so the front routes of a later phase only add an input:
//
//   - tls when the forward is the target of an SNI front's route (a TCP connect
//     would only show that the front is up);
//   - quic when the landing listener is UDP only (Hysteria2, TUIC);
//   - tcp otherwise.
func Method(landingUDPOnly, frontRouteTarget bool) string {
	switch {
	case frontRouteTarget:
		return nodeprotocol.ProbeMethodTLS
	case landingUDPOnly:
		return nodeprotocol.ProbeMethodQUIC
	}
	return nodeprotocol.ProbeMethodTCP
}

// UDPOnlyProtocol reports whether a landing protocol has no TCP listener.
func UDPOnlyProtocol(protocol string) bool {
	switch domain.Protocol(protocol) {
	case domain.ProtoHysteria2, domain.ProtoTUIC:
		return true
	}
	return false
}

// Service is the probe coordinator.
type Service struct {
	settings ports.ScopedSettings
	repo     ports.TransitProbeRepo
	now      func() time.Time
}

// New returns the coordinator. now is time.Now when nil.
func New(settings ports.ScopedSettings, repo ports.TransitProbeRepo, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{settings: settings, repo: repo, now: now}
}

func (s *Service) load(ctx context.Context) (ports.UISettings, error) {
	return s.settings.Load(ctx, ports.UISettings{
		NodePollSeconds: nodeprotocol.DefaultNextPollSeconds, FullReportSeconds: nodeprotocol.DefaultFullReportSeconds,
	})
}

// Effective is the probe settings the panel runs with.
func (s *Service) Effective(ctx context.Context) (ports.TransitProbeEffective, error) {
	settings, err := s.load(ctx)
	if err != nil {
		return ports.TransitProbeEffective{}, fmt.Errorf("linkprobe: load settings: %w", err)
	}
	return ports.TransitProbeEffectiveOf(settings), nil
}

// Config is the Probe block for one server's transit body, or nil when it
// must not be sent. transit is the body without a probe; methods gives the
// method of each forward in it (a forward without one is probed with tcp).
//
// THE CAPABILITY GATE IS HERE and is the whole point: an agent that does not
// know the field would leave the panel showing "never probed" for a line nobody
// measures, so the block goes only to an agent that declared it.
func (s *Service) Config(ctx context.Context, capabilities []string, transit *nodeprotocol.Transit,
	methods map[nodeprotocol.ForwardKey]string) (*nodeprotocol.ProbeSettings, error) {
	if !slices.Contains(capabilities, nodeprotocol.CapabilityTransitProbe) {
		return nil, nil
	}
	if transit == nil || (len(transit.LinksOut) == 0 && len(transit.Forwards) == 0) {
		return nil, nil
	}
	eff, err := s.Effective(ctx)
	if err != nil {
		return nil, err
	}
	if !eff.Enabled {
		return nil, nil
	}
	probe := &nodeprotocol.ProbeSettings{IntervalSeconds: eff.IntervalSeconds, TimeoutMS: eff.TimeoutMS}
	if len(transit.LinksOut) > 0 {
		probe.LinkURL = eff.URL
	}
	for _, forward := range transit.Forwards {
		method := methods[forward.Key]
		if method == "" {
			method = nodeprotocol.ProbeMethodTCP
		}
		probe.Forwards = append(probe.Forwards, nodeprotocol.ProbeTarget{Key: forward.Key, Method: method})
	}
	// What is sent must be what the contract accepts: a block it would refuse
	// fails the whole config, so it is withheld instead, and logged.
	candidate := *transit
	candidate.Probe = probe
	if err := nodeprotocol.ValidateTransit(&candidate); err != nil {
		warnOncePerMinute("link probe settings withheld: the contract refuses them", err)
		return nil, nil
	}
	return probe, nil
}

var (
	warnMu   sync.Mutex
	warnLast = map[string]time.Time{}
)

// warnOncePerMinute logs a refused probe block at most once a minute per
// message: it is worked out on every sync of every server, and one bad setting
// would otherwise fill the log.
func warnOncePerMinute(msg string, err error) {
	warnMu.Lock()
	key := msg + "|" + err.Error()
	due := time.Since(warnLast[key]) >= time.Minute
	if due && len(warnLast) >= 256 {
		for k, at := range warnLast {
			if time.Since(at) >= time.Minute {
				delete(warnLast, k)
			}
		}
	}
	if due {
		warnLast[key] = time.Now()
	}
	warnMu.Unlock()
	if due {
		log.Warn(msg, "err", err)
	}
}

// Accepted filters a full report's probe results down to what this server may
// report: an object in the config PSP sent it, probed on the side that probes
// it, with a probe block in what was sent at all. A link is probed by its entry
// (LinksOut) as e2e (only when a URL was sent) and transport; a forward as
// forward. Everything else is dropped, and the second return value counts what.
func Accepted(sent *nodeprotocol.Transit, results []nodeprotocol.ProbeResult) (kept []nodeprotocol.ProbeResult, dropped int) {
	if sent == nil || sent.Probe == nil {
		return nil, len(results)
	}
	links := make(map[string]bool, len(sent.LinksOut))
	for _, out := range sent.LinksOut {
		links[string(out.Key)] = true
	}
	forwards := make(map[string]bool, len(sent.Probe.Forwards))
	inConfig := make(map[nodeprotocol.ForwardKey]bool, len(sent.Forwards))
	for _, forward := range sent.Forwards {
		inConfig[forward.Key] = true
	}
	for _, target := range sent.Probe.Forwards {
		if inConfig[target.Key] {
			forwards[string(target.Key)] = true
		}
	}
	for _, result := range results {
		ok := false
		switch result.Kind {
		case nodeprotocol.ProbeKindE2E:
			ok = links[result.Key] && sent.Probe.LinkURL != ""
		case nodeprotocol.ProbeKindTransport:
			ok = links[result.Key]
		case nodeprotocol.ProbeKindForward:
			ok = forwards[result.Key]
		}
		if ok {
			kept = append(kept, result)
		} else {
			dropped++
		}
	}
	return kept, dropped
}

// Ingest books the probe results of one full report from one server. sent is
// the config the panel sent that server; capabilities are the report's. It
// reports whether any object's withheld flag changed, so the caller refreshes
// subscriptions on a transition only.
func (s *Service) Ingest(ctx context.Context, panelID int64, capabilities []string, sent *nodeprotocol.Transit,
	results []nodeprotocol.ProbeResult, agentReportedMS int64) (bool, error) {
	if sent == nil || len(results) == 0 || !slices.Contains(capabilities, nodeprotocol.CapabilityTransitProbe) {
		return false, nil
	}
	settings, err := s.load(ctx)
	if err != nil {
		return false, fmt.Errorf("linkprobe: load settings: %w", err)
	}
	eff := ports.TransitProbeEffectiveOf(settings)
	if !eff.Enabled {
		return false, nil
	}
	kept, dropped := Accepted(sent, results)
	if dropped > 0 {
		warnOncePerMinute("link probe results dropped: not in the config sent to this server", fmt.Errorf("panel %d", panelID))
	}
	now := s.now().UTC()
	hysteresis := domain.ProbeHysteresis{Withhold: eff.Withhold, DownAfter: eff.WithholdAfter, OKAfter: eff.RestoreAfter}
	validFor := StaleAfter(eff, nodeprotocol.EffectiveFullReportPeriod(settings.FullReportSeconds, settings.NodePollSeconds))
	changed := false
	// A window counts toward a run when it is new: different from the stored one,
	// or the stored one is a probe interval old (a link that stays down reports
	// the same window every time, and still has been probed since). A little
	// slack keeps a report that arrives just early from being skipped.
	interval := eff.Interval() - eff.Interval()/10
	for _, result := range kept {
		prev, next, err := s.repo.Apply(ctx, panelID, result.Key, result.Kind, func(prev *domain.ProbeState) (domain.ProbeState, bool, error) {
			next := domain.ProbeState{
				Attempts: result.Attempts, Failures: result.Failures,
				RTTMinMS: result.RTTMinMS, RTTMedianMS: result.RTTMedianMS, RTTMaxMS: result.RTTMaxMS,
				LastError: result.LastError, LastSuccessMS: result.LastSuccessMS, LoopbackPort: result.LoopbackPort,
				ReportedAt: now, ValidUntil: now.Add(validFor), AgentReportedMS: agentReportedMS,
			}
			// Only an exact repeat (the same agent timestamp and the same
			// window) is a replay. The agent's clock is never ordered: one that
			// stepped back, or once ran far ahead, must not freeze the row. A
			// report without a timestamp is never taken for a replay.
			if prev != nil && agentReportedMS != 0 && prev.AgentReportedMS == agentReportedMS && prev.LoopbackPort == next.LoopbackPort && prev.SameWindow(next) {
				return domain.ProbeState{}, false, nil
			}
			counts := prev == nil || prev.StaleAt(now) || !prev.SameWindow(next) || now.Sub(prev.CountedAt) >= interval
			if counts {
				next.State, next.Consecutive, next.StateSince, next.Withheld = hysteresis.Advance(prev, domain.ClassifyProbe(result.Attempts, result.Failures), now)
				next.CountedAt = now
				return next, true, nil
			}
			// The same window again: the object is still reporting, nothing new
			// was measured.
			next.State, next.Consecutive, next.StateSince, next.CountedAt = prev.State, prev.Consecutive, prev.StateSince, prev.CountedAt
			next.Withheld = prev.Withheld && hysteresis.Withhold
			return next, true, nil
		})
		if err != nil {
			return changed, fmt.Errorf("linkprobe: record %s %s: %w", result.Key, result.Kind, err)
		}
		if next.Withheld != (prev != nil && prev.Withheld) {
			changed = true
		}
	}
	keep := make([]string, 0, len(sent.LinksOut)+len(sent.Forwards))
	for _, out := range sent.LinksOut {
		keep = append(keep, string(out.Key))
	}
	for _, forward := range sent.Forwards {
		keep = append(keep, string(forward.Key))
	}
	if _, err := s.repo.PruneExcept(ctx, panelID, keep); err != nil {
		return changed, fmt.Errorf("linkprobe: prune: %w", err)
	}
	return changed, nil
}

// StaleAfter is how long a window stays current: three report intervals, where
// an interval is the slower of the probe round and the cadence full reports
// arrive at (a window cannot be fresher than the report that carries it).
func StaleAfter(eff ports.TransitProbeEffective, fullReportPeriodSeconds int) time.Duration {
	interval := eff.Interval()
	if period := time.Duration(fullReportPeriodSeconds) * time.Second; period > interval {
		interval = period
	}
	return domain.ProbeStaleIntervals * interval
}

// Item is one stored result with what it is worth now.
type Item struct {
	domain.ProbeState
	// Health is the state to show: unknown when stale.
	Health domain.ProbeHealth
	// Stale says the window is too old to say anything.
	Stale bool
	// WithheldNow says the object is withheld from subscriptions at this moment.
	WithheldNow bool
	// LossPercent is the failed share of the window.
	Loss int
}

// List returns the stored results of one server (every server when panelID is
// 0), evaluated now.
func (s *Service) List(ctx context.Context, panelID int64) ([]Item, error) {
	rows, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("linkprobe: list: %w", err)
	}
	now := s.now()
	out := make([]Item, 0, len(rows))
	for _, row := range rows {
		if panelID != 0 && row.PanelID != panelID {
			continue
		}
		out = append(out, Item{ProbeState: row, Health: row.HealthAt(now), Stale: row.StaleAt(now),
			WithheldNow: row.WithheldAt(now), Loss: row.LossPercent()})
	}
	return out, nil
}
