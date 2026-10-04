package sqlstore

import (
	"bytes"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

func newDestDefinitionRepo(t *testing.T) *DestDefinitionRepo {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	return NewDestDefinitionRepo(db)
}

func destinationTestPolicy(name string) domain.DestPolicy {
	return domain.DestPolicy{Name: name, Action: domain.DestBlock, Scope: domain.DestScopeAll, Inline: domain.DestInline{Ports: "25,465,587"}, Enabled: true}
}

func TestDestinationPauseAndPublishedStateKeepBodyAndLiveFlagConsistent(t *testing.T) {
	r := newDestDefinitionRepo(t)
	now := time.UnixMilli(1791000000000).UTC()
	p := destinationTestPolicy("pause")
	if err := r.SavePolicy(t.Context(), &p, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"generation":1}`)
	if err := r.Publish(t.Context(), 1, 0, body, now); err != nil {
		t.Fatal(err)
	}
	if err := r.SetPaused(t.Context(), true, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	state, snapshot, found, err := r.PublishedState(t.Context())
	if err != nil || !found || !state.Paused || state.Generation != 2 || state.PublishedGeneration != 1 || snapshot.Generation != state.PublishedGeneration || !bytes.Equal(snapshot.Body, body) || state.FirstUnpublishedAt == nil {
		t.Fatalf("pause read mixed state/snapshot: %+v %+v / %v", state, snapshot, err)
	}
	if err := r.SetPaused(t.Context(), true, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	after, _, _, err := r.PublishedState(t.Context())
	if err != nil || after.Generation != state.Generation || !after.LastWriteAt.Equal(*state.LastWriteAt) {
		t.Fatalf("repeated pause advanced generation: %+v / %v", after, err)
	}
}

func TestDestinationDefinitionWriteAdvancesGenerationWithTheRow(t *testing.T) {
	r := newDestDefinitionRepo(t)
	first := time.UnixMilli(1791000000000).UTC()
	p := destinationTestPolicy("first")
	if err := r.SavePolicy(t.Context(), &p, time.Time{}, first); err != nil {
		t.Fatal(err)
	}
	state, err := r.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if p.ID <= 0 || p.Priority != 1 || state.Generation != 1 || state.PublishedGeneration != 0 || state.FirstUnpublishedAt == nil || !state.FirstUnpublishedAt.Equal(first) {
		t.Fatalf("definition/generation not committed together: policy=%+v state=%+v", p, state)
	}
	second := first.Add(time.Minute)
	other := destinationTestPolicy("second")
	if err := r.SavePolicy(t.Context(), &other, time.Time{}, second); err != nil {
		t.Fatal(err)
	}
	state, err = r.State(t.Context())
	if err != nil || state.Generation != 2 || !state.FirstUnpublishedAt.Equal(first) || !state.LastWriteAt.Equal(second) || other.Priority != 2 {
		t.Fatalf("continuous writes reset first pending time or lost generation: %+v / %v", state, err)
	}
}

func TestDestinationPolicyRejectsStaleWritesIncludingSameMillisecond(t *testing.T) {
	r := newDestDefinitionRepo(t)
	now := time.UnixMilli(1791000000000).UTC()
	p := destinationTestPolicy("original")
	if err := r.SavePolicy(t.Context(), &p, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	old := p.UpdatedAt
	p.Name = "changed"
	if err := r.SavePolicy(t.Context(), &p, old, now); err != nil {
		t.Fatal(err)
	}
	if !p.UpdatedAt.After(old) {
		t.Fatal("same-millisecond edits share a stale-check token")
	}
	p.Name = "stale overwrite"
	if err := r.SavePolicy(t.Context(), &p, old, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale write accepted: %v", err)
	}
	defs, err := r.ReadDefinitions(t.Context())
	if err != nil || len(defs.Policies) != 1 || defs.Policies[0].Name != "changed" || defs.State.Generation != 2 {
		t.Fatalf("stale write changed a row or generation: %+v / %v", defs, err)
	}
}

func TestDestinationPublishCASPreservesConcurrentDefinitionsAndLastSnapshot(t *testing.T) {
	r := newDestDefinitionRepo(t)
	now := time.UnixMilli(1791000000000).UTC()
	p := destinationTestPolicy("one")
	if err := r.SavePolicy(t.Context(), &p, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	body1 := []byte(`{"generation":1,"policies":["one"]}`)
	if err := r.Publish(t.Context(), 1, 0, body1, now); err != nil {
		t.Fatal(err)
	}
	p2 := destinationTestPolicy("two")
	if err := r.SavePolicy(t.Context(), &p2, time.Time{}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	defs, err := r.ReadDefinitions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	p3 := destinationTestPolicy("three")
	if err := r.SavePolicy(t.Context(), &p3, time.Time{}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := r.Publish(t.Context(), defs.State.Generation, defs.State.PublishedGeneration, []byte(`{"stale":true}`), now.Add(3*time.Second)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("old definitions published as a new generation: %v", err)
	}
	snapshot, found, err := r.Published(t.Context())
	state, stateErr := r.State(t.Context())
	if err != nil || stateErr != nil || !found || !bytes.Equal(snapshot.Body, body1) || state.Generation != 3 || state.PublishedGeneration != 1 || state.FirstUnpublishedAt == nil {
		t.Fatalf("CAS failure damaged published state: %+v / %+v / %v / %v", snapshot, state, err, stateErr)
	}
	if err := r.RecordPublishError(t.Context(), 3, 1, domain.DestPublishError{Kind: "domains", Used: 50001, Limit: 50000}, now); err != nil {
		t.Fatal(err)
	}
	state, err = r.State(t.Context())
	if err != nil || state.PublishError == nil || state.PublishedGeneration != 1 {
		t.Fatalf("publish refusal overwrote published state: %+v / %v", state, err)
	}
	if err := r.Publish(t.Context(), 3, 1, []byte(`{"generation":3}`), now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	state, err = r.State(t.Context())
	if err != nil || state.PublishError != nil || state.PublishErrorAt != nil || state.FirstUnpublishedAt != nil || state.PublishedGeneration != 3 {
		t.Fatalf("successful publication did not clear pending/error state: %+v / %v", state, err)
	}
	var count int64
	if err := r.db.Model(&destPolicySnapshotRow{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("old snapshots retained: %d / %v", count, err)
	}
}

func TestDestinationWriteAndPublicationFailuresRollbackAllOwnedState(t *testing.T) {
	r := newDestDefinitionRepo(t)
	now := time.UnixMilli(1791000000000).UTC()
	p := destinationTestPolicy("rollback")
	boom := errors.New("injected state write failure")
	name := "test:destination-state-failure"
	if err := r.db.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_policy_state" {
			tx.AddError(boom)
		}
	}); err != nil {
		t.Fatal(err)
	}
	err := r.SavePolicy(t.Context(), &p, time.Time{}, now)
	r.db.Callback().Update().Remove(name)
	if !errors.Is(err, boom) {
		t.Fatalf("state failure not surfaced: %v", err)
	}
	var count int64
	if err := r.db.Model(&destPolicyRow{}).Count(&count).Error; err != nil || count != 0 || p.ID != 0 {
		t.Fatalf("failed state write left a definition or caller id: %d / %d / %v", count, p.ID, err)
	}
	if err := r.SavePolicy(t.Context(), &p, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	name = "test:destination-snapshot-failure"
	if err := r.db.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_policy_snapshots" {
			tx.AddError(boom)
		}
	}); err != nil {
		t.Fatal(err)
	}
	err = r.Publish(t.Context(), 1, 0, []byte(`{"generation":1}`), now)
	r.db.Callback().Create().Remove(name)
	state, stateErr := r.State(t.Context())
	if !errors.Is(err, boom) || stateErr != nil || state.PublishedGeneration != 0 || state.FirstUnpublishedAt == nil {
		t.Fatalf("snapshot failure committed publication state: %+v / %v / %v", state, err, stateErr)
	}
}

func TestDestinationConsistentReadUsesTransactionBeforeReadingDefinitions(t *testing.T) {
	r := newDestDefinitionRepo(t)
	now := time.UnixMilli(1791000000000).UTC()
	p := destinationTestPolicy("before")
	if err := r.SavePolicy(t.Context(), &p, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	var read atomic.Bool
	var queryErr error
	name := "test:destination-read-interleave"
	if err := r.db.Callback().Query().After("gorm:query").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table != "dest_policy_state" || read.Swap(true) {
			return
		}
		if _, ok := tx.Statement.ConnPool.(*sql.Tx); !ok {
			queryErr = errors.New("generation read happened outside SQL transaction")
			return
		}
		if r.db.Dialector.Name() == "sqlite" {
			return
		} // one pooled connection blocks writers while this read transaction is held
		other := destinationTestPolicy("during")
		queryErr = r.SavePolicy(t.Context(), &other, time.Time{}, now.Add(time.Second))
	}); err != nil {
		t.Fatal(err)
	}
	defs, err := r.ReadDefinitions(t.Context())
	r.db.Callback().Query().Remove(name)
	if err != nil || queryErr != nil || !read.Load() || defs.State.Generation != 1 || len(defs.Policies) != 1 || defs.Policies[0].Name != "before" {
		t.Fatalf("generation and definitions were not one snapshot: %+v / %v / %v", defs, err, queryErr)
	}
	if r.db.Dialector.Name() != "sqlite" {
		state, err := r.State(t.Context())
		if err != nil || state.Generation != 2 {
			t.Fatalf("interleaved write did not commit: %+v / %v", state, err)
		}
	}
}
