package sqlstore

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestDestinationRiskSignalPersistsLowerBoundsAndAttentionTransitions(t *testing.T) {
	r, users, db := newRiskSignalRepo(t)
	u := createRiskUser(t, users, 91, "Current account label")
	for _, count := range []int64{37, 12, 0} {
		v, ev := domain.EvaluateDestBlock(domain.DestBlockPolicy{Threshold: 20}, domain.DestBlockInput{
			CollectingNodes: 2, Sources: []domain.DestBlockSource{{Source: "p12", Count: count}},
			Losses: domain.DestAuditLosses{Rows: 3, Events: 7, Unmatched: 2},
		})
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Save(t.Context(), []domain.RiskSignal{{UserID: u.ID, Kind: domain.RiskKindDestBlock, State: v.State, Code: v.Code, Evidence: raw}}); err != nil {
			t.Fatal(err)
		}
		rows, err := r.ListByUsers(t.Context(), []int64{u.ID})
		if err != nil || len(rows) != 1 || rows[0].Kind != domain.RiskKindDestBlock || rows[0].State != v.State {
			t.Fatalf("destination signal round trip: %+v / %v", rows, err)
		}
		var stored domain.DestBlockEvidence
		if err := json.Unmarshal(rows[0].Evidence, &stored); err != nil || stored.Total != count || stored.Threshold != 20 || stored.Nodes != 2 || stored.CoverageComplete || stored.Losses.Complete || stored.Losses.Scope != "panel" || stored.Losses.Events != 7 {
			t.Fatalf("lower-bound evidence lost at state %s: %+v / %v", v.State, stored, err)
		}
		attention, err := r.AttentionLevels(t.Context(), time.Now().Add(-time.Hour))
		if err != nil || (count > 0 && len(attention) != 1) || (count == 0 && len(attention) != 0) {
			t.Fatalf("destination attention at %d: %+v / %v", count, attention, err)
		}
	}
	var records []flagRecordRow
	if err := db.Where("user_id = ? AND source = ?", u.ID, "dest_block").Order("id").Find(&records).Error; err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || records[0].Event != "enter_flagged" || records[1].Event != "leave_flagged" || records[2].Event != "leave_suspect" {
		t.Fatalf("destination transitions = %+v", records)
	}
	for _, record := range records {
		if record.Params == nil {
			t.Fatal("destination transition lost its evidence")
		}
		var params domain.DestBlockEvidence
		if err := json.Unmarshal([]byte(*record.Params), &params); err != nil {
			t.Fatal(err)
		}
		if params.WindowHours != 24 || params.Threshold != 20 || params.CoverageComplete || params.Losses.Complete || params.Losses.Scope != "panel" || params.Nodes != 2 {
			t.Fatalf("destination record lost lower-bound evidence: %+v", params)
		}
	}
}
