package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// guardClient answers GetInbound with a fixed result and, optionally, reports
// itself as an asynchronous (native) panel.
type guardClient struct {
	ports.XUIClient
	async bool
	inb   *ports.Inbound
	err   error
	dels  int
}

func (c *guardClient) ApplyIsAsynchronous() bool { return c.async }
func (c *guardClient) GetInbound(context.Context, int) (*ports.Inbound, error) {
	return c.inb, c.err
}
func (c *guardClient) DelInbound(context.Context, int) error { c.dels++; return nil }

// A native panel has no unmanaged clients: PSP owns the whole inbound and the
// agent deploys it from PSP's desired config. The guard therefore has nothing to
// check, and must not read the agent's report to say so — a node the agent has
// not reported yet would otherwise be undeletable.
func TestNativeInboundIsDeletableWithoutTheAgentReport(t *testing.T) {
	for name, getErr := range map[string]error{
		"before the first report": ports.ErrNativePanelSnapshotMissing,
		"node not in the report":  domain.ErrNotFound,
		"agent offline":           ports.ErrNativePanelAgentOffline,
	} {
		t.Run(name, func(t *testing.T) {
			c := &guardClient{async: true, err: getErr}
			s := New(&fakePool{xui: c}, &fakeOwnership{})
			if err := s.EnsureInboundDeletable(context.Background(), 1, 5); err != nil {
				t.Fatalf("EnsureInboundDeletable = %v", err)
			}
			if err := s.DeleteInbound(context.Background(), 1, 5); err != nil {
				t.Fatalf("DeleteInbound = %v", err)
			}
		})
	}
}

// 3X-UI keeps the guard: an unreadable inbound is an error, and an inbound that
// holds a client PSP does not manage still blocks the delete.
func TestXUIInboundDeletableGuardIsUnchanged(t *testing.T) {
	boom := errors.New("panel unreachable")
	c := &guardClient{err: boom}
	s := New(&fakePool{xui: c}, &fakeOwnership{})
	if err := s.EnsureInboundDeletable(context.Background(), 1, 5); !errors.Is(err, boom) {
		t.Fatalf("lookup error swallowed: %v", err)
	}

	c = &guardClient{inb: &ports.Inbound{ID: 5, ClientStats: []ports.ClientTraffic{{Email: "friend@x"}}}}
	notOwned := false
	s = New(&fakePool{xui: c}, &fakeOwnership{existsVal: &notOwned})
	if err := s.EnsureInboundDeletable(context.Background(), 1, 5); !errors.Is(err, domain.ErrInboundHasUnmanagedClients) {
		t.Fatalf("unmanaged client must block the delete, got %v", err)
	}
	if err := s.DeleteInbound(context.Background(), 1, 5); !errors.Is(err, domain.ErrInboundHasUnmanagedClients) || c.dels != 0 {
		t.Fatalf("DeleteInbound err=%v dels=%d", err, c.dels)
	}
}
