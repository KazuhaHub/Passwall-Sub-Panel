package sqlstore

import (
	"context"
	"testing"
)

// legacyPSPClientIdentityRow is the exact pre-A1 identity shape: email was the
// second half of the unique key. Keep it local to this migration test so the
// production model can never accidentally recreate that index.
type legacyPSPClientIdentityRow struct {
	ID                       int64  `gorm:"primaryKey;autoIncrement"`
	UserID                   int64  `gorm:"index;not null"`
	PanelID                  int64  `gorm:"not null;uniqueIndex:uk_psp_client,priority:1"`
	Email                    string `gorm:"size:255;not null;uniqueIndex:uk_psp_client,priority:2"`
	CredClass                int    `gorm:"not null;default:0"`
	UUID                     string `gorm:"size:36;not null;default:''"`
	Password                 string `gorm:"size:128;not null;default:''"`
	LifetimeUpBytes          int64  `gorm:"default:0"`
	LifetimeDownBytes        int64  `gorm:"default:0"`
	LifetimeTotalBytes       int64  `gorm:"default:0"`
	LastRawUpBytes           int64  `gorm:"default:0"`
	LastRawDownBytes         int64  `gorm:"default:0"`
	LastRawTotalBytes        int64  `gorm:"default:0"`
	PeriodBaselineUpBytes    int64  `gorm:"default:0"`
	PeriodBaselineDownBytes  int64  `gorm:"default:0"`
	PeriodBaselineTotalBytes int64  `gorm:"default:0"`
}

func (legacyPSPClientIdentityRow) TableName() string { return "psp_clients" }

func TestEnsureSchemaMigratesPSPClientIdentityWithoutLosingBaselines(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	seedV3Baseline(t, db)
	legacy := legacyPSPClientIdentityRow{
		UserID: 7, PanelID: 10, Email: "u7@old.example", CredClass: 1,
		UUID: "uuid-7", Password: "pw-7",
		LifetimeUpBytes: 100, LifetimeDownBytes: 200, LifetimeTotalBytes: 300,
		LastRawUpBytes: 40, LastRawDownBytes: 50, LastRawTotalBytes: 90,
		PeriodBaselineUpBytes: 10, PeriodBaselineDownBytes: 20, PeriodBaselineTotalBytes: 30,
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	if !db.Migrator().HasIndex(&legacyPSPClientIdentityRow{}, "uk_psp_client") {
		t.Fatal("precondition: legacy email identity index was not created")
	}

	if err := EnsureSchema(db); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}
	if db.Migrator().HasIndex(&pspClientRow{}, "uk_psp_client") {
		t.Fatal("legacy unique (panel_id,email) identity index survived migration")
	}
	if !db.Migrator().HasIndex(&pspClientRow{}, "idx_psp_client_panel_email") {
		t.Fatal("replacement non-unique panel/email lookup index is missing")
	}

	repo := NewRepos(db).PSPClient
	got, err := repo.GetByID(context.Background(), legacy.ID)
	if err != nil {
		t.Fatalf("load migrated stable row: %v", err)
	}
	if got.ID != legacy.ID || got.LastRawUpBytes != 40 || got.LastRawDownBytes != 50 || got.LastRawTotalBytes != 90 ||
		got.PeriodBaselineUpBytes != 10 || got.PeriodBaselineDownBytes != 20 || got.PeriodBaselineTotalBytes != 30 {
		t.Fatalf("migration changed stable identity or baselines: %+v", got)
	}

	// The migrated row is now updated by stable ID even when email changes.
	got.Email = "u7@new.example"
	if err := repo.UpdateDefinition(context.Background(), got); err != nil {
		t.Fatalf("update migrated row by ID: %v", err)
	}
	after, err := repo.GetByID(context.Background(), legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Email != "u7@new.example" || after.LastRawTotalBytes != 90 || after.PeriodBaselineTotalBytes != 30 {
		t.Fatalf("ID-keyed merge lost email update or baselines: %+v", after)
	}

	// Email is no longer a unique local identity. The live planner maintains
	// upstream uniqueness; the database must permit a transient old/new row
	// overlap while an ID-keyed reconcile changes the partition layout.
	duplicate := pspClientRow{UserID: 8, PanelID: 10, Email: "u7@new.example", UUID: "uuid-8"}
	if err := db.Create(&duplicate).Error; err != nil {
		t.Fatalf("email is still acting as a unique database identity: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("second EnsureSchema recreated the legacy identity index: %v", err)
	}
}
