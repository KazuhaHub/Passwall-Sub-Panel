package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	pkglog "github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// connection_history is the one table new code writes an IP address to: one
// row per (account, panel, node, source), merged across the detector's
// samples. These tests pin what its readers and its privacy promise rely on —
// an upsert that keeps the first sighting, counts every sample and forgets no
// column; a key that separates what the admin must be able to tell apart; a
// batch that never touches one row twice (PostgreSQL refuses that); reads
// that hide a deleted account at once; a prune by LAST sighting; an orphan
// purge; and SQL that reaches the log without its bound values.

func newConnHistoryRepo(t *testing.T) (*ConnectionHistoryRepo, ports.UserRepo, *gorm.DB) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return NewConnectionHistoryRepo(db), NewRepos(db).User, db
}

// histConn is a connection of uid on (panel, node) from ip, keyed the way the
// snapshot keys it.
func histConn(uid, panel int64, node, ip string) domain.LiveConnection {
	key, _, _ := domain.SourceKey(ip)
	return domain.LiveConnection{UserID: uid, PanelID: panel, Node: node, SourceKey: key, IP: ip}
}

// histRows reads every stored row, in key order, straight from the table.
func histRows(t *testing.T, db *gorm.DB) []connectionHistoryRow {
	t.Helper()
	var rows []connectionHistoryRow
	if err := db.Order("user_id, panel_id, node, source_key").Find(&rows).Error; err != nil {
		t.Fatalf("read connection_history: %v", err)
	}
	return rows
}

func TestConnectionHistoryRepo_RecordInsertsThenIncrements(t *testing.T) {
	r, users, db := newConnHistoryRepo(t)
	ctx := context.Background()
	u := createRiskUser(t, users, 1, "")
	t1 := time.UnixMilli(1_790_000_000_000)
	c := histConn(u.ID, 1, "node-a", "203.0.113.5")

	if err := r.Record(ctx, []domain.LiveConnection{c}, t1); err != nil {
		t.Fatalf("record 1: %v", err)
	}
	rows := histRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("after one sample: %d rows, want 1", len(rows))
	}
	if g := rows[0]; g.FirstSeenMS != t1.UnixMilli() || g.LastSeenMS != t1.UnixMilli() || g.SeenCount != 1 {
		t.Fatalf("new row: first %d, last %d, count %d; want %d, %d, 1", g.FirstSeenMS, g.LastSeenMS, g.SeenCount, t1.UnixMilli(), t1.UnixMilli())
	}

	// Two more samples: the first sighting stays where it was, the last one
	// and the count move — one per sample, never reset.
	t2, t3 := t1.Add(5*time.Minute), t1.Add(10*time.Minute)
	for _, at := range []time.Time{t2, t3} {
		if err := r.Record(ctx, []domain.LiveConnection{c}, at); err != nil {
			t.Fatalf("record at %v: %v", at, err)
		}
	}
	rows = histRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("after three samples of one source: %d rows, want 1", len(rows))
	}
	if g := rows[0]; g.FirstSeenMS != t1.UnixMilli() || g.LastSeenMS != t3.UnixMilli() || g.SeenCount != 3 {
		t.Fatalf("merged row: first %d, last %d, count %d; want %d, %d, 3", g.FirstSeenMS, g.LastSeenMS, g.SeenCount, t1.UnixMilli(), t3.UnixMilli())
	}
}

