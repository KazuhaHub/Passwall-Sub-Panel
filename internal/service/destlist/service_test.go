package destlist

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestCreateCustom_KeepsThePastedTextAndStoresCanonicalEntries(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	text := "# my list\nExample.COM\n*.example.org\nregexp:tracker\n"
	l, report, err := h.svc.CreateCustom(ctx, "mine", "", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if l.Kind != domain.DestListCustom || l.Side != domain.DestSideDest || l.EntryCount != 3 || l.RegexpCount != 1 || !l.Ready() {
		t.Fatalf("list = %+v", l)
	}
	if report.Accepted != 3 || len(report.Warnings) != 1 || report.Warnings[0].Code != WarnRegexpNoAnchor {
		t.Fatalf("report = %+v", report)
	}
	got, _ := h.repos.Lists.GetByID(ctx, l.ID)
	if string(got.SourceText) != text || string(got.Entries) != "domain:example.com\ndomain:example.org\nregexp:tracker" {
		t.Fatalf("stored text %q entries %q", got.SourceText, got.Entries)
	}
	// The text handed in is copied: later edits to the caller's slice do not
	// reach the row.
	// Bad input creates nothing.
	if _, _, err := h.svc.CreateCustom(ctx, "bad", "", []byte("regexp:(oops")); err == nil {
		t.Fatal("a list with a regexp that does not compile was created")
	}
	if _, _, err := h.svc.CreateCustom(ctx, "  ", "", []byte("example.com")); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("a blank name: %v", err)
	}
	if all, _ := h.svc.Lists(ctx); len(all) != 1 {
		t.Fatalf("%d lists, want only the good one", len(all))
	}
}

func TestUpdateCustom_AdvancesTheGenerationOnlyForChangedEntries(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	l, _, _ := h.svc.CreateCustom(ctx, "mine", "", []byte("example.com"))
	g := h.generation()

	if _, _, err := h.svc.UpdateCustom(ctx, l.ID, "renamed", []byte("# now with a comment\nexample.com")); err != nil {
		t.Fatal(err)
	}
	if h.generation() != g {
		t.Fatal("a rename and a comment moved the generation")
	}
	got, _, err := h.svc.UpdateCustom(ctx, l.ID, "renamed", []byte("example.com\nexample.net"))
	if err != nil || got.EntryCount != 2 || h.generation() != g+1 {
		t.Fatalf("new entries: %+v %v generation %d", got, err, h.generation())
	}
	// Only a custom list has text.
	remote := &domain.DestList{Name: "r", Kind: domain.DestListRemote, SourceURL: "https://example.com/x"}
	if err := h.repos.Lists.Create(ctx, remote); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.UpdateCustom(ctx, remote.ID, "r", []byte("x.example")); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("editing a remote list's text: %v", err)
	}
	if _, _, err := h.svc.UpdateCustom(ctx, 9999, "x", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("editing a missing list: %v", err)
	}
}

// ---- remote lists ----

// remoteHarness serves list bodies by path and counts the requests.
func remoteHarness(t *testing.T) (*harness, map[string]string, *int) {
	t.Helper()
	h := newHarness(t)
	var mu sync.Mutex
	bodies := map[string]string{}
	hits := 0
	// The fake upstream's mux has the geosite routes; give lists their own
	// server so a list URL is a plain https URL.
	srv := newListServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		hits++
		body, ok := bodies[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	})
	h.svc.doer = srv.Client()
	h.up.latest = "" // the geosite probe is not under test here
	bodies["base"] = srv.URL
	return h, bodies, &hits
}

