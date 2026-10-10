package sqlstore

import (
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestDestinationOrphanExemptionCleanupPublishesOnceAndRollsBackOnFailure(t *testing.T) {
	r := newDestDefinitionRepo(t)
	destinationTestUser(t, r, 12)
	now := time.Now().UTC()
	if err := r.db.Create(&[]destExemptionRow{{UserID: 12, Reason: "valid"}, {UserID: 999999, Reason: "private orphan reason"}}).Error; err != nil {
		t.Fatal(err)
	}
	recorder := &auditTraceRecorder{Interface: logger.Discard}
	r.db = r.db.Session(&gorm.Session{Logger: recorder})
	if err := r.db.Callback().Update().Before("gorm:update").Register("orphan_exemption_cleanup_rollback", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_policy_state" {
			tx.AddError(errors.New("private SQL values"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Update().Remove("orphan_exemption_cleanup_rollback") })
	if n, err := r.PruneOrphanedExemptions(t.Context(), now); n != 0 || err != errAuditStorage || len(recorder.queries) != 0 {
		t.Fatal("failed orphan cleanup reported committed rows or leaked SQL")
	}
	_ = r.db.Callback().Update().Remove("orphan_exemption_cleanup_rollback")
	if auditRowCount(t, r.db, &destExemptionRow{}) != 2 {
		t.Fatal("generation failure did not roll back orphan removal")
	}
	if n, err := r.PruneOrphanedExemptions(t.Context(), now); err != nil || n != 1 {
		t.Fatal("orphan cleanup did not delete exactly the missing owner")
	}
	if n, err := r.PruneOrphanedExemptions(t.Context(), now); err != nil || n != 0 {
		t.Fatal("orphan cleanup was not idempotent")
	}
	state, err := r.State(t.Context())
	if err != nil || state.Generation != 1 {
		t.Fatal("orphan removal did not publish one committed definition change")
	}
	if n, err := r.PruneOrphanedExemptions(t.Context(), time.Time{}); n != 0 || !errors.Is(err, domain.ErrValidation) {
		t.Fatal("orphan cleanup accepted a missing clock")
	}
}
