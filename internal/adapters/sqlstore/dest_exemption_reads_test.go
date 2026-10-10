package sqlstore

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
)

func TestDestinationExemptionReadsUseBoundedDisplayQueriesWithoutDefinitionBlobs(t *testing.T) {
	r := newDestDefinitionRepo(t)
	rows := make([]userRow, 600)
	for i := range rows {
		rows[i] = *userFromDomain(&domain.User{UPN: fmt.Sprintf("exemption-read-%d@example.test", i), UUID: fmt.Sprintf("exemption-read-uuid-%d", i), SubToken: fmt.Sprintf("exemption-read-sub-%d", i), Role: domain.RoleUser, Enabled: true})
	}
	if err := r.db.CreateInBatches(&rows, 100).Error; err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(1791000000000).UTC()
	ex := domain.DestExemption{UserID: rows[599].ID, CreatedBy: rows[0].ID, Reason: "Display read"}
	if err := r.SaveExemption(t.Context(), &ex, true, now); err != nil {
		t.Fatal(err)
	}
	queries := 0
	problem := errors.New("exemption display accessed private or large columns")
	if err := r.db.Callback().Query().Before("gorm:query").Register("exemption-display-guard", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_lists" {
			tx.AddError(problem)
		}
		if tx.Statement.Table == "users" {
			queries++
			if !slices.Equal(tx.Statement.Selects, []string{"id", "upn"}) {
				tx.AddError(problem)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Query().Remove("exemption-display-guard") })
	ids := make([]int64, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		ids = append(ids, rows[i].ID)
	}
	original := slices.Clone(ids)
	names, err := r.ExemptionUserUPNs(t.Context(), ids)
	if err != nil || len(names) != 600 || queries != 2 || !slices.Equal(ids, original) {
		t.Fatal("display identifiers were not bounded/copy-safe")
	}
	listed, err := r.ListExemptions(t.Context())
	if err != nil || len(listed) != 1 || listed[0].CreatedBy != ex.CreatedBy {
		t.Fatal("narrow exemption list failed")
	}
	got, err := r.GetExemption(t.Context(), ex.UserID)
	if err != nil || got.Reason != ex.Reason {
		t.Fatal("narrow exemption detail failed")
	}
}
