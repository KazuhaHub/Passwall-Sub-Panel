package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
	sqlitedriver "github.com/glebarez/sqlite"
	mysqlsql "github.com/go-sql-driver/mysql"
	mysqldriver "gorm.io/driver/mysql"
	postgresdriver "gorm.io/driver/postgres"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func auditIngestFixture(t *testing.T, rows int) (*DestAuditRepo, domain.DestAuditBatch) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	panel := xuiPanelRow{Kind: string(domain.PanelKindPSP), AuditCollect: "hits", AuditCollectRevision: 1}
	if err := db.Create(&panel).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&nodeAgentRow{AgentID: "agt_audit", PanelID: panel.ID, CredentialSHA256: strings.Repeat("a", 64), ObservedCoreEngine: string(domain.NodeCoreXray), ObservedCapabilities: jsonStrings{protocol.CapabilityAuditHits, protocol.CapabilityAuditUsage}}).Error; err != nil {
		t.Fatal(err)
	}
	const hour = int64(1_800_000_000_000)
	b := domain.DestAuditBatch{AgentID: "agt_audit", BatchID: "0123456789abcdef0123456789abcdef", Kind: "block", PanelID: panel.ID, HourMS: hour, ReceivedHourMS: hour, ReceivedAt: time.UnixMilli(hour + 10).UTC(), CollectRevision: 1, Dropped: 3, Unmatched: 4, Losses: map[string]int64{"unknown_subject": 2}}
	for i := 0; i < rows; i++ {
		b.Hits = append(b.Hits, domain.DestHit{HourMS: hour, PanelID: panel.ID, UserID: 7, Source: "p12", Action: "block", Dest: fmt.Sprintf("h%d.example.com", i), Port: 443, Count: 5, FirstAt: time.UnixMilli(hour + 1).UTC(), LastAt: time.UnixMilli(hour + 2).UTC()})
	}
	return NewDestAuditRepo(db), b
}

func auditRowCount(t *testing.T, db *gorm.DB, model any) int64 {
	t.Helper()
	var count int64
	if err := db.Model(model).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func TestDestAuditSubjectResolverOnlyReadsIDsAndBoundsStatements(t *testing.T) {
	r, _ := auditIngestFixture(t, 0)
	users := make([]userRow, 450)
	for i := range users {
		id := int64(i + 1)
		users[i] = userRow{ID: id, UPN: fmt.Sprintf("audit-resolver%d@example.test", id), SubToken: fmt.Sprintf("audit-resolver-token%d", id), UUID: fmt.Sprintf("%036d", id), Enabled: i%2 == 0}
	}
	if err := r.db.CreateInBatches(&users, 100).Error; err != nil {
		t.Fatal(err)
	}
	recorder := &auditTraceRecorder{Interface: logger.Discard}
	r.db = r.db.Session(&gorm.Session{Logger: recorder})
	queries := 0
	if err := r.db.Callback().Query().Before("gorm:query").Register("audit_id_projection", func(tx *gorm.DB) {
		if tx.Statement.Table != "users" {
			t.Errorf("unexpected resolver table%s", tx.Statement.Table)
		}
		if len(tx.Statement.Selects) != 1 || tx.Statement.Selects[0] != "id" {
			t.Error("resolver loads account fields")
		}
		queries++
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Query().Remove("audit_id_projection") })
	ids := []int64{9000, 1, 450}
	for id := int64(450); id >= 1; id-- {
		ids = append(ids, id)
	}
	known, err := r.ResolveDestinationAuditUsers(t.Context(), ids)
	if err != nil || len(known) != 450 || !known[1] || !known[2] || !known[450] || known[9000] || queries != 3 {
		t.Fatalf("known IDs%d queries%d error%v", len(known), queries, err)
	}
	if len(recorder.queries) != 0 {
		t.Fatal("ID lookup reached debug tracing")
	}
	if empty, err := r.ResolveDestinationAuditUsers(t.Context(), nil); err != nil || len(empty) != 0 || queries != 3 {
		t.Fatal("empty list queried accounts")
	}
	for _, bad := range [][]int64{{0}, {-1}, make([]int64, protocol.MaxAuditUsage+1)} {
		if _, err := r.ResolveDestinationAuditUsers(t.Context(), bad); !errors.Is(err, domain.ErrValidation) {
			t.Fatal("invalid ID input accepted")
		}
	}
	if queries != 3 {
		t.Fatal("invalid IDs reached storage")
	}
}

func TestDestAuditSubjectResolverHidesDatabaseErrorValues(t *testing.T) {
	r, _ := auditIngestFixture(t, 0)
	recorder := &auditTraceRecorder{Interface: logger.Discard}
	r.db = r.db.Session(&gorm.Session{Logger: recorder})
	if err := r.db.Callback().Query().Before("gorm:query").Register("audit_id_private_error", func(tx *gorm.DB) { tx.AddError(errors.New("private account@example.test and SQL details")) }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Query().Remove("audit_id_private_error") })
	if _, err := r.ResolveDestinationAuditUsers(t.Context(), []int64{7}); err != errAuditStorage || len(recorder.queries) != 0 {
		t.Fatal("ID resolver leaked error or SQL tracing")
	}
}

