package sharedclient

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestAppliedCredentialChangesInvalidateButSteadyReceiptsDoNot(t *testing.T) {
	clients := &fakeClients{attachments: []domain.PSPClientInbound{{ClientID: 1, NodeID: 7}}}
	s := New(clients, nil, nil)
	invalidations := 0
	s.SetSubscriptionInvalidator(func() { invalidations++ })
	update := domain.PSPClientInbound{ClientID: 1, NodeID: 7, State: domain.ClientApplyApplied, AppliedEmail: "u1@test", AppliedUUID: "first", AppliedPassword: "password"}
	previous, _ := clients.ListInbounds(context.Background(), 1)
	if err := s.recordApplied(context.Background(), update, previous); err != nil {
		t.Fatal(err)
	}
	if invalidations != 1 {
		t.Fatal("initial confirmed credentials did not invalidate the subscription")
	}
	previous, _ = clients.ListInbounds(context.Background(), 1)
	if err := s.recordApplied(context.Background(), update, previous); err != nil {
		t.Fatal(err)
	}
	if invalidations != 1 {
		t.Fatal("steady-state receipt invalidated every cached subscription")
	}
	update.AppliedUUID = "rotated"
	if err := s.recordApplied(context.Background(), update, previous); err != nil {
		t.Fatal(err)
	}
	if invalidations != 2 {
		t.Fatal("confirmed rotation did not invalidate")
	}
}

type rejectedAppliedRepo struct{ *fakeClients }

func (rejectedAppliedRepo) UpdateInboundState(context.Context, domain.PSPClientInbound) error {
	return errors.New("store unavailable")
}
func TestFailedCredentialConfirmationDoesNotPublishByInvalidating(t *testing.T) {
	s := New(rejectedAppliedRepo{&fakeClients{}}, nil, nil)
	count := 0
	s.SetSubscriptionInvalidator(func() { count++ })
	if err := s.recordApplied(context.Background(), domain.PSPClientInbound{ClientID: 1, NodeID: 7, AppliedUUID: "new"}, nil); err == nil || count != 0 {
		t.Fatal("failed receipt was published")
	}
}
