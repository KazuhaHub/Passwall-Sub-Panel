package destlist

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// ---- the data format ----

func TestSplitDLCRule_StripsAttributeSuffixesLiterally(t *testing.T) {
	for _, c := range []struct {
		in    string
		entry string
		attrs []string
	}{
		{"domain:example.com", "domain:example.com", nil},
		{"domain:adx.36kr.com:@ads", "domain:adx.36kr.com", []string{"ads"}},
		{"full:apilog.acfun.cn:@ads,@telemetry", "full:apilog.acfun.cn", []string{"ads", "telemetry"}},
		{"domain:airchina.ae:@!cn", "domain:airchina.ae", []string{"!cn"}},
		// the format permits repeated suffixes (F49)
		{"domain:x.example:@a:@b", "domain:x.example", []string{"b", "a"}},
		{`regexp:^r+[0-9]+(---|\.)sn-(2x3|ni5)\w{5}\.googlevideo\.com$:@cn`, `regexp:^r+[0-9]+(---|\.)sn-(2x3|ni5)\w{5}\.googlevideo\.com$`, []string{"cn"}},
		// a ":@" that is not an attribute list stays part of the rule
		{"regexp:^a:@b c$", "regexp:^a:@b c$", nil},
	} {
		entry, attrs := splitDLCRule(c.in)
		if entry != c.entry || !slices.Equal(attrs, c.attrs) {
			t.Errorf("%q -> (%q, %v), want (%q, %v)", c.in, entry, attrs, c.entry, c.attrs)
		}
	}
}