func TestRemoteList_CreateRefreshAndTheDigestGate(t *testing.T) {
	h, bodies, hits := remoteHarness(t)
	ctx := context.Background()
	base := bodies["base"]
	bodies["/a.txt"] = "example.com\nexample.org\n"

	if _, _, err := h.svc.CreateRemote(ctx, "plain http", "http://example.com/a.txt", ""); !errors.Is(err, ErrInsecureURL) {
		t.Fatalf("an http URL: %v", err)
	}
	l, report, err := h.svc.CreateRemote(ctx, "remote", base+"/a.txt", "")
	if err != nil || l.Kind != domain.DestListRemote || l.EntryCount != 2 || report.Accepted != 2 || !l.Ready() {
		t.Fatalf("create: %+v %v", l, err)
	}
	// A list that cannot be fetched is not created.
	if _, _, err := h.svc.CreateRemote(ctx, "missing", base+"/nope.txt", ""); err == nil {
		t.Fatal("a 404 list was created")
	}
	if all, _ := h.svc.Lists(ctx); len(all) != 1 {
		t.Fatalf("%d lists", len(all))
	}

	// A refresh moves the generation only for a list a policy in force uses.
	h.policy("uses it", domain.DestStateOn, domain.DestActionBlock, l.ID)
	g := h.generation()
	// The same set in another spelling and order: the DIGEST is of the
	// canonical entries, so nothing changes and nothing restarts (F13).
	bodies["/a.txt"] = "# reshuffled\nEXAMPLE.ORG\nexample.com.\n"
	out, err := h.svc.RefreshList(ctx, l.ID)
	if err != nil || out.Result != RefreshUnchanged || h.generation() != g {
		t.Fatalf("unchanged refresh: %+v %v generation %d", out, err, h.generation())
	}
	bodies["/a.txt"] = "example.com\nexample.org\nexample.net\n"
	out, err = h.svc.RefreshList(ctx, l.ID)
	if err != nil || out.Result != RefreshUpdated || h.generation() != g+1 {
		t.Fatalf("changed refresh: %+v %v generation %d", out, err, h.generation())
	}
	if got, _ := h.repos.Lists.GetByID(ctx, l.ID); got.EntryCount != 3 || got.LastError != "" {
		t.Fatalf("stored: %+v", got)
	}
	// A list nobody uses is refreshed without moving anything.
	g = h.generation()
	lone, _, err := h.svc.CreateRemote(ctx, "lone", base+"/a.txt", "")
	if err != nil {
		t.Fatal(err)
	}
	bodies["/a.txt"] = "example.com\n"
	if out, err := h.svc.RefreshList(ctx, lone.ID); err != nil || out.Result != RefreshUpdated || h.generation() != g {
		t.Fatalf("refresh of an unused list: %+v %v generation %d -> %d", out, err, g, h.generation())
	}
	_ = hits
}

func TestRemoteList_FailuresKeepTheEntriesAndRecordWhy(t *testing.T) {
	h, bodies, _ := remoteHarness(t)
	ctx := context.Background()
	base := bodies["base"]
	bodies["/a.txt"] = "example.com\nexample.org\n"
	l, _, err := h.svc.CreateRemote(ctx, "remote", base+"/a.txt", "")
	if err != nil {
		t.Fatal(err)
	}
	g := h.generation()
	var observed []string
	h.svc.observe = func(r string) { observed = append(observed, r) }

	for _, c := range []struct {
		name   string
		body   *string
		result string
		reason string
	}{
		{"gone", nil, RefreshFailed, "HTTP 404"},
		{"broad entry", strp("example.com\ndomain:co.uk\n"), RefreshBroad, "broad_entry: domain:co.uk"},
		{"bad regexp", strp("example.com\nregexp:(oops\n"), RefreshFailed, "regexp_invalid"},
		{"too many entries", strp(manyHosts(MaxListEntries + 1)), RefreshFailed, "dest_list_too_large"},
	} {
		if c.body == nil {
			delete(bodies, "/a.txt")
		} else {
			bodies["/a.txt"] = *c.body
		}
		out, err := h.svc.RefreshList(ctx, l.ID)
		if err != nil || out.Result != c.result || !strings.Contains(out.Error, c.reason) {
			t.Fatalf("%s: %+v %v, want %s containing %q", c.name, out, err, c.result, c.reason)
		}
		got, _ := h.repos.Lists.GetByID(ctx, l.ID)
		if string(got.Entries) != "domain:example.com\ndomain:example.org" || !strings.Contains(got.LastError, strings.Split(c.reason, ":")[0]) {
			t.Fatalf("%s: stored entries %q error %q", c.name, got.Entries, got.LastError)
		}
		if h.generation() != g {
			t.Fatalf("%s: a failed refresh moved the generation", c.name)
		}
	}
	if !slices.Equal(observed, []string{RefreshFailed, RefreshBroad, RefreshFailed, RefreshFailed}) {
		t.Fatalf("observed %v", observed)
	}
	// Good again: the error clears.
	bodies["/a.txt"] = "example.com\nexample.org\n"
	if out, _ := h.svc.RefreshList(ctx, l.ID); out.Result != RefreshUnchanged {
		t.Fatalf("recovery: %+v", out)
	}
	if got, _ := h.repos.Lists.GetByID(ctx, l.ID); got.LastError != "" {
		t.Fatalf("the error stayed after a good refresh: %q", got.LastError)
	}
	// Only a remote list refreshes from a URL.
	c, _, _ := h.svc.CreateCustom(ctx, "c", "", []byte("example.net"))
	if _, err := h.svc.RefreshList(ctx, c.ID); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("refreshing a custom list: %v", err)
	}
}

func strp(s string) *string { return &s }

func manyHosts(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "h%d.example.com\n", i)
	}
	return b.String()
}

