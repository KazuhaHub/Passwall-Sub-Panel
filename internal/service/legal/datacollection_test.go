package legal

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type fakeDest struct {
	nodes, trialGroups, usageNodes int
	err                            error
}

func (f fakeDest) CollectingNodes(context.Context) (int, error)      { return f.nodes, f.err }
func (f fakeDest) AllowlistTrialGroups(context.Context) (int, error) { return f.trialGroups, f.err }
func (f fakeDest) CollectingUsageNodes(context.Context) (int, error) { return f.usageNodes, f.err }

func intp(v int) *int { return &v }

// The page is generated from the settings the panel actually runs with, so
// each retention is the real stored value (with the same defaults the cleanup
// loops use), not a copy of a default.
func TestCollect_ReadsTheRealRetentionValues(t *testing.T) {
	s := ports.UISettings{
		SubLogRetentionDays:         14,
		AuthEventRetentionDays:      0, // forever
		RiskConnectionRetentionDays: 30,
		RiskFlagRecordRetentionDays: 120,
		RiskHWIDCaptureOff:          false,
		DestHitRetentionDays:        45,
		DestTrialRetentionDays:      3,
	}
	got, err := NewCollector(fakeDest{nodes: 2, trialGroups: 1}).Collect(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Item{
		"sub_log":            {Enabled: true, RetentionDays: intp(14), IncludesIP: true},
		"auth_event":         {Enabled: true, RetentionDays: intp(0), IncludesIP: true},
		"connection_history": {Enabled: true, RetentionDays: intp(30), IncludesIP: true},
		"hwid":               {Enabled: true},
		"flag_records":       {Enabled: true, RetentionDays: intp(120)},
		"risk_signals":       {Enabled: true},
		"risk_reviews":       {Enabled: true},
		"dest_hits":          {Enabled: true, Nodes: intp(2), RetentionDays: intp(45)},
		"dest_trial":         {Enabled: true, Nodes: intp(2), RetentionDays: intp(3)},
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			t.Errorf("item %q missing", k)
			continue
		}
		if !sameItem(g, w) {
			t.Errorf("%s = %+v, want %+v", k, dump(g), dump(w))
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected item %q (usage must not be disclosed by default)", k)
		}
	}
}

func dump(i Item) map[string]any {
	m := map[string]any{"enabled": i.Enabled, "ip": i.IncludesIP}
	if i.RetentionDays != nil {
		m["days"] = *i.RetentionDays
	}
	if i.Nodes != nil {
		m["nodes"] = *i.Nodes
	}
	return m
}

func sameItem(a, b Item) bool {
	eq := func(x, y *int) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	return a.Enabled == b.Enabled && a.IncludesIP == b.IncludesIP && eq(a.RetentionDays, b.RetentionDays) && eq(a.Nodes, b.Nodes)
}