func assertAuditBudget(t *testing.T, r *DestAuditRepo, b domain.DestAuditBatch, want int64) {
	t.Helper()
	var row destAuditIngestBudgetRow
	if err := r.db.Where("agent_id = ? AND received_hour_ms = ? AND kind = ?", b.AgentID, b.ReceivedHourMS, b.Kind).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.RowsReserved != want {
		t.Fatalf("budget%d want%d", row.RowsReserved, want)
	}
}

func TestDestAuditFirstChunkCommitsDedupBudgetAndNodeCountsTogether(t *testing.T) {
	r, b := auditIngestFixture(t, 1001)
	got, err := r.BeginDestinationAudit(t.Context(), b)
	if err != nil || got.Duplicate || got.Reserved != 1001 || got.Stored != 1000 {
		t.Fatalf("first chunk %+v error%v", got, err)
	}
	if n := auditRowCount(t, r.db, &destHitRow{}); n != 1000 {
		t.Fatalf("first transaction wrote%d rows", n)
	}
	assertAuditBudget(t, r, b, 1001)
	var marker destAuditBatchRow
	if err := r.db.First(&marker).Error; err != nil {
		t.Fatal(err)
	}
	if !marker.ReceivedAt.Equal(b.ReceivedAt) {
		t.Fatal("first receipt timestamp changed")
	}
	var losses []destAuditLossHourlyRow
	if err := r.db.Order("reason").Find(&losses).Error; err != nil {
		t.Fatal(err)
	}
	if len(losses) != 3 {
		t.Fatalf("node and mapping losses absent: %+v", losses)
	}
	for _, row := range losses {
		switch row.Reason {
		case "node_dropped":
			if row.Events != 3 || row.Rows != 0 {
				t.Fatal("dropped events mixed with rows")
			}
		case "node_unmatched":
			if row.Unmatched != 4 || row.Rows != 0 {
				t.Fatal("unmatched events mixed with rows")
			}
		case "unknown_subject":
			if row.Rows != 2 || row.Events != 0 {
				t.Fatal("subject row loss mixed with events")
			}
		default:
			t.Fatalf("unexpected reason%s", row.Reason)
		}
	}
	chunk := domain.DestAuditChunk{AgentID: b.AgentID, BatchID: b.BatchID, Kind: b.Kind, PanelID: b.PanelID, CollectRevision: 1, Hits: b.Hits[1000:]}
	if rejected, err := r.WriteDestinationAuditChunk(t.Context(), chunk); err != nil || rejected != "" {
		t.Fatalf("next chunk %s %v", rejected, err)
	}
	if n := auditRowCount(t, r.db, &destHitRow{}); n != 1001 {
		t.Fatal("missing final chunk")
	}
	original := b
	b.ReceivedAt = b.ReceivedAt.Add(time.Hour)
	b.ReceivedHourMS += int64(time.Hour / time.Millisecond)
	duplicate, err := r.BeginDestinationAudit(t.Context(), b)
	if err != nil || !duplicate.Duplicate || duplicate.Reserved != 0 {
		t.Fatalf("replay %+v %v", duplicate, err)
	}
	assertAuditBudget(t, r, original, 1001)
	var first destHitRow
	r.db.First(&first)
	if first.Count != 5 {
		t.Fatal("replay counted events twice")
	}
	var again destAuditBatchRow
	r.db.First(&again)
	if !again.ReceivedAt.Equal(marker.ReceivedAt) {
		t.Fatal("replay extended dedup retention")
	}
}

