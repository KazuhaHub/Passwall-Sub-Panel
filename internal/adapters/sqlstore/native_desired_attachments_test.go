package sqlstore

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestNativeDesiredAttachmentCursorMatchesCompleteModelAndOwnsFailureClocks(t *testing.T) {
	clients, _ := inboundBatchFixture(t, 0)
	for _, id := range []int64{5, 1, 3, 7} {
		if err := clients.db.Create(&pspClientRow{ID: id, UserID: id, PanelID: 7, Email: fmt.Sprintf("attachment-%d@fixture.local", id)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	stamp := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	later := stamp.Add(time.Minute)
	attachments := []pspClientInboundRow{
		{ClientID: 3, NodeID: 9, FlowOverride: "flow-b", State: "pending", AppliedVersion: 1 << 55, AppliedEmail: "second@fixture.local", AppliedUUID: "uuid-b", AppliedPassword: "password-b", FirstFailedAt: &later},
		{ClientID: 1, NodeID: 3, FlowOverride: "flow-a", State: "applied", AppliedVersion: 29, AppliedEmail: "first@fixture.local", AppliedUUID: "uuid-a", AppliedPassword: "password-a"},
		{ClientID: 3, NodeID: 7, State: "rejected", AppliedVersion: 31, FirstFailedAt: &stamp},
		{ClientID: 5, NodeID: 8, State: "blocked"},
		{ClientID: 999, NodeID: 8, State: "applied", AppliedEmail: "unrelated@fixture.local"},
	}
	if err := clients.db.Create(&attachments).Error; err != nil {
		t.Fatal(err)
	}
	r := &nativeDesiredRepo{db: clients.db}
	full, err := r.Load(t.Context(), 7)
	if err != nil || len(full.Clients) != 4 {
		t.Fatalf("attachment fixture lost current clients: %v", err)
	}
	for _, entry := range full.Clients {
		var original []pspClientInboundRow
		if err := clients.db.Where("client_id = ?", entry.Client.ID).Order("node_id").Find(&original).Error; err != nil {
			t.Fatal(err)
		}
		var want []domain.PSPClientInbound
		for _, row := range original {
			want = append(want, domain.PSPClientInbound{ClientID: row.ClientID, NodeID: row.NodeID, FlowOverride: row.FlowOverride, State: domain.ClientApplyState(row.State), AppliedVersion: row.AppliedVersion, AppliedEmail: row.AppliedEmail, AppliedUUID: row.AppliedUUID, AppliedPassword: row.AppliedPassword, FirstFailedAt: row.FirstFailedAt})
		}
		if !reflect.DeepEqual(entry.Inbounds, want) {
			t.Fatal("typed attachments changed complete fields, order, zero version or absent-client shape")
		}
		scoped, err := r.LoadClient(t.Context(), 7, entry.Client.Email)
		if err != nil || len(scoped.Clients) != 1 || !reflect.DeepEqual(scoped.Clients[0].Inbounds, want) {
			t.Fatalf("scoped attachment read changed the complete identity closure: %v", err)
		}
	}
	first, second := full.Clients[1].Inbounds[0].FirstFailedAt, full.Clients[1].Inbounds[1].FirstFailedAt
	if first == nil || second == nil || first == second || !first.Equal(stamp) || !second.Equal(later) {
		t.Fatal("distinct scanned failure clocks share storage")
	}
	*first = first.Add(time.Hour)
	if !second.Equal(later) {
		t.Fatal("changing one result clock changed the following attachment")
	}
	if err := clients.db.Model(&pspClientInboundRow{}).Where("client_id = ? AND node_id = ?", 3, 7).Updates(map[string]any{"state": "applied", "applied_email": "current@fixture.local", "first_failed_at": nil}).Error; err != nil {
		t.Fatal(err)
	}
	current, err := r.LoadClient(t.Context(), 7, full.Clients[1].Client.Email)
	if err != nil || len(current.Clients) != 1 || current.Clients[0].Inbounds[0].State != domain.ClientApplyApplied || current.Clients[0].Inbounds[0].AppliedEmail != "current@fixture.local" || current.Clients[0].Inbounds[0].FirstFailedAt != nil {
		t.Fatalf("later attachment read reused an earlier receipt or clock: %v", err)
	}
}

func TestNativeDesiredAttachmentCursorRejectsPartialScanAndReleasesTransaction(t *testing.T) {
	clients, _ := inboundBatchFixture(t, 0)
	if err := clients.db.Create(&pspClientRow{ID: 1, UserID: 1, PanelID: 7, Email: "attachment-fault@fixture.local"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := clients.db.Create(&[]pspClientInboundRow{{ClientID: 1, NodeID: 7}, {ClientID: 1, NodeID: 8}}).Error; err != nil {
		t.Fatal(err)
	}
	r := &nativeDesiredRepo{db: clients.db}
	pool, err := clients.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	before := pool.Stats().InUse
	const faultSQL = `SELECT client_id, CASE WHEN node_id = 8 THEN 'invalid-integer' ELSE '7' END AS node_id,
		flow_override, state, applied_version, applied_email, applied_uuid, applied_password, first_failed_at
		FROM psp_client_inbounds ORDER BY client_id, node_id`
	const hook = "native-desired-attachment-scan-fault"
	if err := r.db.Callback().Row().Before("gorm:row").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table == "psp_client_inbounds" {
			tx.Statement.SQL.Reset()
			tx.Statement.SQL.WriteString(faultSQL)
			tx.Statement.Vars = nil
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Row().Remove(hook) })
	if snapshot, err := r.Load(t.Context(), 7); snapshot != nil || err == nil || !strings.Contains(err.Error(), "invalid-integer") {
		t.Fatal("failed attachment scan published a partially attached desired snapshot")
	}
	_ = r.db.Callback().Row().Remove(hook)
	if pool.Stats().InUse != before {
		t.Fatal("failed attachment scan retained the transaction or cursor")
	}
	if snapshot, err := r.Load(t.Context(), 7); err != nil || len(snapshot.Clients) != 1 || len(snapshot.Clients[0].Inbounds) != 2 {
		t.Fatalf("failed scan poisoned the next complete attachment closure: %v", err)
	}
	failure := errors.New("owned attachment cursor read failure")
	if err := r.db.Callback().Row().Before("gorm:row").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table == "psp_client_inbounds" {
			tx.AddError(failure)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := r.LoadClient(t.Context(), 7, "attachment-fault@fixture.local"); snapshot != nil || !errors.Is(err, failure) {
		t.Fatal("attachment read failure became an empty healthy attachment set")
	}
}