// An unset retention is the value the cleanup actually applies, not 0 and not
// a number the page invented.
func TestCollect_UnsetRetentionsAreTheEffectiveDefaults(t *testing.T) {
	got, err := NewCollector(fakeDest{}).Collect(context.Background(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]int{
		"connection_history": 7,  // domain.RiskDefaultConnectionRetentionDays
		"flag_records":       90, // domain.RiskDefaultFlagRecordRetentionDays
		"dest_hits":          30, // ports.DestHitRetentionDaysDefault
	} {
		if g := got[key].RetentionDays; g == nil || *g != want {
			t.Errorf("%s retention = %v, want %d", key, g, want)
		}
	}
}

func TestCollect_HWIDOffIsReportedOff(t *testing.T) {
	got, _ := NewCollector(nil).Collect(context.Background(), ports.UISettings{RiskHWIDCaptureOff: true})
	if got["hwid"].Enabled {
		t.Error("hwid capture is off but the page says it is on")
	}
}

func TestCollect_RiskSignalsOffWhenEverySignalIsOff(t *testing.T) {
	s := ports.UISettings{RiskSubSpreadOff: true, RiskDevicesOff: true, RiskDestBlockOff: true, RiskUsageShiftOff: true, RiskLoginCountryOff: true}
	got, _ := NewCollector(nil).Collect(context.Background(), s)
	if got["risk_signals"].Enabled {
		t.Error("every risk signal is off but the page says they are judged")
	}
	s.RiskDevicesOff = false
	got, _ = NewCollector(nil).Collect(context.Background(), s)
	if !got["risk_signals"].Enabled {
		t.Error("one signal is on but the page says none is")
	}
}

func TestCollect_DestItemsFollowTheNodes(t *testing.T) {
	// No node records: hits are off, and so is the trial even with a trial group.
	got, _ := NewCollector(fakeDest{nodes: 0, trialGroups: 1}).Collect(context.Background(), ports.UISettings{})
	if got["dest_hits"].Enabled || got["dest_trial"].Enabled {
		t.Errorf("nothing records but dest items are on: %+v %+v", dump(got["dest_hits"]), dump(got["dest_trial"]))
	}
	// Nodes record but no group is in trial: the trial is off.
	got, _ = NewCollector(fakeDest{nodes: 3, trialGroups: 0}).Collect(context.Background(), ports.UISettings{})
	if !got["dest_hits"].Enabled || got["dest_trial"].Enabled {
		t.Errorf("3 nodes, no trial group: hits %v trial %v", got["dest_hits"].Enabled, got["dest_trial"].Enabled)
	}
	// No counter wired at all: off, not a failure.
	got, err := NewCollector(nil).Collect(context.Background(), ports.UISettings{})
	if err != nil || got["dest_hits"].Enabled {
		t.Errorf("no counter: %v, hits on = %v", err, got["dest_hits"].Enabled)
	}
}

func TestCollect_AFailingCounterFailsTheWholeDocument(t *testing.T) {
	_, err := NewCollector(fakeDest{err: errors.New("db down")}).Collect(context.Background(), ports.UISettings{})
	if err == nil {
		t.Fatal("a disclosure that silently drops the node count would understate what is recorded")
	}
}

// The extension point PSP-U1 uses: an optional entry shown only while its gate
// (the admin's disclose switch) is on.
func TestCollect_AGatedProviderAppearsOnlyWhenItsGateIsOn(t *testing.T) {
	c := NewCollector(nil)
	built := 0
	c.Register(Provider{
		Key:  "dest_example",
		Gate: func(s ports.UISettings) bool { return s.LegalDiscloseUsage },
		Build: func(context.Context, ports.UISettings) (Item, error) {
			built++
			return Item{Enabled: true, Nodes: intp(4), RetentionDays: intp(7)}, nil
		},
	})
	ctx := context.Background()
	got, err := c.Collect(ctx, ports.UISettings{LegalDiscloseUsage: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["dest_example"]; ok || built != 0 {
		t.Fatalf("a gated-off entry was disclosed (built %d times)", built)
	}
	got, _ = c.Collect(ctx, ports.UISettings{LegalDiscloseUsage: true})
	if it, ok := got["dest_example"]; !ok || it.Nodes == nil || *it.Nodes != 4 {
		t.Fatalf("a gated-on entry is missing or wrong: %+v", got["dest_example"])
	}
}

func TestRegister_DuplicateKeyPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("registering a key twice must panic: two writers of one disclosure line is a bug")
		}
	}()
	c := NewCollector(nil)
	p := Provider{Key: "x", Build: func(context.Context, ports.UISettings) (Item, error) { return Item{}, nil }}
	c.Register(p)
	c.Register(p)
}

type fakeOverrides struct {
	n     int
	err   error
	calls int
	typ   string
	names []string
	want  bool
}

func (f *fakeOverrides) CountGroupsOverridingBool(_ context.Context, typ string, names []string, want bool) (int, error) {
	f.calls++
	f.typ, f.names, f.want = typ, names, want
	return f.n, f.err
}

func allSignalsOff() ports.UISettings {
	return ports.UISettings{RiskSubSpreadOff: true, RiskDevicesOff: true, RiskDestBlockOff: true, RiskUsageShiftOff: true, RiskLoginCountryOff: true}
}

// Every risk signal can be switched back on for a group (risk.*_off is in
// ports.OverridableScopeKeys) and the worker honours that. A page that said
// "not judged" because the GLOBAL switches are all off would understate what
// happens to those groups' members.
func TestCollect_RiskSignalsAreOnWhereAGroupTurnsThemBackOn(t *testing.T) {
	ov := &fakeOverrides{n: 2}
	c := NewCollector(nil)
	c.SetRiskOverrides(ov)
	got, err := c.Collect(context.Background(), allSignalsOff())
	if err != nil {
		t.Fatal(err)
	}
	it := got["risk_signals"]
	if !it.Enabled || it.EnabledGroups == nil || *it.EnabledGroups != 2 {
		t.Fatalf("risk_signals = %+v, want enabled for 2 groups", dump(it))
	}
	// An enabling override sets a negative switch to false, and only the five
	// signal switches are asked about.
	if ov.typ != "risk" || ov.want || !reflect.DeepEqual(ov.names, []string{"sub_spread_off", "devices_off", "dest_block_off", "usage_shift_off", "login_country_off"}) {
		t.Errorf("asked %q %v %v", ov.typ, ov.names, ov.want)
	}
}

func TestCollect_RiskSignalsAreOffWhenNoGroupTurnsThemOn(t *testing.T) {
	c := NewCollector(nil)
	c.SetRiskOverrides(&fakeOverrides{n: 0})
	got, _ := c.Collect(context.Background(), allSignalsOff())
	if got["risk_signals"].Enabled || got["risk_signals"].EnabledGroups != nil {
		t.Errorf("risk_signals = %+v, want off", dump(got["risk_signals"]))
	}
}