func TestRefreshDue_RefreshesOnlyRemoteListsOlderThanTheInterval(t *testing.T) {
	h, bodies, hits := remoteHarness(t)
	ctx := context.Background()
	base := bodies["base"]
	bodies["/a.txt"] = "example.com\n"
	l, _, err := h.svc.CreateRemote(ctx, "remote", base+"/a.txt", "")
	if err != nil {
		t.Fatal(err)
	}
	h.svc.CreateCustom(ctx, "custom", "", []byte("example.org"))
	// The harness clock is fixed in 2026-11-13; the list was "fetched" now.
	*hits = 0
	h.svc.RefreshDue(ctx)
	if *hits != 0 {
		t.Fatalf("a list fetched just now was fetched again (%d requests)", *hits)
	}
	h.clock.Advance(23 * time.Hour)
	h.svc.RefreshDue(ctx)
	if *hits != 0 {
		t.Fatal("refreshed before the 24 h default")
	}
	// Shortening the interval takes effect on the next pass, no restart.
	h.settings.set.DestListRefreshHours = 6
	h.svc.RefreshDue(ctx)
	if *hits != 1 {
		t.Fatalf("after shortening to 6 h: %d requests, want 1", *hits)
	}
	// And the failed one is retried on the next pass (it never stamped).
	delete(bodies, "/a.txt")
	h.clock.Advance(7 * time.Hour)
	h.svc.RefreshDue(ctx)
	h.svc.RefreshDue(ctx)
	if *hits != 3 {
		t.Fatalf("a failing list: %d requests, want 3 in all", *hits)
	}
	if got, _ := h.repos.Lists.GetByID(ctx, l.ID); got.LastError == "" {
		t.Fatal("the failure was not recorded")
	}
}

