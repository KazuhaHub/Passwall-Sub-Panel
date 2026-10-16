package sqlstore

import (
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

func TestRouteStateCASAndStaleNodeWrites(t *testing.T) {
	r, ctx := newNodeTestRepo(t)
	n := &domain.Node{PanelID: 1, InboundID: 9, DisplayName: "A"}
	if err := r.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	state := domain.NodeRouteState{Status: "applying", Lines: []domain.VlessRouteLine{{ID: "one", Route: 1001, Enabled: true, OutboundTag: "warp", OnlyLineName: true, EntryRelayID: "front"}}}
	if err := r.SaveRouteState(ctx, n.ID, 0, &state); err != nil {
		t.Fatal(err)
	}
	if state.Revision != 1 {
		t.Fatal(state.Revision)
	}
	if err := r.SaveRouteState(ctx, n.ID, 0, &state); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale CAS accepted: %v", err)
	}
	n.DisplayName = "renamed"
	if err := r.Update(ctx, n); err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateMetadata(ctx, n); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetByID(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RouteState.Revision != 1 || got.RouteState.Status != "applying" || len(got.RouteState.Lines) != 1 || !got.RouteState.Lines[0].OnlyLineName || got.RouteState.Lines[0].EntryRelayID != "front" || got.DisplayName != "renamed" {
		t.Fatalf("stale node write clobbered routes: %+v", got.RouteState)
	}
}

func TestRouteLeaseOwnershipAndExpiry(t *testing.T) {
	r, ctx := newNodeTestRepo(t)
	until := time.Now().UTC().Add(time.Minute)
	if ok, err := r.AcquireRouteLease(ctx, "backend", "one", until); err != nil || !ok {
		t.Fatalf("first acquire: %v %v", ok, err)
	}
	if ok, err := r.AcquireRouteLease(ctx, "backend", "two", until); err != nil || ok {
		t.Fatalf("concurrent acquire: %v %v", ok, err)
	}
	if err := r.ReleaseRouteLease(ctx, "backend", "two"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := r.AcquireRouteLease(ctx, "backend", "three", until); ok {
		t.Fatal("non-owner released lease")
	}
	if err := r.db.Model(&routeLeaseRow{}).Where("backend = ?", "backend").Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if ok, err := r.AcquireRouteLease(ctx, "backend", "two", until); err != nil || !ok {
		t.Fatalf("expired acquire: %v %v", ok, err)
	}
	if err := r.ReleaseRouteLease(ctx, "backend", "one"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := r.AcquireRouteLease(ctx, "backend", "three", until); ok {
		t.Fatal("stale owner released replacement lease")
	}
	if err := r.ReleaseRouteLease(ctx, "backend", "two"); err != nil {
		t.Fatal(err)
	}
	if ok, err := r.AcquireRouteLease(ctx, "backend", "three", until); err != nil || !ok {
		t.Fatalf("released acquire: %v %v", ok, err)
	}
}

func TestRouteConcurrentLeaseAndRevisionCAS(t *testing.T) {
	r, ctx := newNodeTestRepo(t)
	// Independent repository instances must coordinate in SQL, not through a
	// shared in-process mutex. This is the same boundary used by replicas.
	other := &nodeRepo{db: r.db.Session(&gorm.Session{NewDB: true})}
	start := make(chan struct{})
	results := make(chan struct {
		owner string
		ok    bool
		err   error
	}, 2)
	for i, repo := range []*nodeRepo{r, other} {
		go func(owner string, repo *nodeRepo) {
			<-start
			ok, err := repo.AcquireRouteLease(ctx, "concurrent-backend", owner, time.Now().UTC().Add(time.Minute))
			results <- struct {
				owner string
				ok    bool
				err   error
			}{owner, ok, err}
		}([]string{"one", "two"}[i], repo)
	}
	close(start)
	holders := 0
	for i := 0; i < 2; i++ {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.ok {
			holders++
		}
	}
	if holders != 1 {
		t.Fatalf("backend has %d concurrent lease holders", holders)
	}
	n := &domain.Node{PanelID: 1, InboundID: 7, DisplayName: "A"}
	if err := r.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	start = make(chan struct{})
	cas := make(chan error, 2)
	for _, repo := range []*nodeRepo{r, other} {
		go func(repo *nodeRepo) {
			<-start
			state := domain.NodeRouteState{Status: "applied"}
			cas <- repo.SaveRouteState(ctx, n.ID, 0, &state)
		}(repo)
	}
	close(start)
	applied, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		err := <-cas
		if err == nil {
			applied++
		} else if errors.Is(err, domain.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if applied != 1 || conflicts != 1 {
		t.Fatalf("CAS writers applied=%d conflicts=%d", applied, conflicts)
	}
	got, err := r.GetByID(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RouteState.Revision != 1 {
		t.Fatal("revision advanced more than once")
	}
}
