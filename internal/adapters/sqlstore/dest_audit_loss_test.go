package sqlstore

import (
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	mysqlsql "github.com/go-sql-driver/mysql"
	mysqldriver "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func auditLossFixture(t *testing.T) (*DestAuditRepo, domain.DestAuditLossBatch) {
	t.Helper()
	r, original := auditIngestFixture(t, 0)
	r.now = func() time.Time { return original.ReceivedAt }
	return r, domain.DestAuditLossBatch{BatchID: original.BatchID, ReceivedAt: original.ReceivedAt, Losses: []domain.DestAuditLoss{
		{HourMS: original.ReceivedHourMS, PanelID: original.PanelID, Kind: "block", Reason: "queue_full", Rows: 7},
		{HourMS: original.ReceivedHourMS, PanelID: original.PanelID, Kind: "usage", Reason: "ingest_error", Rows: 11},
	}}
}

func TestDestAuditReceiverLossCommitsOnceAndKeepsUnitsSeparate(t *testing.T) {
	r, b := auditLossFixture(t)
	// A node batch may have the same random ID. Receiver markers occupy the
	// empty-agent namespace, which both node creation and ingestion reject.
	node := domain.DestAuditBatch{AgentID: "agt_audit", BatchID: b.BatchID, Kind: "block", PanelID: b.Losses[0].PanelID, HourMS: b.Losses[0].HourMS, ReceivedHourMS: b.Losses[0].HourMS, ReceivedAt: b.ReceivedAt, CollectRevision: 1, Dropped: 3}
	if _, err := r.BeginDestinationAudit(t.Context(), node); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := r.FlushDestinationAuditLoss(t.Context(), b); err != nil {
			t.Fatal(err)
		}
	}
	var rows []destAuditLossHourlyRow
	if err := r.db.Where("reason IN ?", []string{"queue_full", "ingest_error"}).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("loss rows%d", len(rows))
	}
	for _, row := range rows {
		want := int64(7)
		if row.Kind == "usage" {
			want = 11
		}
		if row.Rows != want || row.Events != 0 || row.Unmatched != 0 {
			t.Fatalf("loss units%+v", row)
		}
	}
	if auditRowCount(t, r.db, &destAuditBatchRow{}) != 2 {
		t.Fatal("receiver and node identities collided")
	}
	if auditRowCount(t, r.db, &destAuditIngestBudgetRow{}) != 1 {
		t.Fatal("receiver losses consumed node quota")
	}
	var marker destAuditBatchRow
	if err := r.db.Where("agent_id = ? AND batch_id = ?", "", b.BatchID).First(&marker).Error; err != nil || marker.Kind != "receiver_loss" || !marker.ReceivedAt.Equal(b.ReceivedAt) {
		t.Fatalf("receiver marker%+v %v", marker, err)
	}
}

