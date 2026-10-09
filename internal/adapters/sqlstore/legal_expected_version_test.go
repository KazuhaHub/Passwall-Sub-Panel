package sqlstore

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestLegalPublishExpectedVersionRejectsStaleDraft(t *testing.T) {
	db, repos := legalTestRepos(t)
	ctx := context.Background()
	invalidations := 0
	repos.Legal.(*legalRepo).invalidate = func() { invalidations++ }
	first, err := repos.Legal.Publish(ctx, legalDraft("privacy", "zh-CN", "first", false))
	if err != nil {
		t.Fatal(err)
	}
	stale := legalDraft("privacy", "zh-CN", "unreviewed replacement", true)
	zero := int64(0)
	stale.ExpectedVersion = &zero
	if _, err := repos.Legal.Publish(ctx, stale); !errors.Is(err, domain.ErrLegalVersionConflict) {
		t.Fatalf("stale publication accepted: %v", err)
	}
	current, err := repos.Legal.Latest(ctx, "privacy", "zh-CN")
	if err != nil || current.ID != first.Document.ID || current.Content != "first" {
		t.Fatalf("current = %+v, %v", current, err)
	}
	var state settingRow
	if err := db.Where("type = ? AND name = ?", "legal", "consent_version").First(&state).Error; err != nil || state.Value != "1" {
		t.Fatalf("consent changed: %+v, %v", state, err)
	}
	var rows int64
	if err := db.Model(&legalDocumentRow{}).Count(&rows).Error; err != nil || rows != 1 {
		t.Fatalf("rows = %d, %v", rows, err)
	}
	if invalidations != 1 {
		t.Fatalf("stale draft invalidated cache: %d", invalidations)
	}
}

func TestLegalPublishExpectedVersionIdentityAndConcurrentWinner(t *testing.T) {
	_, repos := legalTestRepos(t)
	ctx := context.Background()
	first, err := repos.Legal.Publish(ctx, legalDraft("terms", "zh-CN", "first", false))
	if err != nil {
		t.Fatal(err)
	}
	// A version in another kind or locale does not block first publication.
	for _, identity := range [][2]string{{"privacy", "zh-CN"}, {"terms", "en-US"}} {
		zero := int64(0)
		draft := legalDraft(identity[0], identity[1], "independent", false)
		draft.ExpectedVersion = &zero
		if _, err := repos.Legal.Publish(ctx, draft); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	errorsSeen := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			expected := first.Document.Version
			draft := legalDraft("terms", "zh-CN", "winner", true)
			draft.ExpectedVersion = &expected
			_, err := repos.Legal.Publish(ctx, draft)
			errorsSeen <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errorsSeen)
	winners, conflicts := 0, 0
	for err := range errorsSeen {
		if err == nil {
			winners++
		} else if errors.Is(err, domain.ErrLegalVersionConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d", winners, conflicts)
	}
	var state settingRow
	if err := repos.Legal.(*legalRepo).db.Where("type = ? AND name = ?", "legal", "consent_version").First(&state).Error; err != nil || state.Value != "2" {
		t.Fatalf("global version = %+v, %v", state, err)
	}
}
