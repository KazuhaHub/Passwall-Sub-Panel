package sqlstore

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type legacyNodeEndpointRow struct {
	ID          int64 `gorm:"primaryKey;autoIncrement"`
	PanelID     int64
	InboundID   int
	DisplayName string
	Region      string
	Port        int
	Protocol    string
}

func (legacyNodeEndpointRow) TableName() string { return "nodes" }

func TestNodeEndpointMigrationSplitsLegacyValueWithoutChangingBehavior(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&legacyNodeEndpointRow{}); err != nil {
		t.Fatal(err)
	}
	legacy := legacyNodeEndpointRow{
		PanelID: 3, InboundID: 9, DisplayName: "legacy", Region: "JP",
		Port: 8443, Protocol: "vless",
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}

	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repo := &nodeRepo{db: db}
	got, err := repo.GetByID(t.Context(), legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DesiredPort != 8443 || got.ObservedPort != 8443 ||
		got.DesiredProtocol != "vless" || got.ObservedProtocol != "vless" || !got.EndpointInSync() {
		t.Fatalf("legacy endpoint was not migrated as converged desired/observed state: %+v", got)
	}
	if db.Migrator().HasColumn(&nodeRow{}, "port") || db.Migrator().HasColumn(&nodeRow{}, "protocol") {
		t.Fatal("ambiguous legacy endpoint columns survived migration")
	}

	// Both the one-shot marker and cleanup are idempotent.
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("second EnsureSchema: %v", err)
	}
}

func TestUpdateObservedEndpointCannotClobberDesired(t *testing.T) {
	repo, ctx := newNodeTestRepo(t)
	n := &domain.Node{
		PanelID: 1, InboundID: 2, DisplayName: "n", Region: "US",
		DesiredPort: 443, DesiredProtocol: "vless",
	}
	if err := repo.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateObservedEndpoint(ctx, n.ID, domain.NodeObservedEndpoint{Port: 8443, Protocol: "trojan"}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DesiredPort != 443 || got.DesiredProtocol != "vless" {
		t.Fatalf("observed writer clobbered desired endpoint: %+v", got)
	}
	if got.ObservedPort != 8443 || got.ObservedProtocol != "trojan" || got.EndpointInSync() {
		t.Fatalf("observed endpoint not persisted as drift: %+v", got)
	}
}
