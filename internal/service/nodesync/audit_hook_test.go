package nodesync

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/corefixtures"
)

// The audit subtree is hit data: it leaves the control path at the top of Sync
// (like the host telemetry), is never kept in the latest-report cache, and is
// handed to the ingest queue only AFTER the response is built, so no failure of
// the store can change what the node is told.

type fakeAudit struct {
	calls   []fakeAuditCall
	onEnq   func()
	enqueue int
}

type fakeAuditCall struct {
	agentID string
	panelID int64
	obs     *nodeprotocol.AuditObservation
}

func (f *fakeAudit) Enqueue(agentID string, panelID int64, o *nodeprotocol.AuditObservation) {
	f.enqueue++
	f.calls = append(f.calls, fakeAuditCall{agentID, panelID, o})
	if f.onEnq != nil {
		f.onEnq()
	}
}

func someAudit() *nodeprotocol.AuditObservation {
	return &nodeprotocol.AuditObservation{BatchID: strings.Repeat("a", 32), Hits: []nodeprotocol.AuditHit{{
		Hour: 3600000, RuleID: "p1", Action: "block", Subject: "usr_1", Dest: "a.example", Port: 1, Count: 1, FirstMS: 3600000, LastMS: 3600000,
	}}}
}

func TestAuditHook_IsEnqueuedForTheAgentsPanelAndStrippedFromTheCache(t *testing.T) {
	f := newConfigAppliedFixture(t)
	sink := &fakeAudit{}
	f.service.SetAudit(sink)
	if _, err := f.syncWith(t, func(r *nodeprotocol.NodeReport) {
		r.Capabilities = []string{nodeprotocol.CapabilityAuditHits}
		r.Audit = someAudit()
	}); err != nil {
		t.Fatal(err)
	}
	if len(sink.calls) != 1 || sink.calls[0].agentID != f.agent.AgentID || sink.calls[0].panelID != f.agent.PanelID || sink.calls[0].obs.BatchID != strings.Repeat("a", 32) {
		t.Fatalf("calls = %+v", sink.calls)
	}
	f.service.mu.RLock()
	cached := f.service.reports[f.agent.AgentID].report
	f.service.mu.RUnlock()
	if cached.Audit != nil {
		t.Fatal("the audit subtree was retained in the latest-full cache")
	}
}

func TestAuditHook_ASyncWithoutAuditEnqueuesNothing(t *testing.T) {
	f := newConfigAppliedFixture(t)
	sink := &fakeAudit{}
	f.service.SetAudit(sink)
	if _, err := f.syncWith(t, nil); err != nil {
		t.Fatal(err)
	}
	if sink.enqueue != 0 {
		t.Fatalf("enqueued %d for a report with no audit", sink.enqueue)
	}
}

// A sync that fails is retried by the node with the same batch: handing the
// batch over before the response exists would store it and then see it again.
func TestAuditHook_AFailedSyncEnqueuesNothing(t *testing.T) {
	f := newConfigAppliedFixture(t)
	sink := &fakeAudit{}
	f.service.SetAudit(sink)
	f.service.SetPolicies(&fakePolicies{err: errors.New("boom")})
	if _, err := f.syncWith(t, func(r *nodeprotocol.NodeReport) {
		r.Capabilities = []string{nodeprotocol.CapabilityDestinationPolicy, nodeprotocol.CapabilityAuditHits}
		r.Audit = someAudit()
	}); err == nil {
		t.Fatal("setup: the compile was meant to fail")
	}
	if sink.enqueue != 0 {
		t.Fatalf("a failed sync enqueued %d batches", sink.enqueue)
	}
}

func TestAuditHook_AbsentSinkIsInert(t *testing.T) {
	f := newConfigAppliedFixture(t)
	if _, err := f.syncWith(t, func(r *nodeprotocol.NodeReport) {
		r.Capabilities = []string{nodeprotocol.CapabilityAuditHits}
		r.Audit = someAudit()
	}); err != nil {
		t.Fatal(err)
	}
}

// The panel's recording choice reaches the compiler through the very panel row
// the sync already reads for its observation.
func TestAuditHook_ThePanelsCollectLevelIsPassedToTheCompiler(t *testing.T) {
	f := newConfigAppliedFixture(t)
	panel := &domain.Panel{Kind: domain.PanelKindPSP, Name: "collect-panel", URL: "psp://agt_collect"}
	if err := f.repos.XUIPanel.Save(t.Context(), panel); err != nil {
		t.Fatal(err)
	}
	agent := &domain.NodeAgent{
		AgentID: "agt_collect", PanelID: panel.ID, Epoch: 1, CredentialSHA256: strings.Repeat("b", 64),
		DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: "26.6.27",
	}
	if err := f.repos.NodeAgent.Create(t.Context(), agent); err != nil {
		t.Fatal(err)
	}
	var err error
	f.agent = agent
	f.service, err = New(Options{CoreCatalog: corefixtures.Static{},
		Desired: f.repos.NativeDesired, Agents: f.repos.NodeAgent, Issues: f.repos.NodeAgentIssue, Tasks: f.repos.NodeAgentTask,
		Users: f.repos.User, Clients: f.repos.PSPClient, Nodes: f.repos.Node, Settings: f.repos.ScopedSettings,
		Panels: f.repos.XUIPanel, Now: func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakePolicies{}
	f.service.SetPolicies(fake)
	caps := func(r *nodeprotocol.NodeReport) {
		r.Capabilities = []string{nodeprotocol.CapabilityDestinationPolicy, nodeprotocol.CapabilityAuditHits}
	}
	if _, err := f.syncWith(t, caps); err != nil {
		t.Fatal(err)
	}
	if fake.input.AuditCollect != domain.AuditCollectHits {
		t.Fatalf("collect = %q, want the default hits", fake.input.AuditCollect)
	}
	off := domain.AuditCollectOff
	if err := f.repos.XUIPanel.(interface {
		UpdateNativeMetadata(ctx context.Context, id int64, name, remark *string, channel *domain.PanelUpdateChannel, collect *domain.AuditCollect, resolve *domain.DestResolve) error
	}).UpdateNativeMetadata(t.Context(), panel.ID, nil, nil, nil, &off, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.syncWith(t, caps); err != nil {
		t.Fatal(err)
	}
	if fake.input.AuditCollect != domain.AuditCollectOff {
		t.Fatalf("collect = %q after the admin switched recording off", fake.input.AuditCollect)
	}
}