// Every mutable column is rewritten by the upsert. One left out of the update
// keeps its FIRST value forever: an address that became a shared exit still
// read as judged, a source placed in Osaka for a month still shown in Tokyo.
//
// Mutation: dropping "city" from the update list turns this red.
func TestConnectionHistoryRepo_RecordUpsertsEveryMutableColumn(t *testing.T) {
	r, users, db := newConnHistoryRepo(t)
	ctx := context.Background()
	u := createRiskUser(t, users, 1, "")
	t1 := time.UnixMilli(1_790_000_000_000)
	t2 := t1.Add(time.Hour)

	// Two members of one IPv6 /64: one source, so one row.
	first := histConn(u.ID, 1, "node-a", "2001:db8:1:2::5")
	first.Place = domain.ConnPlace{CountryCode: "JP", Country: "Japan", Region: "Tokyo", RegionCode: "13", City: "Tokyo"}
	second := histConn(u.ID, 1, "node-a", "2001:db8:1:2::9")
	second.Exclusion = domain.AddressExcludedShared
	second.Place = domain.ConnPlace{CountryCode: "KR", Country: "South Korea", Region: "Seoul", RegionCode: "11", City: "Seoul"}
	if first.SourceKey != second.SourceKey {
		t.Fatalf("fixture: %q and %q must share a source", first.SourceKey, second.SourceKey)
	}

	if err := r.Record(ctx, []domain.LiveConnection{first}, t1); err != nil {
		t.Fatalf("record first: %v", err)
	}
	if err := r.Record(ctx, []domain.LiveConnection{second}, t2); err != nil {
		t.Fatalf("record second: %v", err)
	}
	rows := histRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("%d rows, want 1", len(rows))
	}
	g := rows[0]
	for _, c := range []struct{ col, got, want string }{
		{"ip", g.IP, second.IP},
		{"exclusion", g.Exclusion, second.Exclusion},
		{"country_code", g.CountryCode, second.Place.CountryCode},
		{"country", g.Country, second.Place.Country},
		{"region", g.Region, second.Place.Region},
		{"region_code", g.RegionCode, second.Place.RegionCode},
		{"city", g.City, second.Place.City},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q after the second sample, want %q: the upsert does not rewrite it", c.col, c.got, c.want)
		}
	}
	if g.FirstSeenMS != t1.UnixMilli() || g.LastSeenMS != t2.UnixMilli() || g.SeenCount != 2 {
		t.Errorf("first %d, last %d, count %d; want %d, %d, 2", g.FirstSeenMS, g.LastSeenMS, g.SeenCount, t1.UnixMilli(), t2.UnixMilli())
	}
}

// The key is (account, panel, node, source), each part because the admin
// must be able to tell the difference: two accounts on one exit are the
// shared-exit story, one account on two panels or two nodes is where it
// connected, and a reader with no node layer ("") is its own node.
func TestConnectionHistoryRepo_KeysByUserPanelNodeSource(t *testing.T) {
	r, users, db := newConnHistoryRepo(t)
	ctx := context.Background()
	alice := createRiskUser(t, users, 1, "")
	bob := createRiskUser(t, users, 2, "")
	at := time.UnixMilli(1_790_000_000_000)

	conns := []domain.LiveConnection{
		histConn(alice.ID, 1, "node-a", "203.0.113.5"),
		histConn(bob.ID, 1, "node-a", "203.0.113.5"),   // another account
		histConn(alice.ID, 2, "node-a", "203.0.113.5"), // another panel
		histConn(alice.ID, 1, "node-b", "203.0.113.5"), // another node
		histConn(alice.ID, 1, "", "203.0.113.5"),       // no node layer
		histConn(alice.ID, 1, "node-a", "198.51.100.7"),
	}
	if err := r.Record(ctx, conns, at); err != nil {
		t.Fatalf("record: %v", err)
	}
	rows := histRows(t, db)
	if len(rows) != len(conns) {
		t.Fatalf("%d rows for %d distinct keys: %+v", len(rows), len(conns), rows)
	}
	for _, g := range rows {
		if g.SeenCount != 1 {
			t.Errorf("row %+v: count %d, want 1 — two keys were merged", g, g.SeenCount)
		}
	}
}

// One key twice in one batch is one row with one sample, the LAST value
// winning. PostgreSQL refuses an upsert that touches a row twice; SQLite and
// MySQL would silently count two samples for one poll.
func TestConnectionHistoryRepo_DuplicateKeysInOneBatchCollapse(t *testing.T) {
	r, users, db := newConnHistoryRepo(t)
	ctx := context.Background()
	u := createRiskUser(t, users, 1, "")
	at := time.UnixMilli(1_790_000_000_000)

	a := histConn(u.ID, 1, "node-a", "2001:db8:1:2::5")
	b := histConn(u.ID, 1, "node-a", "2001:db8:1:2::9")
	b.Place.City = "Osaka"
	if err := r.Record(ctx, []domain.LiveConnection{a, b}, at); err != nil {
		t.Fatalf("record a batch naming one key twice: %v", err)
	}
	rows := histRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("%d rows, want 1", len(rows))
	}
	if g := rows[0]; g.SeenCount != 1 || g.IP != b.IP || g.City != "Osaka" {
		t.Fatalf("row = %+v; want one sample carrying the last value (ip %s, city Osaka)", g, b.IP)
	}
}

