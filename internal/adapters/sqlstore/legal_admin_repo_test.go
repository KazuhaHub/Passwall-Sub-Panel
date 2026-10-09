package sqlstore

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestLegalAdmin_HistoryAcrossLocalesIsBounded(t *testing.T) {
	_, repos := legalTestRepos(t)
	ctx := context.Background()
	if rows, err := repos.Legal.HistoryByKind(ctx, "terms", 0, 50); err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("empty history %+v: %v", rows, err)
	}
	var publications []domain.LegalPublication
	for _, locale := range []string{"en-US", "zh-CN", "en-US"} {
		p, err := repos.Legal.Publish(ctx, legalDraft("terms", locale, locale+" body", false))
		if err != nil {
			t.Fatal(err)
		}
		publications = append(publications, p)
	}
	if _, err := repos.Legal.Publish(ctx, legalDraft("privacy", "en-US", "privacy", false)); err != nil {
		t.Fatal(err)
	}
	rows, err := repos.Legal.HistoryByKind(ctx, "terms", 0, 2)
	if err != nil || len(rows) != 2 || rows[0].ID != publications[2].Document.ID || rows[1].ID != publications[1].Document.ID || rows[0].PublishedBy != 42 || rows[0].Version != 2 {
		t.Fatalf("first page %+v: %v", rows, err)
	}
	rows, err = repos.Legal.HistoryByKind(ctx, "terms", rows[1].ID, 2)
	if err != nil || len(rows) != 1 || rows[0].ID != publications[0].Document.ID {
		t.Fatalf("next page %+v: %v", rows, err)
	}
	for _, tc := range []struct {
		kind   string
		before int64
		limit  int
	}{{"unknown", 0, 50}, {"terms", -1, 50}, {"terms", 0, 51}, {"terms", 0, 0}} {
		if _, err := repos.Legal.HistoryByKind(ctx, tc.kind, tc.before, tc.limit); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("invalid bounds %+v: %v", tc, err)
		}
	}
}