func TestDLCData_DeriveFiltersByEveryListedAttribute(t *testing.T) {
	d, err := parseDLC(dlcYAML(map[string][]string{
		"cat": {
			"domain:plain.example", "domain:cn.example:@cn", "domain:notcn.example:@!cn",
			"full:both.example:@ads,@cn", "domain:ads.example:@ads", "domain:plain.example",
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name  string
		attrs []string
		want  []string
	}{
		{"all", nil, []string{"domain:ads.example", "domain:cn.example", "domain:notcn.example", "domain:plain.example", "full:both.example"}},
		{"cn", []string{"cn"}, []string{"domain:cn.example", "full:both.example"}},
		{"!cn is a different attribute", []string{"!cn"}, []string{"domain:notcn.example"}},
		{"every attribute listed", []string{"ads", "cn"}, []string{"full:both.example"}},
		{"none match", []string{"nope"}, nil},
	} {
		res, ok := d.derive("cat", c.attrs)
		if !ok {
			t.Fatalf("%s: category not found", c.name)
		}
		var got []string
		if len(res.Entries) > 0 {
			got = strings.Split(string(res.Entries), "\n")
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if _, ok := d.derive("absent", nil); ok {
		t.Error("an absent category was found")
	}
	// Names are matched case-insensitively.
	if _, ok := d.derive("CAT", nil); !ok {
		t.Error("the category lookup is case-sensitive")
	}
}

func TestDLCData_CategoryIndexListsLiteralAttributes(t *testing.T) {
	d, _ := parseDLC(dlcYAML(map[string][]string{
		"b": {"domain:1.example:@cn", "domain:2.example:@cn,@ads", "domain:3.example:@!cn", "domain:4.example"},
		"a": {"domain:x.example"},
	}))
	idx := d.categories()
	if len(idx) != 2 || idx[0].Name != "a" || idx[1].Name != "b" || idx[1].Entries != 4 {
		t.Fatalf("index = %+v", idx)
	}
	want := []AttrCount{{"!cn", 1}, {"ads", 1}, {"cn", 2}}
	if !slices.Equal(idx[1].Attrs, want) {
		t.Fatalf("attrs = %+v, want %+v", idx[1].Attrs, want)
	}
	if idx[0].Attrs == nil || len(idx[0].Attrs) != 0 {
		t.Fatalf("a category without attributes must list an empty array: %#v", idx[0].Attrs)
	}
}

func TestParseDLC_RefusesWhatIsNotARelease(t *testing.T) {
	for name, in := range map[string]string{
		"not yaml":      "lists: [unclosed",
		"no categories": "lists: []\n",
		"wrong shape":   "hello: world\n",
		"empty":         "",
	} {
		if _, err := parseDLC([]byte(in)); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: %v, want a validation error", name, err)
		}
	}
}

func TestDiffCategories(t *testing.T) {
	old, _ := parseDLC(dlcYAML(map[string][]string{
		"same": {"domain:a.example"}, "grow": {"domain:a.example"}, "gone": {"domain:g.example"}, "swap": {"domain:old.example"},
	}))
	cur, _ := parseDLC(dlcYAML(map[string][]string{
		"same": {"domain:a.example"}, "grow": {"domain:a.example", "domain:b.example"}, "new": {"domain:n.example"}, "swap": {"domain:new.example"},
	}))
	got := diffCategories(old, cur)
	want := []categoryDiff{
		{Name: "gone", Old: 1, New: 0, Removed: 1, State: "removed"},
		{Name: "grow", Old: 1, New: 2, Added: 1, State: "changed"},
		{Name: "new", Old: 0, New: 1, Added: 1, State: "added"},
		{Name: "swap", Old: 1, New: 1, Added: 1, Removed: 1, State: "changed"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("diff = %+v\nwant %+v", got, want)
	}
	// With nothing before, every category is added.
	if d := diffCategories(nil, cur); len(d) != 4 || d[0].State != "added" {
		t.Fatalf("diff from nothing = %+v", d)
	}
}

// ---- upstream probe and download ----

func TestCheckLatest_ProbesOncePerSixHoursAndSurvivesARestart(t *testing.T) {
	h := newHarness(t)
	h.up.release("20261112220449", map[string][]string{"cat": {"domain:a.example"}})
	ctx := context.Background()

	got, err := h.svc.CheckLatest(ctx, false)
	if err != nil || got.Tag != "20261112220449" || h.up.probes != 1 {
		t.Fatalf("first probe: %+v %v probes %d", got, err, h.up.probes)
	}
	if got.ReleasedAt == nil || !got.ReleasedAt.Equal(time.Date(2026, 11, 12, 22, 4, 49, 0, time.UTC)) {
		t.Fatalf("released_at = %v; the tag is the UTC build time", got.ReleasedAt)
	}
	h.up.latest = "20261113000000"
	h.clock.Advance(5 * time.Hour)
	if got, _ = h.svc.CheckLatest(ctx, false); got.Tag != "20261112220449" || h.up.probes != 1 {
		t.Fatalf("within the throttle: %+v probes %d", got, h.up.probes)
	}
	h.clock.Advance(2 * time.Hour)
	if got, _ = h.svc.CheckLatest(ctx, false); got.Tag != "20261113000000" || h.up.probes != 2 {
		t.Fatalf("after the throttle: %+v probes %d", got, h.up.probes)
	}
	// A restart keeps the answer and its time: no request is spent.
	again := h.newService(nil)
	if got, _ = again.CheckLatest(ctx, false); got.Tag != "20261113000000" || h.up.probes != 2 {
		t.Fatalf("after a restart: %+v probes %d", got, h.up.probes)
	}
	// force bypasses the window.
	if _, _ = again.CheckLatest(ctx, true); h.up.probes != 3 {
		t.Fatalf("force did not probe: %d", h.up.probes)
	}
}

func TestCheckLatest_FailureKeepsTheAnswerAndRecordsTheError(t *testing.T) {
	h := newHarness(t)
	h.up.release("20261112220449", map[string][]string{"cat": {"domain:a.example"}})
	ctx := context.Background()
	if _, err := h.svc.CheckLatest(ctx, false); err != nil {
		t.Fatal(err)
	}
	h.up.latest = "../../etc/passwd" // an unusable tag is a failed probe
	h.clock.Advance(7 * time.Hour)
	got, err := h.svc.CheckLatest(ctx, false)
	if err == nil || got.Tag != "20261112220449" || got.Error == "" {
		t.Fatalf("failed probe: %+v %v", got, err)
	}
	// And it is not retried at once.
	probes := h.up.probes
	if _, _ = h.svc.CheckLatest(ctx, false); h.up.probes != probes {
		t.Fatal("a failed probe was retried inside its backoff")
	}
}

func TestGeositeUpdate_DownloadVerifiesTheChecksumBeforeParsing(t *testing.T) {
	h := newHarness(t)
	h.up.release("20261112220449", map[string][]string{"cat": {"domain:a.example"}})
	h.up.badSum["20261112220449"] = true
	_, err := h.svc.GeositeUpdate(context.Background(), UpdateOptions{})
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("a checksum mismatch was accepted: %v", err)
	}
	if st := h.svc.GeositeStatus(context.Background()); st.Current != nil || st.LastUpdateError == "" {
		t.Fatalf("status after a bad download: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "destlists", currentFile)); err == nil {
		t.Fatal("unverified data was written")
	}
}

func TestParseSha256sum(t *testing.T) {
	good := strings.Repeat("ab", 32)
	if got, err := parseSha256sum([]byte(good + "  dlc.dat_plain.yml\n")); err != nil || got != good {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := parseSha256sum([]byte(good)); err != nil {
		t.Fatalf("a bare digest: %v", err)
	}
	for _, bad := range []string{"", "xyz", good + "  something-else.yml", strings.Repeat("g", 64)} {
		if _, err := parseSha256sum([]byte(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// ---- applying an update ----

func TestGeositeUpdate_KeepsCurrentAndPreviousAndSurvivesARestart(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.up.release("20261101000000", map[string][]string{"cat": {"domain:one.example"}})
	r1, err := h.svc.GeositeUpdate(ctx, UpdateOptions{})
	if err != nil || !r1.Applied || r1.From != nil || r1.To.Tag != "20261101000000" {
		t.Fatalf("first update: %+v %v", r1, err)
	}
	h.up.release("20261102000000", map[string][]string{"cat": {"domain:one.example", "domain:two.example"}})
	r2, err := h.svc.GeositeUpdate(ctx, UpdateOptions{})
	if err != nil || !r2.Applied || r2.From == nil || r2.From.Tag != "20261101000000" {
		t.Fatalf("second update: %+v %v", r2, err)
	}
	h.up.release("20261103000000", map[string][]string{"cat": {"domain:three.example"}})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}

	st := h.svc.GeositeStatus(ctx)
	if st.Current == nil || st.Current.Tag != "20261103000000" || st.Previous == nil || st.Previous.Tag != "20261102000000" {
		t.Fatalf("current/previous = %+v / %+v; exactly the last two are kept", st.Current, st.Previous)
	}
	dir := filepath.Join(h.dir, "destlists")
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{prevFile, currentFile, stateFile}) && !slices.Equal(names, []string{currentFile, prevFile, stateFile}) {
		t.Fatalf("files in %s: %v", dir, names)
	}

	// Restart: the same versions come back from disk, verified.
	again := h.newService(nil)
	st2 := again.GeositeStatus(ctx)
	if st2.Current == nil || st2.Current.Tag != "20261103000000" || st2.Previous == nil || st2.DataError != "" {
		t.Fatalf("after a restart: %+v", st2)
	}
	if d, err := again.DeriveGeosite(ctx, "cat", nil); err != nil || string(d.Entries) != "domain:three.example" {
		t.Fatalf("derive after a restart: %v %v", d, err)
	}
	// The same release again is a no-op.
	same, err := again.GeositeUpdate(ctx, UpdateOptions{Tag: "20261103000000"})
	if err != nil || !same.Unchanged || same.Applied {
		t.Fatalf("re-applying the current release: %+v %v", same, err)
	}
}

func TestGeositeStatus_CurrentFileThatNoLongerMatchesIsNotUsed(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.up.release("20261101000000", map[string][]string{"cat": {"domain:one.example"}})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.dir, "destlists", currentFile), []byte("lists: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	again := h.newService(nil)
	st := again.GeositeStatus(ctx)
	if st.DataError != "geosite_data_corrupt" {
		t.Fatalf("status = %+v, want geosite_data_corrupt", st)
	}
	if _, err := again.DeriveGeosite(ctx, "cat", nil); !errors.Is(err, ErrGeositeUnavailable) {
		t.Fatalf("derive from corrupt data: %v", err)
	}
}

func TestGeositeStatus_VersionAgeAndUpdateAvailable(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.up.release("20261101000000", map[string][]string{"cat": {"domain:one.example"}})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	st := h.svc.GeositeStatus(ctx)
	if st.UpdateAvailable || st.Mode != ports.DestRulesUpdateManual || st.CurrentAgeDays == nil || *st.CurrentAgeDays != 12 {
		t.Fatalf("status = %+v", st)
	}
	h.up.release("20261110000000", map[string][]string{"cat": {"domain:one.example"}})
	h.clock.Advance(7 * time.Hour)
	if _, err := h.svc.CheckLatest(ctx, false); err != nil {
		t.Fatal(err)
	}
	st = h.svc.GeositeStatus(ctx)
	if !st.UpdateAvailable || st.BehindDays == nil || *st.BehindDays != 9 || st.Latest.Tag != "20261110000000" {
		t.Fatalf("with a newer release: %+v", st)
	}
	h.settings.set.DestRulesUpdateMode = "auto"
	if h.svc.GeositeStatus(ctx).Mode != ports.DestRulesUpdateAuto {
		t.Fatal("the mode does not follow the setting")
	}
}

func TestGeositeUpdate_DryRunChangesNothing(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.up.release("20261101000000", map[string][]string{"cat": {"domain:one.example"}})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	list, err := h.svc.CreateGeosite(ctx, "cat", nil)
	if err != nil {
		t.Fatal(err)
	}
	h.policy("p", domain.DestStateOn, domain.DestActionBlock, list.ID)
	h.up.release("20261102000000", map[string][]string{"cat": {"domain:one.example", "domain:two.example"}, "extra": {"domain:x.example"}})

	g0 := h.generation()
	res, err := h.svc.GeositeUpdate(ctx, UpdateOptions{DryRun: true})
	if err != nil || !res.DryRun || res.Applied || res.To.Tag != "20261102000000" {
		t.Fatalf("dry run: %+v %v", res, err)
	}
	want := []CategoryDiff{
		{Name: "cat", Old: 1, New: 2, Added: 1, State: "changed", InUse: true},
		{Name: "extra", Old: 0, New: 1, Added: 1, State: "added"},
	}
	if !slices.Equal(res.Categories, want) {
		t.Fatalf("diff = %+v\nwant %+v", res.Categories, want)
	}
	if st := h.svc.GeositeStatus(ctx); st.Current.Tag != "20261101000000" {
		t.Fatalf("a dry run changed the current release: %+v", st.Current)
	}
	if h.generation() != g0 {
		t.Fatal("a dry run moved the generation")
	}
	if got, _ := h.repos.Lists.GetByID(ctx, list.ID); string(got.Entries) != "domain:one.example" {
		t.Fatalf("a dry run changed a list: %q", got.Entries)
	}
}

// The generation moves only when an update changes a category a policy in
// force uses (F13): a daily release changes dozens of categories nobody
// selected, and each move would restart every node.
func TestGeositeUpdate_MovesTheGenerationOnlyForCategoriesInUse(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.up.release("20261101000000", map[string][]string{
		"used": {"domain:u.example"}, "idle": {"domain:i.example"}, "off": {"domain:o.example"},
	})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	mk := func(cat string) *domain.DestList {
		l, err := h.svc.CreateGeosite(ctx, cat, nil)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	used, idle, off := mk("used"), mk("idle"), mk("off")
	h.policy("on", domain.DestStateOn, domain.DestActionBlock, used.ID)
	h.policy("disabled", domain.DestStateOff, domain.DestActionBlock, off.ID)
	_ = idle

	// Only unused categories change: no move, though the lists were rewritten.
	h.up.release("20261102000000", map[string][]string{
		"used": {"domain:u.example"}, "idle": {"domain:i.example", "domain:i2.example"}, "off": {"domain:o2.example"},
	})
	g := h.generation()
	res, err := h.svc.GeositeUpdate(ctx, UpdateOptions{})
	if err != nil || !res.Applied || res.Bumped || res.ListsChanged != 2 {
		t.Fatalf("update of unused categories: %+v %v", res, err)
	}
	if h.generation() != g {
		t.Fatal("changes to categories no policy in force uses moved the generation")
	}
	if got, _ := h.repos.Lists.GetByID(ctx, idle.ID); !bytes.Contains(got.Entries, []byte("i2.example")) {
		t.Fatalf("an unused list was not brought up to date: %q", got.Entries)
	}

	// The category in use changes: one move.
	h.up.release("20261103000000", map[string][]string{
		"used": {"domain:u.example", "domain:u2.example"}, "idle": {"domain:i.example", "domain:i2.example"}, "off": {"domain:o2.example"},
	})
	res, err = h.svc.GeositeUpdate(ctx, UpdateOptions{})
	if err != nil || !res.Bumped || h.generation() != g+1 {
		t.Fatalf("update of a category in use: %+v %v generation %d want %d", res, err, h.generation(), g+1)
	}
	// And an update that changes nothing it uses leaves it alone again.
	h.up.release("20261104000000", map[string][]string{
		"used": {"domain:u.example", "domain:u2.example"}, "idle": {"domain:other.example"}, "off": {"domain:o2.example"},
	})
	if res, _ = h.svc.GeositeUpdate(ctx, UpdateOptions{}); res.Bumped || h.generation() != g+1 {
		t.Fatalf("a later update of nothing in use bumped: %+v", res)
	}
}

// A custom list that references a category is "in use" of it too: its own
// entries do not change, but what they expand to does.
func TestGeositeUpdate_ACustomListsReferenceCountsAsUse(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.up.release("20261101000000", map[string][]string{"cat": {"domain:a.example"}, "other": {"domain:o.example"}})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	l, _, err := h.svc.CreateCustom(ctx, "mine", domain.DestSideDest, []byte("GEOSITE,cat\nextra.example"))
	if err != nil {
		t.Fatal(err)
	}
	h.policy("p", domain.DestStateOn, domain.DestActionBlock, l.ID)
	g := h.generation()

	h.up.release("20261102000000", map[string][]string{"cat": {"domain:a.example"}, "other": {"domain:o2.example"}})
	if res, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil || res.Bumped || h.generation() != g {
		t.Fatalf("an unrelated category moved it: %+v %v", res, err)
	}
	h.up.release("20261103000000", map[string][]string{"cat": {"domain:a.example", "domain:b.example"}, "other": {"domain:o2.example"}})
	res, err := h.svc.GeositeUpdate(ctx, UpdateOptions{})
	if err != nil || !res.Bumped || h.generation() != g+1 {
		t.Fatalf("a referenced category's change did not move the generation: %+v %v", res, err)
	}
	exp, err := h.svc.Expand(ctx, l)
	if err != nil || !slices.Equal(exp.Domains, []string{"domain:a.example", "domain:b.example", "domain:extra.example"}) {
		t.Fatalf("expansion = %+v %v", exp, err)
	}
}

// An update that would make a list in force broad, empty or too large — or push
// the policy over a limit — is refused whole, and everything stays as it was.
func TestGeositeUpdate_RefusesWhatWouldBreakAPolicyInForce(t *testing.T) {
	cases := map[string]struct {
		next   []string
		reason string
	}{
		"broad entry":      {[]string{"domain:ok.example", "keyword:ab"}, "broad_entry"},
		"category emptied": {nil, "category_gone"},
		"regexp over cap":  {manyRegexps(MaxListRegexps + 1), "dest_list_too_large"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			h.up.release("20261101000000", map[string][]string{"cat": {"domain:ok.example"}})
			if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			l, _ := h.svc.CreateGeosite(ctx, "cat", nil)
			h.policy("p", domain.DestStateOn, domain.DestActionBlock, l.ID)
			g := h.generation()

			h.up.release("20261102000000", map[string][]string{"cat": c.next})
			// The preview says so without changing anything...
			dry, err := h.svc.GeositeUpdate(ctx, UpdateOptions{DryRun: true})
			if err != nil || dry.Rejection == nil || dry.Rejection.Reason != c.reason {
				t.Fatalf("dry run: %+v %v, want rejection %s", dry, err, c.reason)
			}
			// ...and the update is refused.
			_, err = h.svc.GeositeUpdate(ctx, UpdateOptions{})
			var rej *UpdateRejectedError
			if !errors.As(err, &rej) || !errors.Is(err, ErrGeositeUpdateRejected) || !errors.Is(err, domain.ErrConflict) || rej.Reason != c.reason {
				t.Fatalf("err = %v, want a rejection %s", err, c.reason)
			}
			if st := h.svc.GeositeStatus(ctx); st.Current.Tag != "20261101000000" || st.LastUpdateError == "" {
				t.Fatalf("the old data must stay current and the reason be recorded: %+v", st)
			}
			if got, _ := h.repos.Lists.GetByID(ctx, l.ID); string(got.Entries) != "domain:ok.example" {
				t.Fatalf("the list changed: %q", got.Entries)
			}
			if h.generation() != g {
				t.Fatal("a refused update moved the generation")
			}
		})
	}
}

func manyRegexps(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf(`regexp:^a%d\.example$`, i)
	}
	return out
}

// The same problems in a list NO policy uses do not hold the update back: the
// list keeps its entries and says why.
func TestGeositeUpdate_ProblemsInUnusedListsAreRecordedNotFatal(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.up.release("20261101000000", map[string][]string{"gone": {"domain:g.example"}, "fine": {"domain:f.example"}})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	gone, _ := h.svc.CreateGeosite(ctx, "gone", nil)
	fine, _ := h.svc.CreateGeosite(ctx, "fine", nil)
	h.up.release("20261102000000", map[string][]string{"fine": {"domain:f.example", "domain:f2.example"}})
	res, err := h.svc.GeositeUpdate(ctx, UpdateOptions{})
	if err != nil || !res.Applied {
		t.Fatalf("update: %+v %v", res, err)
	}
	if got, _ := h.repos.Lists.GetByID(ctx, gone.ID); got.LastError != "category_gone" || string(got.Entries) != "domain:g.example" {
		t.Fatalf("the unused list whose category vanished: %+v", got)
	}
	if got, _ := h.repos.Lists.GetByID(ctx, fine.ID); got.LastError != "" || !bytes.Contains(got.Entries, []byte("f2.example")) {
		t.Fatalf("the other list: %+v", got)
	}
}

// The policy-wide caps: an update that grows what the policies in force send
// past a limit is refused even though no single list is at fault.
func TestGeositeUpdate_RefusesToPushThePolicyOverALimit(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	big := func(n int, prefix string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("domain:%s%d.example", prefix, i)
		}
		return out
	}
	h.up.release("20261101000000", map[string][]string{"a": big(30000, "a"), "b": big(15000, "b")})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	la, _ := h.svc.CreateGeosite(ctx, "a", nil)
	lb, _ := h.svc.CreateGeosite(ctx, "b", nil)
	h.policy("p", domain.DestStateOn, domain.DestActionBlock, la.ID, lb.ID)

	h.up.release("20261102000000", map[string][]string{"a": big(30000, "a"), "b": big(25000, "b")}) // 55 000 > 50 000
	_, err := h.svc.GeositeUpdate(ctx, UpdateOptions{})
	var rej *UpdateRejectedError
	if !errors.As(err, &rej) || rej.Reason != "policy_over_limit" || rej.Kind != "domains" || rej.Used != 55000 || rej.Limit != 50000 {
		t.Fatalf("err = %v (%+v)", err, rej)
	}
}

func TestGeositeRollback(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.GeositeRollback(ctx, false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("rollback with nothing to go back to: %v", err)
	}
	h.up.release("20261101000000", map[string][]string{"cat": {"domain:v1.example"}})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.GeositeRollback(ctx, false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("rollback with one release: %v", err)
	}
	l, _ := h.svc.CreateGeosite(ctx, "cat", nil)
	h.policy("p", domain.DestStateOn, domain.DestActionBlock, l.ID)
	h.up.release("20261102000000", map[string][]string{"cat": {"domain:v2.example"}})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	g := h.generation()

	dry, err := h.svc.GeositeRollback(ctx, true)
	if err != nil || !dry.DryRun || dry.To.Tag != "20261101000000" || len(dry.Categories) != 1 {
		t.Fatalf("rollback preview: %+v %v", dry, err)
	}
	if st := h.svc.GeositeStatus(ctx); st.Current.Tag != "20261102000000" {
		t.Fatal("a rollback preview moved the data")
	}

	res, err := h.svc.GeositeRollback(ctx, false)
	if err != nil || !res.Applied || !res.Bumped || res.To.Tag != "20261101000000" {
		t.Fatalf("rollback: %+v %v", res, err)
	}
	st := h.svc.GeositeStatus(ctx)
	if st.Current.Tag != "20261101000000" || st.Previous == nil || st.Previous.Tag != "20261102000000" {
		t.Fatalf("after the rollback current/previous = %+v / %+v", st.Current, st.Previous)
	}
	if got, _ := h.repos.Lists.GetByID(ctx, l.ID); string(got.Entries) != "domain:v1.example" {
		t.Fatalf("the list did not go back: %q", got.Entries)
	}
	if h.generation() != g+1 {
		t.Fatal("a rollback that changes a policy in force is a publish")
	}
	// And a restart reads the rolled-back files.
	if again := h.newService(nil); again.GeositeStatus(ctx).Current.Tag != "20261101000000" {
		t.Fatal("the rollback did not survive a restart")
	}
	// A second rollback undoes the first.
	if res, err := h.svc.GeositeRollback(ctx, false); err != nil || res.To.Tag != "20261102000000" {
		t.Fatalf("second rollback: %+v %v", res, err)
	}
}

// ---- geosite lists ----

func TestCreateGeosite_ValidatesTheSelectionAndReusesTheIdenticalList(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.svc.CreateGeosite(ctx, "cat", nil); !errors.Is(err, ErrGeositeUnavailable) {
		t.Fatalf("with no data: %v", err)
	}
	h.up.release("20261101000000", map[string][]string{
		"cat":  {"domain:a.example:@cn", "domain:b.example"},
		"bad":  {"domain:com", "domain:ok.example", "keyword:ab"},
		"huge": manyRegexps(MaxListRegexps + 1),
	})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	l, err := h.svc.CreateGeosite(ctx, "CAT", []string{"cn"})
	if err != nil || l.Name != "cat@cn" || l.GeositeAttrs != "cn" || string(l.Entries) != "domain:a.example" || l.LastFetchedAt == nil || !l.Ready() {
		t.Fatalf("created = %+v %v", l, err)
	}
	same, err := h.svc.CreateGeosite(ctx, "cat", []string{"cn"})
	if err != nil || same.ID != l.ID {
		t.Fatalf("an identical selection created a second list: %+v %v", same, err)
	}
	all, _ := h.svc.CreateGeosite(ctx, "cat", nil)
	if all.ID == l.ID {
		t.Fatal("different attrs must be a different list")
	}
	var big *TooLargeError
	for name, c := range map[string]struct {
		cat   string
		attrs []string
		want  error
	}{
		"unknown category": {"absent", nil, ErrGeositeUnavailable},
		"no entries match": {"cat", []string{"zzz"}, domain.ErrValidation},
		"too many regexps": {"huge", nil, ErrListTooLarge},
	} {
		if _, err := h.svc.CreateGeosite(ctx, c.cat, c.attrs); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	_ = big
	// A suffix is NOT broad inside a category (see broadReasonFor): domain:com
	// in "bad" is kept; the short keyword is the thing refused.
	if _, err := h.svc.CreateGeosite(ctx, "bad", nil); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("a category with a short keyword: %v", err)
	}
}

func TestDeriveGeosite_AllowsPublicSuffixEntriesInACategory(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	// Brand TLDs and registries are what category-finance and
	// category-education-cn are made of; failing on them would make the catalog
	// unusable (measured on a real release: 1893 entries in 137 categories).
	h.up.release("20261101000000", map[string][]string{"fin": {"domain:hsbc", "domain:edu.cn", "domain:pages.dev", "domain:bank.example"}})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	spec, err := h.svc.GeositeListSpec(ctx, "fin", nil)
	if err != nil || spec.EntryCount != 4 {
		t.Fatalf("spec = %+v %v", spec, err)
	}
	// The same entries typed into a custom list ARE dropped: there it is a typo.
	p, _ := ParseCustom([]byte("domain:hsbc\ndomain:bank.example"), domain.DestSideDest)
	if p.EntryCount != 1 {
		t.Fatalf("custom list kept a public suffix: %q", p.Entries)
	}
	// And the stored-list probe knows the difference.
	if got := BroadEntriesOf(&domain.DestList{Kind: domain.DestListGeosite, Entries: spec.Entries}); len(got) != 0 {
		t.Fatalf("a geosite list's suffixes read as broad: %v", got)
	}
	if got := BroadEntriesOf(&domain.DestList{Kind: domain.DestListCustom, Entries: spec.Entries}); len(got) != 2 {
		t.Fatalf("a custom list's ICANN suffixes must read as broad (pages.dev is a private suffix): %v", got)
	}
}

// ---- the loop pass ----

func TestRefreshDue_AutoModeAppliesANewReleaseManualDoesNot(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.up.release("20261101000000", map[string][]string{"cat": {"domain:one.example"}})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	h.up.release("20261102000000", map[string][]string{"cat": {"domain:one.example", "domain:two.example"}})
	h.clock.Advance(7 * time.Hour)

	h.svc.RefreshDue(ctx) // manual (the default)
	st := h.svc.GeositeStatus(ctx)
	if st.Current.Tag != "20261101000000" || !st.UpdateAvailable {
		t.Fatalf("manual mode applied or missed the release: %+v", st)
	}

	h.settings.set.DestRulesUpdateMode = "auto"
	h.svc.RefreshDue(ctx)
	if st := h.svc.GeositeStatus(ctx); st.Current.Tag != "20261102000000" || st.UpdateAvailable {
		t.Fatalf("auto mode did not apply: %+v", st)
	}
}

func TestRefreshDue_AutoModeDoesNotRetryAFailedTagUntilTheWindowPasses(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.up.release("20261101000000", map[string][]string{"cat": {"domain:ok.example"}})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	l, _ := h.svc.CreateGeosite(ctx, "cat", nil)
	h.policy("p", domain.DestStateOn, domain.DestActionBlock, l.ID)
	h.settings.set.DestRulesUpdateMode = "auto"

	h.up.release("20261102000000", map[string][]string{"cat": {"keyword:ab"}}) // refused
	h.clock.Advance(7 * time.Hour)
	h.svc.RefreshDue(ctx)
	if st := h.svc.GeositeStatus(ctx); st.Current.Tag != "20261101000000" || st.LastUpdateError == "" {
		t.Fatalf("the refused release was applied or unrecorded: %+v", st)
	}
	downs := h.up.downs
	h.clock.Advance(time.Hour)
	h.svc.RefreshDue(ctx)
	if h.up.downs != downs {
		t.Fatal("a refused release was downloaded again within the window")
	}
	// A newer, good release is tried at once.
	h.up.release("20261103000000", map[string][]string{"cat": {"domain:ok.example", "domain:more.example"}})
	h.clock.Advance(7 * time.Hour)
	h.svc.RefreshDue(ctx)
	if st := h.svc.GeositeStatus(ctx); st.Current.Tag != "20261103000000" || st.LastUpdateError != "" {
		t.Fatalf("the newer release: %+v", st)
	}
}

func TestRefreshInterval_FollowsTheSettingEachCall(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if got := h.svc.RefreshInterval(ctx); got != 24*time.Hour {
		t.Fatalf("default = %v", got)
	}
	h.settings.set.DestListRefreshHours = 6
	if got := h.svc.RefreshInterval(ctx); got != 6*time.Hour {
		t.Fatalf("after the setting changed: %v", got)
	}
	h.settings.set.DestListRefreshHours = 1
	if got := h.svc.RefreshInterval(ctx); got != 6*time.Hour {
		t.Fatalf("clamped: %v", got)
	}
}

// A panel that never used a geosite list does not phone home: the refresh pass
// probes the upstream only once geosite data or a geosite list exists.
func TestRefreshDue_DoesNotProbeTheUpstreamUntilGeositeIsInUse(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.up.release("20261101000000", map[string][]string{"cat": {"domain:a.example"}})
	h.svc.RefreshDue(ctx)
	if h.up.probes != 0 {
		t.Fatalf("an unused feature probed GitHub %d time(s)", h.up.probes)
	}
	// An admin's own update probes for itself, and from then on the pass does.
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	probes := h.up.probes
	h.clock.Advance(7 * time.Hour)
	h.svc.RefreshDue(ctx)
	if h.up.probes != probes+1 {
		t.Fatalf("with geosite data present the pass did not probe (%d -> %d)", probes, h.up.probes)
	}
}

// ---- review fixes ----

// failingLists lets a test intervene around the one write that matters.
type failingLists struct {
	ListStore
	before func()
	fail   error
}

func (f *failingLists) ApplyBatch(ctx context.Context, b domain.DestListBatch) ([]int64, bool, error) {
	if f.before != nil {
		f.before()
	}
	if f.fail != nil {
		return nil, false, f.fail
	}
	return f.ListStore.ApplyBatch(ctx, b)
}

func (h *harness) withLists(wrap func(ListStore) ListStore) *Service {
	s := New(Deps{
		Lists: wrap(h.repos.Lists), Policies: h.repos.Policies, SourceRules: h.repos.SourceRules, State: h.repos.State,
		Settings: h.settings, DataDir: h.dir, HTTP: h.up.srv.Client(), Now: h.clock.Now,
	})
	s.geo.apiURL = h.up.srv.URL + "/api/latest"
	s.geo.downloadBase = h.up.srv.URL + "/dl"
	return s
}

func readCurrent(t *testing.T, h *harness) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.dir, "destlists", currentFile))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The files are swapped in BEFORE the lists commit, and put back if the commit
// fails: a failed commit leaves the old release current, the old file on disk
// and no stray files — not new data on disk under lists that still hold the old.
func TestGeositeUpdate_ACommitFailureRestoresTheFiles(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.up.release("20261101000000", map[string][]string{"cat": {"domain:v1.example"}})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	h.up.release("20261102000000", map[string][]string{"cat": {"domain:v2.example"}})
	failing := &failingLists{fail: errors.New("commit failed")}
	svc := h.withLists(func(l ListStore) ListStore { failing.ListStore = l; return failing })

	before := readCurrent(t, h)
	if _, err := svc.GeositeUpdate(ctx, UpdateOptions{}); err == nil {
		t.Fatal("the failed commit was not reported")
	}
	if got := readCurrent(t, h); got != before {
		t.Fatal("the current file changed although the commit failed")
	}
	if st := svc.GeositeStatus(ctx); st.Current.Tag != "20261101000000" || st.Previous != nil {
		t.Fatalf("status after a failed commit: %+v / %+v", st.Current, st.Previous)
	}
	entries, _ := os.ReadDir(filepath.Join(h.dir, "destlists"))
	for _, e := range entries {
		if e.Name() != currentFile && e.Name() != stateFile {
			t.Fatalf("a stray file %q was left behind", e.Name())
		}
	}
	// And the same failure during a rollback.
	failing.fail = nil
	if _, err := svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	failing.fail = errors.New("commit failed")
	before = readCurrent(t, h)
	if _, err := svc.GeositeRollback(ctx, false); err == nil {
		t.Fatal("the failed rollback commit was not reported")
	}
	if got := readCurrent(t, h); got != before {
		t.Fatal("a failed rollback changed the current file")
	}
	if st := svc.GeositeStatus(ctx); st.Current.Tag != "20261102000000" || st.Previous.Tag != "20261101000000" {
		t.Fatalf("status after a failed rollback: %+v / %+v", st.Current, st.Previous)
	}
}

// The guard is judged INSIDE the write: a policy turned on between the preview
// and the commit that makes a list unusable is caught, not committed.
func TestGeositeUpdate_TheGuardIsReCheckedInsideTheWrite(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.up.release("20261101000000", map[string][]string{"cat": {"domain:ok.example"}})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	l, _ := h.svc.CreateGeosite(ctx, "cat", nil)
	pol := h.policy("p", domain.DestStateOff, domain.DestActionBlock, l.ID) // unused at preview time
	h.up.release("20261102000000", map[string][]string{"cat": {"keyword:ab"}})
	svc := h.withLists(func(inner ListStore) ListStore {
		return &failingLists{ListStore: inner, before: func() {
			cur, _ := h.repos.Policies.GetByID(ctx, pol.ID)
			cur.State = domain.DestStateOn
			if err := h.repos.Policies.Update(ctx, cur, nil); err != nil {
				t.Fatal(err)
			}
		}}
	})
	before := readCurrent(t, h)
	_, err := svc.GeositeUpdate(ctx, UpdateOptions{})
	var rej *UpdateRejectedError
	if !errors.As(err, &rej) || rej.Reason != "broad_entry" {
		t.Fatalf("err = %v, want a broad_entry rejection from the in-transaction check", err)
	}
	if readCurrent(t, h) != before {
		t.Fatal("the files changed although the write was refused")
	}
	if got, _ := h.repos.Lists.GetByID(ctx, l.ID); string(got.Entries) != "domain:ok.example" {
		t.Fatalf("the list changed: %q", got.Entries)
	}
}

// Creating a geosite list must not interleave with an update: the holder of
// the geosite lock excludes the update, and a caller that finds an update
// running gets ErrGeositeBusy at once rather than queueing behind a download.
func TestGeositeLock_ExcludesUpdateAndRollbackAndNeverQueues(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.up.release("20261101000000", map[string][]string{"cat": {"domain:a.example"}})
	unlock, err := h.svc.LockGeosite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); !errors.Is(err, ErrGeositeBusy) || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("update while locked: %v", err)
	}
	if _, err := h.svc.GeositeRollback(ctx, false); !errors.Is(err, ErrGeositeBusy) {
		t.Fatalf("rollback while locked: %v", err)
	}
	if _, err := h.svc.LockGeosite(ctx); !errors.Is(err, ErrGeositeBusy) {
		t.Fatalf("a second holder must not queue: %v", err)
	}
	if _, err := h.svc.CreateGeosite(ctx, "cat", nil); !errors.Is(err, ErrGeositeBusy) {
		t.Fatalf("CreateGeosite during an update: %v", err)
	}
	unlock()
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatalf("update after unlock: %v", err)
	}
}

// ---- interrupted swaps ----

type flakyState struct {
	StateStore
	failGet bool
}

func (f *flakyState) Get(ctx context.Context) (*domain.DestPolicyState, error) {
	if f.failGet {
		return nil, errors.New("database unreachable")
	}
	return f.StateStore.Get(ctx)
}

// interrupted runs one operation in a process whose ListStore commits (or not)
// and then reports an error, and whose database cannot be asked afterwards: the
// state a crash leaves, with the files swapped and the pending record written.
func (h *harness) interrupted(t *testing.T, commits bool, op func(*Service) error) (*Service, *flakyState) {
	t.Helper()
	wrap := &failingLists{fail: errors.New("connection lost")}
	flaky := &flakyState{StateStore: h.repos.State}
	s := New(Deps{
		Lists: nil, Policies: h.repos.Policies, SourceRules: h.repos.SourceRules, State: flaky,
		Settings: h.settings, DataDir: h.dir, HTTP: h.up.srv.Client(), Now: h.clock.Now,
	})
	s.geo.apiURL, s.geo.downloadBase = h.up.srv.URL+"/api/latest", h.up.srv.URL+"/dl"
	if commits {
		s.lists = &commitThenFail{ListStore: h.repos.Lists, after: func() { flaky.failGet = true }}
	} else {
		wrap.ListStore = h.repos.Lists
		wrap.before = func() { flaky.failGet = true }
		s.lists = wrap
	}
	if err := op(s); err == nil {
		t.Fatal("the interrupted operation reported success")
	}
	return s, flaky
}

// commitThenFail commits the batch and then reports an error, with the
// database unreachable afterwards: a COMMIT whose answer was lost.
type commitThenFail struct {
	ListStore
	after func()
}

func (c *commitThenFail) ApplyBatch(ctx context.Context, b domain.DestListBatch) ([]int64, bool, error) {
	if _, _, err := c.ListStore.ApplyBatch(ctx, b); err != nil {
		return nil, false, err
	}
	c.after()
	return nil, false, errors.New("connection lost after commit")
}

func tagsOf(st GeositeStatus) (cur, prev string) {
	if st.Current != nil {
		cur = st.Current.Tag
	}
	if st.Previous != nil {
		prev = st.Previous.Tag
	}
	return
}

func TestGeositeHeal_AnUpdateThatCommittedIsCompleted(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.up.release("20261101000000", map[string][]string{"cat": {"domain:v1.example"}})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	l, _ := h.svc.CreateGeosite(ctx, "cat", nil)
	h.up.release("20261102000000", map[string][]string{"cat": {"domain:v2.example"}})
	h.interrupted(t, true, func(s *Service) error { _, err := s.GeositeUpdate(ctx, UpdateOptions{}); return err })

	restarted := h.newService(nil)
	st := restarted.GeositeStatus(ctx)
	if cur, prev := tagsOf(st); cur != "20261102000000" || prev != "20261101000000" || st.DataError != "" {
		t.Fatalf("after healing a committed update: %+v", st)
	}
	if d, err := restarted.DeriveGeosite(ctx, "cat", nil); err != nil || string(d.Entries) != "domain:v2.example" {
		t.Fatalf("derive = %v %v", d, err)
	}
	if got, _ := h.repos.Lists.GetByID(ctx, l.ID); string(got.Entries) != "domain:v2.example" {
		t.Fatalf("the list: %q", got.Entries)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "destlists", currentFile+rollbackSuffix)); err == nil {
		t.Fatal("the aside file was left")
	}
}