func TestRefreshDue_IsSingleFlight(t *testing.T) {
	h := newHarness(t)
	if !h.svc.refreshMu.TryLock() {
		t.Fatal("the lock is held at rest")
	}
	done := make(chan struct{})
	go func() { h.svc.RefreshDue(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a pass that finds another running blocked instead of returning")
	}
	h.svc.refreshMu.Unlock()
}

// ---- deleting ----

func TestDeleteList_RefusesWhileAPolicyUsesIt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	l, _, _ := h.svc.CreateCustom(ctx, "mine", "", []byte("example.com"))
	p := h.policy("p", domain.DestStateOff, domain.DestActionBlock, l.ID)
	err := h.svc.DeleteList(ctx, l.ID)
	var inUse *ListInUseError
	if !errors.As(err, &inUse) || !errors.Is(err, ErrListInUse) || !errors.Is(err, domain.ErrConflict) || !slices.Equal(inUse.PolicyIDs, []int64{p.ID}) {
		t.Fatalf("err = %v, want it to name policy %d", err, p.ID)
	}
	if err := h.repos.Policies.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.DeleteList(ctx, l.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.DeleteList(ctx, l.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

// ---- expansion and the GeoIP database ----

func TestExpand_ResolvesGeositeAndGeoIPReferences(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	dbPath := buildMMDB(t, t.TempDir(), "geo.mmdb", map[string]string{"1.0.0.0/24": "CN", "1.0.1.0/24": "CN", "5.0.0.0/16": "RU"}, maxmindRecord)
	h.svc = h.newService(&fixedLocator{path: dbPath})
	h.up.release("20261101000000", map[string][]string{"cat": {"domain:g.example", "regexp:^g[0-9]\\.example$"}})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	l, _, err := h.svc.CreateCustom(ctx, "mix", "", []byte("GEOSITE,cat\nGEOIP,CN\n1.0.2.0/24\nfull:www.example.org\ndomain:g.example"))
	if err != nil {
		t.Fatal(err)
	}
	exp, err := h.svc.Expand(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	wantDomains := []string{"domain:g.example", "full:www.example.org", `regexp:^g[0-9]\.example$`}
	if !slices.Equal(exp.Domains, wantDomains) {
		t.Fatalf("domains = %q, want %q", exp.Domains, wantDomains)
	}
	// 1.0.0.0/24 + 1.0.1.0/24 (CN) join to /23; the list's own 1.0.2.0/24 is a
	// sibling-less neighbour and stays.
	if want := []string{"1.0.0.0/23", "1.0.2.0/24"}; !slices.Equal(exp.CIDRs, want) {
		t.Fatalf("cidrs = %q, want %q", exp.CIDRs, want)
	}

	// A reference that cannot be resolved is an ERROR, never a quiet shortfall.
	noGeo := h.newService(nil)
	if _, err := noGeo.Expand(ctx, l); !errors.Is(err, ErrGeoIPUnavailable) {
		t.Fatalf("no GeoIP database: %v", err)
	}
	gone := &domain.DestList{Kind: domain.DestListCustom, Entries: []byte("geosite:absent")}
	if _, err := h.svc.Expand(ctx, gone); !errors.Is(err, ErrGeositeUnavailable) {
		t.Fatalf("an absent category: %v", err)
	}
	empty, err := h.svc.Expand(ctx, &domain.DestList{Kind: domain.DestListCustom})
	if err != nil || len(empty.Domains) != 0 || len(empty.CIDRs) != 0 {
		t.Fatalf("an empty list: %+v %v", empty, err)
	}
}

// The GeoIP digest the policy was reconciled with is PERSISTED, so a database
// that appeared while no rule could use it, or was replaced while the panel was
// down, republishes — and an unused or off rule never does.
func TestRefreshDue_GeoIPDigestIsPersistedAndComparedAgainstThat(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	dir := t.TempDir()
	path := dir + "/geo.mmdb"
	rule := func(state domain.DestState) {
		t.Helper()
		if err := h.repos.SourceRules.Upsert(ctx, &domain.DestSourceRule{NodeID: 1, Countries: []string{"CN"}, State: state}, nil); err != nil {
			t.Fatal(err)
		}
	}

	// A rule that denies a country, and NO database: the rule is skipped.
	rule(domain.DestStateOn)
	loc := &fixedLocator{path: path}
	h.svc = h.newService(loc)
	h.svc.RefreshDue(ctx)
	g := h.generation()

	// The first database appears: the rule now compiles, so republish.
	buildMMDB(t, dir, "geo.mmdb", map[string]string{"1.0.0.0/24": "CN"}, maxmindRecord)
	h.svc.RefreshDue(ctx)
	if h.generation() != g+1 {
		t.Fatalf("no database -> first database under a rule: generation %d, want %d", h.generation(), g+1)
	}
	st, _ := h.repos.State.Get(ctx)
	if len(st.GeoIPSHA256) != 64 {
		t.Fatalf("the digest was not persisted: %q", st.GeoIPSHA256)
	}
	g = h.generation()
	h.svc.RefreshDue(ctx)
	if h.generation() != g {
		t.Fatal("an unchanged database moved the generation")
	}

	// Replaced while the panel was down: a NEW process, with no memory.
	buildMMDB(t, dir, "geo.mmdb", map[string]string{"1.0.0.0/24": "CN", "9.0.0.0/8": "CN"}, maxmindRecord)
	restarted := h.newService(loc)
	restarted.RefreshDue(ctx)
	if h.generation() != g+1 {
		t.Fatalf("a database replaced while down: generation %d, want %d", h.generation(), g+1)
	}

	// A database that is configured but cannot be read right now (a transient
	// error) skips the pass: it must not read as "no database" and republish
	// the policy with every country rule skipped.
	g = h.generation()
	if err := os.Rename(path, path+".away"); err != nil {
		t.Fatal(err)
	}
	restarted.RefreshDue(ctx)
	if h.generation() != g {
		t.Fatal("an unreadable database was treated as no database")
	}
	// The database really is gone (no active path): the rules are skipped,
	// which is a change.
	loc.path = ""
	restarted.RefreshDue(ctx)
	if h.generation() != g+1 {
		t.Fatal("a vanished database under a rule did not republish")
	}
	loc.path = path
	if err := os.Rename(path+".away", path); err != nil {
		t.Fatal(err)
	}

	// A rule that is off (or no rule at all) records the digest and moves nothing.
	rule(domain.DestStateOff)
	g = h.generation()
	buildMMDB(t, dir, "geo.mmdb", map[string]string{"7.0.0.0/8": "CN"}, maxmindRecord)
	restarted.RefreshDue(ctx)
	if h.generation() != g {
		t.Fatal("a rule that is off held the generation moving")
	}
	if st, _ := h.repos.State.Get(ctx); len(st.GeoIPSHA256) != 64 {
		t.Fatal("the digest must still be recorded when nothing uses it")
	}
}

func TestRefreshDue_AListWithACountryInItCountsAsUseOfTheGeoIPDatabase(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	dir := t.TempDir()
	buildMMDB(t, dir, "geo.mmdb", map[string]string{"1.0.0.0/24": "CN"}, maxmindRecord)
	h.svc = h.newService(&fixedLocator{path: dir + "/geo.mmdb"})
	l, _, _ := h.svc.CreateCustom(ctx, "cn", "", []byte("example.com\nGEOIP,CN"))
	pol := h.policy("p", domain.DestStateOn, domain.DestActionBlock, l.ID)
	_ = pol
	h.svc.RefreshDue(ctx)
	g := h.generation()
	buildMMDB(t, dir, "geo.mmdb", map[string]string{"2.0.0.0/8": "CN"}, maxmindRecord)
	h.svc.RefreshDue(ctx)
	if h.generation() != g+1 {
		t.Fatalf("generation %d, want %d", h.generation(), g+1)
	}
}