func TestCollect_AGloballyOnSignalNeedsNoGroupLookup(t *testing.T) {
	ov := &fakeOverrides{n: 9}
	c := NewCollector(nil)
	c.SetRiskOverrides(ov)
	s := allSignalsOff()
	s.RiskLoginCountryOff = false
	got, _ := c.Collect(context.Background(), s)
	if !got["risk_signals"].Enabled || got["risk_signals"].EnabledGroups != nil || ov.calls != 0 {
		t.Errorf("risk_signals = %+v after %d lookups", dump(got["risk_signals"]), ov.calls)
	}
}

func TestCollect_AFailingGroupLookupFailsTheDocument(t *testing.T) {
	c := NewCollector(nil)
	c.SetRiskOverrides(&fakeOverrides{err: errors.New("db down")})
	if _, err := c.Collect(context.Background(), allSignalsOff()); err == nil {
		t.Fatal("an understated disclosure must not be served as if it were complete")
	}
}

type countingDest struct{ nodes, trial int }

func (d *countingDest) CollectingNodes(context.Context) (int, error) {
	d.nodes++
	return 3, nil
}
func (d *countingDest) CollectingUsageNodes(context.Context) (int, error) { return 2, nil }
func (d *countingDest) AllowlistTrialGroups(context.Context) (int, error) {
	d.trial++
	return 1, nil
}

// The public page is anonymous, so what it asks the database is bounded: the
// node and group counts are remembered for 30 seconds, while everything read
// from the settings stays live.
func TestCollect_CachesTheDatabaseDerivedCountsFor30Seconds(t *testing.T) {
	d := &countingDest{}
	ov := &fakeOverrides{n: 1}
	c := NewCollector(d)
	c.SetRiskOverrides(ov)
	now := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { return now }
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := c.Collect(ctx, allSignalsOff()); err != nil {
			t.Fatal(err)
		}
	}
	if d.nodes != 1 || d.trial != 1 || ov.calls != 1 {
		t.Fatalf("5 requests asked nodes %d, trial %d, overrides %d times; want once each", d.nodes, d.trial, ov.calls)
	}
	// A setting is read live, with no wait.
	s := allSignalsOff()
	s.SubLogRetentionDays = 77
	if got, _ := c.Collect(ctx, s); *got["sub_log"].RetentionDays != 77 {
		t.Errorf("a settings change was not visible at once: %d", *got["sub_log"].RetentionDays)
	}
	now = now.Add(31 * time.Second)
	c.Collect(ctx, allSignalsOff())
	if d.nodes != 2 || d.trial != 2 || ov.calls != 2 {
		t.Errorf("after 31s: nodes %d, trial %d, overrides %d; want a second read each", d.nodes, d.trial, ov.calls)
	}
}

func TestCollect_AFailedCountIsNotCached(t *testing.T) {
	d := &fakeDest{err: errors.New("down")}
	c := NewCollector(d)
	if _, err := c.Collect(context.Background(), ports.UISettings{}); err == nil {
		t.Fatal("want the error")
	}
	d.err = nil
	d.nodes = 4
	got, err := c.Collect(context.Background(), ports.UISettings{})
	if err != nil || got["dest_hits"].Nodes == nil || *got["dest_hits"].Nodes != 4 {
		t.Fatalf("after recovery: %v, %v; a failure must not be remembered", err, got["dest_hits"])
	}
}

// PER-SITE USAGE IS DISCLOSED ONLY WHEN THE ADMINISTRATOR CHOSE TO. The entry is
// absent while legal_disclose_usage is off, whatever is recording; when on it
// carries the number of nodes recording it and the usage retention.
func TestCollect_UsageIsListedOnlyWhenTheAdminDiscloses(t *testing.T) {
	c := NewCollector(fakeDest{nodes: 3, usageNodes: 2})
	ctx := context.Background()
	got, err := c.Collect(ctx, ports.UISettings{DestUsageRetentionDays: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["dest_usage"]; ok {
		t.Fatal("usage recording was disclosed without the administrator's switch")
	}
	got, err = c.Collect(ctx, ports.UISettings{LegalDiscloseUsage: true, DestUsageRetentionDays: 10})
	if err != nil {
		t.Fatal(err)
	}
	it, ok := got["dest_usage"]
	if !ok || !it.Enabled || it.Nodes == nil || *it.Nodes != 2 || it.RetentionDays == nil || *it.RetentionDays != 10 || it.IncludesIP {
		t.Fatalf("dest_usage = %+v", it)
	}
	// Switch on but nothing records it: listed, not enabled.
	got, _ = NewCollector(fakeDest{nodes: 3}).Collect(ctx, ports.UISettings{LegalDiscloseUsage: true})
	if it := got["dest_usage"]; it.Enabled || it.Nodes == nil || *it.Nodes != 0 {
		t.Fatalf("dest_usage with no usage node = %+v", it)
	}
}