func TestGeositeHeal_AnUpdateThatDidNotCommitIsUndone(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.up.release("20261101000000", map[string][]string{"cat": {"domain:v1.example"}})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	h.up.release("20261102000000", map[string][]string{"cat": {"domain:v2.example"}})
	h.interrupted(t, false, func(s *Service) error { _, err := s.GeositeUpdate(ctx, UpdateOptions{}); return err })

	restarted := h.newService(nil)
	st := restarted.GeositeStatus(ctx)
	if cur, prev := tagsOf(st); cur != "20261101000000" || prev != "" || st.DataError != "" {
		t.Fatalf("after healing an uncommitted update: %+v", st)
	}
	if d, err := restarted.DeriveGeosite(ctx, "cat", nil); err != nil || string(d.Entries) != "domain:v1.example" {
		t.Fatalf("derive = %v %v", d, err)
	}
}

// A rollback's file moves are undone move for move: the previous release is
// not destroyed by the heal, whichever way the commit went.
func TestGeositeHeal_ARollbackNeverLosesThePreviousRelease(t *testing.T) {
	for _, commits := range []bool{false, true} {
		h := newHarness(t)
		ctx := context.Background()
		h.up.release("20261101000000", map[string][]string{"cat": {"domain:v1.example"}})
		if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
		_, _ = h.svc.CreateGeosite(ctx, "cat", nil)
		h.up.release("20261102000000", map[string][]string{"cat": {"domain:v2.example"}})
		if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
		h.interrupted(t, commits, func(s *Service) error { _, err := s.GeositeRollback(ctx, false); return err })

		restarted := h.newService(nil)
		st := restarted.GeositeStatus(ctx)
		cur, prev := tagsOf(st)
		wantCur, wantPrev, wantEntry := "20261102000000", "20261101000000", "domain:v2.example"
		if commits {
			wantCur, wantPrev, wantEntry = "20261101000000", "20261102000000", "domain:v1.example"
		}
		if cur != wantCur || prev != wantPrev || st.DataError != "" {
			t.Fatalf("commits=%v: current/previous %s/%s (%+v), want %s/%s", commits, cur, prev, st.DataError, wantCur, wantPrev)
		}
		if d, err := restarted.DeriveGeosite(ctx, "cat", nil); err != nil || string(d.Entries) != wantEntry {
			t.Fatalf("commits=%v: derive %v %v", commits, d, err)
		}
		// Both releases are still on disk and the previous one can be rolled to.
		if _, err := restarted.GeositeRollback(ctx, true); err != nil {
			t.Fatalf("commits=%v: the previous release was lost: %v", commits, err)
		}
	}
}

