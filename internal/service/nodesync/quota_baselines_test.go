package nodesync

import (
	"math"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

func TestQuotaBaselinesKeepCurrentFullReportsAndKnownClosedCounters(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	s := &Service{reports: map[string]receivedFullReport{"owned": {receivedAtMS: now.UnixMilli(), report: protocol.NodeReport{Clients: []protocol.ClientCounters{{Key: protocol.NewClientKey(99), UpBytes: 999}}}}}}
	current := protocol.NodeReport{Clients: []protocol.ClientCounters{
		{Key: protocol.NewClientKey(1), Present: false, UpBytes: 10, DownBytes: 20, LiveIPs: []string{"203.0.113.1"}},
		{Key: protocol.NewClientKey(2), UpBytes: -10, DownBytes: 30},
		{Key: protocol.NewClientKey(3), UpBytes: math.MaxInt64, DownBytes: 99},
		{Key: protocol.NewClientKey(4), UpBytes: -10, DownBytes: 30},
		{Key: protocol.ClientKey("invalid"), UpBytes: 999},
		{Key: protocol.NewClientKey(2), UpBytes: 0, DownBytes: 0},
	}}
	before := current
	before.Clients = slices.Clone(current.Clients)
	for i := range before.Clients {
		before.Clients[i].LiveIPs = slices.Clone(current.Clients[i].LiveIPs)
	}
	got, full := s.quotaBaselines("owned", current, now, 1)
	if !full || !reflect.DeepEqual(got, map[int64]int64{1: 30, 2: 0, 3: math.MaxInt64, 4: 30}) {
		t.Fatal("quota baseline changed known closed, duplicate, invalid or saturated counters")
	}
	if !reflect.DeepEqual(before, current) {
		t.Fatal("quota baseline mutated its current full report")
	}
	got[1] = 999
	if next, _ := s.quotaBaselines("owned", current, now, 1); next[1] != 30 {
		t.Fatal("caller mutated future quota baselines")
	}
	if empty, full := s.quotaBaselines("owned", protocol.NodeReport{}, now, 1); !full || len(empty) != 0 {
		t.Fatal("empty full enumeration reused an older counter")
	}
}

func TestQuotaBaselinesUseReceiptFreshnessForPartialReports(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	partial := protocol.NodeReport{Partial: true, Clients: []protocol.ClientCounters{{Key: protocol.NewClientKey(99), UpBytes: 999}}}
	for _, sample := range []struct {
		name        string
		receipt     int64
		found, want bool
	}{
		{"missing", now.UnixMilli(), false, false},
		{"no receipt", 0, true, false},
		{"future", now.UnixMilli() + 1, true, false},
		{"boundary", now.UnixMilli() - 1000, true, true},
		{"stale", now.UnixMilli() - 1001, true, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			s := &Service{reports: make(map[string]receivedFullReport)}
			if sample.found {
				s.reports["owned"] = receivedFullReport{receivedAtMS: sample.receipt, report: protocol.NodeReport{ReportedAtMS: now.UnixMilli() + 999999, Clients: []protocol.ClientCounters{{Key: protocol.NewClientKey(1), Present: false, UpBytes: 7, DownBytes: 8}}}}
			}
			got, full := s.quotaBaselines("owned", partial, now, 1)
			if full != sample.want || (full && !reflect.DeepEqual(got, map[int64]int64{1: 15})) || (!full && len(got) != 0) {
				t.Fatal("partial report changed receipt-based freshness or used partial counters")
			}
			if got, full := s.quotaBaselines("owned", protocol.NodeReport{}, now, 1); !full || len(got) != 0 {
				t.Fatal("current full enumeration depended on an older receipt")
			}
		})
	}
}

func TestQuotaBaselinesObserveOwnedImmutableReportReplacements(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	makeReport := func(value int64) receivedFullReport {
		return receivedFullReport{receivedAtMS: now.UnixMilli(), report: protocol.NodeReport{Clients: []protocol.ClientCounters{{Key: protocol.NewClientKey(1), UpBytes: value}, {Key: protocol.NewClientKey(2), UpBytes: value}}}}
	}
	first, second := makeReport(1), makeReport(2)
	s := &Service{reports: map[string]receivedFullReport{"owned": first}}
	partial := protocol.NodeReport{Partial: true}
	before, _ := s.quotaBaselines("owned", partial, now, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 200 {
			s.mu.Lock()
			if i%2 == 0 {
				s.reports["owned"] = first
			} else {
				s.reports["owned"] = second
			}
			s.mu.Unlock()
		}
	})
	for range 200 {
		got, full := s.quotaBaselines("owned", partial, now, 1)
		if !full || len(got) != 2 || got[1] != got[2] || (got[1] != 1 && got[1] != 2) {
			t.Error("quota reader mixed immutable report replacements")
		}
	}
	wg.Wait()
	s.mu.Lock()
	s.reports["owned"] = second
	s.mu.Unlock()
	after, full := s.quotaBaselines("owned", partial, now, 1)
	if !full || before[1] != 1 || after[1] != 2 {
		t.Fatal("quota baselines retained old state or shared output")
	}
}

func TestCurrentQuotaBaselineAllocationsIgnoreSupersededFullReports(t *testing.T) {
	measure := func(size int) float64 {
		old, _ := clientIdentityObservationFixture(size)
		s := &Service{reports: map[string]receivedFullReport{"owned": {report: old}}}
		current := protocol.NodeReport{Clients: []protocol.ClientCounters{{Key: protocol.NewClientKey(1), UpBytes: 3}}}
		return testing.AllocsPerRun(5, func() {
			got, full := s.quotaBaselines("owned", current, time.Unix(1000, 0), 1)
			if !full || len(got) != 1 || got[1] != 3 {
				t.Fatal("current quota baseline changed")
			}
		})
	}
	if small, large := measure(1), measure(2000); large > small+2 {
		t.Fatalf("superseded reports increased quota allocation count: %.0f / %.0f", small, large)
	}
}
