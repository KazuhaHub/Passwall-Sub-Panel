package sqlstore

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func appliedBatchFixture(t *testing.T) (*nodeAgentRepo, *domain.NodeAgent, []ports.NodeAgentStreamApplied) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	r := NewRepos(db).NodeAgent.(*nodeAgentRepo)
	a := &domain.NodeAgent{AgentID: "agt_applied_batch", PanelID: 7, CredentialSHA256: strings.Repeat("ab", 32)}
	if err := r.Create(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	var receipts []ports.NodeAgentStreamApplied
	for _, name := range []domain.NodeAgentStreamName{domain.NodeAgentStreamConfig, domain.NodeAgentStreamRoster, domain.NodeAgentStreamDirectives} {
		stream, _, err := r.MintStream(t.Context(), a.AgentID, name, []byte(`{"fixture":"A"}`), time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		receipts = append(receipts, ports.NodeAgentStreamApplied{Stream: name, Epoch: a.Epoch, Version: stream.DesiredVersion, ETag: stream.DesiredETag})
	}
	return r, a, receipts
}

func TestNodeAgentAppliedBatchUsesOneOwnerReadAndPreservesHistoricalETag(t *testing.T) {
	r, a, receipts := appliedBatchFixture(t)
	stamp := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	for _, body := range [][]byte{[]byte(`{"fixture":"B"}`), []byte(`{"fixture":"A"}`)} {
		if _, _, err := r.MintStream(t.Context(), a.AgentID, domain.NodeAgentStreamRoster, body, stamp); err != nil {
			t.Fatal(err)
		}
	}
	reads, ownerWrites, streamWrites := 0, 0, 0
	queryHook, updateHook := "applied-batch-query-shape", "applied-batch-write-shape"
	if err := r.db.Callback().Query().After("gorm:query").Register(queryHook, func(tx *gorm.DB) {
		if tx.Statement.Table == "node_agents" || tx.Statement.Table == "node_agent_streams" {
			reads++
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.db.Callback().Update().After("gorm:update").Register(updateHook, func(tx *gorm.DB) {
		if tx.Statement.Table == "node_agents" {
			ownerWrites++
		}
		if tx.Statement.Table == "node_agent_streams" {
			streamWrites++
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Query().Remove(queryHook); _ = r.db.Callback().Update().Remove(updateHook) })
	documents, err := r.RecordAppliedBatch(t.Context(), a.AgentID, receipts, stamp)
	if err != nil || reads != 2 || ownerWrites != 1 || streamWrites != 3 || len(documents) != 3 {
		t.Fatal("receipt vector retained repeated owner reads or writes")
	}
	if documents[1].DesiredVersion != 3 || documents[1].AppliedVersion != 0 || !bytes.Equal(documents[1].DesiredBody, []byte(`{"fixture":"A"}`)) {
		t.Fatal("batch did not return the pre-ACK issuer documents in input order")
	}
	_ = r.db.Callback().Query().Remove(queryHook)
	_ = r.db.Callback().Update().Remove(updateHook)
	for _, receipt := range receipts {
		stream, err := r.GetStream(t.Context(), a.AgentID, receipt.Stream)
		if err != nil || stream.AppliedVersion != receipt.Version || stream.AppliedEpoch != receipt.Epoch || stream.AppliedETag != receipt.ETag || stream.PendingSince != nil || stream.LastSeen == nil || !stream.LastSeen.Equal(stamp) {
			t.Fatal("batch lost historical ETag convergence or stream freshness")
		}
	}
	agent, err := r.GetByAgentID(t.Context(), a.AgentID)
	if err != nil || agent.LastSeen == nil || !agent.LastSeen.Equal(stamp) {
		t.Fatal("batch did not persist the actual shared heartbeat")
	}
}

func TestNodeAgentAppliedBatchInvalidReceiptAndLateWriteRollbackEverything(t *testing.T) {
	r, a, receipts := appliedBatchFixture(t)
	before, err := r.ListStreams(t.Context(), a.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	for _, change := range []func(*ports.NodeAgentStreamApplied){
		func(v *ports.NodeAgentStreamApplied) { v.Epoch++ },
		func(v *ports.NodeAgentStreamApplied) { v.Version++ },
		func(v *ports.NodeAgentStreamApplied) { v.ETag = "conflicting-fixture" },
	} {
		invalid := append([]ports.NodeAgentStreamApplied(nil), receipts...)
		change(&invalid[2])
		if docs, err := r.RecordAppliedBatch(t.Context(), a.AgentID, invalid, stamp); err == nil || docs != nil {
			t.Fatal("invalid final receipt committed earlier observations")
		}
	}
	wantErr, writes := errors.New("owned late stream write failure"), 0
	hook := "applied-batch-late-failure"
	if err := r.db.Callback().Update().Before("gorm:update").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table == "node_agent_streams" {
			writes++
			if writes == 2 {
				tx.AddError(wantErr)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	docs, err := r.RecordAppliedBatch(t.Context(), a.AgentID, receipts, stamp)
	_ = r.db.Callback().Update().Remove(hook)
	if !errors.Is(err, wantErr) || docs != nil {
		t.Fatal("failed transaction published partial documents")
	}
	after, err := r.ListStreams(t.Context(), a.AgentID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("invalid or late failed batch changed any stream")
	}
	agent, err := r.GetByAgentID(t.Context(), a.AgentID)
	if err != nil || agent.LastSeen != nil {
		t.Fatal("rolled-back stream vector left a fresh heartbeat")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if docs, err := r.RecordAppliedBatch(ctx, a.AgentID, receipts, stamp); !errors.Is(err, context.Canceled) || docs != nil {
		t.Fatal("cancelled batch returned observations")
	}
	if _, err := r.RecordAppliedBatch(t.Context(), a.AgentID, append(receipts, receipts[0]), stamp); err == nil {
		t.Fatal("duplicate or oversized stream vector was accepted")
	}
}

func TestNodeAgentAppliedBatchMatchesScalarPendingAndOldEpochSemantics(t *testing.T) {
	r, a, receipts := appliedBatchFixture(t)
	other := &domain.NodeAgent{AgentID: "agt_applied_scalar", PanelID: 8, CredentialSHA256: strings.Repeat("cd", 32)}
	if err := r.Create(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	for _, receipt := range receipts {
		if _, _, err := r.MintStream(t.Context(), other.AgentID, receipt.Stream, []byte(`{"fixture":"A"}`), stamp); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.db.Model(&nodeAgentStreamRow{}).Where("agent_id IN ?", []string{a.AgentID, other.AgentID}).Update("pending_since", stamp).Error; err != nil {
		t.Fatal(err)
	}
	receipts[1].Epoch, receipts[1].Version, receipts[1].ETag = 0, 77, "historical-fixture"
	receipts[2].Epoch, receipts[2].Version, receipts[2].ETag = a.Epoch, 0, "older-content-fixture"
	for _, receipt := range receipts {
		if err := r.RecordApplied(t.Context(), other.AgentID, receipt.Stream, receipt.Epoch, receipt.Version, receipt.ETag, stamp.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.RecordAppliedBatch(t.Context(), a.AgentID, receipts, stamp.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	batchRows, err := r.ListStreams(t.Context(), a.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	scalarRows, err := r.ListStreams(t.Context(), other.AgentID)
	if err != nil || len(batchRows) != len(scalarRows) {
		t.Fatal("batch and scalar stream scopes differ")
	}
	for i := range batchRows {
		batchRows[i].AgentID, scalarRows[i].AgentID = "", ""
		batchRows[i].CreatedAt, scalarRows[i].CreatedAt = time.Time{}, time.Time{}
		batchRows[i].UpdatedAt, scalarRows[i].UpdatedAt = time.Time{}, time.Time{}
		if !reflect.DeepEqual(batchRows[i], scalarRows[i]) {
			t.Fatal("batch changed pending clocks, historical epochs, freshness or desired documents")
		}
	}
}
