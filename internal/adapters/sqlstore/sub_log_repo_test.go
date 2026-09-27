package sqlstore

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// sub_logs is the panel's highest-write-rate table and the source the hourly
// risk worker aggregates a whole week of. These tests pin the two things that
// read needs from the store: every declared-device column survives the round
// trip, and the window read is exact and streamed however the rows' zones and
// the dialect's time comparison disagree.

func newSubLogRepo(t *testing.T) (ports.SubLogRepo, *gorm.DB) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return NewRepos(db).SubLog, db
}

// scanAll collects one ScanSince pass, recording each callback's size.
func scanAll(t *testing.T, repo ports.SubLogRepo, since time.Time, batch int) (rows []domain.SubLog, sizes []int) {
	t.Helper()
	err := repo.ScanSince(context.Background(), since, batch, func(b []domain.SubLog) error {
		sizes = append(sizes, len(b))
		rows = append(rows, b...) // copies: fn must not retain the slice itself
		return nil
	})
	if err != nil {
		t.Fatalf("ScanSince: %v", err)
	}
	return rows, sizes
}

func TestSubLogRepo_InsertStoresDeviceColumns(t *testing.T) {
	repo, _ := newSubLogRepo(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	// 64 multi-byte runes: the column is size:64 and must hold 64 runes, not
	// 64 bytes, on every dialect (Postgres/MySQL varchar count characters).
	longLabel := strings.Repeat("字", 64)
	in := []*domain.SubLog{
		{UserID: 7, IP: "203.0.113.9", UA: "Happ/3.13.0", ClientType: "mihomo", AccessedAt: now,
			DeviceID: "0123456789abcdef", DeviceLabel: "iOS 17.5 · iPhone15,2"},
		{UserID: 8, IP: "198.51.100.4", UA: "v2rayN/7.0", ClientType: "uri-list", AccessedAt: now,
			DeviceID: "fedcba9876543210", DeviceLabel: longLabel},
		{UserID: 9, IP: "192.0.2.1", UA: "clash", ClientType: "mihomo", AccessedAt: now},
	}
	for _, l := range in {
		if err := repo.Insert(ctx, l); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// batch 0 takes the default size: one callback for three rows.
	got, sizes := scanAll(t, repo, now.Add(-time.Minute), 0)
	if !slices.Equal(sizes, []int{3}) {
		t.Fatalf("callback sizes = %v, want [3]", sizes)
	}
	for i, want := range in {
		g := got[i]
		if g.ID != want.ID || g.UserID != want.UserID || g.IP != want.IP || g.UA != want.UA ||
			g.ClientType != want.ClientType || !g.AccessedAt.Equal(want.AccessedAt) {
			t.Fatalf("row %d = %+v, want %+v", i, g, *want)
		}
		if g.DeviceID != want.DeviceID || g.DeviceLabel != want.DeviceLabel {
			t.Fatalf("row %d device = (%q, %q), want (%q, %q)", i, g.DeviceID, g.DeviceLabel, want.DeviceID, want.DeviceLabel)
		}
	}

	// The admin list reads the same columns (json:"-" on SubLog; an
	// admin-only view copies them out).
	listed, _, err := repo.List(ctx, ports.SubLogFilter{Pagination: ports.Pagination{Page: 1, PageSize: 10, SortBy: "id", SortDir: "asc"}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 3 {
		t.Fatalf("list returned %d rows, want 3", len(listed))
	}
	for i, want := range in {
		if listed[i].DeviceID != want.DeviceID || listed[i].DeviceLabel != want.DeviceLabel {
			t.Fatalf("listed row %d device = (%q, %q), want (%q, %q)",
				i, listed[i].DeviceID, listed[i].DeviceLabel, want.DeviceID, want.DeviceLabel)
		}
	}
}

// A row written before capture existed never had the columns: the ADD COLUMN
// must backfill it with the empty string (NOT NULL with an empty DEFAULT), and
// it must read as "nothing declared" rather than fail the insert or the scan.
func TestSubLogRepo_LegacyRowsReadEmptyDevice(t *testing.T) {
	repo, db := newSubLogRepo(t)
	at := time.Now().Truncate(time.Second)
	if err := db.Exec(
		"INSERT INTO sub_logs (user_id, ip, ua, client_type, accessed_at) VALUES (?, ?, ?, ?, ?)",
		int64(7), "203.0.113.9", "clash", "mihomo", at,
	).Error; err != nil {
		t.Fatalf("insert a row without the device columns: %v", err)
	}
	got, _ := scanAll(t, repo, at.Add(-time.Minute), 0)
	if len(got) != 1 {
		t.Fatalf("scanned %d rows, want 1", len(got))
	}
	if got[0].DeviceID != "" || got[0].DeviceLabel != "" {
		t.Fatalf("legacy row device = (%q, %q), want empty", got[0].DeviceID, got[0].DeviceLabel)
	}
}

// The worker reads a week of fetches; it must never hold them all at once.
// Five rows in the window at batch 2 are three callbacks of at most two rows,
// in id order. The two rows before since come first in id order and sit
// inside the SQL bound's slack, so it is the exact filter that drops them —
// and the batch they fill is dropped whole, never delivered empty.
func TestSubLogRepo_ScanSinceStreamsInIDOrderAndBatches(t *testing.T) {
	repo, _ := newSubLogRepo(t)
	ctx := context.Background()
	since := time.Now().Truncate(time.Second).Add(-time.Hour)
	var want []int64
	for i, at := range []time.Time{
		since.Add(-2 * time.Hour), since.Add(-time.Second), // before since
		since, since.Add(time.Minute), since.Add(2 * time.Minute), since.Add(3 * time.Minute), since.Add(4 * time.Minute),
	} {
		l := &domain.SubLog{UserID: int64(100 + i), IP: "192.0.2.1", UA: "clash", ClientType: "mihomo", AccessedAt: at}
		if err := repo.Insert(ctx, l); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if i >= 2 {
			want = append(want, l.ID)
		}
	}
	// An old row inserted last, outside even the SQL bound's slack: id order
	// is not time order, and the highest id is not automatically in the window.
	if err := repo.Insert(ctx, &domain.SubLog{UserID: 99, AccessedAt: since.Add(-3 * 24 * time.Hour)}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, sizes := scanAll(t, repo, since, 2)
	if !slices.Equal(sizes, []int{2, 2, 1}) {
		t.Fatalf("callback sizes = %v, want [2 2 1]", sizes)
	}
	ids := make([]int64, len(got))
	for i, g := range got {
		ids[i] = g.ID
	}
	if !slices.Equal(ids, want) {
		t.Fatalf("scanned ids = %v, want %v (the rows at or after since, in id order)", ids, want)
	}
}

// sub_logs rows are written with time.Now() in the process zone, and SQLite
// compares times as zone-bearing strings: a naive bound misorders rows whose
// offsets differ, by up to 26 hours across UTC+14..UTC-12. The read must be
// exact to the second whatever zone the rows and since are in.
func TestSubLogRepo_ScanSinceIsExactAcrossZones(t *testing.T) {
	repo, _ := newSubLogRepo(t)
	ctx := context.Background()
	plus14 := time.FixedZone("UTC+14", 14*3600)
	minus12 := time.FixedZone("UTC-12", -12*3600)
	since := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)

	in := []struct {
		at   time.Time
		keep bool
	}{
		{since.Add(-time.Second).In(plus14), false},
		{since.Add(time.Second).In(plus14), true},
		{since.Add(-time.Second).In(minus12), false},
		{since.Add(time.Second).In(minus12), true},
	}
	var want []int64
	for i, r := range in {
		l := &domain.SubLog{UserID: int64(i + 1), AccessedAt: r.at}
		if err := repo.Insert(ctx, l); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if r.keep {
			want = append(want, l.ID)
		}
	}

	for _, z := range []*time.Location{plus14, minus12} {
		got, _ := scanAll(t, repo, since.In(z), 0)
		ids := make([]int64, len(got))
		for i, g := range got {
			ids[i] = g.ID
			if g.AccessedAt.Before(since) {
				t.Fatalf("since in %s: row %d accessed %s is before since %s", z, g.ID, g.AccessedAt, since)
			}
		}
		if !slices.Equal(ids, want) {
			t.Fatalf("since in %s: scanned ids = %v, want %v (only the +1s rows)", z, ids, want)
		}
	}
}

func TestSubLogRepo_ScanSinceStopsOnCallbackError(t *testing.T) {
	repo, _ := newSubLogRepo(t)
	ctx := context.Background()
	since := time.Now().Truncate(time.Second).Add(-time.Hour)
	for i := range 4 {
		if err := repo.Insert(ctx, &domain.SubLog{UserID: int64(i + 1), AccessedAt: since.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	stop := errors.New("stop here")
	calls := 0
	err := repo.ScanSince(ctx, since, 1, func([]domain.SubLog) error {
		calls++
		if calls == 2 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("ScanSince error = %v, want the callback's error", err)
	}
	if calls != 2 {
		t.Fatalf("callback ran %d times, want 2 (the scan stops at the first error)", calls)
	}
}

// The live view infers a connection's device from the fetches of ONE page of
// accounts (RecentForUsers). The read must be exact to the second whatever
// zone the rows were written in — sub_logs rows carry the writer's zone and
// SQLite compares them as strings — and its order must be by instant, not by
// the stored string: across a daylight-saving change the same wall clock
// means two instants an hour apart, and a UTC+14 row's string sorts after a
// UTC-12 row that happened later. It returns only the accounts asked for,
// newest first, at most limit rows, and a caller asking for more than one
// page of accounts is refused rather than served a fleet-wide scan.
func TestSubLogRepo_RecentForUsersIsExactAndBounded(t *testing.T) {
	repo, _ := newSubLogRepo(t)
	ctx := context.Background()
	plus14 := time.FixedZone("UTC+14", 14*3600)
	minus12 := time.FixedZone("UTC-12", -12*3600)
	// Two sides of a daylight-saving change, as a process zone writes them.
	standard := time.FixedZone("EST", -5*3600)
	daylight := time.FixedZone("EDT", -4*3600)
	since := time.Now().UTC().Truncate(time.Second).Add(-3 * time.Hour)

	type row struct {
		uid  int64
		at   time.Time
		keep bool
	}
	// Inserted out of time order on purpose: the id order is not the
	// answer's order either.
	in := []row{
		{7, since.Add(-time.Second).In(plus14), false},
		{7, since.Add(time.Second).In(plus14), true},
		{7, since.Add(-time.Second).In(minus12), false},
		{7, since.Add(2 * time.Second).In(minus12), true},
		{7, since.Add(time.Hour + 10*time.Minute).In(standard), true}, // later instant, earlier-sorting string
		{7, since.Add(time.Hour).In(daylight), true},
		{8, since.Add(30 * time.Minute), true},
		{9, since.Add(40 * time.Minute), false}, // an account nobody asked for
	}
	ids := map[time.Time]int64{}
	for _, r := range in {
		l := &domain.SubLog{UserID: r.uid, IP: "203.0.113.7", UA: "clash", AccessedAt: r.at}
		if err := repo.Insert(ctx, l); err != nil {
			t.Fatalf("insert: %v", err)
		}
		ids[r.at] = l.ID
	}
	var want []int64
	var kept []row
	for _, r := range in {
		if r.keep {
			kept = append(kept, r)
		}
	}
	slices.SortFunc(kept, func(a, b row) int { return b.at.Compare(a.at) })
	for _, r := range kept {
		want = append(want, ids[r.at])
	}

	for _, z := range []*time.Location{time.UTC, plus14, minus12} {
		got, err := repo.RecentForUsers(ctx, []int64{7, 8}, since.In(z), 0)
		if err != nil {
			t.Fatalf("since in %s: %v", z, err)
		}
		gotIDs := make([]int64, len(got))
		for i, g := range got {
			gotIDs[i] = g.ID
			if g.AccessedAt.Before(since) {
				t.Fatalf("since in %s: row %d accessed %s is before since %s", z, g.ID, g.AccessedAt, since)
			}
		}
		if !slices.Equal(gotIDs, want) {
			t.Fatalf("since in %s: ids = %v, want %v (accounts 7 and 8, at or after since, newest instant first)", z, gotIDs, want)
		}
	}

	// At most limit rows: the most recently written ones.
	got, err := repo.RecentForUsers(ctx, []int64{7, 8}, since, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 2 {
		t.Fatalf("limit 2 returned %d rows", len(got))
	}
	for _, g := range got {
		if g.UserID == 9 {
			t.Fatalf("an account nobody asked for was returned: %+v", g)
		}
	}

	// No accounts: nothing, and no query to fail.
	if got, err := repo.RecentForUsers(ctx, nil, since, 0); err != nil || len(got) != 0 {
		t.Fatalf("no accounts = %d rows, %v; want none", len(got), err)
	}
	// More than one page of accounts is a caller's bug, not a query to run.
	many := make([]int64, 201)
	for i := range many {
		many[i] = int64(i + 1)
	}
	if _, err := repo.RecentForUsers(ctx, many, since, 0); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("201 accounts: err = %v, want ErrValidation", err)
	}
}
