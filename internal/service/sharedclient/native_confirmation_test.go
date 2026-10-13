package sharedclient

import (
	"context"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type asynchronousFixtureClient struct{ *fakeXUI }

func (*asynchronousFixtureClient) ApplyIsAsynchronous() bool { return true }

type asynchronousStoredClient struct{ *storingXUI }

func (*asynchronousStoredClient) ApplyIsAsynchronous() bool { return true }

type unconfirmedAsynchronousClient struct{ *asynchronousFixtureClient }

func (*unconfirmedAsynchronousClient) GetClient(context.Context, string) (*ports.ClientDetail, error) {
	return nil, nil
}

func TestNativeProvisionReadbackKeepsVersionedAttachmentAuthority(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[existing], func(t *testing.T) {
			original := domain.PSPClientInbound{ClientID: 1, NodeID: 11, State: domain.ClientApplyApplied, AppliedVersion: 17, AppliedEmail: "confirmed-fixture", AppliedUUID: "confirmed-fixture-uuid"}
			clients := &fakeClients{attachments: []domain.PSPClientInbound{original}}
			adapter := &asynchronousFixtureClient{fakeXUI: &fakeXUI{confirm: []int{101}}}
			if existing {
				adapter.preExist = []int{101}
			}
			service := New(clients, fakePool{c: adapter}, fakeNodes{byID: map[int64]*domain.Node{11: enabledNode(11, 10, 101)}})
			result, err := service.ProvisionClient(t.Context(), &domain.PSPClient{ID: 1, PanelID: 10, Email: "fixture", UUID: "desired-fixture", DesiredEnable: true})
			if err != nil || result.Provisioned != 1 || adapter.getClientCalls == 0 {
				t.Fatal("native provisioning suppressed its actual runtime read-back")
			}
			if len(clients.applied) != 0 || len(clients.provisioned) != 0 {
				t.Fatal("legacy read-back replaced version-bound native attachment proof")
			}
			stored, err := clients.ListInbounds(t.Context(), 1)
			if err != nil || len(stored) != 1 || !reflect.DeepEqual(stored[0], original) {
				t.Fatal("native read-back reset a confirmed roster version or credentials")
			}
		})
	}
}

func TestNativeLifecycleReadbackDoesNotOverwriteReportConfirmedCredentials(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "changed"}[changed], func(t *testing.T) {
			original := domain.PSPClientInbound{ClientID: 1, NodeID: 11, State: domain.ClientApplyApplied, AppliedVersion: 17, AppliedEmail: "confirmed-fixture", AppliedUUID: "report-confirmed-fixture"}
			clients := &fakeClients{attachments: []domain.PSPClientInbound{original}}
			current := "desired-fixture"
			if changed {
				current = "previous-runtime-fixture"
			}
			adapter := &asynchronousStoredClient{storingXUI: &storingXUI{stored: &ports.ClientDetail{ID: current, Auth: current, Email: "fixture", Enable: true}}}
			service := New(clients, fakePool{c: adapter}, fakeNodes{})
			client := &domain.PSPClient{ID: 1, PanelID: 10, Email: "fixture", UUID: "desired-fixture"}
			if err := syncLifecycle(service, t.Context(), client, domain.UserLifecycle{Enable: true}); err != nil {
				t.Fatal(err)
			}
			if changed && adapter.writes != 1 || !changed && adapter.writes != 0 {
				t.Fatal("native lifecycle lost its compare/write behavior")
			}
			if len(clients.applied) != 0 {
				t.Fatal("legacy lifecycle read-back overwrote native credential confirmation")
			}
			stored, err := clients.ListInbounds(t.Context(), 1)
			if err != nil || !reflect.DeepEqual(stored[0], original) {
				t.Fatal("native lifecycle changed the report's versioned attachment")
			}
		})
	}
}

func TestNativeUnconfirmedProvisionStillFailsWithoutChangingAttachments(t *testing.T) {
	clients := &fakeClients{attachments: []domain.PSPClientInbound{{ClientID: 1, NodeID: 11, State: domain.ClientApplyPending}}}
	adapter := &unconfirmedAsynchronousClient{asynchronousFixtureClient: &asynchronousFixtureClient{fakeXUI: &fakeXUI{}}}
	service := New(clients, fakePool{c: adapter}, fakeNodes{byID: map[int64]*domain.Node{11: enabledNode(11, 10, 101)}})
	result, err := service.ProvisionClient(t.Context(), &domain.PSPClient{ID: 1, PanelID: 10, Email: "fixture", UUID: "desired-fixture", DesiredEnable: true})
	if err == nil || result.Provisioned != 0 || len(clients.applied) != 0 {
		t.Fatal("unconfirmed native intent became applied or permitted cleanup")
	}
}
