package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func inboundBatchFixture(t *testing.T, count int) (*pspClientRepo, []domain.PSPClientInbound) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if connection, err := db.DB(); err == nil {
			_ = connection.Close()
		}
	})
	var rows []pspClientInboundRow
	var updates []domain.PSPClientInbound
	for i := 1; i <= count; i++ {
		rows = append(rows, pspClientInboundRow{ClientID: int64(i), NodeID: 7, FlowOverride: "preserved-flow", State: string(domain.ClientApplyApplied), AppliedVersion: 4, AppliedUUID: "confirmed-fixture"})
		states := []domain.ClientApplyState{domain.ClientApplyApplied, domain.ClientApplyPending, domain.ClientApplyRejected, domain.ClientApplyBlocked}
		updates = append(updates, domain.PSPClientInbound{ClientID: int64(i), NodeID: 7, State: states[(i-1)%4], AppliedVersion: 5, AppliedUUID: fmt.Sprintf("next-fixture-%d", i)})
	}
	if err := db.CreateInBatches(rows, 100).Error; err != nil {
		t.Fatal(err)
	}
	return &pspClientRepo{db: db}, updates
}

func TestPSPClientInboundBatchBoundsReadsAndReplaysEveryStateWithoutWrites(t *testing.T) {
	r, updates := inboundBatchFixture(t, 601)
	queries, writes, maxArgs, affected := 0, 0, 0, int64(0)
	queryHook, updateHook := "batch-query-cardinality", "batch-write-cardinality"
	if err := r.db.Callback().Query().After("gorm:query").Register(queryHook, func(tx *gorm.DB) {
		if tx.Statement.Table == "psp_client_inbounds" {
			queries++
			maxArgs = max(maxArgs, len(tx.Statement.Vars))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.db.Callback().Update().After("gorm:update").Register(updateHook, func(tx *gorm.DB) {
		if tx.Statement.Table == "psp_client_inbounds" {
			writes++
			affected += tx.RowsAffected
			maxArgs = max(maxArgs, len(tx.Statement.Vars))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Query().Remove(queryHook); _ = r.db.Callback().Update().Remove(updateHook) })
	if err := r.UpdateInboundStates(t.Context(), updates); err != nil {
		t.Fatal(err)
	}
	if queries != 3 || writes != 19 || affected != 601 || maxArgs > 500 {
		t.Fatalf("queries=%d writes=%d affected=%d args=%d", queries, writes, affected, maxArgs)
	}
	var before []pspClientInboundRow
	if err := r.db.Order("id").Find(&before).Error; err != nil {
		t.Fatal(err)
	}
	queries, writes, maxArgs, affected = 0, 0, 0, 0
	if err := r.UpdateInboundStates(t.Context(), updates); err != nil {
		t.Fatal(err)
	}
	if queries != 3 || writes != 0 || affected != 0 || maxArgs > 500 {
		t.Fatalf("replay queries=%d writes=%d args=%d", queries, writes, maxArgs)
	}
	var after []pspClientInboundRow
	if err := r.db.Order("id").Find(&after).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("replay changed state, credentials or original failure clocks")
	}
	for i, row := range after {
		if row.FlowOverride != "preserved-flow" || row.State != string(updates[i].State) {
			t.Fatal("batch changed attachment identity/flow or lost reported state")
		}
		failed := updates[i].State == domain.ClientApplyPending || updates[i].State == domain.ClientApplyRejected
		if (row.FirstFailedAt != nil) != failed {
			t.Fatal("batch changed failure clock semantics")
		}
		if updates[i].State == domain.ClientApplyApplied {
			if row.AppliedVersion != 5 || row.AppliedUUID != updates[i].AppliedUUID {
				t.Fatal("batch lost exact confirmed credentials")
			}
		} else if row.AppliedVersion != 4 || row.AppliedUUID != "confirmed-fixture" {
			t.Fatal("unapplied observation replaced confirmed credentials")
		}
	}
}

func TestPSPClientInboundBatchRollsBackLateWritesAndRejectsInvalidInput(t *testing.T) {
	r, updates := inboundBatchFixture(t, 65)
	wantErr := errors.New("owned late attachment write failure")
	writes := 0
	hook := "batch-late-write-error"
	if err := r.db.Callback().Update().Before("gorm:update").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table == "psp_client_inbounds" {
			writes++
			if writes == 2 {
				tx.AddError(wantErr)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateInboundStates(t.Context(), updates); !errors.Is(err, wantErr) {
		t.Fatal("batch lost its late write failure")
	}
	_ = r.db.Callback().Update().Remove(hook)
	var rows []pspClientInboundRow
	if err := r.db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.State != string(domain.ClientApplyApplied) || row.AppliedVersion != 4 || row.FirstFailedAt != nil || row.AppliedUUID != "confirmed-fixture" {
			t.Fatal("late failure left partial attachment state")
		}
	}
	for _, invalid := range []domain.PSPClientInbound{{ClientID: 0, NodeID: 7, State: domain.ClientApplyApplied}, {ClientID: 1, State: domain.ClientApplyApplied}, {ClientID: 1, NodeID: 7, State: "unknown"}} {
		if err := r.UpdateInboundStates(t.Context(), append([]domain.PSPClientInbound{updates[0]}, invalid)); err == nil {
			t.Fatal("invalid batch was accepted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := r.UpdateInboundStates(ctx, updates); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled batch lost cancellation")
	}
	if err := r.UpdateInboundStates(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateInboundStates(t.Context(), []domain.PSPClientInbound{{ClientID: 9999, NodeID: 7, State: domain.ClientApplyApplied}}); err != nil {
		t.Fatal("missing attachment was recreated or rejected")
	}
	var after []pspClientInboundRow
	if err := r.db.Find(&after).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows, after) {
		t.Fatal("invalid/cancelled/missing batch changed stored attachments")
	}
}

func TestPSPClientInboundBatchKeepsSingleWriterSemanticsAndDuplicateOrder(t *testing.T) {
	r, _ := inboundBatchFixture(t, 2)
	stamp := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	sequence := []domain.PSPClientInbound{
		{State: domain.ClientApplyPending, FirstFailedAt: &stamp},
		{State: domain.ClientApplyRejected},
		{State: domain.ClientApplyApplied, AppliedVersion: 9},
		{State: domain.ClientApplyBlocked},
		{State: domain.ClientApplyApplied, AppliedVersion: 10, AppliedEmail: "next-fixture", AppliedUUID: "next-uuid", AppliedPassword: "next-password"},
		{State: domain.ClientApplyPending, FirstFailedAt: &stamp, AppliedVersion: 999, AppliedUUID: "unconfirmed"},
	}
	var batch []domain.PSPClientInbound
	for _, value := range sequence {
		value.ClientID, value.NodeID = 1, 7
		if err := r.UpdateInboundState(t.Context(), value); err != nil {
			t.Fatal(err)
		}
		value.ClientID = 2
		batch = append(batch, value)
	}
	if err := r.UpdateInboundStates(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	var rows []pspClientInboundRow
	if err := r.db.Order("client_id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	rows[0].ID, rows[1].ID, rows[0].ClientID, rows[1].ClientID = 0, 0, 0, 0
	if !reflect.DeepEqual(rows[0], rows[1]) {
		t.Fatal("batch changed single-row state/credential/failure-clock behavior or duplicate order")
	}
}

func TestPSPClientInboundBatchBindsExactPairsAndKeepsUnrequestedAttachments(t *testing.T) {
	r, updates := inboundBatchFixture(t, 2)
	unrequested := pspClientInboundRow{ClientID: 1, NodeID: 8, State: string(domain.ClientApplyApplied), AppliedVersion: 19, AppliedUUID: "unrequested-fixture"}
	if err := r.db.Create(&unrequested).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateInboundStates(t.Context(), []domain.PSPClientInbound{updates[0], {ClientID: 2, NodeID: 8, State: domain.ClientApplyBlocked}}); err != nil {
		t.Fatal(err)
	}
	var rows []pspClientInboundRow
	if err := r.db.Order("client_id, node_id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatal("batch created a missing attachment")
	}
	for _, row := range rows {
		if row.ClientID == 1 && row.NodeID == 7 {
			if row.AppliedVersion != 5 || row.AppliedUUID != updates[0].AppliedUUID {
				t.Fatal("requested pair lost its observed transition")
			}
		} else if row.NodeID == 8 {
			if row.AppliedVersion != 19 || row.AppliedUUID != "unrequested-fixture" {
				t.Fatal("batch changed an unrequested node attachment")
			}
		} else if row.ClientID == 2 && (row.State != string(domain.ClientApplyApplied) || row.AppliedVersion != 4) {
			t.Fatal("missing pair changed a different attachment for the same client")
		}
	}
}

func TestPSPClientInboundBatchCoalescesReplayAndReseedsClearedFailureClock(t *testing.T) {
	r, _ := inboundBatchFixture(t, 2)
	stamp := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := r.db.Model(&pspClientInboundRow{}).Where("client_id = ?", 2).Updates(map[string]any{"state": string(domain.ClientApplyPending), "first_failed_at": stamp}).Error; err != nil {
		t.Fatal(err)
	}
	writes := 0
	hook := "batch-coalesced-write-cardinality"
	if err := r.db.Callback().Update().Before("gorm:update").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table == "psp_client_inbounds" {
			writes++
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Update().Remove(hook) })
	if err := r.UpdateInboundStates(t.Context(), []domain.PSPClientInbound{
		{ClientID: 1, NodeID: 7, State: domain.ClientApplyPending},
		{ClientID: 1, NodeID: 7, State: domain.ClientApplyApplied, AppliedVersion: 4, AppliedUUID: "confirmed-fixture"},
		{ClientID: 2, NodeID: 7, State: domain.ClientApplyPending},
	}); err != nil {
		t.Fatal(err)
	}
	if writes != 0 {
		t.Fatal("final unchanged observations performed database writes")
	}
	if err := r.UpdateInboundStates(t.Context(), []domain.PSPClientInbound{
		{ClientID: 2, NodeID: 7, State: domain.ClientApplyApplied, AppliedVersion: 4},
		{ClientID: 2, NodeID: 7, State: domain.ClientApplyPending},
		{ClientID: 2, NodeID: 7, State: domain.ClientApplyRejected},
	}); err != nil {
		t.Fatal(err)
	}
	var row pspClientInboundRow
	if err := r.db.Where("client_id = ?", 2).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if writes != 1 || row.State != string(domain.ClientApplyRejected) || row.FirstFailedAt == nil || !row.FirstFailedAt.After(stamp) || row.AppliedUUID != "confirmed-fixture" || row.AppliedVersion != 4 {
		t.Fatal("coalescing lost input-order clock reset or preserved confirmation")
	}
}

func TestPSPClientInboundBatchStateOnlyWritesDoNotBindConfirmedCredentials(t *testing.T) {
	r, _ := inboundBatchFixture(t, 2)
	credentialColumns := false
	hook := "batch-state-only-columns"
	if err := r.db.Callback().Update().After("gorm:update").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table == "psp_client_inbounds" {
			for _, column := range []string{"applied_email", "applied_uuid", "applied_password", "applied_version"} {
				credentialColumns = credentialColumns || strings.Contains(tx.Statement.SQL.String(), column)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Update().Remove(hook) })
	if err := r.UpdateInboundStates(t.Context(), []domain.PSPClientInbound{
		{ClientID: 1, NodeID: 7, State: domain.ClientApplyPending, AppliedUUID: "unconfirmed-fixture", AppliedPassword: "unconfirmed-password"},
		{ClientID: 2, NodeID: 7, State: domain.ClientApplyBlocked},
	}); err != nil {
		t.Fatal(err)
	}
	var rows []pspClientInboundRow
	if err := r.db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if credentialColumns || len(rows) != 2 {
		t.Fatal("state-only batch bound confirmed identity columns or changed membership")
	}
	for _, row := range rows {
		if row.AppliedVersion != 4 || row.AppliedUUID != "confirmed-fixture" {
			t.Fatal("state-only batch replaced confirmed identity")
		}
	}
}
