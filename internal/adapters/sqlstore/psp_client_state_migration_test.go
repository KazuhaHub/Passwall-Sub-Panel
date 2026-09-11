package sqlstore

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type legacyPSPClientInboundRow struct {
	ID           int64  `gorm:"primaryKey;autoIncrement"`
	ClientID     int64  `gorm:"not null;index;uniqueIndex:uk_psp_client_inbound,priority:1"`
	NodeID       int64  `gorm:"not null;uniqueIndex:uk_psp_client_inbound,priority:2"`
	FlowOverride string `gorm:"size:64;not null;default:''"`
	Provisioned  *bool
}

func (legacyPSPClientInboundRow) TableName() string { return "psp_client_inbounds" }

func TestEnsureSchemaMigratesProvisionedBoolToFourState(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&pspClientRow{}, &legacyPSPClientInboundRow{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&pspClientRow{
		ID: 1, UserID: 2, PanelID: 3, Email: "u2@psp.local",
		UUID: "00000000-0000-0000-0000-000000000002", Password: "applied-password",
	}).Error; err != nil {
		t.Fatal(err)
	}
	yes, no := true, false
	for _, row := range []legacyPSPClientInboundRow{
		{ClientID: 1, NodeID: 10, Provisioned: &yes},
		{ClientID: 1, NodeID: 11, Provisioned: &no},
		{ClientID: 1, NodeID: 12, Provisioned: nil},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasColumn(&pspClientInboundRow{}, "provisioned") {
		t.Fatal("legacy provisioned column survived four-state migration")
	}
	got, err := NewRepos(db).PSPClient.ListInbounds(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].State != domain.ClientApplyApplied || got[0].FirstFailedAt != nil {
		t.Fatalf("true did not migrate to applied: %+v", got)
	}
	if got[0].AppliedEmail != "u2@psp.local" || got[0].AppliedUUID != "00000000-0000-0000-0000-000000000002" || got[0].AppliedPassword != "applied-password" {
		t.Fatalf("applied row did not receive credential snapshot: %+v", got[0])
	}
	for _, pending := range got[1:] {
		if pending.State != domain.ClientApplyPending || pending.FirstFailedAt == nil {
			t.Fatalf("false/unknown must migrate to pending with a clock: %+v", pending)
		}
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("second EnsureSchema was not idempotent: %v", err)
	}
}
