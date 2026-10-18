package vlessroute

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestLocalTargetOnlySaveAndNoOpSkipRemoteWrites(t *testing.T) {
	s, _, c := serviceFixture(t)
	invalidations := 0
	s.invalidate = func() { invalidations++ }
	view, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{Name: "B", OutboundTag: "direct", Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	input := LineInput{ID: view.State.Lines[0].ID, Name: "renamed", OnlyLineName: true, OutboundTag: "direct", Enabled: true}
	revision, previousInvalidations := view.State.Revision, invalidations
	before := append(json.RawMessage(nil), c.template.Config...)
	view, err = s.Save(context.Background(), 1, SaveRequest{Revision: revision, Lines: []LineInput{input}})
	if err != nil {
		t.Fatal(err)
	}
	if c.writes != 1 || !sameConfig(before, c.template.Config) || view.State.Revision != revision+1 || invalidations != previousInvalidations+1 {
		t.Fatalf("local save: writes=%d revision=%d invalidations=%d", c.writes, view.State.Revision, invalidations)
	}
	revision, previousInvalidations = view.State.Revision, invalidations
	view, err = s.Save(context.Background(), 1, SaveRequest{Revision: revision, Lines: []LineInput{input}})
	if err != nil || c.writes != 1 || view.State.Revision != revision || invalidations != previousInvalidations {
		t.Fatalf("no-op save mutated state: %+v %v", view, err)
	}
	view, err = s.Save(context.Background(), 1, SaveRequest{Revision: revision, Lines: []LineInput{{ID: input.ID, Name: input.Name, OutboundTag: "direct", Enabled: true, Port: "443"}}})
	if err != nil || c.writes != 2 || view.State.Status != "applied" {
		t.Fatalf("real rule change skipped: %+v %v", view, err)
	}
}

func TestLocalSaveCoreFailureKeepsTargetButWithdraws(t *testing.T) {
	s, n, c := serviceFixture(t)
	view, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{Name: "B", OutboundTag: "direct", Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	c.statusFailureAt = c.statusReads + 2
	view, err = s.Save(context.Background(), 1, SaveRequest{Revision: view.State.Revision, Lines: []LineInput{{ID: view.State.Lines[0].ID, Name: "new", OutboundTag: "direct", Enabled: true}}})
	if err != nil || c.writes != 1 || view.State.Status != "unknown" || n.node.RouteState.Lines[0].Name != "new" {
		t.Fatalf("failed confirmation lost the draft or wrote remotely: %+v %v", view, err)
	}
}

func TestEditorRefreshReadsOneSnapshotAndKeepsEmptyState(t *testing.T) {
	for _, configured := range []bool{false, true} {
		s, _, c := serviceFixture(t)
		if configured {
			if _, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{Name: "B", OutboundTag: "direct", Enabled: true}}}); err != nil {
				t.Fatal(err)
			}
		}
		reads, statuses, inbounds, writes := c.reads, c.statusReads, c.inboundReads, c.writes
		view, err := s.RefreshEditor(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}
		wantInbounds := 0
		if configured {
			wantInbounds = 1
		}
		if c.reads-reads != 1 || c.statusReads-statuses != 1 || c.inboundReads-inbounds != wantInbounds || c.writes != writes || len(view.Outbounds) != 1 {
			t.Fatalf("duplicate snapshot: reads=%d status=%d inbound=%d view=%+v", c.reads-reads, c.statusReads-statuses, c.inboundReads-inbounds, view)
		}
		if !configured && view.State.Status != "" {
			t.Fatal("catalog read invented applied state")
		}
		c.readError = true
		view, err = s.RefreshEditor(context.Background(), 1)
		if err != nil || view.OutboundsError != "catalog_unavailable" || len(view.Outbounds) != 0 {
			t.Fatalf("catalog failure disguised as empty success: %+v %v", view, err)
		}
		if configured && view.State.Status != "unknown" {
			t.Fatal("unavailable remote remained applied")
		}
	}
}

