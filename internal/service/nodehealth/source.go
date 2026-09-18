package nodehealth

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/alert"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodemetrics"
)

// Source turns stored telemetry into the findings the bell renders.
//
// IT LIVES HERE RATHER THAN WITH THE METRICS SERVICE because the evaluator
// already depends on the derivation package, and putting the reader there would
// close an import cycle. The direction that works is also the direction that
// reads best: health reads metrics, not the other way round.
type Source struct {
	Agents  ports.NodeAgentRepo
	Metrics ports.NodeHostMetricRepo
	Now     func() time.Time
}

// evaluationWindow is how much history the state machine replays.
//
// FORTY-FIVE MINUTES COVERS THE LONGEST HOLD THIS EVALUATOR KNOWS ABOUT — the
// fifteen-minute load and swap holds — with margin for the intervals that
// precede them. A shorter window would make a condition that has been true for
// half an hour look as though it had just started.
const evaluationWindow = 45 * time.Minute

// oomWindow is how long an OOM event stays visible, per §10.4.
const oomWindow = 24 * time.Hour

// ResourceFindings evaluates every native agent and returns the active
// conditions per panel.
//
// ONE AGENT'S FAILURE IS NOT THE PASS'S. A node whose history cannot be read is
// logged and skipped: the bell is a summary of a fleet, and letting one unreadable
// node empty it would hide every other node's problems.
func (s *Source) ResourceFindings(ctx context.Context) ([]alert.NodeResourceEntry, error) {
	if s.Agents == nil || s.Metrics == nil {
		return nil, nil
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	agents, err := s.Agents.List(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]alert.NodeResourceEntry, 0, len(agents))
	for _, agent := range agents {
		if agent == nil {
			continue
		}
		entry, ok := s.evaluateAgent(ctx, agent, now)
		if !ok {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s *Source) evaluateAgent(ctx context.Context, agent *domain.NodeAgent, now time.Time) (alert.NodeResourceEntry, bool) {
	window, ok := s.buildWindow(ctx, agent, now)
	if !ok {
		return alert.NodeResourceEntry{}, false
	}
	entry := alert.NodeResourceEntry{PanelID: agent.PanelID, PanelName: agent.AgentID}
	// AN OFFLINE NODE HAS ONE PROBLEM, and it is the connectivity one. Reporting
	// its frozen metrics as thresholds would alert forever about a machine nobody
	// can currently reach.
	entry.Offline = agent.LastSeen == nil || now.Sub(*agent.LastSeen) > offlineThreshold
	if entry.Offline {
		return entry, true
	}
	capabilityObserved := observedHostTelemetry(agent)
	for _, finding := range Evaluate(window, Options{CapabilityObserved: capabilityObserved}) {
		entry.Findings = append(entry.Findings, alert.NodeResourceFinding{
			Code: finding.Code, Critical: finding.Severity == SeverityCritical, StartedAt: finding.StartedAt,
		})
	}
	s.appendOOMEvents(ctx, agent.AgentID, now, &entry)
	return entry, true
}

// buildWindow reads one agent's evaluation history and derives each point.
func (s *Source) buildWindow(ctx context.Context, agent *domain.NodeAgent, now time.Time) (Window, bool) {
	from := now.Add(-evaluationWindow)
	samples, err := s.Metrics.RawRange(ctx, agent.AgentID, from, now, true)
	if err != nil {
		log.Warn("alert: read node host history", "agent_id", agent.AgentID, "err", err)
		return Window{}, false
	}
	if len(samples) == 0 {
		// No history is not an empty window: it is a node this evaluator cannot
		// say anything about, and the caller must not read it as healthy.
		return Window{}, false
	}
	points := make([]Point, 0, len(samples))
	for index := range samples {
		var previous *domain.NodeHostMetricSample
		if index > 0 {
			previous = &samples[index-1]
		}
		points = append(points, Point{Sample: samples[index], Derived: nodemetrics.Derive(previous, &samples[index])})
	}
	period := nodemetrics.DefaultNodeHostReportInterval
	if len(samples) > 1 {
		// The ACTUAL cadence, taken from the newest gap: a freshness threshold
		// computed against the requested interval would mark a healthy node stale
		// every cycle when the node reports on a different one.
		gap := samples[len(samples)-1].ReceivedAt.Sub(samples[len(samples)-2].ReceivedAt)
		if gap > 0 {
			period = gap
		}
	}
	return Window{Points: points, Now: now, EffectivePeriod: period}, true
}

// appendOOMEvents adds an OOM that happened inside the 24-hour window.
//
// IT READS THE HOURLY TABLE RATHER THAN THE RAW ONE. Twenty-four hours of raw
// samples is over a thousand rows per agent to answer one yes-or-no question, and
// the rollup has already summed exactly that delta into a column.
func (s *Source) appendOOMEvents(ctx context.Context, agentID string, now time.Time, entry *alert.NodeResourceEntry) {
	rows, err := s.Metrics.HourlyRange(ctx, agentID, now.Add(-oomWindow), now)
	if err != nil {
		return
	}
	for _, row := range rows {
		if row.CgroupOOMDelta == nil && row.CgroupOOMKillDelta == nil {
			continue
		}
		critical := row.CgroupOOMKillDelta != nil && *row.CgroupOOMKillDelta > 0
		oomed := row.CgroupOOMDelta != nil && *row.CgroupOOMDelta > 0
		if !critical && !oomed {
			continue
		}
		entry.Findings = append(entry.Findings, alert.NodeResourceFinding{
			Code: CodeCgroupOOM, Critical: critical, StartedAt: row.BucketStart,
		})
	}
}

// offlineThreshold is how long without a report makes a node offline.
//
// It is generous relative to the telemetry cadence because it is a CONNECTIVITY
// judgement, and the sync path already has its own, tighter view of that: this
// only has to be long enough that a node reporting on a slow cadence is not
// mistaken for a dead one.
const offlineThreshold = 10 * time.Minute

// observedHostTelemetry reports whether the agent's most recent report claimed
// the capability.
//
// A node that never claimed it is UNSUPPORTED rather than stale, which is why
// this gates the staleness rule and nothing else.
func observedHostTelemetry(agent *domain.NodeAgent) bool {
	for _, capability := range agent.ObservedCapabilities {
		if capability == hostTelemetryCapability {
			return true
		}
	}
	return false
}

// hostTelemetryCapability mirrors the wire constant without importing the
// protocol package for one string.
const hostTelemetryCapability = "host.telemetry.v1"