// Record spans statements: a busy poll records far more than one statement's
// rows, and all of them land.
func TestConnectionHistoryRepo_RecordSpansBatches(t *testing.T) {
	r, users, db := newConnHistoryRepo(t)
	ctx := context.Background()
	u := createRiskUser(t, users, 1, "")
	var conns []domain.LiveConnection
	for i := 0; i < 450; i++ {
		conns = append(conns, histConn(u.ID, 1, "node-a", fmt.Sprintf("10.0.%d.%d", i/250, i%250)))
	}
	if err := r.Record(ctx, conns, time.UnixMilli(1_790_000_000_000)); err != nil {
		t.Fatalf("record: %v", err)
	}
	if got := len(histRows(t, db)); got != 450 {
		t.Fatalf("%d rows, want 450", got)
	}
}

// A value the columns cannot hold on every dialect, or an exclusion reason
// the detector does not have, is refused before anything is written, as
// risk_signals refuses its own: left to the database,
// SQLite stores it, PostgreSQL refuses it and MySQL truncates or refuses it
// by SQL mode. The producer cuts every value to these widths, so this is the
// backstop for a producer that forgets — and the refusal never echoes the
// value, which may be an address and may reach a log.
func TestConnectionHistoryRepo_RecordRefusesWhatTheColumnsCannotHold(t *testing.T) {
	long := strings.Repeat("x", 65)
	for _, c := range []struct {
		name string
		edit func(*domain.LiveConnection)
	}{
		{"an empty source", func(c *domain.LiveConnection) { c.SourceKey = "" }},
		{"a wide source", func(c *domain.LiveConnection) { c.SourceKey = long }},
		{"a wide node", func(c *domain.LiveConnection) { c.Node = long }},
		{"a wide address", func(c *domain.LiveConnection) { c.IP = long }},
		{"a wide exclusion", func(c *domain.LiveConnection) { c.Exclusion = strings.Repeat("e", 17) }},
		// The exclusion is a closed set: the history's filter names the
		// four reasons, and a fifth stored one could never be selected.
		{"an unknown exclusion", func(c *domain.LiveConnection) { c.Exclusion = "vpn" }},
		{"a wide country code", func(c *domain.LiveConnection) { c.Place.CountryCode = "ABCDEFGHI" }},
		{"a wide country", func(c *domain.LiveConnection) { c.Place.Country = long }},
		{"a wide region", func(c *domain.LiveConnection) { c.Place.Region = strings.Repeat("r", 129) }},
		{"a wide region code", func(c *domain.LiveConnection) { c.Place.RegionCode = "ABCDEFGHI" }},
		{"a wide city", func(c *domain.LiveConnection) { c.Place.City = strings.Repeat("c", 129) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, users, db := newConnHistoryRepo(t)
			u := createRiskUser(t, users, 1, "")
			good := histConn(u.ID, 1, "node-a", "203.0.113.5")
			bad := histConn(u.ID, 1, "node-a", "198.51.100.77")
			c.edit(&bad)
			err := r.Record(context.Background(), []domain.LiveConnection{good, bad}, time.UnixMilli(1_790_000_000_000))
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("Record = %v, want ErrValidation", err)
			}
			if strings.Contains(err.Error(), "198.51.100.77") || strings.Contains(err.Error(), long) {
				t.Fatalf("the refusal echoes the value: %v", err)
			}
			if n := len(histRows(t, db)); n != 0 {
				t.Fatalf("%d rows written; a refused batch writes nothing", n)
			}
		})
	}
}