// An ambiguous commit error consults the database instead of assuming failure.
func TestGeositeUpdate_AnAmbiguousCommitErrorConsultsTheDatabase(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.up.release("20261101000000", map[string][]string{"cat": {"domain:v1.example"}})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	h.up.release("20261102000000", map[string][]string{"cat": {"domain:v2.example"}})
	svc := h.withLists(func(inner ListStore) ListStore {
		return &commitThenFail{ListStore: inner, after: func() {}} // the database stays reachable
	})
	res, err := svc.GeositeUpdate(ctx, UpdateOptions{})
	if err != nil || !res.Applied {
		t.Fatalf("a commit that went through must be completed, not undone: %+v %v", res, err)
	}
	if cur, prev := tagsOf(svc.GeositeStatus(ctx)); cur != "20261102000000" || prev != "20261101000000" {
		t.Fatalf("current/previous %s/%s", cur, prev)
	}
}

// A realistic excerpt of the release's layout: quoted rules, comma-joined
// attributes after ":@", a negated attribute, a regexp with escaped dots.
func TestParseDLC_RealReleaseLayoutExcerpt(t *testing.T) {
	excerpt := "lists:\n" +
		"  - name: \"0x0\"\n    length: 1\n    rules:\n      - \"domain:0x0.st\"\n" +
		"  - name: \"category-ads-all\"\n    length: 4\n    rules:\n" +
		"      - \"domain:adx.36kr.com:@ads\"\n" +
		"      - \"full:apilog-web.acfun.cn:@ads,@telemetry\"\n" +
		"      - \"domain:airchina.ae:@!cn\"\n" +
		"      - \"regexp:^r+[0-9]+(---|\\\\.)sn-(2x3|ni5|j5o)\\\\w{5}\\\\.googlevideo\\\\.com$:@cn\"\n"
	d, err := parseDLC([]byte(excerpt))
	if err != nil {
		t.Fatal(err)
	}
	all, _ := d.derive("category-ads-all", nil)
	want := []string{"domain:adx.36kr.com", "domain:airchina.ae", "full:apilog-web.acfun.cn", `regexp:^r+[0-9]+(---|\.)sn-(2x3|ni5|j5o)\w{5}\.googlevideo\.com$`}
	if got := strings.Split(string(all.Entries), "\n"); !slices.Equal(got, want) || all.RegexpCount != 1 {
		t.Fatalf("entries = %q regexps %d", got, all.RegexpCount)
	}
	if ads, _ := d.derive("category-ads-all", []string{"ads", "telemetry"}); string(ads.Entries) != "full:apilog-web.acfun.cn" {
		t.Fatalf("ads+telemetry = %q", ads.Entries)
	}
	if neg, _ := d.derive("category-ads-all", []string{"!cn"}); string(neg.Entries) != "domain:airchina.ae" {
		t.Fatalf("!cn = %q", neg.Entries)
	}
}

