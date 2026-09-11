package pspnode

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type snapshotReader struct {
	snapshot *ports.NativePanelSnapshot
	err      error
}

func TestGetServerStatusDoesNotHideMissingAgentReport(t *testing.T) {
	client, err := New(&domain.Panel{ID: 9, Kind: domain.PanelKindPSP},
		snapshotReader{err: domain.ErrNotFound}, nodeRepo{}, &agentRepo{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetServerStatus(context.Background()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing report status error = %v", err)
	}
}

func (r snapshotReader) NativePanelSnapshot(context.Context, int64) (*ports.NativePanelSnapshot, error) {
	return r.snapshot, r.err
}

type nodeRepo struct {
	ports.NodeRepo
	nodes []*domain.Node
}

type agentRepo struct {
	ports.NodeAgentRepo
	agent   *domain.NodeAgent
	version string
	allow   bool
}

func (r *agentRepo) GetByPanelID(context.Context, int64) (*domain.NodeAgent, error) {
	return r.agent, nil
}

func (r *agentRepo) UpdateCoreSelection(_ context.Context, _ string, version string, allow bool) error {
	r.version = version
	r.allow = allow
	return nil
}

func (r nodeRepo) List(context.Context) ([]*domain.Node, error) { return r.nodes, nil }

func TestClientProjectsObservedSnapshotAndKeepsWritesAsIntent(t *testing.T) {
	reader := snapshotReader{snapshot: &ports.NativePanelSnapshot{
		Inbounds: []ports.Inbound{{
			ID: 8, Port: 443, Protocol: "vless", CounterEpoch: 3,
			ClientStats: []ports.ClientTraffic{{Email: "a@psp", Up: 4, Down: 5, CounterEpoch: 7}},
		}},
		Clients: map[string]ports.ClientDetail{
			"a@psp": {ID: "uuid", Email: "a@psp", InboundIDs: []int{8}},
		},
		LiveClientIPs: map[string][]string{"a@psp": {"203.0.113.1"}},
		Status:        ports.ServerStatus{PanelVersion: "v0.1", XrayVersion: "v25", XrayState: "running"},
	}}
	client, err := New(&domain.Panel{ID: 9, Kind: domain.PanelKindPSP}, reader, nodeRepo{}, &agentRepo{})
	if err != nil {
		t.Fatal(err)
	}
	if !client.ApplyIsAsynchronous() || ports.SupportsCapability(client, ports.CapabilityClientIPLimit) {
		t.Fatal("native adapter capability declaration is unsafe")
	}
	inbounds, err := client.ListInboundsSlim(context.Background())
	if err != nil || len(inbounds) != 1 || inbounds[0].CounterEpoch != 3 || inbounds[0].ClientStats[0].CounterEpoch != 7 {
		t.Fatalf("inbound projection = (%+v, %v)", inbounds, err)
	}
	detail, err := client.GetClient(context.Background(), "a@psp")
	if err != nil || detail == nil || detail.ID != "uuid" || len(detail.InboundIDs) != 1 {
		t.Fatalf("client projection = (%+v, %v)", detail, err)
	}
	// Mutating a caller-owned result cannot corrupt the coordinator's cache.
	detail.InboundIDs[0] = 999
	again, _ := client.GetClient(context.Background(), "a@psp")
	if again.InboundIDs[0] != 8 {
		t.Fatalf("GetClient leaked mutable snapshot storage: %+v", again)
	}
	if err := client.UpdateClient(context.Background(), ports.ClientSpec{Email: "a@psp"}); err != nil {
		t.Fatalf("intent-only write failed: %v", err)
	}
	status, err := client.GetServerStatus(context.Background())
	if err != nil || status.XrayState != "running" {
		t.Fatalf("status = (%+v, %v)", status, err)
	}
}

func TestAddInboundAllocatesCompatibilityIDWithoutUsingItAsWireIdentity(t *testing.T) {
	client, err := New(
		&domain.Panel{ID: 9, Kind: domain.PanelKindPSP},
		snapshotReader{snapshot: &ports.NativePanelSnapshot{}},
		nodeRepo{nodes: []*domain.Node{
			{PanelID: 9, InboundID: 4}, {PanelID: 9, InboundID: 9}, {PanelID: 10, InboundID: 100},
		}},
		&agentRepo{},
	)
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.AddInbound(context.Background(), ports.InboundSpec{})
	if err != nil || first != 10 {
		t.Fatalf("first compatibility id = (%d, %v), want 10", first, err)
	}
	second, err := client.AddInbound(context.Background(), ports.InboundSpec{})
	if err != nil || second != 11 {
		t.Fatalf("second compatibility id = (%d, %v), want 11", second, err)
	}
}

func TestCoreUpdaterUsesAuditedCatalogAndPersistsDesiredVersion(t *testing.T) {
	repo := &agentRepo{agent: &domain.NodeAgent{AgentID: "agt_9", PanelID: 9}}
	client, err := New(&domain.Panel{ID: 9, Kind: domain.PanelKindPSP},
		snapshotReader{snapshot: &ports.NativePanelSnapshot{}}, nodeRepo{}, repo)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := client.GetCoreVersionList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 3 || versions[0] != "26.6.27" || versions[2] != "26.9.9" {
		t.Fatalf("catalog versions = %v", versions)
	}
	if err := client.InstallCore(context.Background(), "latest"); err == nil {
		t.Fatal("latest unexpectedly accepted")
	}
	if err := client.InstallCore(context.Background(), "26.9.9"); err != nil {
		t.Fatal(err)
	}
	if repo.version != "26.9.9" || !repo.allow {
		t.Fatalf("persisted selection = %q allow=%v", repo.version, repo.allow)
	}
}