type auditTraceRecorder struct {
	logger.Interface
	queries []string
}

func (l *auditTraceRecorder) Trace(_ context.Context, _ time.Time, query func() (string, int64), _ error) {
	sql, _ := query()
	l.queries = append(l.queries, sql)
}

func TestDestAuditPrivateSQLAndErrorsStayOutOfDebugLogs(t *testing.T) {
	r, b := auditIngestFixture(t, 1)
	recorder := &auditTraceRecorder{Interface: logger.Discard}
	r.db = r.db.Session(&gorm.Session{Logger: recorder})
	if _, err := r.BeginDestinationAudit(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if len(recorder.queries) != 0 {
		t.Fatal("audit parameters reached database tracing")
	}
	if err := r.db.Callback().Create().Before("gorm:create").Register("audit_private_error", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_hits" {
			tx.AddError(errors.New("database detail contains private.example.com usr_7"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Create().Remove("audit_private_error") })
	b.BatchID = "1123456789abcdef0123456789abcdef"
	_, err := r.BeginDestinationAudit(t.Context(), b)
	if err != errAuditStorage || err.Error() != "destination audit storage write failed" || len(recorder.queries) != 0 {
		t.Fatal("private audit failure escaped sanitized boundary")
	}
}

func TestDestAuditStatementsHaveAtMost200Rows(t *testing.T) {
	r, b := auditIngestFixture(t, 1001)
	var statements []int
	if err := r.db.Callback().Create().Before("gorm:create").Register("audit_statement_bound", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_hits" {
			statements = append(statements, tx.Statement.ReflectValue.Len())
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Create().Remove("audit_statement_bound") })
	if _, err := r.BeginDestinationAudit(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if len(statements) != 5 {
		t.Fatalf("first chunk statements%v", statements)
	}
	for _, rows := range statements {
		if rows != 200 {
			t.Fatalf("statement rows%d", rows)
		}
	}
}

func TestDestAuditConcurrentReplayReservesOnce(t *testing.T) {
	r, b := auditIngestFixture(t, 1)
	var wg sync.WaitGroup
	var mu sync.Mutex
	duplicates, writes := 0, 0
	for i := 0; i < 10; i++ {
		wg.Go(func() {
			got, err := r.BeginDestinationAudit(t.Context(), b)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				t.Error(err)
			} else if got.Duplicate {
				duplicates++
			} else if got.Stored == 1 {
				writes++
			}
		})
	}
	wg.Wait()
	if duplicates != 9 || writes != 1 {
		t.Fatalf("concurrent writes%d duplicates%d", writes, duplicates)
	}
	assertAuditBudget(t, r, b, 1)
	var row destHitRow
	if err := r.db.First(&row).Error; err != nil || row.Count != 5 {
		t.Fatalf("concurrent count %+v %v", row, err)
	}
}

func TestDestAuditCollectionWriterSharesChunkGate(t *testing.T) {
	r, b := auditIngestFixture(t, 1)
	repos := NewRepos(r.db)
	store := repos.DestAudit.(*DestAuditRepo)
	panels := repos.XUIPanel.(*xuiPanelRepo)
	if panels.auditGates != store.gates {
		t.Fatal("collection writer and ingestion use different panel gates")
	}
	unlock := store.gates.Lock(b.PanelID)
	var once sync.Once
	release := func() { once.Do(unlock) }
	defer release()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		off := domain.AuditCollectOff
		done <- panels.UpdateNativeMetadata(context.Background(), b.PanelID, nil, nil, nil, &off)
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("metadata completed inside ingestion gate: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("metadata did not finish after gate release")
	}
	if got, err := store.BeginDestinationAudit(t.Context(), b); err != nil || got.Rejected != "stale_collect_revision" {
		t.Fatalf("old batch crossed successful setting write %+v %v", got, err)
	}
}

func TestDestAuditDedupIsIndependentOfNoopAffectedRowReporting(t *testing.T) {
	r, b := auditIngestFixture(t, 1)
	if _, err := r.BeginDestinationAudit(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	// Simulate CLIENT_FOUND_ROWS on a no-op insert conflict. The locked-owner
	// precheck must identify replay before any affected-row ambiguity matters.
	if err := r.db.Callback().Create().After("gorm:create").Register("audit_found_rows", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_audit_batches" && tx.Error == nil && tx.RowsAffected == 0 {
			tx.RowsAffected = 1
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Create().Remove("audit_found_rows") })
	if got, err := r.BeginDestinationAudit(t.Context(), b); err != nil || !got.Duplicate {
		t.Fatalf("no-op affected row reporting defeated dedup %+v %v", got, err)
	}
	assertAuditBudget(t, r, b, 1)
}

func TestDestAuditMySQLClientFoundRowsStillDeduplicates(t *testing.T) {
	r, b := auditIngestFixture(t, 1)
	if r.db.Dialector.Name() != "mysql" {
		t.Skip("requires the actual MySQL dialect job")
	}
	dialect := r.db.Dialector
	if wrapper, ok := dialect.(reusableSchemaDialector); ok {
		dialect = wrapper.Dialector
	}
	dsn := dialect.(*mysqldriver.Dialector).Config.DSN
	config, err := mysqlsql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ClientFoundRows = true
	other, err := Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeGormDB(other) })
	second := NewDestAuditRepo(other)
	if _, err := r.BeginDestinationAudit(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if got, err := second.BeginDestinationAudit(t.Context(), b); err != nil || !got.Duplicate {
		t.Fatalf("MySQL found rows replay %+v %v", got, err)
	}
	assertAuditBudget(t, second, b, 1)
}

