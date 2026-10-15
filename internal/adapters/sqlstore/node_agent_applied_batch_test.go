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
	if err != nil || reads != 2 || ownerWrites != 1 || streamWrites != 1 || len(documents) != 3 {
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
			if writes == 1 {
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

func TestNodeAgentAppliedVectorKeepsPartialScopeForeignOwnerAndBoundValues(t *testing.T) {
	r, a, receipts := appliedBatchFixture(t)
	stamp := time.Date(2026, time.October, 2, 12, 0, 0, 0, time.UTC)
	// Exercise creation of a missing pending clock independently of the
	// preserved existing-clock case covered by scalar equivalence below.
	if err := r.db.Model(&nodeAgentStreamRow{}).Where("agent_id = ? AND stream = ?", a.AgentID, string(domain.NodeAgentStreamConfig)).Update("pending_since", nil).Error; err != nil {
		t.Fatal(err)
	}
	foreign := &domain.NodeAgent{AgentID: "agt_applied_untouched", PanelID: 19, CredentialSHA256: strings.Repeat("ef", 32)}
	if err := r.Create(t.Context(), foreign); err != nil {
		t.Fatal(err)
	}
	for _, receipt := range receipts {
		if _, _, err := r.MintStream(t.Context(), foreign.AgentID, receipt.Stream, []byte(`{"foreign":true}`), stamp); err != nil {
			t.Fatal(err)
		}
	}
	foreignBefore, err := r.ListStreams(t.Context(), foreign.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	omittedBefore, err := r.GetStream(t.Context(), a.AgentID, domain.NodeAgentStreamRoster)
	if err != nil {
		t.Fatal(err)
	}
	// Input order differs from SQL lock order, and every row has its own
	// receipt/pending result. The quoted old ETag must remain a bound value.
	partial := []ports.NodeAgentStreamApplied{receipts[2], receipts[0]}
	partial[1].Epoch, partial[1].Version, partial[1].ETag = 0, 77, "historical'quoted-fixture"
	writes, parameters := 0, 0
	hook := "applied-vector-bound-shape"
	if err := r.db.Callback().Update().After("gorm:update").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table != "node_agent_streams" {
			return
		}
		writes++
		parameters = len(tx.Statement.Vars)
		if strings.Contains(tx.Statement.SQL.String(), partial[1].ETag) {
			t.Error("receipt ETag interpolated into SQL")
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Update().Remove(hook) })
	docs, err := r.RecordAppliedBatch(t.Context(), a.AgentID, partial, stamp)
	_ = r.db.Callback().Update().Remove(hook)
	if err != nil || len(docs) != 2 || docs[0].Stream != partial[0].Stream || docs[1].Stream != partial[1].Stream || docs[0].AppliedVersion != 0 || docs[1].AppliedVersion != 0 || writes != 1 || parameters == 0 || parameters > 32 {
		t.Fatalf("partial vector lost issuer order/bounded single write: writes=%d parameters=%d / %v", writes, parameters, err)
	}
	config, err := r.GetStream(t.Context(), a.AgentID, domain.NodeAgentStreamConfig)
	if err != nil || config.AppliedEpoch != 0 || config.AppliedVersion != 77 || config.AppliedETag != partial[1].ETag || config.PendingSince == nil || !config.PendingSince.Equal(stamp) || config.LastSeen == nil || !config.LastSeen.Equal(stamp) {
		t.Fatal("per-row historic receipt or new pending clock was lost")
	}
	directives, err := r.GetStream(t.Context(), a.AgentID, domain.NodeAgentStreamDirectives)
	if err != nil || directives.AppliedVersion != partial[0].Version || directives.AppliedETag != partial[0].ETag || directives.PendingSince != nil || directives.LastSeen == nil || !directives.LastSeen.Equal(stamp) {
		t.Fatal("applied row inherited another stream's receipt or pending clock")
	}
	omittedAfter, err := r.GetStream(t.Context(), a.AgentID, domain.NodeAgentStreamRoster)
	if err != nil || !reflect.DeepEqual(omittedBefore, omittedAfter) {
		t.Fatal("partial vector touched an omitted stream")
	}
	foreignAfter, err := r.ListStreams(t.Context(), foreign.AgentID)
	if err != nil || !reflect.DeepEqual(foreignBefore, foreignAfter) {
		t.Fatal("vector write crossed agent ownership")
	}
	foreignAgent, err := r.GetByAgentID(t.Context(), foreign.AgentID)
	if err != nil || foreignAgent.LastSeen != nil {
		t.Fatal("vector write refreshed another owner's heartbeat")
	}
	// A later one-stream vector clears exactly this pending clock and reads
	// its current issuer state, without touching either excluded stream.
	later := stamp.Add(time.Minute)
	docs, err = r.RecordAppliedBatch(t.Context(), a.AgentID, receipts[:1], later)
	if err != nil || len(docs) != 1 || docs[0].AppliedVersion != 77 {
		t.Fatal("one-stream vector reused an earlier issuer document")
	}
	config, err = r.GetStream(t.Context(), a.AgentID, domain.NodeAgentStreamConfig)
	if err != nil || config.PendingSince != nil || config.AppliedVersion != receipts[0].Version || config.LastSeen == nil || !config.LastSeen.Equal(later) {
		t.Fatal("one-stream vector did not clear pending or refresh current receipt")
	}
	directivesAfter, err := r.GetStream(t.Context(), a.AgentID, domain.NodeAgentStreamDirectives)
	if err != nil || !reflect.DeepEqual(directives, directivesAfter) {
		t.Fatal("one-stream vector touched an excluded stream")
	}
}
