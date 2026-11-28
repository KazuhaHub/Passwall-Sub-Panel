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
// 65535: past that size every sync of the server failed outright.
//
// Pinned by counting the parameters the attachment read binds, not by building
// a panel above the limit: tens of thousands of rows took minutes under the
// race detector, and proved nothing on the two dialects whose limit is higher.
func TestNativeDesiredLoadIsNotBoundedByParameterLimit(t *testing.T) {
	const panelID, otherPanelID, nodeID, clients = int64(3), int64(4), int64(900), 600
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	attachmentReadParams := -1
	if err := db.Callback().Query().After("gorm:query").Register("test:count_attachment_params", func(tx *gorm.DB) {
		if tx.Statement.Table == "psp_client_inbounds" {
			attachmentReadParams = len(tx.Statement.Vars)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove("test:count_attachment_params") })
	// No statement log: the slow-query logger would print each multi-row insert whole.
	seed := db.Session(&gorm.Session{Logger: logger.Discard})
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

	snapshot, err := NewRepos(db).NativeDesired.Load(t.Context(), panelID)
	if err != nil {
		t.Fatalf("load a %d-client panel: %v", clients, err)
	}
	if attachmentReadParams != 1 {
		t.Fatalf("the attachment read bound %d parameters for %d clients, want 1 (the panel id)", attachmentReadParams, clients)
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
