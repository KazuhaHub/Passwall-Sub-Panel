package clientdoc_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/clientdoc"
)

func TestSameDesiredInputDoesNotAdvanceStreamVersion(t *testing.T) {
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "psp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, closeErr := db.DB(); closeErr == nil {
			_ = sqlDB.Close()
		}
	})
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo := sqlstore.NewRepos(db).NodeAgent
	agent := &domain.NodeAgent{
		AgentID: "agt_clientdoc", PanelID: 10,
		CredentialSHA256: strings.Repeat("ef", 32),
	}
	if err := repo.Create(ctx, agent); err != nil {
		t.Fatal(err)
	}
	client := &domain.PSPClient{ID: 7, UserID: 8, PanelID: 10, UUID: "uuid", Password: "password"}
	client.SetDesiredLifecycle(domain.UserLifecycle{Enable: true, ExpiryTime: 42})
	body, documentETag, err := clientdoc.Mint(client, []domain.PSPClientInbound{{NodeID: 11}}).Canonical()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	first, minted, err := repo.MintStream(ctx, agent.AgentID, domain.NodeAgentStreamRoster, body, now)
	if err != nil || !minted {
		t.Fatalf("first mint = (%+v, %v, %v)", first, minted, err)
	}
	second, minted, err := repo.MintStream(ctx, agent.AgentID, domain.NodeAgentStreamRoster, body, now.Add(time.Hour))
	if err != nil || minted {
		t.Fatalf("same-content mint = (%+v, %v, %v)", second, minted, err)
	}
	if first.DesiredVersion != second.DesiredVersion || first.DesiredETag != second.DesiredETag ||
		first.DesiredETag != documentETag {
		t.Fatalf("same input changed version/etag: first=%+v second=%+v document_etag=%s", first, second, documentETag)
	}
}
