package node

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// getByIDRepo answers GetByID with one node so the mutating paths below reach
// their write instead of bailing out on a lookup.
type getByIDRepo struct {
	fakeNodeRepo
	node *domain.Node
	// enabledWritten / enabledValue record the column-scoped write so a test
	// can assert the local row was committed even when the panel push and its
	// retry both failed.
	enabledWritten bool
	enabledValue   bool
}

func (r *getByIDRepo) UpdateEnabled(_ context.Context, _ int64, enabled bool) error {
	r.enabledWritten, r.enabledValue = true, enabled
	return nil
}

func (r *getByIDRepo) GetByID(context.Context, int64) (*domain.Node, error) {
	cp := *r.node
	return &cp, nil
}

type enableCapableClient struct{ ports.XUIClient }

func (enableCapableClient) Capabilities() []ports.PanelCapability {
	return []ports.PanelCapability{ports.CapabilityInboundEnable}
}
func (enableCapableClient) SetInboundEnable(context.Context, int, bool) error { return nil }

type oneClientPool struct {
	ports.XUIPool
	c ports.XUIClient
}

func (p oneClientPool) Get(int64) (ports.XUIClient, error) { return p.c, nil }

// The two mutations a user actually reported, at the service level.
//
// Both wrote to the database correctly and both returned success — the admin
// UI was telling the truth about the write. What neither did was drop the
// rendered-subscription cache, so for up to its TTL the subscription kept
// serving the node or separator that had just been switched off. "The write
// succeeded and nothing happened" is the exact shape this repository keeps
// producing, and the enable/disable path is the one where it has teeth: an
// operator taking a node out of rotation is usually doing it for a reason.
func TestDisablingInvalidatesTheSubscriptionCache(t *testing.T) {
	t.Run("node", func(t *testing.T) {
		repo := &getByIDRepo{node: &domain.Node{ID: 7, PanelID: 1, InboundID: 11, Enabled: true}}
		svc := &Service{nodes: repo, pool: oneClientPool{c: enableCapableClient{}}}
		invalidations := 0
		svc.SetSubscriptionInvalidator(func() { invalidations++ })

		if err := svc.SetEnabled(context.Background(), 7, false); err != nil {
			t.Fatal(err)
		}
		if invalidations != 1 {
			t.Fatalf("invalidations = %d, want 1 — a disabled node kept being served until this fired", invalidations)
		}
	})

	t.Run("separator", func(t *testing.T) {
		repo := &captureSeparatorRepo{stored: &domain.SeparatorEntry{ID: 1, SortOrder: 10, Enabled: true}}
		svc := &Service{separators: repo}
		invalidations := 0
		svc.SetSubscriptionInvalidator(func() { invalidations++ })

		err := svc.UpdateSeparator(context.Background(), &domain.SeparatorEntry{
			ID: 1, DisplayName: "---- TW ----", SortOrder: 10, Enabled: false,
		})
		if err != nil {
			t.Fatal(err)
		}
		if invalidations != 1 {
			t.Fatalf("invalidations = %d, want 1 — the separator stayed in the subscription until this fired", invalidations)
		}
	})
}

// Creating and deleting move a row in and out of the subscription just as
// surely as disabling does, and both were equally silent.
func TestSeparatorCreateAndDeleteInvalidate(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*Service) error
	}{
		{"create", func(s *Service) error {
			return s.CreateSeparator(context.Background(), &domain.SeparatorEntry{DisplayName: "---- JP ----"})
		}},
		{"delete", func(s *Service) error {
			return s.DeleteSeparator(context.Background(), 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &Service{separators: &captureSeparatorRepo{stored: &domain.SeparatorEntry{ID: 1, SortOrder: 10}}, nodes: &fakeNodeRepo{}}
			invalidations := 0
			svc.SetSubscriptionInvalidator(func() { invalidations++ })
			if err := tc.run(svc); err != nil {
				t.Fatal(err)
			}
			if invalidations != 1 {
				t.Fatalf("invalidations = %d, want 1", invalidations)
			}
		})
	}
}

// Re-wiring must not stack decorators: two wrappers would drop every rendered
// subscription twice per write, doubling the re-render stampede.
func TestRewiringTheInvalidatorDoesNotStackDecorators(t *testing.T) {
	svc := &Service{separators: &captureSeparatorRepo{stored: &domain.SeparatorEntry{ID: 1, SortOrder: 10}}, nodes: &fakeNodeRepo{}}
	invalidations := 0
	svc.SetSubscriptionInvalidator(func() { invalidations++ })
	svc.SetSubscriptionInvalidator(func() { invalidations++ })

	if err := svc.DeleteSeparator(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if invalidations != 1 {
		t.Fatalf("invalidations = %d, want 1 — the decorator stacked", invalidations)
	}
}