// An update in flight has a pending record too. A status or derive call made
// while it runs must NOT heal it (that would undo a swap whose commit is about
// to land): it serves the release before the update, and when the update
// finishes the files, the record and the database agree on the new release.
func TestGeositeHeal_NeverActsOnALiveUpdate(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.up.release("20261101000000", map[string][]string{"cat": {"domain:v1.example"}})
	if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	l, _ := h.svc.CreateGeosite(ctx, "cat", nil)
	h.policy("p", domain.DestStateOn, domain.DestActionBlock, l.ID)
	h.up.release("20261102000000", map[string][]string{"cat": {"domain:v2.example"}})

	reached, release := make(chan struct{}), make(chan struct{})
	h.svc.afterSwap = func() { close(reached); <-release }
	done := make(chan error, 1)
	go func() { _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); done <- err }()
	<-reached // files swapped, pending written, lists not yet committed

	st := h.svc.GeositeStatus(ctx)
	if cur, _ := tagsOf(st); cur != "20261101000000" || st.DataError != "" {
		t.Fatalf("during the update status = %+v, want the release before it", st)
	}
	if d, err := h.svc.DeriveGeosite(ctx, "cat", nil); err != nil || string(d.Entries) != "domain:v1.example" {
		t.Fatalf("during the update derive = %v %v", d, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	st = h.svc.GeositeStatus(ctx)
	if cur, prev := tagsOf(st); cur != "20261102000000" || prev != "20261101000000" || st.DataError != "" {
		t.Fatalf("after the update status = %+v", st)
	}
	if got := readCurrent(t, h); !strings.Contains(got, "v2.example") {
		t.Fatal("the files are not the new release")
	}
	if got, _ := h.repos.Lists.GetByID(ctx, l.ID); string(got.Entries) != "domain:v2.example" {
		t.Fatalf("the list: %q", got.Entries)
	}
	if dbst, _ := h.repos.State.Get(ctx); dbst.GeositeRelease != "20261102000000" {
		t.Fatalf("the database records release %q", dbst.GeositeRelease)
	}
	// And a fresh process agrees: nothing pending is left to heal.
	if cur, _ := tagsOf(h.newService(nil).GeositeStatus(ctx)); cur != "20261102000000" {
		t.Fatalf("a restart reads %s", cur)
	}
}

// An outcome that could not be settled (the database was unreachable) is left
// for a LATER call in the same process, once nothing holds the geosite lock:
// no restart needed.
func TestGeositeHeal_AnUnknownOutcomeIsSettledByALaterCall(t *testing.T) {
	for _, commits := range []bool{true, false} {
		h := newHarness(t)
		ctx := context.Background()
		h.up.release("20261101000000", map[string][]string{"cat": {"domain:v1.example"}})
		if _, err := h.svc.GeositeUpdate(ctx, UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
		h.up.release("20261102000000", map[string][]string{"cat": {"domain:v2.example"}})
		s, flaky := h.interrupted(t, commits, func(s *Service) error { _, err := s.GeositeUpdate(ctx, UpdateOptions{}); return err })

		// Still unreachable: nothing is guessed, and the geosite lock heals nothing.
		if _, err := s.LockGeosite(ctx); err == nil {
			t.Fatal("the lock was granted over an unsettled swap")
		}
		flaky.failGet = false
		st := s.GeositeStatus(ctx) // the later call
		want := "20261101000000"
		if commits {
			want = "20261102000000"
		}
		if cur, _ := tagsOf(st); cur != want || st.DataError != "" {
			t.Fatalf("commits=%v: after the later call %+v, want %s", commits, st, want)
		}
		unlock, err := s.LockGeosite(ctx)
		if err != nil {
			t.Fatalf("commits=%v: the lock after healing: %v", commits, err)
		}
		unlock()
	}
}
