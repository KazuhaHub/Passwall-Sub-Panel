package app

import (
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/sharedclient"
)

func TestNativeSharedClientReadbackPreservesAuthenticatedRosterConfirmation(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	ctx := t.Context()
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
	f.report.Clients = []protocol.ClientCounters{{Key: protocol.NewClientKey(client.ID), Present: true, CounterEpoch: 1, Gate: protocol.GateUnconfigured}}
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
}
