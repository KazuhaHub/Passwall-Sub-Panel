package sqlstore

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestXUIPanelRowPreservesAdapterKindAndDefaultsLegacyRows(t *testing.T) {
	row, err := xuiPanelFromDomain(&domain.Panel{
		Kind: domain.PanelKindSUI, Name: "sui", URL: "https://sui.example.test",
	})
	if err != nil {
		t.Fatalf("from domain: %v", err)
	}
	if row.Kind != string(domain.PanelKindSUI) {
		t.Fatalf("row kind = %q", row.Kind)
	}
	got, err := row.toDomain()
	if err != nil {
		t.Fatalf("to domain: %v", err)
	}
	if got.Kind != domain.PanelKindSUI {
		t.Fatalf("domain kind = %q", got.Kind)
	}

	legacy, err := (&xuiPanelRow{Name: "old", URL: "https://old.example.test"}).toDomain()
	if err != nil {
		t.Fatalf("legacy to domain: %v", err)
	}
	if legacy.Kind != domain.PanelKind3XUI {
		t.Fatalf("legacy kind = %q, want %q", legacy.Kind, domain.PanelKind3XUI)
	}
}

func TestXUIPanelKindSchemaRoundTrip(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrap db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	repo := NewRepos(db).XUIPanel
	want := &domain.Panel{
		Kind: domain.PanelKindSUI, Name: "sui", URL: "https://sui.example.test", APIToken: "token",
	}
	if err := repo.Save(context.Background(), want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := repo.GetByID(context.Background(), want.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Kind != domain.PanelKindSUI || got.Name != want.Name || got.APIToken != want.APIToken {
		t.Fatalf("round-trip = %#v", got)
	}
}
