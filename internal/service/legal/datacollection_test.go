package legal

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type fakeDest struct {
	nodes, trialGroups int
	err                error
}

func (f fakeDest) CollectingNodes(context.Context) (int, error)      { return f.nodes, f.err }
func (f fakeDest) AllowlistTrialGroups(context.Context) (int, error) { return f.trialGroups, f.err }

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
		Key:  "dest_usage",
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
	if _, ok := got["dest_usage"]; ok || built != 0 {
		t.Fatalf("a gated-off entry was disclosed (built %d times)", built)
	}
	got, _ = c.Collect(ctx, ports.UISettings{LegalDiscloseUsage: true})
	if it, ok := got["dest_usage"]; !ok || it.Nodes == nil || *it.Nodes != 4 {
		t.Fatalf("a gated-on entry is missing or wrong: %+v", got["dest_usage"])
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
