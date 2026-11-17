package nodesync

import (
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// lineNode is node 3 on panel 9 with its direct line at direct (nil =
// default) and relay line "r1" at ×3.
func lineNode(direct *int) *domain.Node {
	three := 3000
	return &domain.Node{ID: 3, PanelID: 9, TrafficFactorMilli: direct,
		Relays: []domain.RelayLine{{ID: "r1", Enabled: true, TrafficFactorMilli: &three}}}
}

func TestNativeHeadroomVectors(t *testing.T) {
	ptr := func(v int64) *int64 { return &v }
	cases := []struct {
		name      string
		remaining int64
		factor    int
		known     bool
		want      *int64
	}{
		{"default factor is unchanged", 1500, 1000, true, ptr(1500)},
		{"expensive server floors", 1500, 1499, true, ptr(1000)}, // 1000.67
		{"expensive server", 1500, 1500, true, ptr(1000)},
		{"cheap server", 1500, 500, true, ptr(3000)},
		{"exhausted stays closed", 0, 1500, true, ptr(0)},
		{"unknown counter stays closed", 1500, 1500, false, ptr(0)},
		{"free server with quota left is ungated", 1500, 0, true, nil},
		{"free server ungated even before its counter is known", 1500, 0, false, nil},
		{"free server closes once the quota is gone", 0, 0, true, ptr(0)},
	}
	for _, c := range cases {
		got := nativeHeadroom(c.remaining, c.factor, c.known)
		switch {
		case c.want == nil && got != nil:
			t.Errorf("%s: headroom = %d, want nil", c.name, *got)
		case c.want != nil && (got == nil || *got != *c.want):
			t.Errorf("%s: headroom = %v, want %d", c.name, got, *c.want)
		}
	}
}

// A node gates each client's RAW counter, so the charged grant is divided by
// that client's line factor, and the advance grant for the next period
// likewise.
func TestBuildDirectivesConvertsQuotaThroughServerFactor(t *testing.T) {
	now := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)
	ptr := func(v int64) *int64 { return &v }
	for _, tc := range []struct {
		name     string
		factor   *int // nil: the direct line has no factor set
		used     int64
		reported bool
		want     *int64
		next     *int64
	}{
		{name: "no factor is one-for-one", used: 1500, reported: true, want: ptr(1500), next: ptr(3000)},
		{name: "default factor", factor: intPtr(1000), used: 1500, reported: true, want: ptr(1500), next: ptr(3000)},
		{name: "×1.5", factor: intPtr(1500), used: 1500, reported: true, want: ptr(1000), next: ptr(2000)},
		{name: "×0.5", factor: intPtr(500), used: 1500, reported: true, want: ptr(3000), next: ptr(6000)},
		{name: "free with quota left", factor: intPtr(0), used: 1500, reported: true, want: nil, next: nil},
		{name: "free before the counter is known", factor: intPtr(0), used: 1500, reported: false, want: nil, next: nil},
		{name: "free but exhausted", factor: intPtr(0), used: 3000, reported: true, want: ptr(0), next: nil},
		{name: "weighted, counter unknown", factor: intPtr(1500), used: 1500, reported: false, want: ptr(0), next: ptr(2000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := &domain.User{ID: 7, TrafficLimitBytes: 3000, TrafficResetPeriod: domain.ResetMonthly, LifetimeTotalBytes: tc.used}
			client := &domain.PSPClient{ID: 11, UserID: user.ID, PanelID: 9, NodeID: 3, LineID: domain.DirectLineID}
			agent := &domain.NodeAgent{AgentID: "agt_factor", PanelID: client.PanelID}
			service := &Service{
				users:    emergencyDirectiveUsers{user: user},
				clients:  emergencyDirectiveClients{client: client},
				agents:   emergencyDirectiveAgents{agent: agent},
				settings: emergencyDirectiveSettings{},
				reports:  make(map[string]receivedFullReport),
				anchors:  make(map[int64]nodeprotocol.ClientCounters),
				grants:   make(map[string]map[nodeprotocol.ClientKey]int64),
			}
			report := nodeprotocol.NodeReport{}
			if tc.reported {
				report.Clients = []nodeprotocol.ClientCounters{{Key: nodeprotocol.NewClientKey(client.ID), UpBytes: 100, DownBytes: 50, CounterEpoch: 1}}
			}
			body, _, err := service.buildDirectives(t.Context(), agent, &ports.NativeDesiredSnapshot{
				Nodes:   []*domain.Node{lineNode(tc.factor)},
				Clients: []ports.NativeDesiredClient{{Client: client}},
			}, report, nodeprotocol.Version{Epoch: 1, Version: 1}, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(body.Quota) != 1 {
				t.Fatalf("quota entries = %d, want 1", len(body.Quota))
			}
			entry := body.Quota[0]
			assertHeadroom(t, "headroom", entry.HeadroomBytes, tc.want)
			assertHeadroom(t, "next-period headroom", entry.NextPeriodHeadroomBytes, tc.next)
			if (tc.next == nil) != (entry.PeriodEndsAtMS == 0) {
				t.Fatalf("period end = %d with next-period headroom %v", entry.PeriodEndsAtMS, entry.NextPeriodHeadroomBytes)
			}
		})
	}
}

// The emergency window's grant is converted the same way.
func TestBuildDirectivesConvertsEmergencyQuotaThroughServerFactor(t *testing.T) {
	const gib = int64(1 << 30)
	now := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)
	until := now.Add(time.Hour)
	user := &domain.User{
		ID: 7, TrafficLimitBytes: gib, TrafficResetPeriod: domain.ResetMonthly,
		LifetimeTotalBytes: 5 * gib, EmergencyUntil: &until, EmergencyBaselineBytes: 3 * gib,
	}
	client := &domain.PSPClient{ID: 11, UserID: user.ID, PanelID: 9, NodeID: 3, LineID: domain.DirectLineID}
	agent := &domain.NodeAgent{AgentID: "agt_emergency_factor", PanelID: client.PanelID}
	service := &Service{
		users:    emergencyDirectiveUsers{user: user},
		clients:  emergencyDirectiveClients{client: client},
		agents:   emergencyDirectiveAgents{agent: agent},
		settings: emergencyDirectiveSettings{perUser: map[int64]ports.UISettings{user.ID: {EmergencyAccessQuotaGB: 5}}},
		reports:  make(map[string]receivedFullReport),
		anchors:  make(map[int64]nodeprotocol.ClientCounters),
		grants:   make(map[string]map[nodeprotocol.ClientKey]int64),
	}
	body, _, err := service.buildDirectives(t.Context(), agent, &ports.NativeDesiredSnapshot{
		Nodes:   []*domain.Node{lineNode(intPtr(2000))},
		Clients: []ports.NativeDesiredClient{{Client: client}},
	}, nodeprotocol.NodeReport{Clients: []nodeprotocol.ClientCounters{{Key: nodeprotocol.NewClientKey(client.ID), UpBytes: 1, CounterEpoch: 1}}},
		nodeprotocol.Version{Epoch: 1, Version: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	// 5 GiB quota − 2 GiB used = 3 GiB charged = 1.5 GiB raw at ×2.
	want := 3 * gib / 2
	assertHeadroom(t, "emergency headroom", body.Quota[0].HeadroomBytes, &want)
	next := gib / 2
	assertHeadroom(t, "next-period headroom", body.Quota[0].NextPeriodHeadroomBytes, &next)
}

// Pending usage is charged like the poll will book it: each client's raw
// pending delta at its own line's factor, or at the factor it was last booked
// at when its line is not among the given ones.
func TestPendingUsageIsWeightedByEachClientsLine(t *testing.T) {
	service := &Service{
		reports: map[string]receivedFullReport{
			"agent-a": {report: nodeprotocol.NodeReport{Clients: []nodeprotocol.ClientCounters{
				{Key: nodeprotocol.NewClientKey(1), Present: true, CounterEpoch: 1, UpBytes: 10, DownBytes: 20},
			}}},
			"agent-b": {report: nodeprotocol.NodeReport{Clients: []nodeprotocol.ClientCounters{
				{Key: nodeprotocol.NewClientKey(2), Present: true, CounterEpoch: 1, UpBytes: 20, DownBytes: 30},
			}}},
		},
		anchors: make(map[int64]nodeprotocol.ClientCounters),
	}
	clients := map[int64]*domain.PSPClient{
		1: {ID: 1, UserID: 101, PanelID: 10, NodeID: 3, LineID: "r1", LastCounterEpoch: 1},
		2: {ID: 2, UserID: 101, PanelID: 20, NodeID: 8, LineID: domain.DirectLineID, LastCounterEpoch: 1, LastFactorMilli: intPtr(0)},
	}
	agents := []*domain.NodeAgent{{AgentID: "agent-a", PanelID: 10}, {AgentID: "agent-b", PanelID: 20}}

	usage := service.pendingUsage(clients, agents, domain.LineFactors([]*domain.Node{lineNode(nil)}))
	if usage[101] != 90 {
		t.Fatalf("pending = %d, want 30 raw at ×3 + 50 raw last booked free = 90", usage[101])
	}
}

func intPtr(v int) *int { return &v }

func assertHeadroom(t *testing.T, what string, got, want *int64) {
	t.Helper()
	switch {
	case want == nil && got != nil:
		t.Fatalf("%s = %d, want nil", what, *got)
	case want != nil && (got == nil || *got != *want):
		t.Fatalf("%s = %v, want %d", what, got, *want)
	}
}
