package nodesync

import (
	"context"
	"errors"
	"testing"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type statusAgentRepo struct {
	ports.NodeAgentRepo
	agent *domain.NodeAgent
	err   error
}

func (r *statusAgentRepo) GetByPanelID(ctx context.Context, id int64) (*domain.NodeAgent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id != r.agent.PanelID {
		return nil, domain.ErrNotFound
	}
	return r.agent, r.err
}

type statusSettings struct {
	ports.ScopedSettings
	err error
}

func (r statusSettings) Load(_ context.Context, defaults ports.UISettings) (ports.UISettings, error) {
	defaults.NodePollSeconds, defaults.FullReportSeconds = 15, 45
	return defaults, r.err
}

func TestNativePanelStatusUsesCurrentFullObservationAndSharedFreshnessGates(t *testing.T) {
	now := time.UnixMilli(100000)
	seen := now
	agents := &statusAgentRepo{agent: &domain.NodeAgent{AgentID: "owned-status", PanelID: 9, LastSeen: &seen}}
	service := &Service{agents: agents, settings: statusSettings{}, now: func() time.Time { return now }, reports: make(map[string]receivedFullReport)}
	// Desired and other repositories are deliberately absent. A status read
	// must not construct membership, credentials, streams or inbound projections.
	if _, err := service.NativePanelStatus(t.Context(), 9); !errors.Is(err, ports.ErrNativePanelSnapshotMissing) {
		t.Fatal("status without a full report was accepted")
	}
	report := receivedFullReport{receivedAtMS: now.UnixMilli(), report: protocol.NodeReport{AgentVersion: "v0.2", CoreEngine: "xray", CoreVersion: "26.6.27", CoreState: "running"}}
	service.reports[agents.agent.AgentID] = report
	want := ports.ServerStatus{PanelVersion: "v0.2", CoreEngine: "xray", XrayVersion: "26.6.27", XrayState: "running"}
	got, err := service.NativePanelStatus(t.Context(), 9)
	if err != nil || got != want {
		t.Fatal("status did not project the owned full runtime observation")
	}
	got.XrayState = "changed-by-caller"
	report.report.CoreState = "error"
	service.reports[agents.agent.AgentID] = report
	latest, err := service.NativePanelStatus(t.Context(), 9)
	if err != nil || latest.XrayState != "error" {
		t.Fatal("status reused a stale or aliased observation")
	}
	seen = now.Add(-45 * time.Second)
	report.receivedAtMS = now.Add(-60 * time.Second).UnixMilli()
	service.reports[agents.agent.AgentID] = report
	if _, err := service.NativePanelStatus(t.Context(), 9); err != nil {
		t.Fatal("status tightened the existing inclusive freshness boundaries")
	}
	for _, tc := range []struct {
		name           string
		seen, received int64
		want           error
	}{
		{"offline", now.Add(-46 * time.Second).UnixMilli(), now.UnixMilli(), ports.ErrNativePanelAgentOffline},
		{"stale", now.UnixMilli(), now.Add(-61 * time.Second).UnixMilli(), ports.ErrNativePanelSnapshotStale},
		{"future", now.UnixMilli(), now.Add(time.Millisecond).UnixMilli(), ports.ErrNativePanelSnapshotStale},
		{"missing-time", now.UnixMilli(), 0, ports.ErrNativePanelSnapshotStale},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen = time.UnixMilli(tc.seen)
			report.receivedAtMS = tc.received
			service.reports[agents.agent.AgentID] = report
			_, narrowErr := service.NativePanelStatus(t.Context(), 9)
			_, fullErr := service.NativePanelSnapshot(t.Context(), 9)
			if !errors.Is(narrowErr, tc.want) || !errors.Is(fullErr, tc.want) {
				t.Fatal("status and full snapshot disagree on freshness")
			}
		})
	}
	seen = now
	report.receivedAtMS = now.UnixMilli()
	service.reports[agents.agent.AgentID] = report
	failure := errors.New("owned status repository error")
	service.settings = statusSettings{err: failure}
	if got, err := service.NativePanelStatus(t.Context(), 9); !errors.Is(err, failure) || got != (ports.ServerStatus{}) {
		t.Fatal("status ignored cadence read failure")
	}
	service.settings = statusSettings{}
	agents.err = failure
	if got, err := service.NativePanelStatus(t.Context(), 9); !errors.Is(err, failure) || got != (ports.ServerStatus{}) {
		t.Fatal("status ignored current agent read failure")
	}
	agents.err = nil
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.NativePanelStatus(ctx, 9); !errors.Is(err, context.Canceled) {
		t.Fatal("status ignored cancelled read")
	}
	if _, err := service.NativePanelStatus(t.Context(), 10); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("status borrowed another panel's agent")
	}
}
