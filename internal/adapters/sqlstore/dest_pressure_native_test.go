//go:build destination_pressure && linux

package sqlstore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destaudit"
)

type pressureSQLLane struct {
	Kind         string  `json:"kind"`
	Batches      int64   `json:"batches"`
	Bytes        int64   `json:"canonical_bytes"`
	Wire         int64   `json:"batch_wire_bytes"`
	Offered      int64   `json:"offered_rows"`
	Stored       int64   `json:"stored_events"`
	Budget       int64   `json:"budget_rows"`
	QueueFull    int64   `json:"queue_full_rows"`
	OverBudget   int64   `json:"over_budget_rows"`
	Transactions int64   `json:"transactions"`
	MaxRows      int64   `json:"max_chunk_rows"`
	Durations    []int64 `json:"transaction_ns"`
}

// The wrapper measures complete calls to the real repository, including its
// gate and SQL transaction. It never substitutes storage or exports SQL values.
type pressureSQLStore struct {
	*DestAuditRepo
	lanes    map[string]*pressureSQLLane
	schedule []string
}

func (r *pressureSQLStore) record(kind string, rows int, started time.Time) {
	lane := r.lanes[kind]
	lane.Transactions++
	lane.MaxRows = max(lane.MaxRows, int64(rows))
	lane.Durations = append(lane.Durations, max(1, time.Since(started).Nanoseconds()))
	if len(r.schedule) < 7 {
		r.schedule = append(r.schedule, kind)
	}
}

func (r *pressureSQLStore) BeginDestinationAudit(ctx context.Context, b domain.DestAuditBatch) (domain.DestAuditBegin, error) {
	started := time.Now()
	out, err := r.DestAuditRepo.BeginDestinationAudit(ctx, b)
	r.record(b.Kind, out.Stored, started)
	return out, err
}

func (r *pressureSQLStore) WriteDestinationAuditChunk(ctx context.Context, b domain.DestAuditChunk) (string, error) {
	started := time.Now()
	out, err := r.DestAuditRepo.WriteDestinationAuditChunk(ctx, b)
	r.record(b.Kind, len(b.Hits)+len(b.Usage), started)
	return out, err
}

