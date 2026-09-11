package sqlstore

import (
	"context"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestNodeAgentIssueRepoDeduplicatesPreservesReviewAndFilters(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repo := NewRepos(db).NodeAgentIssue
	ctx := context.Background()
	firstSeen := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	issue := domain.NodeAgentIssue{
		Code: "object_rejected_timeout", Key: "cli_7", Detail: "roster object remained rejected",
	}
	if err := repo.RecordBatch(ctx, "agt_1", []domain.NodeAgentIssue{issue}, firstSeen); err != nil {
		t.Fatal(err)
	}
	secondSeen := firstSeen.Add(time.Minute)
	if err := repo.RecordBatch(ctx, "agt_1", []domain.NodeAgentIssue{
		issue,
		{Code: issue.Code, Key: issue.Key, Detail: "a different rejected version"},
	}, secondSeen); err != nil {
		t.Fatal(err)
	}
	items, total, err := repo.List(ctx, ports.NodeAgentIssueFilter{})
	if err != nil || total != 2 || len(items) != 2 {
		t.Fatalf("issue list = (%+v, %d, %v)", items, total, err)
	}
	var repeated *domain.NodeAgentIssue
	for _, item := range items {
		if item.Detail == issue.Detail {
			repeated = item
		}
	}
	if repeated == nil || !repeated.FirstSeenAt.Equal(firstSeen) || !repeated.LastSeenAt.Equal(secondSeen) {
		t.Fatalf("replayed issue = %+v", repeated)
	}
	acknowledgedAt := secondSeen.Add(time.Minute)
	if err := repo.Acknowledge(ctx, repeated.ID, acknowledgedAt); err != nil {
		t.Fatal(err)
	}
	thirdSeen := acknowledgedAt.Add(time.Minute)
	if err := repo.RecordBatch(ctx, "agt_1", []domain.NodeAgentIssue{issue}, thirdSeen); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, repeated.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AcknowledgedAt == nil || !got.AcknowledgedAt.Equal(acknowledgedAt) || !got.LastSeenAt.Equal(thirdSeen) {
		t.Fatalf("acknowledged replay = %+v", got)
	}
	unacknowledged := false
	filtered, total, err := repo.List(ctx, ports.NodeAgentIssueFilter{
		AgentID: "agt_1", Code: issue.Code, Acknowledged: &unacknowledged,
		Pagination: ports.Pagination{Keyword: "different", Page: 1, PageSize: 10},
	})
	if err != nil || total != 1 || len(filtered) != 1 || filtered[0].AcknowledgedAt != nil {
		t.Fatalf("filtered issue list = (%+v, %d, %v)", filtered, total, err)
	}
	if err := repo.Acknowledge(ctx, 999_999, acknowledgedAt); err != domain.ErrNotFound {
		t.Fatalf("missing issue acknowledge error = %v", err)
	}
}
