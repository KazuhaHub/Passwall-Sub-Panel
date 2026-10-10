package sqlstore

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func hitReadFixture(t *testing.T) (*DestAuditRepo, domain.DestHitQuery, int64, int64, int64) {
	t.Helper()
	r, p, u, g, now := auditCleanupFixture(t)
	policy := destPolicyRow{Name: "Read policy", Action: "block", Scope: "all"}
	if err := r.db.Create(&policy).Error; err != nil {
		t.Fatal(err)
	}
	since := now.Truncate(time.Hour).Add(-24 * time.Hour)
	q := domain.DestHitQuery{Since: since, Until: now.Truncate(time.Hour), Page: 1, PageSize: 50}
	rows := []destHitRow{
		cleanupHit(p, u, since.UnixMilli(), fmt.Sprintf("p%d", policy.ID), "block", "api.example.co.uk", 443),
		cleanupHit(p, u, since.Add(time.Hour).UnixMilli(), fmt.Sprintf("p%d", policy.ID), "observe", "www.example.co.uk", 443),
		cleanupHit(p, u, since.Add(2*time.Hour).UnixMilli(), fmt.Sprintf("g%d", g), "block", "a.foo.github.io", 443),
		cleanupHit(p, 0, since.Add(3*time.Hour).UnixMilli(), fmt.Sprintf("g%d", g), "observe", "foo.github.io", 0),
		cleanupHit(p, 999999, since.Add(4*time.Hour).UnixMilli(), "p999999", "block", "192.0.2.1", 80),
		cleanupHit(p+1, u, since.Add(5*time.Hour).UnixMilli(), fmt.Sprintf("p%d", policy.ID), "block", "literal100%_match!.test", 80),
		cleanupHit(p, u, q.Until.UnixMilli(), fmt.Sprintf("p%d", policy.ID), "block", "at-end.test", 443),
	}
	for i := range rows {
		rows[i].Count = int64(i + 1)
	}
	if err := r.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []destAuditLossHourlyRow{
		{PanelID: p, ObservedHourMS: since.UnixMilli(), Kind: "block", Reason: "queue_full", Rows: 2},
		{PanelID: p, ObservedHourMS: since.UnixMilli(), Kind: "observe", Reason: "node_dropped", Events: 3},
		{PanelID: p, ObservedHourMS: since.UnixMilli(), Kind: "block", Reason: "node_unmatched", Unmatched: 4},
		{PanelID: p, ObservedHourMS: since.UnixMilli(), Kind: "trial", Reason: "queue_full", Rows: 7},
		{PanelID: p, ObservedHourMS: since.UnixMilli(), Kind: "usage", Reason: "queue_full", Rows: 100},
		{PanelID: p + 1, ObservedHourMS: since.UnixMilli(), Kind: "block", Reason: "queue_full", Rows: 11},
		{PanelID: p + 10, ObservedHourMS: since.UnixMilli(), Kind: "block", Reason: "queue_full", Rows: 999},
	} {
		if err := r.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return r, q, p, u, policy.ID
}

func TestDestinationHitReadsFiltersHaveIndependentSummaryAndLossUnits(t *testing.T) {
	r, q, p, u, policy := hitReadFixture(t)
	q.PanelID, q.UserID, q.Source, q.SourceKind, q.Action, q.Keyword = p, u, fmt.Sprintf("p%d", policy), "policy", "observe", "WWW."
	got, err := r.ReadDestinationHits(t.Context(), q)
	if err != nil || got.Total != 1 || len(got.Records) != 1 || got.Records[0].Dest != "www.example.co.uk" || got.Records[0].Count != 2 {
		t.Fatalf("filtered records %v error %v", got, err)
	}
	if got.Summary != (domain.DestHitSummary{Block: 1, Deny: 3, Observe: 2, Users: 1}) {
		t.Fatal("list filters narrowed metric-card scope")
	}
	if got.Losses != (domain.DestAuditLosses{Rows: 2, Events: 3, Unmatched: 4, Scope: "panel"}) || got.DroppedInRange != 2 {
		t.Fatal("loss units or scope changed with account/source filters")
	}
	if len(got.Sources) != 3 {
		t.Fatal("source choices were filtered by the selected rule")
	}
	row := got.Records[0]
	if row.UserUPN == nil || *row.UserUPN != "cleanup@example.test" || row.SourceName == nil || *row.SourceName != "Read policy" || row.PanelName == nil {
		t.Fatal("record display metadata omitted")
	}
	q.Keyword = "does-not-exist.test"
	empty, err := r.ReadDestinationHits(t.Context(), q)
	if err != nil || empty.Total != 0 || len(empty.Records) != 0 || empty.Summary != got.Summary || empty.Losses != got.Losses {
		t.Fatal("empty filtered page erased independent summaries")
	}
}

func TestDestinationHitReadsTrialDeletedSourcesAndLiteralKeyword(t *testing.T) {
	r, q, p, _, _ := hitReadFixture(t)
	got, err := r.ReadDestinationHits(t.Context(), q)
	if err != nil || got.Total != 5 || len(got.Records) != 5 || got.Summary != (domain.DestHitSummary{Block: 12, Deny: 3, Observe: 2, Users: 2}) {
		t.Fatal("default rows included trial or lost frozen deleted-source actions")
	}
	var deleted, missingAccount bool
	for _, row := range got.Records {
		if row.Source == "p999999" {
			deleted = row.SourceName == nil
			missingAccount = row.UserUPN == nil && row.UserID == 999999
		}
	}
	if !deleted || !missingAccount {
		t.Fatal("deleted owners were relabeled or hidden")
	}
	q.IncludeTrial = true
	got, err = r.ReadDestinationHits(t.Context(), q)
	if err != nil || got.Total != 6 || got.Losses.Rows != 1019 || got.Losses.Events != 3 || got.Losses.Unmatched != 4 {
		t.Fatal("trial loss selection included usage or combined units")
	}
	q.PanelID = p
	q.Keyword = "100%_match!"
	got, err = r.ReadDestinationHits(t.Context(), q)
	if err != nil || got.Total != 0 {
		t.Fatal("keyword escaped panel scope")
	}
	q.PanelID = 0
	got, err = r.ReadDestinationHits(t.Context(), q)
	if err != nil || got.Total != 1 || got.Records[0].Dest != "literal100%_match!.test" {
		t.Fatal("keyword LIKE wildcard escaping changed")
	}
}

func TestDestinationHitReadsStablePagingAndAggregation(t *testing.T) {
	r, q, _, _, _ := hitReadFixture(t)
	q.PageSize = 2
	first, err := r.ReadDestinationHits(t.Context(), q)
	q.Page = 2
	second, err2 := r.ReadDestinationHits(t.Context(), q)
	if err != nil || err2 != nil || first.Total != 5 || second.Total != 5 || len(first.Records) != 2 || len(second.Records) != 2 || first.Records[0].Hour <= second.Records[0].Hour {
		t.Fatal("page ordering or totals unstable")
	}
	q.Page = 999
	last, err := r.ReadDestinationHits(t.Context(), q)
	if err != nil || last.Total != 5 || len(last.Records) != 0 || last.Summary != first.Summary {
		t.Fatal("out of range page lost totals")
	}
	q.Page, q.PageSize, q.GroupBy = 1, 50, "site"
	grouped, err := r.ReadDestinationHits(t.Context(), q)
	if err != nil || grouped.Total != 4 || len(grouped.Groups) != 4 {
		t.Fatalf("site groups %v error %v", grouped.Groups, err)
	}
	byKey := map[string]domain.DestHitGroup{}
	for _, g := range grouped.Groups {
		byKey[g.Key] = g
	}
	if byKey["example.co.uk"].Count != 3 || byKey["foo.github.io"].Count != 3 || byKey["(ip)"].Count != 5 || byKey["example.co.uk"].Users != 1 || byKey["example.co.uk"].Sources != 1 {
		t.Fatal("site aggregation ignored public/private suffixes or cardinalities")
	}
	for _, kind := range []string{"user", "policy"} {
		q.GroupBy = kind
		got, err := r.ReadDestinationHits(t.Context(), q)
		if err != nil || got.Total != map[string]int64{"user": 2, "policy": 3}[kind] || len(got.Records) != 0 {
			t.Fatalf("%s grouped read failed", kind)
		}
	}
}

func TestDestinationHitReadsSummaryAndGroupsSaturateExactly(t *testing.T) {
	r, p, u, _, now := auditCleanupFixture(t)
	hour := now.Truncate(time.Hour)
	rows := []destHitRow{cleanupHit(p, u, hour.UnixMilli(), "p1", "block", "one.example.test", 443), cleanupHit(p, u, hour.UnixMilli(), "p1", "block", "two.example.test", 443)}
	rows[0].Count, rows[1].Count = math.MaxInt64-1, 1
	if err := r.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	q := domain.DestHitQuery{Since: hour, Until: now, GroupBy: "site"}
	got, err := r.ReadDestinationHits(t.Context(), q)
	if err != nil || got.Summary.Block != math.MaxInt64 || len(got.Groups) != 1 || got.Groups[0].Count != math.MaxInt64 {
		t.Fatal("exact large counters rounded")
	}
	rows[0].Dest = "three.example.test"
	rows[0].Count = 99
	if err := r.db.Create(&rows[0]).Error; err != nil {
		t.Fatal(err)
	}
	got, err = r.ReadDestinationHits(t.Context(), q)
	if err != nil || got.Summary.Block != math.MaxInt64 || got.Groups[0].Count != math.MaxInt64 {
		t.Fatal("large aggregate overflowed")
	}
}

func TestDestinationHitReadsValidateAndSuppressPrivateSQLFailures(t *testing.T) {
	r, q, _, _, _ := hitReadFixture(t)
	for _, edit := range []func(*domain.DestHitQuery){
		func(q *domain.DestHitQuery) { q.Since = time.Time{} }, func(q *domain.DestHitQuery) { q.Until = q.Since }, func(q *domain.DestHitQuery) { q.Until = q.Since.Add(31*24*time.Hour + time.Millisecond) },
		func(q *domain.DestHitQuery) { q.UserID = -1 }, func(q *domain.DestHitQuery) { q.PanelID = -1 }, func(q *domain.DestHitQuery) { q.Source = "p1x1" }, func(q *domain.DestHitQuery) { q.Source = "p01" },
		func(q *domain.DestHitQuery) { q.SourceKind = "other" }, func(q *domain.DestHitQuery) { q.Action = "allow" }, func(q *domain.DestHitQuery) { q.GroupBy = "dest;DELETE" }, func(q *domain.DestHitQuery) { q.Page = -1 },
		func(q *domain.DestHitQuery) { q.PageSize = 201 }, func(q *domain.DestHitQuery) { q.Page = math.MaxInt; q.PageSize = 200 },
	} {
		bad := q
		edit(&bad)
		if _, err := r.ReadDestinationHits(t.Context(), bad); !errors.Is(err, domain.ErrValidation) {
			t.Fatal("invalid query was broadened or reached storage")
		}
	}
	recorder := &auditTraceRecorder{Interface: logger.Discard}
	r.db = r.db.Session(&gorm.Session{Logger: recorder})
	if err := r.db.Callback().Query().Before("gorm:query").Register("hit_read_private_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "dest_hits" {
			tx.AddError(errors.New("private query and account values"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.db.Callback().Query().Remove("hit_read_private_failure") })
	got, err := r.ReadDestinationHits(t.Context(), q)
	if err != errAuditStorage || !reflect.DeepEqual(got, domain.DestHitPage{}) || len(recorder.queries) != 0 {
		t.Fatal("failed read exposed partial results or private errors")
	}
}