func TestNativeDestinationSQLPressure(t *testing.T) {
	path := os.Getenv("PSP_NATIVE_SQL_PRESSURE_METRICS")
	root, err := filepath.EvalSymlinks(os.Getenv("RUNNER_TEMP"))
	dir, dirErr := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil || dirErr != nil || root != dir || filepath.Base(path) != "destination-sql-pressure-metrics.json" || os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" || os.Getenv("GITHUB_REPOSITORY") != "KazuhaHub/Passwall-Sub-Panel" {
		t.Fatal("SQL pressure requires owned evidence on the hosted Linux runner")
	}
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "pressure.db"))
	if err != nil {
		t.Fatal("SQL pressure database open failed")
	}
	t.Cleanup(func() { closeGormDB(db) })
	if EnsureSchema(db) != nil {
		t.Fatal("SQL pressure schema failed")
	}
	panel := xuiPanelRow{Kind: string(domain.PanelKindPSP), AuditCollect: "hits_and_usage", AuditCollectRevision: 1}
	if db.Create(&panel).Error != nil || db.Create(&nodeAgentRow{AgentID: "agt_pressure", PanelID: panel.ID, CredentialSHA256: strings.Repeat("a", 64), ObservedCoreEngine: string(domain.NodeCoreXray), ObservedCapabilities: jsonStrings{protocol.CapabilityAuditHits, protocol.CapabilityAuditUsage}}).Error != nil || db.Create(&userRow{ID: 7, UPN: "pressure@example.test", SubToken: "pressure-token", UUID: "00000000-0000-0000-0000-000000000007", Enabled: true}).Error != nil {
		t.Fatal("SQL pressure principals failed")
	}
	r := &pressureSQLStore{DestAuditRepo: NewDestAuditRepo(db), lanes: map[string]*pressureSQLLane{}}
	kinds := []string{"block", "observe", "trial", "usage"}
	for _, kind := range kinds {
		r.lanes[kind] = &pressureSQLLane{Kind: kind}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	unexpected := false
	s, err := destaudit.New(ctx, r, destaudit.Options{Emit: func(e destaudit.Event) {
		lane := r.lanes[e.Kind]
		if lane == nil {
			unexpected = true
			return
		}
		switch e.Outcome {
		case "accepted":
			lane.Batches++
		case "queue_full":
			lane.QueueFull += e.Rows
		case "stored":
			lane.Stored += e.Rows
		case "over_budget":
			lane.OverBudget += e.Rows
		default:
			unexpected = true
		}
	}})
	if err != nil {
		t.Fatal("SQL pressure control seed failed")
	}
	now := time.Now().UTC()
	hour := now.Truncate(time.Hour).UnixMilli()
	wires := map[string][]byte{}
	for _, kind := range kinds {
		body := protocol.AuditObservation{BatchID: fmt.Sprintf("%032x", 1), Kind: kind, Hour: hour, CollectRevision: 1}
		for i := 0; i < 2001; i++ {
			site := fmt.Sprintf("d%05d%s.test", i, strings.Repeat("a", 48))
			if kind == "usage" {
				body.Usage = append(body.Usage, protocol.AuditUsage{Hour: hour, Subject: protocol.NewSubjectKey(7), Site: site, Count: 1})
			} else {
				hit := protocol.AuditHit{Hour: hour, RuleID: "p1", Action: "block", Subject: protocol.NewSubjectKey(7), Dest: "sub." + strings.Repeat("b", 56) + "." + strings.Repeat("c", 56) + "." + site, Port: 443, Count: 1, FirstMS: now.UnixMilli(), LastMS: now.UnixMilli()}
				if kind == "observe" {
					hit.Action = "observe"
				}
				if kind == "trial" {
					hit.RuleID, hit.Action, hit.Subject, hit.Dest, hit.Port = "g1", "observe", "", site, 0
				}
				body.Hits = append(body.Hits, hit)
			}
		}
		wire, err := json.Marshal(body)
		if err != nil || len(wire) > protocol.MaxAuditObservationBytes || protocol.ValidateAuditObservation(body) != nil {
			t.Fatal("SQL pressure wire invalid")
		}
		wires[kind] = wire
		r.lanes[kind].Wire = int64(len(wire))
	}
	// Fill all four real lanes together before starting the worker. Each request
	// owns an independently decoded body; the actual clock and limits stay intact.
	for batch := 1; batch <= 256; batch++ {
		full := 0
		for index, kind := range kinds {
			lane := r.lanes[kind]
			if lane.QueueFull > 0 {
				full++
				continue
			}
			var body protocol.AuditObservation
			if json.Unmarshal(wires[kind], &body) != nil {
				t.Fatal("SQL pressure decoding failed")
			}
			body.BatchID = fmt.Sprintf("%032x", batch*4+index)
			accepted := lane.Batches
			lane.Offered += 2001
			s.Offer("agt_pressure", panel.ID, now, body)
			if lane.Batches > accepted {
				lane.Bytes += lane.Wire
			}
		}
		if full == 4 {
			break
		}
	}
	for _, kind := range kinds {
		if r.lanes[kind].QueueFull != 2001 {
			t.Fatal("SQL pressure did not saturate each lane")
		}
	}
	s.StopOffers()
	started := time.Now()
	if summary := s.Run(ctx); summary != (destaudit.Summary{}) || ctx.Err() != nil || unexpected {
		t.Fatal("SQL pressure did not completely drain")
	}
	if strings.Join(r.schedule, ",") != "block,block,block,block,observe,trial,usage" {
		t.Fatal("SQL pressure lost original chunk scheduling")
	}
	var budgets []destAuditIngestBudgetRow
	var losses []destAuditLossHourlyRow
	if db.Find(&budgets).Error != nil || db.Find(&losses).Error != nil || len(budgets) != 4 || len(losses) != 8 {
		t.Fatal("SQL pressure accounting incomplete")
	}
	for _, row := range budgets {
		r.lanes[row.Kind].Budget = row.RowsReserved
	}
	for _, row := range losses {
		lane := r.lanes[row.Kind]
		if lane == nil || row.Events != 0 || row.Unmatched != 0 || (row.Reason != "over_budget" && row.Reason != "queue_full") {
			t.Fatal("SQL pressure loss category invalid")
		}
		want := lane.OverBudget
		if row.Reason == "queue_full" {
			want = lane.QueueFull
		}
		if row.Rows != want {
			t.Fatal("SQL pressure durable loss differs from worker")
		}
	}
	var privateTrial int64
	if db.Model(&destHitRow{}).Where("source = ? AND (user_id <> 0 OR port <> 0)", "g1").Count(&privateTrial).Error != nil || privateTrial != 0 {
		t.Fatal("SQL pressure identified trial")
	}
	for _, kind := range kinds {
		lane := r.lanes[kind]
		limit := int64(20000)
		if kind == "usage" {
			limit = 40000
		}
		var events int64
		query := db.Model(&destHitRow{}).Select("COALESCE(SUM(count), 0)").Where("source = ? AND action = ?", "p1", kind)
		if kind == "trial" {
			query = db.Model(&destHitRow{}).Select("COALESCE(SUM(count), 0)").Where("source = ?", "g1")
		}
		if kind == "usage" {
			query = db.Model(&destUsageHourlyRow{}).Select("COALESCE(SUM(count), 0)")
		}
		if query.Scan(&events).Error != nil || events != lane.Stored || lane.Stored != limit || lane.Budget != limit || lane.OverBudget <= 0 || lane.Stored+lane.OverBudget+lane.QueueFull != lane.Offered || lane.MaxRows != 1000 || lane.Transactions != int64(len(lane.Durations)) {
			t.Fatal("SQL pressure row budget or chunk ledger failed")
		}
	}
	report := struct {
		Schema   int                `json:"schema_version"`
		Database string             `json:"database"`
		Drain    int64              `json:"drain_ns"`
		Schedule []string           `json:"first_cycle"`
		Lanes    []*pressureSQLLane `json:"lanes"`
	}{Schema: 1, Database: "sqlite", Drain: time.Since(started).Nanoseconds(), Schedule: r.schedule}
	for _, kind := range kinds {
		report.Lanes = append(report.Lanes, r.lanes[kind])
	}
	data, err := json.Marshal(report)
	if err != nil || len(data) > 32*1024 || os.WriteFile(path, append(data, '\n'), 0644) != nil {
		t.Fatal("SQL pressure bounded evidence write failed")
	}
}
