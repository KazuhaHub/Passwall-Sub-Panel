package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestDestinationRiskWindowSelectsCurrentOptedInPoliciesAndFrozenBlocks(t *testing.T) {
	r, q, p, u, policy := hitReadFixture(t)
	if err := r.db.Model(&destPolicyRow{}).Where("id = ?", policy).Updates(map[string]any{"counts_as_risk": true, "action": "observe", "enabled": false}).Error; err != nil {
		t.Fatal(err)
	}
	other := destPolicyRow{Name: "Excluded from risk", Action: "block", Scope: "all"}
	if err := r.db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	extra := []destHitRow{
		cleanupHit(p, u, q.Since.UnixMilli(), fmt.Sprintf("p%d", other.ID), "block", "198.51.100.1", 25),
		cleanupHit(p, u, q.Since.Add(-time.Hour).UnixMilli(), fmt.Sprintf("p%d", policy), "block", "old.private.test", 22),
		cleanupHit(p, u, q.Since.UnixMilli(), fmt.Sprintf("P%d", policy), "block", "uppercase.private.test", 443),
		cleanupHit(p, u, q.Since.UnixMilli(), fmt.Sprintf("p%d", policy), "BLOCK", "wrong-action.private.test", 443),
		cleanupHit(p, 999999, q.Since.UnixMilli(), fmt.Sprintf("p%d", policy), "block", "orphan.private.test", 443),
	}
	for i := range extra {
		extra[i].Count = 999
	}
	if err := r.db.Create(&extra).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Create(&pspClientRow{UserID: u, PanelID: p + 20, Email: "risk-client@example.test"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Create(&destAuditLossHourlyRow{PanelID: p + 20, ObservedHourMS: q.Since.UnixMilli(), Kind: "block", Reason: "queue_full", Rows: 17, Events: 19}).Error; err != nil {
		t.Fatal(err)
	}
	recorder := &auditTraceRecorder{Interface: logger.Discard}
	r.db = r.db.Session(&gorm.Session{Logger: recorder})
	got, err := r.ReadDestinationRiskWindow(t.Context(), q.Since, q.Until)
	if err != nil {
		t.Fatal(err)
	}
	row := got.Users[u]
	if len(got.Users) != 1 || !reflect.DeepEqual(row.Sources, []domain.DestBlockSource{{Source: fmt.Sprintf("p%d", policy), Count: 7}}) || !reflect.DeepEqual(row.ClientPanelIDs, []int64{p + 20}) || row.Losses != (domain.DestAuditLosses{Rows: 30, Events: 19, Unmatched: 4, Scope: "panel"}) || len(recorder.queries) != 0 {
		t.Fatalf("risk read scope or privacy incorrect: %+v", got)
	}
	if err := r.db.Model(&destPolicyRow{}).Where("id = ?", policy).Update("counts_as_risk", false).Error; err != nil {
		t.Fatal(err)
	}
	got, err = r.ReadDestinationRiskWindow(t.Context(), q.Since, q.Until)
	if err != nil || len(got.Users[u].Sources) != 0 || got.Users[u].Losses.Rows != 17 {
		t.Fatal("opting out kept historical risk counts or unrelated losses")
	}
	if err := r.db.Delete(&destPolicyRow{}, policy).Error; err != nil {
		t.Fatal(err)
	}
	got, err = r.ReadDestinationRiskWindow(t.Context(), q.Since, q.Until)
	if err != nil || len(got.Users[u].Sources) != 0 {
		t.Fatal("deleted policy remained in risk counts")
	}
}

func TestDestinationRiskWindowBulkProjectionAndSaturation(t *testing.T) {
	r, q, p, u, _ := hitReadFixture(t)
	policies := make([]destPolicyRow, 205)
	for i := range policies {
		policies[i] = destPolicyRow{Name: fmt.Sprintf("Risk bulk %03d", i), CountsAsRisk: true, Action: "block", Scope: "all"}
	}
	if err := r.db.Create(&policies).Error; err != nil {
		t.Fatal(err)
	}
	hits := make([]destHitRow, 0, len(policies)+1)
	for _, policy := range policies {
		hits = append(hits, cleanupHit(p, u, q.Since.UnixMilli(), fmt.Sprintf("p%d", policy.ID), "block", "203.0.113.9", 443))
	}
	hits[0].Count = math.MaxInt64
	hits = append(hits, cleanupHit(p, u, q.Since.Add(time.Hour).UnixMilli(), hits[0].Source, "block", "198.51.100.8", 22))
	if err := r.db.Create(&hits).Error; err != nil {
		t.Fatal(err)
	}
	users := make([]userRow, 120)
	for i := range users {
		users[i] = userRow{UPN: fmt.Sprintf("risk-bulk-%d@example.test", i), UUID: fmt.Sprintf("risk-bulk-%d", i), SubToken: fmt.Sprintf("risk-bulk-token-%d", i)}
	}
	if err := r.db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	clients := make([]pspClientRow, len(users))
	for i, user := range users {
		clients[i] = pspClientRow{UserID: user.ID, PanelID: p, Email: fmt.Sprintf("risk-bulk-%d@example.test", i)}
	}
	if err := r.db.Create(&clients).Error; err != nil {
		t.Fatal(err)
	}
	queries := 0
	if err := r.db.Callback().Row().Before("gorm:row").Register("risk-bulk-projection", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_hits" {
			queries++
			if !reflect.DeepEqual(tx.Statement.Selects, []string{"dest_hits.user_id", "dest_hits.panel_id", "dest_hits.source", "dest_hits.action", "dest_hits.count"}) {
				t.Errorf("risk reader widened its safe projection: %v", tx.Statement.Selects)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Row().Remove("risk-bulk-projection") })
	got, err := r.ReadDestinationRiskWindow(t.Context(), q.Since, q.Until)
	if err != nil || len(got.Users) != 121 || len(got.Users[u].Sources) != 205 || queries != 2 {
		t.Fatalf("bulk read was incomplete or queried per user: users=%d queries=%d error=%v", len(got.Users), queries, err)
	}
	found := false
	for _, source := range got.Users[u].Sources {
		if source.Source == hits[0].Source {
			found = source.Count == math.MaxInt64
		}
	}
	if !found {
		t.Fatal("risk source total overflowed")
	}
	for _, user := range users {
		row := got.Users[user.ID]
		if len(row.Sources) != 0 || !reflect.DeepEqual(row.ClientPanelIDs, []int64{p}) || row.Losses.Rows != 2 || row.Losses.Events != 0 || row.Losses.Unmatched != 4 || row.Losses.Complete {
			t.Fatal("no-hit current clients lost related block coverage")
		}
	}
}

func TestDestinationRiskWindowPartialHoursNeverCountOutsideThe24HourWindow(t *testing.T) {
	r, q, p, u, policy := hitReadFixture(t)
	if err := r.db.Model(&destPolicyRow{}).Where("id = ?", policy).Update("counts_as_risk", true).Error; err != nil {
		t.Fatal(err)
	}
	since, until := q.Since.Add(37*time.Minute), q.Until.Add(37*time.Minute)
	straddlesStart := cleanupHit(p, u, q.Since.UnixMilli(), fmt.Sprintf("p%d", policy), "block", "straddles-start.test", 443)
	straddlesStart.FirstAt, straddlesStart.LastAt, straddlesStart.Count = q.Since.Add(10*time.Minute), q.Since.Add(50*time.Minute), 999
	straddlesEnd := cleanupHit(p, u, q.Until.UnixMilli(), fmt.Sprintf("p%d", policy), "block", "straddles-end.test", 443)
	straddlesEnd.FirstAt, straddlesEnd.LastAt, straddlesEnd.Count = q.Until.Add(5*time.Minute), q.Until.Add(40*time.Minute), 999
	inside := cleanupHit(p, u, q.Until.UnixMilli(), fmt.Sprintf("p%d", policy), "block", "inside-last-hour.test", 443)
	inside.FirstAt, inside.LastAt, inside.Count = q.Until.Add(5*time.Minute), q.Until.Add(30*time.Minute), 13
	if err := r.db.Create(&[]destHitRow{straddlesStart, straddlesEnd, inside}).Error; err != nil {
		t.Fatal(err)
	}
	got, err := r.ReadDestinationRiskWindow(t.Context(), since, until)
	// Six hits in a complete hour, seven previously at the upper boundary,
	// and thirteen known inside the final partial hour. A mixed bucket cannot
	// establish how many of its observations occurred inside the window.
	if err != nil || len(got.Users[u].Sources) != 1 || got.Users[u].Sources[0].Count != 26 || got.Users[u].Losses.Rows != 0 {
		t.Fatal("partial-hour risk counts included observations outside 24 hours")
	}
}

func TestDestinationRiskWindowFailsAtomicallyAndSuppressesPrivateTrace(t *testing.T) {
	r, q, p, u, policy := hitReadFixture(t)
	if err := r.db.Model(&destPolicyRow{}).Where("id = ?", policy).Update("counts_as_risk", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.db.Create(&pspClientRow{UserID: u, PanelID: p, Email: "risk-error@example.test"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, until := range []time.Time{q.Since, q.Since.Add(time.Hour), q.Since.Add(25 * time.Hour)} {
		if _, err := r.ReadDestinationRiskWindow(t.Context(), q.Since, until); !errors.Is(err, domain.ErrValidation) {
			t.Fatal("risk reader accepted a non-24h window")
		}
	}
	recorder := &auditTraceRecorder{Interface: logger.Discard}
	r.db = r.db.Session(&gorm.Session{Logger: recorder})
	if err := r.db.Callback().Row().Before("gorm:row").Register("risk-private-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_audit_loss_hourly" {
			tx.AddError(errors.New("private risk destination secret"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Row().Remove("risk-private-failure") })
	got, err := r.ReadDestinationRiskWindow(t.Context(), q.Since, q.Until)
	if err != errAuditStorage || got.Users != nil || strings.Contains(err.Error(), "private") || len(recorder.queries) != 0 {
		t.Fatal("failed risk read exposed partial data or private SQL")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, err = r.ReadDestinationRiskWindow(ctx, q.Since, q.Until)
	if !errors.Is(err, context.Canceled) || got.Users != nil {
		t.Fatal("cancelled risk read returned partial data")
	}
}

func TestDestinationRiskWindowUsesOneSnapshotAcrossPolicyClientsCountsAndLosses(t *testing.T) {
	r, q, p, u, policy := hitReadFixture(t)
	if r.db.Dialector.Name() == "sqlite" {
		t.Skip("concurrent server snapshot acceptance runs on real MySQL and PostgreSQL")
	}
	if err := r.db.Model(&destPolicyRow{}).Where("id = ?", policy).Update("counts_as_risk", true).Error; err != nil {
		t.Fatal(err)
	}
	pool, err := r.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	oldMax := pool.Stats().MaxOpenConnections
	pool.SetMaxOpenConns(2)
	t.Cleanup(func() { pool.SetMaxOpenConns(oldMax) })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	if err := r.db.Callback().Row().Before("gorm:row").Register("risk-snapshot-barrier", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_audit_loss_hourly" {
			enteredOnce.Do(func() { close(entered) })
			select {
			case <-release:
			case <-ctx.Done():
				tx.AddError(ctx.Err())
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Row().Remove("risk-snapshot-barrier") })
	type result struct {
		window domain.DestRiskWindow
		err    error
	}
	done := make(chan result, 1)
	go func() { window, err := r.ReadDestinationRiskWindow(ctx, q.Since, q.Until); done <- result{window, err} }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("risk snapshot did not reach losses")
	}
	if err := r.privateDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&destPolicyRow{}).Where("id = ?", policy).Update("counts_as_risk", false).Error; err != nil {
			return err
		}
		if err := tx.Model(&destHitRow{}).Where("user_id = ?", u).Update("count", 99).Error; err != nil {
			return err
		}
		if err := tx.Create(&pspClientRow{UserID: u, PanelID: p + 20, Email: "risk-snapshot-new@example.test"}).Error; err != nil {
			return err
		}
		return tx.Create(&destAuditLossHourlyRow{PanelID: p + 20, ObservedHourMS: q.Since.UnixMilli(), Kind: "block", Reason: "queue_full", Rows: 17}).Error
	}); err != nil {
		t.Fatal(err)
	}
	finish()
	select {
	case got := <-done:
		row := got.window.Users[u]
		if got.err != nil || len(row.Sources) != 1 || row.Sources[0].Count != 7 || len(row.ClientPanelIDs) != 0 || row.Losses.Rows != 13 {
			t.Fatal("risk read mixed snapshots")
		}
	case <-ctx.Done():
		t.Fatal("risk snapshot did not complete")
	}
	later, err := r.ReadDestinationRiskWindow(ctx, q.Since, q.Until)
	if err != nil || len(later.Users[u].Sources) != 0 || !reflect.DeepEqual(later.Users[u].ClientPanelIDs, []int64{p + 20}) || later.Users[u].Losses.Rows != 17 {
		t.Fatal("next risk read did not see committed policy/client/loss changes")
	}
}
