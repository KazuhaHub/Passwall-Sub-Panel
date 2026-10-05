package clientprov

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type failingRetirementRepo struct {
	*fakePSPClientRepo
	failPanel int64
}

func (r *failingRetirementRepo) UpdateDefinition(ctx context.Context, c *domain.PSPClient) error {
	if c.PanelID == r.failPanel {
		return domain.ErrUnavailable
	}
	return r.fakePSPClientRepo.UpdateDefinition(ctx, c)
}

func TestRetirementsDistinguishPanelRemovalFromReplacementAndPartialFailure(t *testing.T) {
	ctx := t.Context()
	repo := &failingRetirementRepo{fakePSPClientRepo: newFakeRepo()}
	s := New(repo)
	nodes := []*domain.Node{
		{ID: 1, PanelID: 81, DesiredProtocol: "vless"},
		{ID: 2, PanelID: 81, DesiredProtocol: "vless", Flow: "xtls-rprx-vision"},
		{ID: 3, PanelID: 82, DesiredProtocol: "vless"},
	}
	if _, err := s.SyncUserRetirements(ctx, 42, "uuid-x", rules, nodes); err != nil {
		t.Fatal(err)
	}
	// Combining two partitions on the still-desired panel needs a replacement;
	// retiring the other panel needs no replacement or successful remote write.
	nodes[1].Flow = ""
	retired, err := s.SyncUserRetirements(ctx, 42, "uuid-x", rules, nodes[:2])
	if err != nil || len(retired[81].Emails) != 1 || retired[81].PanelRemoved || len(retired[82].Emails) != 1 || !retired[82].PanelRemoved {
		t.Fatalf("retirement classes: %+v / %v", retired, err)
	}
	// A failed write on the remaining panel does not discard the successful
	// removed-panel retirement. Its stable empty row yields it again for retry.
	repo.failPanel = 81
	retired, err = s.SyncUserRetirements(ctx, 42, "uuid-x", rules, nodes[:2])
	if !errors.Is(err, domain.ErrUnavailable) || len(retired) != 1 || len(retired[82].Emails) != 1 || !retired[82].PanelRemoved {
		t.Fatalf("partial retirement lost safe removal: %+v / %v", retired, err)
	}
}
