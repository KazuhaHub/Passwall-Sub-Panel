package sqlstore

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestDestinationNoOpPolicyDoesNotPublishAndClockRollbackCannotReuseVersion(t *testing.T) {
	r := newDestDefinitionRepo(t)
	now := time.UnixMilli(1791000000000).UTC()
	p := destinationTestPolicy("unchanged")
	if err := r.SavePolicy(t.Context(), &p, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	version := p.UpdatedAt
	if err := r.SavePolicy(t.Context(), &p, version, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	s, err := r.State(t.Context())
	if err != nil || s.Generation != 1 || !p.UpdatedAt.Equal(version) {
		t.Fatalf("no-op changed generation/version: %+v / %+v / %v", p, s, err)
	}
	p.Enabled, p.CountsAsRisk = false, true
	if err := r.SavePolicy(t.Context(), &p, version, now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	defs, err := r.ReadDefinitions(t.Context())
	if err != nil || defs.Policies[0].Enabled || !defs.Policies[0].CountsAsRisk || !p.UpdatedAt.After(version) || !defs.Policies[0].UpdatedAt.Equal(p.UpdatedAt) || defs.State.Generation != 2 {
		t.Fatalf("clock rollback or false fields lost: %+v / %v", defs, err)
	}
}

func TestDestinationConcurrentPolicyCreationAllocatesPerActionPriorities(t *testing.T) {
	r := newDestDefinitionRepo(t)
	now := time.UnixMilli(1791000000000).UTC()
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	for i := range 12 {
		wg.Go(func() {
			p := destinationTestPolicy(fmt.Sprintf("policy-%d", i))
			errors <- r.SavePolicy(t.Context(), &p, time.Time{}, now)
		})
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	defs, err := r.ReadDefinitions(t.Context())
	if err != nil || len(defs.Policies) != 12 || defs.State.Generation != 12 {
		t.Fatalf("lost concurrent definitions: %+v / %v", defs, err)
	}
	for i, p := range defs.Policies {
		if p.Priority != i+1 {
			t.Fatalf("priority collision: %+v", defs.Policies)
		}
	}
	p := destinationTestPolicy("allow")
	p.Action, p.Priority = domain.DestAllow, 999
	if err := r.SavePolicy(t.Context(), &p, time.Time{}, now); err != nil || p.Priority != 1 {
		t.Fatalf("client priority accepted or actions shared ordering: %+v / %v", p, err)
	}
	p.Action, p.Priority = domain.DestBlock, 999
	if err := r.SavePolicy(t.Context(), &p, p.UpdatedAt, now); err != nil || p.Priority != 13 {
		t.Fatalf("action change not appended: %+v / %v", p, err)
	}
}

func TestDestinationReorderRejectsIncompleteAndDuplicateIDsAndInvalidatesOldForms(t *testing.T) {
	r := newDestDefinitionRepo(t)
	now := time.UnixMilli(1791000000000).UTC()
	one, two := destinationTestPolicy("one"), destinationTestPolicy("two")
	two.Enabled = false
	for _, p := range []*domain.DestPolicy{&one, &two} {
		if err := r.SavePolicy(t.Context(), p, time.Time{}, now); err != nil {
			t.Fatal(err)
		}
	}
	for _, ids := range [][]int64{{one.ID}, {one.ID, one.ID}, {one.ID, two.ID, 999}} {
		if err := r.ReorderPolicies(t.Context(), domain.DestBlock, ids, now); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("stale order accepted: %v / %v", ids, err)
		}
	}
	if err := r.ReorderPolicies(t.Context(), domain.DestBlock, []int64{two.ID, one.ID}, now); err != nil {
		t.Fatal(err)
	}
	if err := r.SavePolicy(t.Context(), &one, one.UpdatedAt, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("old form accepted after reorder: %v", err)
	}
	defs, err := r.ReadDefinitions(t.Context())
	if err != nil || defs.State.Generation != 3 || defs.Policies[0].ID != two.ID || defs.Policies[1].Priority != 2 {
		t.Fatalf("ordering and generation not one transaction: %+v / %v", defs, err)
	}
	if err := r.ReorderPolicies(t.Context(), domain.DestBlock, []int64{two.ID, one.ID}, now); err != nil {
		t.Fatal(err)
	}
	s, err := r.State(t.Context())
	if err != nil || s.Generation != 3 {
		t.Fatalf("no-op reorder published: %+v / %v", s, err)
	}
}

func TestDestinationListRefreshCannotOverwriteEditedOrDeletedSources(t *testing.T) {
	r := newDestDefinitionRepo(t)
	now := time.UnixMilli(1791000000000).UTC()
	list := domain.DestList{Name: "remote", Kind: domain.DestListRemote, SourceURL: "https://example.org/old"}
	if err := r.SaveList(t.Context(), &list, time.Time{}, now); err != nil || list.ID == 0 {
		t.Fatalf("list not committed: %+v / %v", list, err)
	}
	old := list
	list.SourceURL = "https://example.org/new"
	if err := r.SaveList(t.Context(), &list, old.UpdatedAt, now); err != nil {
		t.Fatal(err)
	}
	result := domain.DestListRefresh{Entries: []byte("domain:example.org\n"), ContentSHA256: "sha1", EntryCount: 1}
	if err := r.CommitListRefresh(t.Context(), old, result, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("old source result accepted: %v", err)
	}
	if err := r.CommitListRefresh(t.Context(), list, result, now); err != nil {
		current, readErr := r.ReadDefinitions(t.Context())
		t.Fatalf("current source rejected: captured=%+v stored=%+v read=%v err=%v", list, current.Lists, readErr, err)
	}
	defs, err := r.ReadDefinitions(t.Context())
	if err != nil || len(defs.Lists) != 1 || !bytes.Equal(defs.Lists[0].Entries, result.Entries) || defs.State.Generation != 3 {
		t.Fatalf("fresh content not atomic: %+v / %v", defs, err)
	}
	current := defs.Lists[0]
	if err := r.CommitListRefresh(t.Context(), list, domain.DestListRefresh{LastError: "old failure"}, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("same-ms old failure overwrote success: %v", err)
	}
	if err := r.CommitListRefresh(t.Context(), current, result, now); err != nil {
		t.Fatal(err)
	}
	defs, err = r.ReadDefinitions(t.Context())
	if err != nil || defs.State.Generation != 3 || !defs.Lists[0].UpdatedAt.After(current.UpdatedAt) {
		t.Fatalf("unchanged refresh republished or reused version: %+v / %v", defs, err)
	}
	current = defs.Lists[0]
	if err := r.CommitListRefresh(t.Context(), current, domain.DestListRefresh{LastError: "network failure", Entries: []byte("wrong")}, now); err != nil {
		t.Fatal(err)
	}
	defs, err = r.ReadDefinitions(t.Context())
	if err != nil || defs.State.Generation != 3 || !bytes.Equal(defs.Lists[0].Entries, result.Entries) || defs.Lists[0].LastError != "network failure" || !defs.Lists[0].LastFetchedAt.Equal(*current.LastFetchedAt) {
		t.Fatalf("failure destroyed usable list: %+v / %v", defs, err)
	}
	if err := r.DeleteList(t.Context(), list.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitListRefresh(t.Context(), current, result, now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted list revived: %v", err)
	}
	defs, err = r.ReadDefinitions(t.Context())
	if err != nil || len(defs.Lists) != 0 || defs.State.Generation != 4 {
		t.Fatalf("delete not final: %+v / %v", defs, err)
	}
}

func TestDestinationListDeleteChecksDisabledReferences(t *testing.T) {
	r := newDestDefinitionRepo(t)
	now := time.UnixMilli(1791000000000).UTC()
	list := domain.DestList{Name: "used", Kind: domain.DestListCustom, ContentSHA256: "sha1", Entries: []byte("domain:example.org\n"), EntryCount: 1}
	if err := r.SaveList(t.Context(), &list, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	p := destinationTestPolicy("disabled")
	p.ListIDs, p.Enabled = []int64{list.ID}, false
	if err := r.SavePolicy(t.Context(), &p, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	err := r.DeleteList(t.Context(), list.ID, now)
	var used *domain.DestListInUseError
	if !errors.As(err, &used) || len(used.UsedBy) != 1 || used.UsedBy[0].ID != p.ID || used.UsedBy[0].Name != p.Name {
		t.Fatalf("disabled reference omitted: %v", err)
	}
	if err := r.DeletePolicy(t.Context(), p.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteList(t.Context(), list.ID, now); err != nil {
		t.Fatal(err)
	}
	s, err := r.State(t.Context())
	if err != nil || s.Generation != 4 {
		t.Fatalf("refusal advanced generation: %+v / %v", s, err)
	}
}

func TestDestinationExemptionDuplicatesAndExpiryAreAtomic(t *testing.T) {
	r := newDestDefinitionRepo(t)
	now := time.UnixMilli(1791000000000).UTC()
	until := now.Add(time.Hour)
	ex := domain.DestExemption{UserID: 12, Reason: "temporary", CreatedBy: 9, ExpiresAt: &until}
	if err := r.SaveExemption(t.Context(), &ex, true, now); err != nil {
		t.Fatal(err)
	}
	if err := r.SaveExemption(t.Context(), &ex, true, now); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate exemption accepted: %v", err)
	}
	permanent := domain.DestExemption{UserID: 13, Reason: "permanent", CreatedBy: 9}
	if err := r.SaveExemption(t.Context(), &permanent, true, now); err != nil {
		t.Fatal(err)
	}
	if n, err := r.PruneExpiredExemptions(t.Context(), until.Add(-time.Millisecond)); err != nil || n != 0 {
		t.Fatalf("early expiry: %d / %v", n, err)
	}
	if n, err := r.PruneExpiredExemptions(t.Context(), until); err != nil || n != 1 {
		t.Fatalf("expiry not inclusive: %d / %v", n, err)
	}
	defs, err := r.ReadDefinitions(t.Context())
	if err != nil || defs.State.Generation != 3 || len(defs.Exemptions) != 1 || defs.Exemptions[0].UserID != 13 || defs.Exemptions[0].ExpiresAt != nil {
		t.Fatalf("expiry removed permanent row or lost generation: %+v / %v", defs, err)
	}
	if err := r.DeleteExemption(t.Context(), 13, until); err != nil {
		t.Fatal(err)
	}
}

func TestDestinationLatePublicationErrorCannotReplaceNewerState(t *testing.T) {
	r := newDestDefinitionRepo(t)
	now := time.UnixMilli(1791000000000).UTC()
	p := destinationTestPolicy("one")
	if err := r.SavePolicy(t.Context(), &p, time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	issue := domain.DestPublishError{Kind: "domains", Used: 50001, Limit: 50000}
	if err := r.RecordPublishError(t.Context(), 1, 0, issue, now); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordPublishError(t.Context(), 1, 0, issue, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	s, err := r.State(t.Context())
	if err != nil || s.PublishErrorAt == nil || !s.PublishErrorAt.Equal(now) {
		t.Fatalf("identical error reset first failure: %+v / %v", s, err)
	}
	p.Name = "two"
	if err := r.SavePolicy(t.Context(), &p, p.UpdatedAt, now); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordPublishError(t.Context(), 1, 0, issue, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("old error attached to new definitions: %v", err)
	}
	if err := r.Publish(t.Context(), 2, 0, []byte(`{"generation":2}`), now); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordPublishError(t.Context(), 2, 0, issue, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("old error overwrote successful publication: %v", err)
	}
	s, err = r.State(t.Context())
	if err != nil || s.PublishError != nil || s.PublishedGeneration != 2 {
		t.Fatalf("successful publication damaged: %+v / %v", s, err)
	}
}

func TestDestinationMissingOrCorruptPublishedSnapshotIsAnError(t *testing.T) {
	for _, body := range [][]byte{nil, []byte("broken"), []byte("null")} {
		r := newDestDefinitionRepo(t)
		now := time.UnixMilli(1791000000000).UTC()
		p := destinationTestPolicy("one")
		if err := r.SavePolicy(t.Context(), &p, time.Time{}, now); err != nil {
			t.Fatal(err)
		}
		if err := r.Publish(t.Context(), 1, 0, []byte(`{"generation":1}`), now); err != nil {
			t.Fatal(err)
		}
		if body == nil {
			if err := r.db.Where("generation = ?", 1).Delete(&destPolicySnapshotRow{}).Error; err != nil {
				t.Fatal(err)
			}
		} else {
			if err := r.db.Model(&destPolicySnapshotRow{}).Where("generation = ?", 1).Update("body", destBytes(body)).Error; err != nil {
				t.Fatal(err)
			}
		}
		_, found, err := r.Published(t.Context())
		if !errors.Is(err, domain.ErrUnavailable) || found {
			t.Fatalf("corrupt publication silently became empty: body=%s found=%v error=%v", body, found, err)
		}
	}
}