func injectAuditCreateFailure(t *testing.T, r *DestAuditRepo, name string) func() {
	t.Helper()
	if err := r.db.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_hits" {
			tx.AddError(errors.New("injected audit write failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := r.db.Callback().Create().Remove(name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDestAuditFirstRollbackLeavesNoMarkerBudgetOrLoss(t *testing.T) {
	r, b := auditIngestFixture(t, 1)
	remove := injectAuditCreateFailure(t, r, "audit_first_failure")
	_, err := r.BeginDestinationAudit(t.Context(), b)
	remove()
	if err == nil {
		t.Fatal("injected first write did not fail")
	}
	for _, model := range []any{&destAuditBatchRow{}, &destAuditIngestBudgetRow{}, &destAuditLossHourlyRow{}, &destHitRow{}} {
		if n := auditRowCount(t, r.db, model); n != 0 {
			t.Fatalf("rollback retained%d rows in%T", n, model)
		}
	}
	if got, err := r.BeginDestinationAudit(t.Context(), b); err != nil || got.Duplicate || got.Stored != 1 {
		t.Fatalf("rollback could not retry %+v %v", got, err)
	}
}

func TestDestAuditSecondStatementRollbackKeepsFirstChunkAtomic(t *testing.T) {
	r, b := auditIngestFixture(t, 1000)
	statements := 0
	if err := r.db.Callback().Create().Before("gorm:create").Register("audit_second_statement_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_hits" {
			statements++
			if statements == 2 {
				tx.AddError(errors.New("injected second statement failure"))
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Create().Remove("audit_second_statement_failure") })
	if _, err := r.BeginDestinationAudit(t.Context(), b); err == nil || statements != 2 {
		t.Fatal("second statement failure was not observed")
	}
	for _, model := range []any{&destAuditBatchRow{}, &destAuditIngestBudgetRow{}, &destAuditLossHourlyRow{}, &destHitRow{}} {
		if n := auditRowCount(t, r.db, model); n != 0 {
			t.Fatalf("first transaction partially committed%d rows in%T", n, model)
		}
	}
}

func TestDestAuditLaterChunkMustKeepFrozenHour(t *testing.T) {
	r, b := auditIngestFixture(t, 1001)
	if _, err := r.BeginDestinationAudit(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	row := b.Hits[1000]
	row.HourMS += int64(time.Hour / time.Millisecond)
	row.FirstAt = row.FirstAt.Add(time.Hour)
	row.LastAt = row.LastAt.Add(time.Hour)
	if _, err := r.WriteDestinationAuditChunk(t.Context(), domain.DestAuditChunk{AgentID: b.AgentID, BatchID: b.BatchID, PanelID: b.PanelID, Kind: b.Kind, CollectRevision: 1, Hits: []domain.DestHit{row}}); !errors.Is(err, domain.ErrValidation) {
		t.Fatal("continuation changed frozen hour")
	}
	if n := auditRowCount(t, r.db, &destHitRow{}); n != 1000 {
		t.Fatal("invalid continuation wrote data")
	}
}

func TestDestAuditLaterFailureKeepsWholeBudgetAndReplayCannotFillGap(t *testing.T) {
	r, b := auditIngestFixture(t, 1001)
	if _, err := r.BeginDestinationAudit(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	remove := injectAuditCreateFailure(t, r, "audit_later_failure")
	_, err := r.WriteDestinationAuditChunk(t.Context(), domain.DestAuditChunk{AgentID: b.AgentID, BatchID: b.BatchID, PanelID: b.PanelID, Kind: b.Kind, CollectRevision: 1, Hits: b.Hits[1000:]})
	remove()
	if err == nil {
		t.Fatal("injected later write did not fail")
	}
	if got, err := r.BeginDestinationAudit(t.Context(), b); err != nil || !got.Duplicate {
		t.Fatalf("partial replay %+v %v", got, err)
	}
	if n := auditRowCount(t, r.db, &destHitRow{}); n != 1000 {
		t.Fatal("failed chunk replay filled gap")
	}
	assertAuditBudget(t, r, b, 1001)
}

func TestDestAuditBudgetTruncatesLogicalRowsAndSeparatesKinds(t *testing.T) {
	r, b := auditIngestFixture(t, 2)
	if err := r.db.Create(&destAuditIngestBudgetRow{AgentID: b.AgentID, ReceivedHourMS: b.ReceivedHourMS, Kind: b.Kind, RowsReserved: 19999}).Error; err != nil {
		t.Fatal(err)
	}
	got, err := r.BeginDestinationAudit(t.Context(), b)
	if err != nil || got.Reserved != 1 || got.Stored != 1 {
		t.Fatalf("remaining budget %+v %v", got, err)
	}
	assertAuditBudget(t, r, b, 20000)
	var loss destAuditLossHourlyRow
	if err := r.db.Where("reason = ?", "over_budget").First(&loss).Error; err != nil || loss.Rows != 1 {
		t.Fatalf("over budget loss %+v %v", loss, err)
	}
	b.Kind = "observe"
	b.BatchID = "1123456789abcdef0123456789abcdef"
	for i := range b.Hits {
		b.Hits[i].Action = "observe"
	}
	if got, err := r.BeginDestinationAudit(t.Context(), b); err != nil || got.Reserved != 2 || got.Stored != 2 {
		t.Fatalf("observe borrowed block budget %+v %v", got, err)
	}
	assertAuditBudget(t, r, b, 2)
}

func TestDestAuditUpsertsCountAndFirstLastAcrossBatches(t *testing.T) {
	r, b := auditIngestFixture(t, 1)
	if _, err := r.BeginDestinationAudit(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	b.BatchID = "1123456789abcdef0123456789abcdef"
	b.Hits[0].Count = 9
	b.Hits[0].FirstAt = b.Hits[0].FirstAt.Add(-time.Millisecond)
	b.Hits[0].LastAt = b.Hits[0].LastAt.Add(5 * time.Millisecond)
	if _, err := r.BeginDestinationAudit(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	var row destHitRow
	if err := r.db.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Count != 14 || !row.FirstAt.Equal(b.Hits[0].FirstAt) || !row.LastAt.Equal(b.Hits[0].LastAt) {
		t.Fatalf("upsert %+v", row)
	}
}

func TestDestAuditCounterOnlyClampsUnsignedCountsAndDeduplicates(t *testing.T) {
	r, b := auditIngestFixture(t, 0)
	b.Dropped = math.MaxUint64
	b.Unmatched = math.MaxUint64
	b.Losses = nil
	if got, err := r.BeginDestinationAudit(t.Context(), b); err != nil || got.Reserved != 0 || got.Stored != 0 {
		t.Fatalf("counter-only %+v %v", got, err)
	}
	var loss destAuditLossHourlyRow
	if err := r.db.Where("reason = ?", "node_dropped").First(&loss).Error; err != nil || loss.Events != math.MaxInt64 {
		t.Fatalf("unsigned overflow %+v %v", loss, err)
	}
	if got, err := r.BeginDestinationAudit(t.Context(), b); err != nil || !got.Duplicate {
		t.Fatalf("counter-only dedup %+v %v", got, err)
	}
	var again destAuditLossHourlyRow
	r.db.Where("reason = ?", "node_dropped").First(&again)
	if again.Events != math.MaxInt64 {
		t.Fatal("counter replay overflowed")
	}
}

func TestDestAuditChecksCurrentCollectionBeforeEachChunk(t *testing.T) {
	r, b := auditIngestFixture(t, 1001)
	if _, err := r.BeginDestinationAudit(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	off := domain.AuditCollectOff
	if err := (&xuiPanelRepo{db: r.db}).UpdateNativeMetadata(context.Background(), b.PanelID, nil, nil, nil, &off); err != nil {
		t.Fatal(err)
	}
	chunk := domain.DestAuditChunk{AgentID: b.AgentID, BatchID: b.BatchID, PanelID: b.PanelID, Kind: b.Kind, CollectRevision: 1, Hits: b.Hits[1000:]}
	if reason, err := r.WriteDestinationAuditChunk(t.Context(), chunk); err != nil || reason != "stale_collect_revision" {
		t.Fatalf("old collection chunk %s %v", reason, err)
	}
	if n := auditRowCount(t, r.db, &destHitRow{}); n != 1000 {
		t.Fatal("off accepted a later chunk")
	}
	b.BatchID = "2123456789abcdef0123456789abcdef"
	b.CollectRevision = 2
	if got, err := r.BeginDestinationAudit(t.Context(), b); err != nil || got.Rejected != "collect_off" {
		t.Fatalf("off accepted first chunk %+v %v", got, err)
	}
}

func TestDestAuditUsesCurrentEngineCapabilitiesAndNeverRevivesOldRevision(t *testing.T) {
	for _, tc := range []struct {
		name   string
		engine string
		caps   jsonStrings
	}{{"sing-box", "sing-box", jsonStrings{"audit.hits.v1", "audit.usage.v1"}}, {"missing hits", "xray", jsonStrings{"audit.usage.v1"}}, {"missing usage", "xray", jsonStrings{"audit.hits.v1"}}} {
		t.Run(tc.name, func(t *testing.T) {
			r, b := auditIngestFixture(t, 0)
			b.Kind = "usage"
			if err := r.db.Model(&xuiPanelRow{}).Where("id = ?", b.PanelID).Update("audit_collect", "hits_and_usage").Error; err != nil {
				t.Fatal(err)
			}
			if err := r.db.Model(&nodeAgentRow{}).Where("agent_id = ?", b.AgentID).Updates(map[string]any{"observed_core_engine": tc.engine, "observed_capabilities": tc.caps}).Error; err != nil {
				t.Fatal(err)
			}
			if got, err := r.BeginDestinationAudit(t.Context(), b); err != nil || got.Rejected != "no_capability" {
				t.Fatalf("unsupported audit permission %+v %v", got, err)
			}
			if auditRowCount(t, r.db, &destAuditBatchRow{}) != 0 || auditRowCount(t, r.db, &destAuditLossHourlyRow{}) != 0 {
				t.Fatal("unsupported audit persisted")
			}
		})
	}
	r, b := auditIngestFixture(t, 1001)
	repos := NewRepos(r.db)
	store := repos.DestAudit.(*DestAuditRepo)
	panels := repos.XUIPanel.(*xuiPanelRepo)
	if _, err := store.BeginDestinationAudit(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []domain.AuditCollect{domain.AuditCollectOff, domain.AuditCollectHits} {
		if err := panels.UpdateNativeMetadata(t.Context(), b.PanelID, nil, nil, nil, &mode); err != nil {
			t.Fatal(err)
		}
	}
	if reason, err := store.WriteDestinationAuditChunk(t.Context(), domain.DestAuditChunk{AgentID: b.AgentID, BatchID: b.BatchID, PanelID: b.PanelID, Kind: b.Kind, CollectRevision: 1, Hits: b.Hits[1000:]}); err != nil || reason != "stale_collect_revision" {
		t.Fatalf("off/on revived old revision %s %v", reason, err)
	}
	if auditRowCount(t, r.db, &destHitRow{}) != 1000 {
		t.Fatal("off/on restored old pending rows")
	}
}

func TestDestAuditUsageBudgetAndAnonymousTrialPersistWithoutMixing(t *testing.T) {
	r, b := auditIngestFixture(t, 0)
	if err := r.db.Model(&xuiPanelRow{}).Where("id = ?", b.PanelID).Update("audit_collect", "hits_and_usage").Error; err != nil {
		t.Fatal(err)
	}
	b.Kind = "usage"
	b.Usage = []domain.DestUsage{{HourMS: b.HourMS, PanelID: b.PanelID, UserID: 7, Site: "example.com", Count: 100}, {HourMS: b.HourMS, PanelID: b.PanelID, UserID: 7, Site: "(ip)", Count: 200}}
	if err := r.db.Create(&destAuditIngestBudgetRow{AgentID: b.AgentID, ReceivedHourMS: b.ReceivedHourMS, Kind: b.Kind, RowsReserved: 39999}).Error; err != nil {
		t.Fatal(err)
	}
	if got, err := r.BeginDestinationAudit(t.Context(), b); err != nil || got.Reserved != 1 || got.Stored != 1 {
		t.Fatalf("usage reservation %+v %v", got, err)
	}
	assertAuditBudget(t, r, b, 40000)
	var usage destUsageHourlyRow
	if err := r.db.First(&usage).Error; err != nil || usage.Count != 100 {
		t.Fatalf("usage count %+v %v", usage, err)
	}
	b.Kind = "trial"
	b.BatchID = "1123456789abcdef0123456789abcdef"
	b.Usage = nil
	b.Hits = []domain.DestHit{{HourMS: b.HourMS, PanelID: b.PanelID, Source: "g5", Action: "observe", Dest: "example.com", Count: 3, FirstAt: time.UnixMilli(b.HourMS).UTC(), LastAt: time.UnixMilli(b.HourMS + 1).UTC()}}
	if got, err := r.BeginDestinationAudit(t.Context(), b); err != nil || got.Stored != 1 {
		t.Fatalf("trial %+v %v", got, err)
	}
	var trial destHitRow
	if err := r.db.First(&trial).Error; err != nil || trial.UserID != 0 || trial.Port != 0 || trial.Count != 3 {
		t.Fatalf("anonymous trial changed %+v %v", trial, err)
	}
	assertAuditBudget(t, r, b, 1)
}

func TestDestAuditRejectsUnmergedKeysAndSaturatesStorageCounts(t *testing.T) {
	r, b := auditIngestFixture(t, 1)
	b.Hits = append(b.Hits, b.Hits[0])
	if _, err := r.BeginDestinationAudit(t.Context(), b); !errors.Is(err, domain.ErrValidation) {
		t.Fatal("unmerged final keys accepted")
	}
	if auditRowCount(t, r.db, &destAuditBatchRow{}) != 0 {
		t.Fatal("invalid rows left a marker")
	}
	b.Hits = b.Hits[:1]
	b.Hits[0].Count = math.MaxInt64
	if _, err := r.BeginDestinationAudit(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	b.BatchID = "1123456789abcdef0123456789abcdef"
	b.Hits[0].Count = 1
	if _, err := r.BeginDestinationAudit(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	var row destHitRow
	if err := r.db.First(&row).Error; err != nil || row.Count != math.MaxInt64 {
		t.Fatalf("signed count overflow %+v %v", row, err)
	}
}

func TestDestAuditConflictSQLUsesDialectTimeAndCountExpressions(t *testing.T) {
	for _, kind := range []string{"sqlite", "postgres", "mysql"} {
		t.Run(kind, func(t *testing.T) {
			var dialect gorm.Dialector
			switch kind {
			case "sqlite":
				dialect = sqlitedriver.Open(":memory:")
			case "mysql":
				conn, _ := sql.Open("mysql", "u:p@tcp(127.0.0.1:1)/dryrun")
				t.Cleanup(func() { _ = conn.Close() })
				dialect = mysqldriver.New(mysqldriver.Config{Conn: conn, SkipInitializeWithVersion: true})
			case "postgres":
				conn, _ := sql.Open("pgx", "postgres://u:p@127.0.0.1:1/dryrun")
				t.Cleanup(func() { _ = conn.Close() })
				dialect = postgresdriver.New(postgresdriver.Config{Conn: conn})
			}
			db, err := gorm.Open(dialect, &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: logger.Discard})
			if err != nil {
				t.Fatal(err)
			}
			query := db.ToSQL(func(tx *gorm.DB) *gorm.DB { return tx.Clauses(auditHitConflict(kind)).Create(&destHitRow{}) })
			least, greatest := "LEAST(", "GREATEST("
			incoming := "excluded.count"
			if kind == "sqlite" {
				least, greatest = "MIN(", "MAX("
			}
			if kind == "mysql" {
				incoming = "VALUES(`count`)"
			}
			if !strings.Contains(query, least) || !strings.Contains(query, greatest) || !strings.Contains(query, incoming) || !strings.Contains(query, "CASE WHEN") {
				t.Fatalf("wrong %s upsert expressions", kind)
			}
			marker := db.ToSQL(func(tx *gorm.DB) *gorm.DB {
				return tx.Clauses(auditKeepConflict(kind, "agent_id", "batch_id")).Create(&destAuditBatchRow{})
			})
			if kind == "mysql" {
				if !strings.Contains(marker, "ON DUPLICATE KEY UPDATE `agent_id`=`agent_id`") {
					t.Fatal("invalid MySQL no-op marker insert")
				}
			} else if !strings.Contains(marker, "DO NOTHING") {
				t.Fatal("marker insert has no conflict-safe skip")
			}
		})
	}
}
