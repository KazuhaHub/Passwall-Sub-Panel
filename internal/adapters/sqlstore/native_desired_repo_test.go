package sqlstore

import (
	"strconv"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// A panel's roster is not bounded by a statement's parameter limit. The
// attachment read used to list every client ID of the panel as its own bound
// parameter, which SQLite refuses above 32766 and PostgreSQL/MySQL above
// 65535: past that size every sync of the server failed outright. 33000 is
// above the smallest of those limits.
func TestNativeDesiredLoadIsNotBoundedByParameterLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("inserts 33000 clients")
	}
	const panelID, otherPanelID, nodeID, clients = int64(3), int64(4), int64(900), 33000
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	// One transaction, and no statement log: the fixture is some 130 multi-row
	// inserts, each of which the slow-query logger would otherwise print whole.
	seed := db.Session(&gorm.Session{Logger: logger.Discard}).Begin()
	rows := make([]pspClientRow, 0, clients+1)
	for i := 1; i <= clients; i++ {
		rows = append(rows, pspClientRow{ID: int64(i), UserID: int64(i), PanelID: panelID, Email: "c" + strconv.Itoa(i)})
	}
	// A client of another panel, attached to a node of its own: neither may
	// appear in this panel's snapshot.
	rows = append(rows, pspClientRow{ID: clients + 1, UserID: 1, PanelID: otherPanelID, Email: "other"})
	if err := seed.CreateInBatches(rows, 500).Error; err != nil {
		t.Fatal(err)
	}
	attachments := make([]pspClientInboundRow, 0, clients+1)
	for i := 1; i <= clients; i++ {
		attachments = append(attachments, pspClientInboundRow{ClientID: int64(i), NodeID: nodeID, State: string(domain.ClientApplyApplied), AppliedVersion: 7})
	}
	attachments = append(attachments, pspClientInboundRow{ClientID: clients + 1, NodeID: nodeID + 1, State: string(domain.ClientApplyPending)})
	if err := seed.CreateInBatches(attachments, 500).Error; err != nil {
		t.Fatal(err)
	}
	if err := seed.Commit().Error; err != nil {
		t.Fatal(err)
	}

	snapshot, err := NewRepos(db).NativeDesired.Load(t.Context(), panelID)
	if err != nil {
		t.Fatalf("load a %d-client panel: %v", clients, err)
	}
	if len(snapshot.Clients) != clients {
		t.Fatalf("clients = %d, want %d", len(snapshot.Clients), clients)
	}
	for i, desired := range snapshot.Clients {
		if desired.Client == nil || desired.Client.ID != int64(i+1) {
			t.Fatalf("client %d out of id order: %+v", i, desired.Client)
		}
		if len(desired.Inbounds) != 1 || desired.Inbounds[0].ClientID != desired.Client.ID ||
			desired.Inbounds[0].NodeID != nodeID || desired.Inbounds[0].AppliedVersion != 7 ||
			desired.Inbounds[0].State != domain.ClientApplyApplied {
			t.Fatalf("client %d attachments = %+v", desired.Client.ID, desired.Inbounds)
		}
	}
}
