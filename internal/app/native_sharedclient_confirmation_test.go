package app

import (
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/sharedclient"
)

func TestNativeSharedClientReadbackPreservesAuthenticatedRosterConfirmation(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	ctx := t.Context()
	// Exercise actual member-input caching, which collection-only agents skip.
	saveWiringPolicy(t, f)
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy}
	clients, err := f.a.repos.PSPClient.ListByUser(ctx, f.user.ID)
	if err != nil || len(clients) != 1 {
		t.Fatal("fixture must have one persisted shared client")
	}
	client := clients[0]
	service := sharedclient.New(f.a.repos.PSPClient, f.a.xuiPool, f.a.repos.Node)
	f.report.AgentVersion = "fixture-native-confirmation"
	f.report.CoreEngine, f.report.CoreVersion, f.report.CoreState = "xray", "26.6.27", "running"
	initial := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if initial.Config.Body == nil || initial.Roster.Body == nil || initial.Roster.Version.Version == 0 {
		t.Fatal("sync must produce actual versioned config and roster documents")
	}
	// Adapter intent alone cannot confirm a client absent from the full report.
	if result, err := service.ProvisionClient(ctx, client); err == nil || result.Provisioned != 0 {
		t.Fatal("unconfirmed native client was provisioned by a legacy read-back")
	}
	before, err := f.a.repos.PSPClient.ListInbounds(ctx, client.ID)
	if err != nil || len(before) != 1 || before[0].Applied() || before[0].AppliedVersion != 0 {
		t.Fatal("unconfirmed native intent changed persisted attachment proof")
	}
	f.report.Have = map[string]protocol.StreamState{
		protocol.StreamConfig:     {Applied: initial.Config.Version, ETag: initial.Config.ETag},
		protocol.StreamRoster:     {Applied: initial.Roster.Version, ETag: initial.Roster.ETag},
		protocol.StreamDirectives: {Applied: initial.Directives.Version, ETag: initial.Directives.ETag},
	}
	f.report.Objects = []protocol.ObjectStatus{
		{Stream: protocol.StreamConfig, Key: string(protocol.NewListenerKey(f.node.ID)), State: protocol.ObjectApplied, SinceVersion: initial.Config.Version},
		{Stream: protocol.StreamRoster, Key: string(protocol.NewClientKey(client.ID)), State: protocol.ObjectApplied, SinceVersion: initial.Roster.Version},
	}
	f.report.ListenerCounters = []protocol.ListenerCounters{{Key: protocol.NewListenerKey(f.node.ID), Present: true, CounterEpoch: 1}}
	f.report.Clients = []protocol.ClientCounters{{Key: protocol.NewClientKey(client.ID), Present: true, UpBytes: 100, DownBytes: 200, CounterEpoch: 1, Gate: protocol.GateUnconfigured}}
	syncNativeCacheFixture(t, f.a, f.credential, f.report)
	confirmed, err := f.a.repos.PSPClient.ListInbounds(ctx, client.ID)
	if err != nil || len(confirmed) != 1 || !confirmed[0].Applied() || confirmed[0].AppliedVersion != initial.Roster.Version.Version || confirmed[0].AppliedUUID != client.UUID {
		t.Fatal("authenticated full report did not persist the exact roster proof")
	}
	for range 2 {
		if result, err := service.ProvisionClient(ctx, client); err != nil || result.Provisioned != 1 {
			t.Fatal("confirmed native runtime read-back did not provision its actual inbound")
		}
		if err := service.SyncLifecycle(ctx, client); err != nil {
			t.Fatal(err)
		}
		after, err := f.a.repos.PSPClient.ListInbounds(ctx, client.ID)
		if err != nil || !reflect.DeepEqual(after, confirmed) {
			t.Fatal("legacy provisioning or lifecycle read-back overwrote authenticated native confirmation")
		}
	}
	rotated := *client
	rotated.UUID = "22222222-2222-4222-8222-222222222222"
	if err := f.a.repos.PSPClient.UpdateDefinition(ctx, &rotated); err != nil {
		t.Fatal(err)
	}
	rotated.SetDesiredLifecycle(domain.UserLifecycle{Enable: false})
	if err := f.a.repos.PSPClient.UpdateDesiredLifecycleByUser(ctx, client.UserID, domain.UserLifecycle{Enable: false}); err != nil {
		t.Fatal(err)
	}
	adapter, err := f.a.xuiPool.Get(client.PanelID)
	if err != nil {
		t.Fatal(err)
	}
	if detail, err := adapter.GetClient(ctx, client.Email); err != nil || detail != nil {
		t.Fatal("old authenticated receipt exposed a newly edited desired identity")
	}
	inbounds, err := adapter.ListInbounds(ctx)
	if err != nil || len(inbounds) != 1 || len(inbounds[0].ClientStats) != 1 || inbounds[0].ClientStats[0].Up != 100 || inbounds[0].ClientStats[0].Down != 200 || inbounds[0].ClientStats[0].CounterEpoch != 1 || !inbounds[0].ClientStats[0].Enable {
		t.Fatal("pending identity suppressed actual counters from the stable client key")
	}
	if err := service.SyncLifecycle(ctx, &rotated); err == nil {
		t.Fatal("native lifecycle confirmed an identity absent from the accepted roster")
	}
	if result, err := service.ProvisionClient(ctx, &rotated); err == nil || result.Provisioned != 0 {
		t.Fatal("native provisioning confirmed an identity absent from the accepted roster")
	}
	stillConfirmed, err := f.a.repos.PSPClient.ListInbounds(ctx, client.ID)
	if err != nil || !reflect.DeepEqual(stillConfirmed, confirmed) {
		t.Fatal("unconfirmed credential rotation overwrote the previous report proof")
	}
	next := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if next.Roster.Body == nil || next.Roster.Version.Version <= initial.Roster.Version.Version {
		t.Fatal("credential rotation did not mint a new actual roster")
	}
	policyCacheKey := func() string {
		t.Helper()
		agent, err := f.a.repos.NodeAgent.GetByAgentID(ctx, f.agent.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := f.a.repos.NativeDesired.Load(ctx, f.agent.PanelID)
		if err != nil {
			t.Fatal(err)
		}
		base := *initial.Config.Body
		base.Policy = nil
		candidate, err := f.a.destCompiler.Compile(ctx, agent, snapshot, f.report.Capabilities, base)
		if err != nil {
			t.Fatal(err)
		}
		if candidate.CacheKey == "" {
			t.Fatal("production compiler did not bind its actual tracked membership inputs")
		}
		return candidate.CacheKey
	}
	beforeCredentialReceipt := policyCacheKey()
	f.report.Have[protocol.StreamRoster] = protocol.StreamState{Applied: next.Roster.Version, ETag: next.Roster.ETag}
	f.report.Have[protocol.StreamDirectives] = protocol.StreamState{Applied: next.Directives.Version, ETag: next.Directives.ETag}
	f.report.Objects[1].SinceVersion = next.Roster.Version
	f.report.Clients[0].Present = false
	syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if policyCacheKey() != beforeCredentialReceipt {
		t.Fatal("credential confirmation discarded unrelated policy membership inputs")
	}
	if detail, err := adapter.GetClient(ctx, rotated.Email); err != nil || detail == nil || detail.ID != rotated.UUID || detail.Enable {
		t.Fatal("new authenticated roster receipt did not expose the rotated identity")
	}
	if result, err := service.ProvisionClient(ctx, &rotated); err != nil || result.Provisioned != 1 {
		t.Fatal("rotated confirmed identity did not recover native provisioning")
	}
	if err := service.SyncLifecycle(ctx, &rotated); err != nil {
		t.Fatal(err)
	}
	latest, err := f.a.repos.PSPClient.ListInbounds(ctx, client.ID)
	if err != nil || len(latest) != 1 || !latest[0].Applied() || latest[0].AppliedVersion != next.Roster.Version.Version || latest[0].AppliedUUID != rotated.UUID {
		t.Fatal("native read-back lost the newly confirmed rotated roster proof")
	}
}
