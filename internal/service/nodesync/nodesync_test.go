package nodesync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type panelObservationRepo struct {
	ports.XUIPanelRepo
	panel   *domain.XUIPanel
	updates int
}

type coreObservationAgentRepo struct {
	ports.NodeAgentRepo
	engine domain.NodeCoreEngine
}

func (r *coreObservationAgentRepo) UpdateCoreObservation(_ context.Context, _ string, engine domain.NodeCoreEngine) error {
	r.engine = engine
	return nil
}

func (r *panelObservationRepo) GetByID(context.Context, int64) (*domain.XUIPanel, error) {
	copy := *r.panel
	return &copy, nil
}

func (r *panelObservationRepo) UpdateVersion(_ context.Context, _ int64, panelVersion, xrayVersion string, checkedAt *time.Time) error {
	r.updates++
	r.panel.PanelVersion = panelVersion
	r.panel.XrayVersion = xrayVersion
	r.panel.VersionCheckedAt = checkedAt
	return nil
}

func TestNativeCoreObservationPersistsAndInvalidatesRenderCache(t *testing.T) {
	repo := &panelObservationRepo{panel: &domain.XUIPanel{
		ID: 9, Kind: domain.PanelKindPSP, PanelVersion: "v0.1.0", XrayVersion: "26.7.28",
	}}
	invalidations := 0
	agents := &coreObservationAgentRepo{}
	service := &Service{panels: repo, agents: agents, invalidateRender: func() { invalidations++ }}
	now := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
	agent := &domain.NodeAgent{AgentID: "agt_test", PanelID: 9}
	if err := service.recordPanelObservation(t.Context(), agent, nodeprotocol.NodeReport{
		AgentVersion: "v0.2.0", CoreEngine: "sing-box", CoreVersion: "1.14.0", CoreState: "running",
	}, now); err != nil {
		t.Fatal(err)
	}
	if repo.panel.PanelVersion != "v0.2.0" || repo.panel.XrayVersion != "1.14.0" || agents.engine != domain.NodeCoreSingBox || repo.updates != 1 || invalidations != 1 {
		t.Fatalf("persisted panel=%+v updates=%d invalidations=%d", repo.panel, repo.updates, invalidations)
	}
	if err := service.recordPanelObservation(t.Context(), agent, nodeprotocol.NodeReport{}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if repo.panel.PanelVersion != "v0.2.0" || repo.panel.XrayVersion != "1.14.0" || invalidations != 1 {
		t.Fatalf("empty observation erased state or invalidated cache: panel=%+v invalidations=%d", repo.panel, invalidations)
	}
}

func TestSyncMintsDocumentsThenIngestsAppliedObservation(t *testing.T) {
	ctx := context.Background()
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "nodesync.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := sqlstore.NewRepos(db)
	limit, ipLimit := int64(1_000), 2
	user := &domain.User{
		UPN: "native@example.test", Email: "native@example.test",
		SSOProvider: domain.SSOProviderLocal, SSOSubject: "native@example.test",
		Role: domain.RoleUser, SubToken: "test-sub-token", UUID: "11111111-1111-4111-8111-111111111111",
		Limits:             domain.LimitOverrides{TrafficLimitBytes: &limit, IPLimit: &ipLimit},
		TrafficResetPeriod: domain.ResetMonthly, Enabled: true,
	}
	if err := repos.User.Create(ctx, user); err != nil {
		t.Fatal(err)
	}
	node := &domain.Node{
		PanelID: 9, InboundID: 1, DisplayName: "native", ServerAddress: "node.example.test",
		DesiredProtocol: "vless", DesiredPort: 443, InboundListen: "0.0.0.0",
		InboundRemark: "native", InboundSettings: `{}`, StreamSettings: `{}`,
		Sniffing: `{}`, Allocate: `{}`, Region: "CA", Enabled: true,
	}
	if err := repos.Node.Create(ctx, node); err != nil {
		t.Fatal(err)
	}
	client := &domain.PSPClient{
		UserID: user.ID, PanelID: 9, Email: "native@psp.local",
		UUID: user.UUID, Password: "secret", DesiredEnable: true, DesiredMinted: true,
	}
	clientID, err := repos.PSPClient.Create(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	client.ID = clientID
	if err := repos.PSPClient.SetInbounds(ctx, client.ID, []domain.PSPClientInbound{{
		ClientID: client.ID, NodeID: node.ID, State: domain.ClientApplyPending,
	}}); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("credential"))
	agent := &domain.NodeAgent{
		AgentID: "agt_test", PanelID: 9, Epoch: 1,
		CredentialSHA256: hex.EncodeToString(digest[:]),
	}
	if err := repos.NodeAgent.Create(ctx, agent); err != nil {
		t.Fatal(err)
	}
	if err := repos.Settings.Save(ctx, ports.UISettings{NodePollSeconds: 15, FullReportSeconds: 45}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 10, 12, 0, 0, 0, time.UTC)
	currentNow := now
	service, err := New(Options{
		Desired: repos.NativeDesired, Agents: repos.NodeAgent, Issues: repos.NodeAgentIssue, Users: repos.User,
		Clients: repos.PSPClient, Nodes: repos.Node, Settings: repos.Settings,
		Now: func() time.Time { return currentNow },
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := service.Sync(ctx, nodeprotocol.NodeReport{
		AgentID: agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
		ReportedAtMS: now.UnixMilli(), Have: emptyProtocolHave(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Config.Body == nil || first.Roster.Body == nil || first.Directives.Body == nil {
		t.Fatalf("first response must carry all bodies: %+v", first)
	}
	if len(first.Config.Body.Listeners) != 1 || first.Config.Body.Listeners[0].Key != nodeprotocol.NewListenerKey(node.ID) {
		t.Fatalf("config did not use stable node row identity: %+v", first.Config.Body)
	}
	if first.Config.Body.Core.Engine != "xray" || first.Config.Body.Core.Version != "26.6.27" || first.Config.Body.Core.AllowRestrictedReality {
		t.Fatalf("config did not default to the recommended audited core: %+v", first.Config.Body.Core)
	}
	if len(first.Roster.Body.Clients) != 1 || first.Roster.Body.Clients[0].Key != nodeprotocol.NewClientKey(client.ID) {
		t.Fatalf("roster did not use stable client row identity: %+v", first.Roster.Body)
	}
	if first.Envelope.NextPollSeconds != 15 || first.Envelope.FullReportSeconds != 45 {
		t.Fatalf("agent cadence was not sourced from settings: %+v", first.Envelope)
	}
	if first.Directives.Body.Quota[0].HeadroomBytes == nil || *first.Directives.Body.Quota[0].HeadroomBytes != 0 {
		t.Fatalf("limited client with no reported counter must fail closed: %+v", first.Directives.Body.Quota[0])
	}

	have := map[string]nodeprotocol.StreamState{
		nodeprotocol.StreamConfig:     {Applied: first.Config.Version, ETag: first.Config.ETag},
		nodeprotocol.StreamRoster:     {Applied: first.Roster.Version, ETag: first.Roster.ETag},
		nodeprotocol.StreamDirectives: {Applied: first.Directives.Version, ETag: first.Directives.ETag},
	}
	second, err := service.Sync(ctx, nodeprotocol.NodeReport{
		AgentID: agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
		ReportedAtMS: now.Add(time.Second).UnixMilli(), Have: have,
		AgentVersion: "v0.1.0", CoreVersion: "v25", CoreState: "running",
		Objects: []nodeprotocol.ObjectStatus{
			{Stream: nodeprotocol.StreamConfig, Key: string(nodeprotocol.NewListenerKey(node.ID)), State: nodeprotocol.ObjectApplied, SinceVersion: first.Config.Version},
			{Stream: nodeprotocol.StreamRoster, Key: string(nodeprotocol.NewClientKey(client.ID)), State: nodeprotocol.ObjectApplied, SinceVersion: first.Roster.Version},
		},
		ListenerCounters: []nodeprotocol.ListenerCounters{{
			Key: nodeprotocol.NewListenerKey(node.ID), Present: true, UpBytes: 40, DownBytes: 60, CounterEpoch: 1,
		}},
		Clients: []nodeprotocol.ClientCounters{{
			Key: nodeprotocol.NewClientKey(client.ID), Present: true,
			UpBytes: 70, DownBytes: 80, CounterEpoch: 1, Gate: nodeprotocol.GateUnconfigured,
			LiveIPs: []string{"203.0.113.2", "203.0.113.1"},
		}},
		Issues: []nodeprotocol.Issue{{
			Code: nodeprotocol.IssueObjectRejectedTimeout, Key: string(nodeprotocol.NewClientKey(client.ID)),
			Detail: "roster object has remained rejected",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Config.Unchanged || second.Config.Body != nil || !second.Roster.Unchanged || second.Roster.Body != nil {
		t.Fatalf("stable documents were not conditionally skipped: %+v", second)
	}
	if second.Directives.Body == nil || second.Directives.Body.Quota[0].BaselineBytes != 150 ||
		second.Directives.Body.Quota[0].HeadroomBytes == nil || *second.Directives.Body.Quota[0].HeadroomBytes != limit {
		t.Fatalf("first counter observation must seed baseline and retain full headroom: %+v", second.Directives)
	}
	if second.Envelope.OverburnHeadroomBytes != limit || second.Envelope.NumeratorAsOfMS != now.UnixMilli() {
		t.Fatalf("quota exposure/freshness envelope = %+v", second.Envelope)
	}
	if second.Directives.Body.IPShadow[0].IPLimit != ipLimit {
		t.Fatalf("IP shadow limit = %d, want %d", second.Directives.Body.IPShadow[0].IPLimit, ipLimit)
	}
	currentNow = now.Add(46 * time.Second)
	staleHave := make(map[string]nodeprotocol.StreamState, len(have))
	for stream, state := range have {
		staleHave[stream] = state
	}
	staleHave[nodeprotocol.StreamDirectives] = nodeprotocol.StreamState{
		Applied: second.Directives.Version, ETag: second.Directives.ETag,
	}
	stale, err := service.Sync(ctx, nodeprotocol.NodeReport{
		AgentID: agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
		ReportedAtMS: now.Add(24 * time.Hour).UnixMilli(), Partial: true, Have: staleHave,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !stale.Envelope.WantFullReport || stale.Directives.Body == nil ||
		stale.Directives.Body.Quota[0].HeadroomBytes == nil || *stale.Directives.Body.Quota[0].HeadroomBytes != 0 {
		t.Fatalf("stale full report did not request refresh and fail closed: %+v / %+v", stale.Envelope, stale.Directives)
	}
	currentNow = now
	attachments, err := repos.PSPClient.ListInbounds(ctx, client.ID)
	if err != nil || len(attachments) != 1 || !attachments[0].Applied() || attachments[0].AppliedVersion != first.Roster.Version.Version {
		t.Fatalf("attachment convergence = (%+v, %v)", attachments, err)
	}
	storedNode, err := repos.Node.GetByID(ctx, node.ID)
	if err != nil || storedNode.ObservedPort != node.DesiredPort || storedNode.ObservedProtocol != node.DesiredProtocol {
		t.Fatalf("observed endpoint = (%+v, %v)", storedNode, err)
	}
	panelView, err := service.NativePanelSnapshot(ctx, 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(panelView.Inbounds) != 1 || panelView.Inbounds[0].CounterEpoch != 1 ||
		len(panelView.Inbounds[0].ClientStats) != 1 || panelView.Inbounds[0].ClientStats[0].CounterEpoch != 1 {
		t.Fatalf("native traffic projection incomplete: %+v", panelView.Inbounds)
	}
	if got := panelView.LiveClientIPs[client.Email]; len(got) != 2 || got[0] != "203.0.113.1" || got[1] != "203.0.113.2" {
		t.Fatalf("live IP projection = %#v", got)
	}
	// The last full enumeration is now 61s old. With a 45s effective full
	// cadence and one 15s poll of jitter allowance it must no longer back the
	// panel read facade, even though the partial heartbeat at +46s is still live.
	currentNow = now.Add(61 * time.Second)
	if _, err := service.NativePanelSnapshot(ctx, 9); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("stale full report remained readable: %v", err)
	}
	currentNow = now
	issues, total, err := repos.NodeAgentIssue.List(ctx, ports.NodeAgentIssueFilter{})
	if err != nil || total != 1 || len(issues) != 1 || issues[0].AgentID != agent.AgentID ||
		issues[0].Code != nodeprotocol.IssueObjectRejectedTimeout {
		t.Fatalf("persisted node issue = (%+v, %d, %v)", issues, total, err)
	}
	_, err = service.Sync(ctx, nodeprotocol.NodeReport{
		AgentID: agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
		ReportedAtMS: now.Add(2 * time.Second).UnixMilli(), Have: have,
		ListenerCounters: []nodeprotocol.ListenerCounters{{
			Key: nodeprotocol.NewListenerKey(node.ID), Present: true, CounterEpoch: 1,
		}},
		Clients: []nodeprotocol.ClientCounters{{
			Key: nodeprotocol.NewClientKey(client.ID), Present: true, CounterEpoch: 1,
			Gate: nodeprotocol.GateUnconfigured,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	missingIssues, total, err := repos.NodeAgentIssue.List(ctx, ports.NodeAgentIssueFilter{
		Code: nodeprotocol.IssueReportMissingObject,
	})
	if err != nil || total != 2 || len(missingIssues) != 2 {
		t.Fatalf("missing object issues = (%+v, %d, %v)", missingIssues, total, err)
	}
	missingKeys := map[string]bool{}
	for _, issue := range missingIssues {
		missingKeys[issue.Key] = true
	}
	if !missingKeys[string(nodeprotocol.NewClientKey(client.ID))] || !missingKeys[string(nodeprotocol.NewListenerKey(node.ID))] {
		t.Fatalf("missing object issue keys = %+v", missingKeys)
	}
	_, err = service.Sync(ctx, nodeprotocol.NodeReport{
		AgentID: agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1,
		ReportedAtMS: now.Add(3 * time.Second).UnixMilli(), Partial: true, Have: have,
		TaskResults: []nodeprotocol.TaskResult{{ID: "future-task", OK: true}},
	})
	if err == nil || !strings.Contains(err.Error(), "task result ingestion is not configured") {
		t.Fatalf("unsupported task result error = %v", err)
	}
}

func TestBuildConfigCarriesExactRestrictedCoreAcknowledgement(t *testing.T) {
	t.Parallel()
	body, err := buildConfig(&ports.NativeDesiredSnapshot{}, &domain.NodeAgent{
		DesiredCoreVersion: "26.9.9", AllowRestrictedReality: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if body.Core.Engine != "xray" || body.Core.Version != "26.9.9" || !body.Core.AllowRestrictedReality {
		t.Fatalf("core selection = %+v", body.Core)
	}
	if _, err := buildConfig(&ports.NativeDesiredSnapshot{}, &domain.NodeAgent{DesiredCoreVersion: "26.9.9"}); err == nil {
		t.Fatal("restricted core without acknowledgement unexpectedly minted")
	}
	if _, err := buildConfig(&ports.NativeDesiredSnapshot{}, &domain.NodeAgent{DesiredCoreVersion: "latest"}); err == nil {
		t.Fatal("latest unexpectedly minted")
	}
	singBox, err := buildConfig(&ports.NativeDesiredSnapshot{}, &domain.NodeAgent{
		DesiredCoreEngine: domain.NodeCoreSingBox, DesiredCoreVersion: "1.14.0",
	})
	if err != nil || singBox.Core.Engine != "sing-box" || singBox.Core.Version != "1.14.0" || singBox.Core.AllowRestrictedReality {
		t.Fatalf("sing-box core selection = (%+v, %v)", singBox.Core, err)
	}
}

func TestPendingUsageKeepsOriginalEpochAnchorUntilTrafficPollPersists(t *testing.T) {
	service := &Service{anchors: make(map[int64]nodeprotocol.ClientCounters)}
	client := &domain.PSPClient{ID: 7, UserID: 3}
	first := nodeprotocol.ClientCounters{CounterEpoch: 4, UpBytes: 100, DownBytes: 50}
	if got := service.pendingCounterDelta(client, first); got != 0 {
		t.Fatalf("first observation delta = %d, want 0", got)
	}
	second := nodeprotocol.ClientCounters{CounterEpoch: 4, UpBytes: 160, DownBytes: 70}
	if got := service.pendingCounterDelta(client, second); got != 80 {
		t.Fatalf("second observation delta = %d, want 80", got)
	}
	third := nodeprotocol.ClientCounters{CounterEpoch: 4, UpBytes: 190, DownBytes: 90}
	if got := service.pendingCounterDelta(client, third); got != 130 {
		t.Fatalf("anchor moved before DB caught up: delta = %d, want 130", got)
	}
	client.LastCounterEpoch, client.LastRawUpBytes, client.LastRawDownBytes = 4, 190, 90
	if got := service.pendingCounterDelta(client, third); got != 0 {
		t.Fatalf("persisted baseline delta = %d, want 0", got)
	}
}

func TestPendingUsageRejectsCrossPanelCounterSpoofing(t *testing.T) {
	service := &Service{
		reports: map[string]receivedFullReport{
			"agent-a": {report: nodeprotocol.NodeReport{Clients: []nodeprotocol.ClientCounters{
				{Key: nodeprotocol.NewClientKey(1), Present: true, CounterEpoch: 1, UpBytes: 10, DownBytes: 20},
				{Key: nodeprotocol.NewClientKey(2), Present: true, CounterEpoch: 1, UpBytes: 400, DownBytes: 500},
			}}},
			"agent-b": {report: nodeprotocol.NodeReport{Clients: []nodeprotocol.ClientCounters{
				{Key: nodeprotocol.NewClientKey(2), Present: true, CounterEpoch: 1, UpBytes: 20, DownBytes: 30},
			}}},
			"retired-agent": {report: nodeprotocol.NodeReport{Clients: []nodeprotocol.ClientCounters{
				{Key: nodeprotocol.NewClientKey(1), Present: true, CounterEpoch: 1, UpBytes: 900, DownBytes: 900},
			}}},
		},
		anchors: make(map[int64]nodeprotocol.ClientCounters),
	}
	clients := map[int64]*domain.PSPClient{
		1: {ID: 1, UserID: 101, PanelID: 10, LastCounterEpoch: 1},
		2: {ID: 2, UserID: 202, PanelID: 20, LastCounterEpoch: 1},
	}
	agents := []*domain.NodeAgent{
		{AgentID: "agent-a", PanelID: 10},
		{AgentID: "agent-b", PanelID: 20},
	}

	usage := service.pendingUsage(clients, agents)
	if usage[101] != 30 || usage[202] != 50 {
		t.Fatalf("scoped pending usage = %+v, want user 101=30 and user 202=50", usage)
	}
}

func TestFleetEnvelopeUsesAllOutstandingAgentGrantsAndOldestReport(t *testing.T) {
	now := time.UnixMilli(10_000)
	service := &Service{
		reports: map[string]receivedFullReport{
			"a": {report: nodeprotocol.NodeReport{ReportedAtMS: 99_500}, receivedAtMS: 9_500},
			"b": {report: nodeprotocol.NodeReport{ReportedAtMS: 98_000}, receivedAtMS: 8_000},
		},
		grants: map[string]map[nodeprotocol.ClientKey]int64{
			"b": {nodeprotocol.NewClientKey(2): 300},
		},
	}
	headroom := int64(200)
	asOf, age, overburn := service.recordGrantsAndFleetEnvelope("a", []nodeprotocol.QuotaEntry{{
		Client: nodeprotocol.NewClientKey(1), HeadroomBytes: &headroom,
	}}, now, 8_000)
	if asOf != 8_000 || age != 2_000 || overburn != 500 {
		t.Fatalf("fleet envelope = (%d, %d, %d), want (8000, 2000, 500)", asOf, age, overburn)
	}
}

func TestAggregateCoverageUsesPSPOwnNativeRowsAndTagsStaleEntries(t *testing.T) {
	now := time.UnixMilli(100_000)
	service := &Service{reports: map[string]receivedFullReport{
		"a": {
			report:       nodeprotocol.NodeReport{ReportedAtMS: 9_500_000, Clients: []nodeprotocol.ClientCounters{{Key: nodeprotocol.NewClientKey(1), Present: true}}},
			receivedAtMS: 95_000,
		},
		"b": {
			report:       nodeprotocol.NodeReport{ReportedAtMS: 9_800_000, Clients: []nodeprotocol.ClientCounters{{Key: nodeprotocol.NewClientKey(2), Present: true}}},
			receivedAtMS: 30_000,
		},
	}}
	agents := []*domain.NodeAgent{{AgentID: "a", PanelID: 10}, {AgentID: "b", PanelID: 20}}
	clients := []*domain.PSPClient{
		{ID: 1, UserID: 7, PanelID: 10},
		{ID: 2, UserID: 7, PanelID: 20},
		{ID: 3, UserID: 8, PanelID: 20},
		{ID: 4, UserID: 9, PanelID: 30}, // legacy panel: outside native coverage
	}
	coverage, oldest := service.aggregateCoverage(clients, agents, 60, now)
	if coverage != (nodeprotocol.SegmentCounts{Entries: 3, EntriesStale: 2, Subjects: 2}) || oldest != 30_000 {
		t.Fatalf("aggregate coverage = (%+v, %d)", coverage, oldest)
	}

	delete(service.reports, "b")
	coverage, oldest = service.aggregateCoverage(clients, agents, 60, now)
	if coverage.EntriesStale != 2 || oldest != 0 {
		t.Fatalf("missing agent report coverage = (%+v, %d), want stale rows and unknown aggregate timestamp", coverage, oldest)
	}
}

func TestEffectiveFullReportPeriodMatchesDiscretePollSchedule(t *testing.T) {
	tests := []struct {
		name             string
		full, poll, want int
	}{
		{name: "every poll", full: 0, poll: 30, want: 30},
		{name: "exact multiple", full: 60, poll: 30, want: 60},
		{name: "round up to poll", full: 45, poll: 30, want: 60},
		{name: "full faster than poll", full: 10, poll: 30, want: 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveFullReportPeriod(tt.full, tt.poll); got != tt.want {
				t.Fatalf("effective period = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFullReportFreshnessFailsClosedAcrossControlPlaneClockRollback(t *testing.T) {
	now := time.UnixMilli(100_000)
	if !fullReportStale(100_001, now, 60) {
		t.Fatal("future receipt time after a PSP clock rollback was treated as fresh")
	}
	service := &Service{reports: map[string]receivedFullReport{
		"a": {
			report:       nodeprotocol.NodeReport{Clients: []nodeprotocol.ClientCounters{{Key: nodeprotocol.NewClientKey(1), Present: true}}},
			receivedAtMS: 100_001,
		},
	}}
	coverage, oldest := service.aggregateCoverage(
		[]*domain.PSPClient{{ID: 1, UserID: 7, PanelID: 10}},
		[]*domain.NodeAgent{{AgentID: "a", PanelID: 10}},
		60,
		now,
	)
	if coverage.EntriesStale != 1 || oldest != 0 {
		t.Fatalf("clock-rollback coverage = (%+v, %d), want stale with unknown timestamp", coverage, oldest)
	}
}

func TestSyncRejectsInvalidReportBeforeRepositoryAccess(t *testing.T) {
	service := &Service{}
	_, err := service.Sync(context.Background(), nodeprotocol.NodeReport{AgentID: "agt_bad"})
	if err == nil || !strings.Contains(err.Error(), "have.config is required") {
		t.Fatalf("invalid report error = %v", err)
	}
}

func emptyProtocolHave() map[string]nodeprotocol.StreamState {
	return map[string]nodeprotocol.StreamState{
		nodeprotocol.StreamConfig: {}, nodeprotocol.StreamRoster: {}, nodeprotocol.StreamDirectives: {},
	}
}

var _ ports.NativePanelSnapshotReader = (*Service)(nil)