func TestPublicRouteViewsOmitRemovedReasonAndOutboundSource(t *testing.T) {
	s, n, _ := serviceFixture(t)
	n.node.DesiredProtocol = "trojan"
	view, err := s.RefreshEditor(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(view)
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	if _, exists := decoded["reason"]; exists || view.Supported {
		t.Fatalf("unsupported response: %s", raw)
	}
	if view.State.Status == "applied" {
		t.Fatal("unsupported node was confirmed")
	}
}

func TestEditorDoesNotConfirmSnapshotAfterBackendURLChanges(t *testing.T) {
	s, _, c := serviceFixture(t)
	if _, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{OutboundTag: "direct", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	c.afterRead = func() { s.repos.XUIPanel.(*testPanelRepo).panel.URL = "https://other.example" }
	view, err := s.RefreshEditor(context.Background(), 1)
	if err != nil || view.State.Status != "drift" || view.OutboundsError != "catalog_unavailable" || len(view.Outbounds) != 0 || c.writes != 1 {
		t.Fatalf("confirmed old backend: %+v %v", view, err)
	}
}

func TestRefreshUsesLockedRegistryRatherThanUnlockedEmptySnapshot(t *testing.T) {
	s, _, c := serviceFixture(t)
	stale, _ := s.repos.Node.GetByID(context.Background(), 1)
	panel, _ := s.repos.XUIPanel.GetByID(context.Background(), 1)
	if _, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{OutboundTag: "direct", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	reads, inbounds := c.reads, c.inboundReads
	checked, err := s.checkGroup(context.Background(), panel, []*domain.Node{stale}, true)
	if err != nil || checked.nodes[1].RouteState.Status != "applied" || c.reads-reads != 1 || c.inboundReads-inbounds != 1 {
		t.Fatalf("unlocked scan controlled verification: %+v %v", checked, err)
	}
}

func TestRefreshRechecksNodeBindingAfterSlowInboundRead(t *testing.T) {
	s, n, c := serviceFixture(t)
	if _, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{OutboundTag: "direct", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	c.afterInbounds = func() { n.node.DesiredPort = 8443 }
	view, err := s.RefreshEditor(context.Background(), 1)
	if err != nil || view.State.Status != "drift" || view.State.Error != "inbound_binding_changed" || c.writes != 1 {
		t.Fatalf("stale node binding confirmed: %+v %v", view, err)
	}
}

type recordingRouteLease struct {
	ports.NodeRouteStore
	backend, owner string
	released       bool
	t              *testing.T
}

func (r *recordingRouteLease) AcquireRouteLease(_ context.Context, backend, owner string, expires time.Time) (bool, error) {
	r.backend, r.owner = backend, owner
	if remaining := time.Until(expires); remaining < 89*time.Second || remaining > 90*time.Second {
		r.t.Fatalf("unexpected lease duration: %s", remaining)
	}
	return true, nil
}

func (r *recordingRouteLease) ReleaseRouteLease(ctx context.Context, backend, owner string) error {
	r.released = true
	deadline, bounded := ctx.Deadline()
	if ctx.Err() != nil || !bounded || time.Until(deadline) > 3*time.Second || backend != r.backend || owner != r.owner || owner == "" {
		r.t.Fatalf("release lost ownership or inherited canceled operation: %s %s %v", backend, owner, ctx.Err())
	}
	return nil
}

func TestLeaseReleaseRetainsOwnerAfterOperationCancellation(t *testing.T) {
	record := &recordingRouteLease{t: t}
	s := &Service{repos: ports.Repos{NodeRoute: record}}
	ctx, cancel := context.WithCancel(context.Background())
	release, acquired, err := s.acquireBackendLease(ctx, "https://panel.example/", true)
	if err != nil || !acquired {
		t.Fatalf("lease failed: %v", err)
	}
	cancel()
	release()
	if !record.released {
		t.Fatal("lease not released")
	}
}
