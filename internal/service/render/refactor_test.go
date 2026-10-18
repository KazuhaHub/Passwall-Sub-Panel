package render

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type countingRenderCredentials struct {
	ports.PSPClientRepo
	users, attachments int
}

func TestLineCandidateContractOmitsRedundantState(t *testing.T) {
	_, node, _, _ := referenceRenderFixture(t)
	raw, err := json.Marshal(inspectNodeLines([]*domain.Node{node}))
	if err != nil {
		t.Fatal(err)
	}
	var candidates []map[string]any
	if err := json.Unmarshal(raw, &candidates); err != nil {
		t.Fatal(err)
	}
	if len(candidates) == 0 {
		t.Fatal("missing candidate catalog")
	}
	for _, line := range candidates {
		for _, key := range []string{"name", "available", "line_id"} {
			if _, exists := line[key]; !exists {
				t.Fatalf("missing %s: %s", key, raw)
			}
		}
		for _, key := range []string{"enabled", "reason"} {
			if _, exists := line[key]; exists {
				t.Fatalf("redundant %s: %s", key, raw)
			}
		}
	}
}

func (r *countingRenderCredentials) ListByUser(ctx context.Context, id int64) ([]*domain.PSPClient, error) {
	r.users++
	return r.PSPClientRepo.ListByUser(ctx, id)
}

func (r *countingRenderCredentials) ListInbounds(ctx context.Context, id int64) ([]domain.PSPClientInbound, error) {
	r.attachments++
	return r.PSPClientRepo.ListInbounds(ctx, id)
}

func TestRenderSharesOneAppliedCredentialSnapshotAndPreviewReadsNone(t *testing.T) {
	for _, client := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox, domain.ClientURIList} {
		t.Run(string(client), func(t *testing.T) {
			s, _, user, rule := referenceRenderFixture(t)
			user.GroupID = 1
			repo := &countingRenderCredentials{PSPClientRepo: s.repos.PSPClient}
			s.repos.PSPClient = repo
			if _, err := s.RenderForUser(context.Background(), user, client); err != nil {
				t.Fatal(err)
			}
			if repo.users != 1 || repo.attachments != 1 {
				t.Fatalf("credential reads: users=%d attachments=%d", repo.users, repo.attachments)
			}
			if _, err := s.PreviewMihomoGroup(context.Background(), user.GroupID, rule); err != nil {
				t.Fatal(err)
			}
			if repo.users != 1 || repo.attachments != 1 {
				t.Fatal("preview loaded personal credentials")
			}
		})
	}
}