func TestDestAuditReceiverLossRollbackAndPrivateErrors(t *testing.T) {
	r, b := auditLossFixture(t)
	recorder := &auditTraceRecorder{Interface: logger.Discard}
	r.db = r.db.Session(&gorm.Session{Logger: recorder})
	if err := r.db.Callback().Create().Before("gorm:create").Register("receiver_loss_fault", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_audit_loss_hourly" {
			tx.AddError(errors.New("private SQL account@example.test"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Create().Remove("receiver_loss_fault") })
	if err := r.FlushDestinationAuditLoss(t.Context(), b); err != errAuditStorage {
		t.Fatalf("private error%v", err)
	}
	if len(recorder.queries) != 0 {
		t.Fatal("receiver loss reached debug tracing")
	}
	if auditRowCount(t, r.db, &destAuditBatchRow{}) != 0 || auditRowCount(t, r.db, &destAuditLossHourlyRow{}) != 0 {
		t.Fatal("loss and marker were not atomic")
	}
	_ = r.db.Callback().Create().Remove("receiver_loss_fault")
	if err := r.FlushDestinationAuditLoss(t.Context(), b); err != nil {
		t.Fatal(err)
	}
}

func TestDestAuditReceiverLossRetryCannotOutliveDedupRetention(t *testing.T) {
	for _, committed := range []bool{false, true} {
		r, b := auditLossFixture(t)
		if committed {
			if err := r.FlushDestinationAuditLoss(t.Context(), b); err != nil {
				t.Fatal(err)
			}
		}
		r.now = func() time.Time { return b.ReceivedAt.Add(48 * time.Hour) }
		if err := r.FlushDestinationAuditLoss(t.Context(), b); err != nil {
			t.Fatal("48h boundary rejected", err)
		}
		// Simulate marker pruning after its 72h retention. An old frozen batch
		// must not be inserted again, including after an ambiguous commit.
		if err := r.db.Where("agent_id = ?", "").Delete(&destAuditBatchRow{}).Error; err != nil {
			t.Fatal(err)
		}
		r.now = func() time.Time { return b.ReceivedAt.Add(73 * time.Hour) }
		if err := r.FlushDestinationAuditLoss(t.Context(), b); !errors.Is(err, domain.ErrDestAuditLossExpired) {
			t.Fatalf("expired flush%v", err)
		}
		var row destAuditLossHourlyRow
		if err := r.db.Where("reason = ?", "queue_full").First(&row).Error; err != nil || row.Rows != 7 {
			t.Fatalf("expired replay%+v %v", row, err)
		}
	}
}

func TestDestAuditReceiverLossBoundsAndSaturation(t *testing.T) {
	r, b := auditLossFixture(t)
	for _, mutate := range []func(*domain.DestAuditLossBatch){
		func(b *domain.DestAuditLossBatch) { b.BatchID = strings.Repeat("g", 32) },
		func(b *domain.DestAuditLossBatch) { b.ReceivedAt = time.Time{} },
		func(b *domain.DestAuditLossBatch) { b.Losses = nil },
		func(b *domain.DestAuditLossBatch) { b.Losses = make([]domain.DestAuditLoss, 201) },
		func(b *domain.DestAuditLossBatch) { b.Losses[0].Rows = 0 },
		func(b *domain.DestAuditLossBatch) { b.Losses[0].PanelID = 0 },
		func(b *domain.DestAuditLossBatch) { b.Losses[0].HourMS++ },
		func(b *domain.DestAuditLossBatch) { b.Losses[0].Reason = "node_dropped" },
		func(b *domain.DestAuditLossBatch) { b.Losses[0].Kind = "unknown" },
		func(b *domain.DestAuditLossBatch) { b.Losses[1] = b.Losses[0] },
	} {
		bad := b
		bad.Losses = append([]domain.DestAuditLoss(nil), b.Losses...)
		mutate(&bad)
		if err := r.FlushDestinationAuditLoss(t.Context(), bad); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("invalid flush%v", err)
		}
	}
	if auditRowCount(t, r.db, &destAuditBatchRow{}) != 0 {
		t.Fatal("invalid loss input reached storage")
	}
	b.Losses[0].Rows = math.MaxInt64
	if err := r.FlushDestinationAuditLoss(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	b.BatchID = "1123456789abcdef0123456789abcdef"
	if err := r.FlushDestinationAuditLoss(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	var row destAuditLossHourlyRow
	if err := r.db.Where("reason = ?", "queue_full").First(&row).Error; err != nil || row.Rows != math.MaxInt64 {
		t.Fatalf("saturation%+v %v", row, err)
	}
}

func TestDestAuditReceiverLossConcurrentRepositoryRetries(t *testing.T) {
	r, b := auditLossFixture(t)
	other := NewDestAuditRepo(r.db)
	other.now = r.now
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			repo := r
			if i%2 != 0 {
				repo = other
			}
			if err := repo.FlushDestinationAuditLoss(t.Context(), b); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var row destAuditLossHourlyRow
	if err := r.db.Where("reason = ?", "queue_full").First(&row).Error; err != nil || row.Rows != 7 {
		t.Fatalf("concurrent flush%+v %v", row, err)
	}
}

func TestDestAuditReceiverLossMySQLFoundRowsConnectionRetriesOnce(t *testing.T) {
	r, b := auditLossFixture(t)
	if r.db.Dialector.Name() != "mysql" {
		t.Skip("requires the actual MySQL dialect job")
	}
	dialect := r.db.Dialector
	if wrapper, ok := dialect.(reusableSchemaDialector); ok {
		dialect = wrapper.Dialector
	}
	config, err := mysqlsql.ParseDSN(dialect.(*mysqldriver.Dialector).Config.DSN)
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
	second.now = r.now
	if err := r.FlushDestinationAuditLoss(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := second.FlushDestinationAuditLoss(t.Context(), b); err != nil {
			t.Fatal(err)
		}
	}
	var row destAuditLossHourlyRow
	if err := second.db.Where("reason = ?", "queue_full").First(&row).Error; err != nil || row.Rows != 7 {
		t.Fatalf("MySQL found rows receiver replay%+v %v", row, err)
	}
}