// List is the admin history read: every filter narrows, a deleted account's
// rows are invisible at once (the hourly purge deletes them later), and the
// account's names come from users.
func TestConnectionHistoryRepo_ListFiltersAndHidesOrphans(t *testing.T) {
	r, users, _ := newConnHistoryRepo(t)
	ctx := context.Background()
	alice := createRiskUser(t, users, 1, "Alice")
	bob := createRiskUser(t, users, 2, "Bob")
	gone := createRiskUser(t, users, 3, "Gone")
	base := time.UnixMilli(1_790_000_000_000)

	a1 := histConn(alice.ID, 1, "node-a", "203.0.113.5")
	a1.Place = domain.ConnPlace{CountryCode: "JP", Country: "Japan", Region: "Tokyo", RegionCode: "13", City: "Shinjuku"}
	a2 := histConn(alice.ID, 2, "node-b", "198.51.100.7")
	a2.Exclusion = domain.AddressExcludedInfra
	b1 := histConn(bob.ID, 1, "node-a", "192.0.2.44")
	b1.Exclusion = domain.AddressExcludedShared
	b1.Place = domain.ConnPlace{CountryCode: "US", Country: "United States", Region: "Oregon", RegionCode: "OR", City: "Portland"}
	g1 := histConn(gone.ID, 1, "node-a", "203.0.113.99")
	for i, c := range []domain.LiveConnection{a1, a2, b1, g1} {
		if err := r.Record(ctx, []domain.LiveConnection{c}, base.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	if err := users.Delete(ctx, gone.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	ptr := func(v int64) *int64 { return &v }
	at := func(h int) *time.Time { v := base.Add(time.Duration(h) * time.Hour); return &v }
	for _, c := range []struct {
		name string
		f    ports.ConnectionHistoryFilter
		want []string // IPs, newest first
	}{
		{"everything visible, newest first", ports.ConnectionHistoryFilter{}, []string{b1.IP, a2.IP, a1.IP}},
		{"one account", ports.ConnectionHistoryFilter{UserID: ptr(alice.ID)}, []string{a2.IP, a1.IP}},
		{"a deleted account is nobody", ports.ConnectionHistoryFilter{UserID: ptr(gone.ID)}, nil},
		{"one panel", ports.ConnectionHistoryFilter{PanelID: ptr(1)}, []string{b1.IP, a1.IP}},
		{"judged sources", ports.ConnectionHistoryFilter{Exclusion: ports.ConnExclusionKept}, []string{a1.IP}},
		{"excluded sources", ports.ConnectionHistoryFilter{Exclusion: ports.ConnExclusionExcluded}, []string{b1.IP, a2.IP}},
		{"one reason", ports.ConnectionHistoryFilter{Exclusion: domain.AddressExcludedInfra}, []string{a2.IP}},
		{"last seen since", ports.ConnectionHistoryFilter{Since: at(1)}, []string{b1.IP, a2.IP}},
		{"last seen until", ports.ConnectionHistoryFilter{Until: at(1)}, []string{a2.IP, a1.IP}},
		{"search an address", ports.ConnectionHistoryFilter{Search: "198.51"}, []string{a2.IP}},
		{"search a city, any case", ports.ConnectionHistoryFilter{Search: "shinJUKU"}, []string{a1.IP}},
		{"search a region", ports.ConnectionHistoryFilter{Search: "oregon"}, []string{b1.IP}},
		{"search a country code", ports.ConnectionHistoryFilter{Search: "jp"}, []string{a1.IP}},
		{"search the account", ports.ConnectionHistoryFilter{Search: bob.UPN}, []string{b1.IP}},
		{"search does not reach a deleted account", ports.ConnectionHistoryFilter{Search: "203.0.113.99"}, nil},
		{"a LIKE wildcard is literal", ports.ConnectionHistoryFilter{Search: "203_0"}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, total, err := r.List(ctx, c.f)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			var ips []string
			for _, g := range got {
				ips = append(ips, g.IP)
			}
			if strings.Join(ips, ",") != strings.Join(c.want, ",") {
				t.Fatalf("List = %v, want %v", ips, c.want)
			}
			if total != int64(len(c.want)) {
				t.Fatalf("total = %d, want %d", total, len(c.want))
			}
		})
	}

	// Every column comes back, with the account's names from users.
	got, _, err := r.List(ctx, ports.ConnectionHistoryFilter{UserID: ptr(bob.ID)})
	if err != nil || len(got) != 1 {
		t.Fatalf("List bob = %+v, %v", got, err)
	}
	want := domain.ConnectionRecord{
		UserID: bob.ID, UPN: bob.UPN, DisplayName: "Bob", PanelID: 1, Node: "node-a",
		SourceKey: b1.SourceKey, IP: b1.IP, Exclusion: domain.AddressExcludedShared, Place: b1.Place,
		FirstSeenMS: base.Add(2 * time.Hour).UnixMilli(), LastSeenMS: base.Add(2 * time.Hour).UnixMilli(), Count: 1,
	}
	if got[0] != want {
		t.Fatalf("record = %+v\nwant %+v", got[0], want)
	}

	// An exclusion filter the store does not know is refused rather than
	// read as "matches nothing", which an admin would take for an answer.
	if _, _, err := r.List(ctx, ports.ConnectionHistoryFilter{Exclusion: "banana"}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("List(exclusion banana) = %v, want ErrValidation", err)
	}
}

// Newest last sighting first, pages of the requested size, a total that
// counts every page, and a stable order when sightings tie.
func TestConnectionHistoryRepo_ListPaginatesNewestFirst(t *testing.T) {
	r, users, _ := newConnHistoryRepo(t)
	ctx := context.Background()
	alice := createRiskUser(t, users, 1, "")
	bob := createRiskUser(t, users, 2, "")
	base := time.UnixMilli(1_790_000_000_000)

	// Three distinct sightings, then two that tie on the newest instant: the
	// tie is broken by account, then panel, node and source.
	if err := r.Record(ctx, []domain.LiveConnection{histConn(alice.ID, 1, "n", "192.0.2.1")}, base); err != nil {
		t.Fatal(err)
	}
	if err := r.Record(ctx, []domain.LiveConnection{histConn(alice.ID, 1, "n", "192.0.2.2")}, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := r.Record(ctx, []domain.LiveConnection{histConn(alice.ID, 1, "n", "192.0.2.3")}, base.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := r.Record(ctx, []domain.LiveConnection{
		histConn(bob.ID, 1, "n", "192.0.2.5"),
		histConn(alice.ID, 1, "n", "192.0.2.4"),
	}, base.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}

	var all []string
	for page := 1; page <= 3; page++ {
		got, total, err := r.List(ctx, ports.ConnectionHistoryFilter{Pagination: ports.Pagination{Page: page, PageSize: 2}})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if total != 5 {
			t.Fatalf("page %d: total %d, want 5", page, total)
		}
		for _, g := range got {
			all = append(all, g.IP)
		}
	}
	if want := "192.0.2.4,192.0.2.5,192.0.2.3,192.0.2.2,192.0.2.1"; strings.Join(all, ",") != want {
		t.Fatalf("pages = %v, want %s", all, want)
	}

	// An explicit sort is honoured; an unknown one falls back to the newest
	// sighting rather than reaching ORDER BY.
	got, _, err := r.List(ctx, ports.ConnectionHistoryFilter{Pagination: ports.Pagination{SortBy: "first_seen", SortDir: "asc", PageSize: 1}})
	if err != nil || len(got) != 1 || got[0].IP != "192.0.2.1" {
		t.Fatalf("first_seen asc = %+v, %v; want 192.0.2.1 first", got, err)
	}
	got, _, err = r.List(ctx, ports.ConnectionHistoryFilter{Pagination: ports.Pagination{SortBy: "ip; DROP TABLE users", PageSize: 1}})
	if err != nil || len(got) != 1 || got[0].IP != "192.0.2.4" {
		t.Fatalf("unknown sort = %+v, %v; want the newest sighting first", got, err)
	}
}

// Retention is by LAST sighting: a source first seen a month ago and still
// connecting is current, not old. The cutoff itself is kept.
func TestConnectionHistoryRepo_DeleteBeforeUsesLastSeen(t *testing.T) {
	r, users, db := newConnHistoryRepo(t)
	ctx := context.Background()
	u := createRiskUser(t, users, 1, "")
	now := time.UnixMilli(1_790_000_000_000)
	cutoff := now.Add(-7 * 24 * time.Hour)

	longLived := histConn(u.ID, 1, "n", "192.0.2.1")
	old := histConn(u.ID, 1, "n", "192.0.2.2")
	onTheCutoff := histConn(u.ID, 1, "n", "192.0.2.3")
	if err := r.Record(ctx, []domain.LiveConnection{longLived}, now.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := r.Record(ctx, []domain.LiveConnection{longLived}, now); err != nil {
		t.Fatal(err)
	}
	if err := r.Record(ctx, []domain.LiveConnection{old}, cutoff.Add(-time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := r.Record(ctx, []domain.LiveConnection{onTheCutoff}, cutoff); err != nil {
		t.Fatal(err)
	}

	n, err := r.DeleteBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("DeleteBefore: %v", err)
	}
	if n != 1 {
		t.Fatalf("deleted %d, want 1", n)
	}
	var left []string
	for _, g := range histRows(t, db) {
		left = append(left, g.IP)
	}
	if strings.Join(left, ",") != "192.0.2.1,192.0.2.3" {
		t.Fatalf("left %v, want the long-lived source and the one on the cutoff", left)
	}
}

// A deleted account's addresses must not wait out the retention: the hourly
// purge deletes them, and nothing of an existing account's.
func TestConnectionHistoryRepo_PurgeOrphansDeletesOnlyOrphans(t *testing.T) {
	r, users, db := newConnHistoryRepo(t)
	ctx := context.Background()
	kept := createRiskUser(t, users, 1, "")
	gone := createRiskUser(t, users, 2, "")
	at := time.UnixMilli(1_790_000_000_000)
	if err := r.Record(ctx, []domain.LiveConnection{
		histConn(kept.ID, 1, "n", "192.0.2.1"),
		histConn(gone.ID, 1, "n", "192.0.2.2"),
		histConn(gone.ID, 2, "n", "192.0.2.3"),
	}, at); err != nil {
		t.Fatal(err)
	}
	if err := users.Delete(ctx, gone.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	n, err := r.PurgeOrphans(ctx)
	if err != nil {
		t.Fatalf("PurgeOrphans: %v", err)
	}
	if n != 2 {
		t.Fatalf("purged %d, want 2", n)
	}
	rows := histRows(t, db)
	if len(rows) != 1 || rows[0].UserID != kept.ID {
		t.Fatalf("left %+v, want only the existing account's row", rows)
	}
}

// captureLog runs fn with pkg/log writing into a pipe and returns what it
// wrote. pkg/log binds os.Stdout when its logger is built, so the logger is
// rebuilt around the swap and again after it. The package's tests do not run
// in parallel, and nothing else logs while fn runs.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	out := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(rd)
		out <- string(b)
	}()
	orig := os.Stdout
	func() {
		defer func() {
			os.Stdout = orig
			pkglog.SetLevel(slog.LevelInfo)
		}()
		os.Stdout = wr
		pkglog.SetLevel(slog.LevelInfo)
		fn()
	}()
	_ = wr.Close()
	s := <-out
	_ = rd.Close()
	return s
}

// A failed or slow query is logged with its SQL, and PSP's GORM logger renders
// the bound values into it (conn.go). For this table a bound value is an
// address, and the log is not where an admin-only, age-limited record may
// leak to. Every statement of the store runs on a session whose logger
// withholds the values, so the logged SQL keeps its placeholders.
//
// The failure is forced by dropping the table under the store, which fails
// the insert, the search and the prune the same way on every dialect. The
// same failing insert through the unwrapped handle is the control: it does
// log the address, so the capture would have seen a leak.
//
// Mutation: a ParamsFilter that returns the values turns this red.
func TestConnectionHistoryRepo_FailedQueryLogsNoAddress(t *testing.T) {
	db, err := openIsolatedTestDB(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	r := NewConnectionHistoryRepo(db)
	u := createRiskUser(t, NewRepos(db).User, 1, "")
	const ip = "203.0.113.77"
	c := histConn(u.ID, 1, "node-a", ip)
	ctx := context.Background()
	if err := r.Record(ctx, []domain.LiveConnection{c}, time.UnixMilli(1_790_000_000_000)); err != nil {
		t.Fatalf("record before the failure: %v", err)
	}
	if err := db.Exec("DROP TABLE connection_history").Error; err != nil {
		t.Fatalf("drop: %v", err)
	}

	var recordErr, listErr error
	logged := captureLog(t, func() {
		recordErr = r.Record(ctx, []domain.LiveConnection{c}, time.UnixMilli(1_790_000_300_000))
		_, _, listErr = r.List(ctx, ports.ConnectionHistoryFilter{Search: ip})
		_, _ = r.DeleteBefore(ctx, time.UnixMilli(1_790_000_000_000))
		_, _ = r.PurgeOrphans(ctx)
	})
	if recordErr == nil || listErr == nil {
		t.Fatalf("the forced failure did not fail: record %v, list %v", recordErr, listErr)
	}
	for _, e := range []error{recordErr, listErr} {
		if strings.Contains(e.Error(), ip) {
			t.Errorf("the returned error carries the address: %v", e)
		}
	}
	if n := strings.Count(logged, "database query failed"); n < 4 {
		t.Fatalf("%d failed queries logged, want the insert, the search, the prune and the purge:\n%s", n, logged)
	}
	if !strings.Contains(logged, "connection_history") {
		t.Fatalf("the logged SQL does not name the table:\n%s", logged)
	}
	if strings.Contains(logged, ip) || strings.Contains(logged, "203.0.113") {
		t.Fatalf("an address reached the log:\n%s", logged)
	}
	if !strings.Contains(logged, "?") && !strings.Contains(logged, "$1") {
		t.Fatalf("the logged SQL has no placeholder, so its values went somewhere:\n%s", logged)
	}

	control := captureLog(t, func() {
		_ = db.Exec("INSERT INTO connection_history (ip) VALUES (?)", ip).Error
	})
	if !strings.Contains(control, ip) {
		t.Fatalf("control: the unwrapped handle's failure did not log the value, so the capture proves nothing:\n%s", control)
	}
}

// (guard) Each column is exactly as wide as the value the domain cuts to, so
// a value the producer made always fits and the store's check refuses only
// what no producer should make. Every string is a varchar: no text column,
// so a NOT NULL empty-string default is portable (MySQL refuses a default
// on TEXT).
//
// Mutation: node at size 63 turns this red.
func TestConnectionHistoryRow_WidthsMatchTheDomain(t *testing.T) {
	s, err := schema.Parse(&connectionHistoryRow{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse connectionHistoryRow: %v", err)
	}
	if s.Table != "connection_history" {
		t.Fatalf("table %q", s.Table)
	}
	for column, want := range map[string]int{
		"node":         domain.LiveConnNodeMaxBytes,
		"source_key":   domain.LiveConnKeyMaxBytes,
		"ip":           domain.LiveConnIPMaxBytes,
		"exclusion":    connExclusionWidth,
		"country_code": domain.ConnPlaceCountryCodeMaxBytes,
		"country":      domain.ConnPlaceCountryMaxBytes,
		"region":       domain.ConnPlaceRegionMaxBytes,
		"region_code":  domain.ConnPlaceRegionCodeMaxBytes,
		"city":         domain.ConnPlaceCityMaxBytes,
	} {
		f := s.LookUpField(column)
		if f == nil {
			t.Fatalf("connection_history has no %s column", column)
		}
		if f.Size != want {
			t.Errorf("connection_history.%s is size %d, the domain cuts to %d", column, f.Size, want)
		}
	}
	for _, reason := range []string{domain.AddressExcludedInternal, domain.AddressExcludedListed, domain.AddressExcludedInfra, domain.AddressExcludedShared} {
		if len(reason) > connExclusionWidth {
			t.Errorf("exclusion reason %q does not fit connection_history.exclusion (%d)", reason, connExclusionWidth)
		}
	}
	for _, f := range s.Fields {
		if f.DataType == schema.String && f.Size <= 0 {
			t.Errorf("connection_history.%s is an unsized string (text): give it a width", f.DBName)
		}
	}
	var keys []string
	for _, f := range s.PrimaryFields {
		keys = append(keys, f.DBName)
		if f.AutoIncrement {
			t.Errorf("connection_history.%s is auto-increment; the key is the connection itself", f.DBName)
		}
	}
	if strings.Join(keys, ",") != "user_id,panel_id,node,source_key" {
		t.Errorf("primary key = %v, want user_id, panel_id, node, source_key", keys)
	}
}
