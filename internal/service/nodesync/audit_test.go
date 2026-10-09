package nodesync

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestAuditNeverRemainsInControlReportClones(t *testing.T) {
	for _, clone := range []struct {
		name string
		run  func(protocol.NodeReport) protocol.NodeReport
	}{{"full", cloneReport}, {"observation", cloneObservationReport}} {
		t.Run(clone.name, func(t *testing.T) {
			report := protocol.NodeReport{AgentID: "agt_audit", Have: emptyProtocolHave(), Audit: &protocol.AuditObservation{BatchID: strings.Repeat("a", 32), Kind: "block", Hour: 1_800_000_000_000, CollectRevision: 1, Dropped: 1}}
			out := clone.run(report)
			if out.Audit != nil {
				t.Fatal("transient audit batch retained in control cache")
			}
			if report.Audit == nil || out.AgentID != report.AgentID || len(out.Have) != 3 {
				t.Fatal("cloning changed input or removed control")
			}
		})
	}
}

type auditCollectorFunc func(string, int64, time.Time, protocol.AuditObservation)

func (f auditCollectorFunc) Offer(agent string, panel int64, received time.Time, b protocol.AuditObservation) {
	f(agent, panel, received, b)
}

func auditOfferReport(f *configAppliedFixture) protocol.NodeReport {
	return protocol.NodeReport{AgentID: f.agent.AgentID, ProtocolVersion: protocol.ProtocolVersion1, Have: emptyProtocolHave(), ReportedAtMS: f.now.UnixMilli(), Capabilities: []string{protocol.CapabilityAuditHits}, Audit: &protocol.AuditObservation{BatchID: strings.Repeat("a", 32), Kind: "block", Hour: f.now.Truncate(time.Hour).UnixMilli(), CollectRevision: 1, Dropped: 1}}
}

func TestAuditOfferFollowsResponseValidationAndAgentUnlock(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(map[bool]string{false: "partial", true: "full"}[full], func(t *testing.T) {
			f := newConfigAppliedFixture(t)
			report := auditOfferReport(f)
			report.Partial = !full
			baseline := report
			baseline.Audit = nil
			before, err := f.service.Sync(t.Context(), baseline)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			f.service.audit = auditCollectorFunc(func(agent string, panel int64, received time.Time, b protocol.AuditObservation) {
				calls++
				if f.service.agentLocks.Len() != 0 {
					t.Error("audit offer held agent lock")
				}
				if agent != f.agent.AgentID || panel != f.agent.PanelID || !received.Equal(f.now) || b.BatchID != report.Audit.BatchID {
					t.Error("audit identity or server receipt changed")
				}
			})
			after, err := f.service.Sync(t.Context(), report)
			if err != nil || calls != 1 {
				t.Fatalf("valid telemetry calls%d error%v", calls, err)
			}
			a, _ := json.Marshal(before)
			b, _ := json.Marshal(after)
			if string(a) != string(b) {
				t.Fatal("best-effort telemetry changed control response")
			}
			f.service.mu.RLock()
			cached, found := f.service.reports[f.agent.AgentID]
			f.service.mu.RUnlock()
			if cached.report.Audit != nil || (full && !found) {
				t.Fatal("transient audit retained in full-report cache")
			}
			if report.Audit == nil {
				t.Fatal("sync mutated caller telemetry")
			}
		})
	}
}

func TestAuditOfferIsOptionalAndRejectsOnlyUnsupportedTelemetry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*protocol.NodeReport)
		want   int
	}{
		{"absent", func(r *protocol.NodeReport) { r.Audit = nil }, 0},
		{"missing_hits_capability", func(r *protocol.NodeReport) { r.Capabilities = nil }, 0},
		{"invalid_subtree", func(r *protocol.NodeReport) { r.Audit.BatchID = "bad" }, 0},
		{"usage_missing_capability", func(r *protocol.NodeReport) { r.Audit.Kind = "usage" }, 0},
		{"usage_counter_only", func(r *protocol.NodeReport) {
			r.Audit.Kind = "usage"
			r.Capabilities = append(r.Capabilities, protocol.CapabilityAuditUsage)
		}, 1},
		{"hits_counter_only", func(*protocol.NodeReport) {}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newConfigAppliedFixture(t)
			r := auditOfferReport(f)
			tc.mutate(&r)
			calls := 0
			f.service.audit = auditCollectorFunc(func(string, int64, time.Time, protocol.AuditObservation) { calls++ })
			if _, err := f.service.Sync(t.Context(), r); err != nil || calls != tc.want {
				t.Fatalf("isolated telemetry calls%d error%v", calls, err)
			}
			f.service.audit = nil
			if _, err := f.service.Sync(t.Context(), r); err != nil {
				t.Fatal("nil collector broke control")
			}
		})
	}
}

type auditFailDesired struct {
	ports.NativeDesiredSnapshotRepo
}

func (r auditFailDesired) Load(context.Context, int64) (*ports.NativeDesiredSnapshot, error) {
	return nil, errors.New("control storage failed")
}

func TestAuditOfferNeverRunsForFailedControl(t *testing.T) {
	f := newConfigAppliedFixture(t)
	calls := 0
	f.service.audit = auditCollectorFunc(func(string, int64, time.Time, protocol.AuditObservation) { calls++ })
	r := auditOfferReport(f)
	invalid := r
	invalid.AgentID = ""
	if _, err := f.service.Sync(t.Context(), invalid); err == nil {
		t.Fatal("invalid control accepted")
	}
	f.service.desired = auditFailDesired{}
	if _, err := f.service.Sync(t.Context(), r); err == nil {
		t.Fatal("control storage fault ignored")
	}
	if calls != 0 || f.service.agentLocks.Len() != 0 {
		t.Fatal("failed control offered audit or leaked lock")
	}
}

func TestAuditOfferFreezesReceiptBeforeWaitingForAgentLock(t *testing.T) {
	f := newConfigAppliedFixture(t)
	r := auditOfferReport(f)
	initial := f.now
	clockCalled := make(chan struct{}, 1)
	f.service.now = func() time.Time {
		now := f.now
		select {
		case clockCalled <- struct{}{}:
		default:
		}
		return now
	}
	offered := make(chan time.Time, 1)
	f.service.audit = auditCollectorFunc(func(_ string, _ int64, received time.Time, _ protocol.AuditObservation) { offered <- received })
	unlock := f.service.agentLocks.Lock(f.agent.AgentID)
	defer unlock()
	done := make(chan error, 1)
	go func() { _, err := f.service.Sync(t.Context(), r); done <- err }()
	select {
	case <-clockCalled:
	case <-time.After(time.Second):
		unlock()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
		t.Fatal("receipt waited for agent lock")
	}
	f.now = f.now.Add(3 * time.Hour)
	unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("sync never released")
	}
	select {
	case received := <-offered:
		if !received.Equal(initial) {
			t.Fatal("receipt moved while waiting for control")
		}
	default:
		t.Fatal("valid audit not offered")
	}
}

func TestNewConfiguresOptionalAuditCollector(t *testing.T) {
	f := newConfigAppliedFixture(t)
	s := f.service
	collector := auditCollectorFunc(func(string, int64, time.Time, protocol.AuditObservation) {})
	configured, err := New(Options{Desired: s.desired, Agents: s.agents, Issues: s.issues, Tasks: s.tasks, Users: s.users, Clients: s.clients, Nodes: s.nodes, Settings: s.settings, CoreCatalog: s.coreCatalog, Audit: collector})
	if err != nil || configured.audit == nil {
		t.Fatal("audit collector option was discarded")
	}
}
