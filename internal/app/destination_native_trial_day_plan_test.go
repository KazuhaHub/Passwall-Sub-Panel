//go:build destination_acceptance && linux

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math/bits"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"golang.org/x/net/publicsuffix"
)

const (
	nativeDayOwners   = 10
	nativeDayUsers    = 200
	nativeDaySites    = 300
	nativeDayHours    = 24
	nativeDaySlots    = nativeDayUsers * nativeDaySites * nativeDayHours
	nativeDayInterval = 60 * time.Millisecond
	nativeDayDuration = 24 * time.Hour
)

type nativeDaySlot struct{ hour, owner, user, site int }

func nativeTrialDaySlot(index int) (nativeDaySlot, bool) {
	if index < 0 || index >= nativeDaySlots {
		return nativeDaySlot{}, false
	}
	user, site := index%nativeDayUsers, (index%(nativeDayUsers*nativeDaySites))/nativeDayUsers
	return nativeDaySlot{index / (nativeDayUsers * nativeDaySites), (user + site) % nativeDayOwners, user, site}, true
}

func nativeTrialDayDomains() ([nativeDaySites]string, bool) {
	var out [nativeDaySites]string
	seen := make(map[string]bool, nativeDaySites)
	for i := range out {
		out[i] = fmt.Sprintf("trial-day-%03d.test", i)
		site, err := publicsuffix.EffectiveTLDPlusOne(out[i])
		if err != nil || site != out[i] || seen[site] {
			return out, false
		}
		seen[site] = true
	}
	return out, true
}

func nativeDayWait(ctx context.Context, due time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay := time.Until(due); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ctx.Err()
}

type nativeDayNodeHour struct {
	Hour             int       `json:"hour"`
	Owner            int       `json:"owner"`
	Samples          int       `json:"samples"`
	Keys             [4]int    `json:"peak_keys"`
	Dropped          [4]uint64 `json:"peak_retained_dropped"`
	Unmatched        [4]uint64 `json:"peak_retained_unmatched"`
	AccessDropped    uint64    `json:"peak_access_dropped"`
	AccessUnmatched  uint64    `json:"peak_access_unmatched"`
	AccessStale      uint64    `json:"peak_access_stale"`
	PendingDropped   uint64    `json:"peak_pending_dropped"`
	PendingUnmatched uint64    `json:"peak_pending_unmatched"`
	Heap             uint64    `json:"peak_heap_bytes"`
}

type nativeDayNodeRecorder struct {
	mu      sync.Mutex
	start   time.Time
	latest  [nativeDayOwners]time.Time
	hours   [nativeDayHours][nativeDayOwners]nativeDayNodeHour
	last    [nativeDayOwners]nativeMixedNodeReading
	base    [nativeDayOwners]nativeMixedAccess
	invalid bool
}

func (r *nativeDayNodeRecorder) observe(owner int, reading nativeMixedNodeReading) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if owner < 0 || owner >= nativeDayOwners {
		return
	}
	now := time.Now()
	r.latest[owner] = now
	r.last[owner] = reading
	hour := int(now.Sub(r.start) / time.Hour)
	if r.start.IsZero() || hour < 0 || hour >= nativeDayHours {
		return
	}
	h := &r.hours[hour][owner]
	h.Hour, h.Owner, h.Samples = hour, owner, h.Samples+1
	h.Heap = max(h.Heap, reading.HeapAlloc)
	base := r.base[owner]
	if reading.Access.Dropped < base.Dropped || reading.Access.Unmatched < base.Unmatched || reading.Access.Stale < base.Stale {
		r.invalid = true
		return
	}
	h.AccessDropped = max(h.AccessDropped, reading.Access.Dropped-base.Dropped)
	h.AccessUnmatched = max(h.AccessUnmatched, reading.Access.Unmatched-base.Unmatched)
	h.AccessStale = max(h.AccessStale, reading.Access.Stale-base.Stale)
	h.PendingDropped = max(h.PendingDropped, reading.Access.PendingDropped)
	h.PendingUnmatched = max(h.PendingUnmatched, reading.Access.PendingUnmatched)
	for i, lane := range reading.Lanes {
		if i >= 4 {
			break
		}
		h.Keys[i] = max(h.Keys[i], lane.Keys)
		h.Dropped[i] = max(h.Dropped[i], lane.Dropped)
		h.Unmatched[i] = max(h.Unmatched[i], lane.Unmatched)
	}
}

func (r *nativeDayNodeRecorder) readyLocked() bool {
	for owner := range nativeDayOwners {
		if r.latest[owner].IsZero() || time.Since(r.latest[owner]) >= 10*time.Second || !r.last[owner].Access.quiescent() || len(r.last[owner].Lanes) != 4 {
			return false
		}
		for _, lane := range r.last[owner].Lanes {
			if lane.Keys != 0 || lane.Pending != 0 || lane.Dropped != 0 || lane.Unmatched != 0 {
				return false
			}
		}
	}
	return !r.invalid
}
func (r *nativeDayNodeRecorder) ready() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.readyLocked()
}
func (r *nativeDayNodeRecorder) begin(start time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.readyLocked() {
		return false
	}
	r.start = start
	for owner := range nativeDayOwners {
		r.base[owner] = r.last[owner].Access
	}
	return true
}
func (r *nativeDayNodeRecorder) baseline() [nativeDayOwners]nativeMixedAccess {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.base
}

func (r *nativeDayNodeRecorder) snapshot() ([]nativeDayNodeHour, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []nativeDayNodeHour
	fresh := !r.invalid
	for owner := range nativeDayOwners {
		fresh = fresh && !r.latest[owner].IsZero() && time.Since(r.latest[owner]) < 10*time.Second
	}
	for hour := range nativeDayHours {
		for owner := range nativeDayOwners {
			out = append(out, r.hours[hour][owner])
		}
	}
	return out, fresh
}

func TestNativeTrialDayPlanKeepsFullWallClockAndDistinctDomains(t *testing.T) {
	t.Run("sqlite_measurements_use_real_schema_and_owned_files", func(t *testing.T) {
		a := buildDestinationListsFixture(t)
		t.Setenv("RUNNER_TEMP", filepath.Dir(a.cfg.DataDir))
		var bytes int64
		nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT COALESCE(SUM(pgsize),0) FROM dbstat WHERE name='dest_hits'").Scan(&bytes), "read actual SQLite dbstat in the short boundary test")
		files := nativeDayFileSizes(t, a)
		if bytes <= 0 || files[0] <= 0 {
			t.Fatal("real SQLite schema or physical file measurement missing")
		}
	})
	t.Run("all_panel_pages_reject_foreign_and_identified_trial_rows", func(t *testing.T) {
		own := nativePageIdentity{user: 3, policy: 4, group: 5, generation: 6}
		upn := "private@example.test"
		for _, tc := range []struct {
			name  string
			row   domain.DestHitRecord
			valid bool
		}{
			{"first_owner", domain.DestHitRecord{PanelID: 2, Source: "g5", Action: "observe", Dest: "trial-day-000.test", Count: 1}, true},
			{"other_owned_node", domain.DestHitRecord{PanelID: 7, Source: "g5", Action: "observe", Dest: "trial-day-001.test", Count: 1}, true},
			{"foreign", domain.DestHitRecord{PanelID: 9, Source: "g5", Action: "observe", Dest: "trial-day-000.test", Count: 1}, false},
			{"identified", domain.DestHitRecord{PanelID: 2, UserID: 3, Source: "g5", Action: "observe", Dest: "trial-day-000.test", Count: 1}, false},
			{"upn", domain.DestHitRecord{PanelID: 2, UserUPN: &upn, Source: "g5", Action: "observe", Dest: "trial-day-000.test", Count: 1}, false},
			{"port", domain.DestHitRecord{PanelID: 2, Port: 443, Source: "g5", Action: "observe", Dest: "trial-day-000.test", Count: 1}, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("panel_id") != "" || r.URL.Query().Get("page") != "1" || r.Header.Get("Authorization") != "Bearer owned" {
						w.WriteHeader(400)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"group_by": "none", "page": 1, "page_size": 50, "total": 1, "losses": domain.DestAuditLosses{Scope: "panel"}, "items": []domain.DestHitRecord{tc.row}})
				}))
				defer server.Close()
				_, err := nativeDayPage(t.Context(), server.Client(), server.URL, "owned", false, own, map[int64]bool{2: true, 7: true}, time.Now())
				if (err == nil) != tc.valid {
					t.Fatal("day page accepted a foreign or identified trial, or filtered out another owned node")
				}
			})
		}
	})
	t.Run("strict_separate_65_and_10_entrypoints", func(t *testing.T) {
		for _, day := range []bool{false, true} {
			owners, name := 65, "destination-global-bootstrap-metrics.json"
			if day {
				owners, name = 10, "destination-trial-day-bootstrap-metrics.json"
			}
			if !nativeOwnedCohortScope(owners, name, day, false) {
				t.Fatal("dedicated cohort entrypoint rejected")
			}
			for _, bad := range []int{0, 9, 11, 64, 66} {
				if nativeOwnedCohortScope(bad, name, day, false) {
					t.Fatal("cohort size was configurable outside its exact scope")
				}
			}
			if nativeOwnedCohortScope(owners, name, !day, false) || nativeOwnedCohortScope(owners, "other.json", day, false) {
				t.Fatal("cohort borrowed another evidence or database scope")
			}
		}
		if nativeOwnedCohortScope(10, "destination-trial-day-bootstrap-metrics.json", true, true) {
			t.Fatal("long trial borrowed injected storage or admissions")
		}
	})
	t.Run("all_hour_user_sites_and_ten_owners", func(t *testing.T) {
		var sites [24][200][5]uint64
		var owners [24][10]int
		for index := 0; index < nativeDaySlots; index++ {
			s, ok := nativeTrialDaySlot(index)
			if !ok {
				t.Fatal("complete real-day slot missing")
			}
			bit := uint64(1) << (s.site % 64)
			if sites[s.hour][s.user][s.site/64]&bit != 0 {
				t.Fatal("hour/user/site repeated")
			}
			sites[s.hour][s.user][s.site/64] |= bit
			owners[s.hour][s.owner]++
		}
		for h := range 24 {
			for u := range 200 {
				n := 0
				for _, word := range sites[h][u] {
					n += bits.OnesCount64(word)
				}
				if n != 300 {
					t.Fatal("user did not visit the same 300 sites in every hour")
				}
			}
			for _, n := range owners[h] {
				if n != 6000 {
					t.Fatal("owner coverage changed")
				}
			}
		}
		if time.Duration(nativeDaySlots)*nativeDayInterval != nativeDayDuration {
			t.Fatal("schedule does not span real24h")
		}
		for _, bad := range []int{-1, nativeDaySlots} {
			if _, ok := nativeTrialDaySlot(bad); ok {
				t.Fatal("out of scope slot accepted")
			}
		}
	})
	t.Run("materialized_domains_preserve_psl_identity", func(t *testing.T) {
		domains, ok := nativeTrialDayDomains()
		if !ok {
			t.Fatal("300 distinct registrable domains unavailable")
		}
		for _, domain := range domains {
			site, err := publicsuffix.EffectiveTLDPlusOne("sub." + domain)
			if err != nil || site != domain {
				t.Fatal("subdomain did not fold to its stable site")
			}
		}
	})
	t.Run("wait_uses_original_deadline_and_cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if nativeDayWait(ctx, time.Now().Add(24*time.Hour)) != context.Canceled {
			t.Fatal("cancelled real day did not stop")
		}
		if err := nativeDayWait(t.Context(), time.Now().Add(-time.Hour)); err != nil {
			t.Fatal("overdue original clock reset")
		}
	})
	t.Run("node_history_is_bounded_and_requires_fresh_real_readings", func(t *testing.T) {
		var r nativeDayNodeRecorder
		if _, ok := r.snapshot(); ok {
			t.Fatal("absent node samples treated as fresh")
		}
		for owner := range 10 {
			r.observe(owner, nativeMixedNodeReading{HeapAlloc: 100, Lanes: make([]nativeMixedNodeLane, 4), Access: nativeMixedAccess{Available: 1, Limit: 4096}})
		}
		if !r.begin(time.Now()) {
			t.Fatal("drained actual readings did not establish a baseline")
		}
		for owner := range 10 {
			r.observe(owner, nativeMixedNodeReading{HeapAlloc: 100, Lanes: []nativeMixedNodeLane{{Keys: 3, Dropped: 2}}})
		}
		rows, ok := r.snapshot()
		if !ok || len(rows) != 240 || rows[0].Keys[0] != 3 || rows[0].Dropped[0] != 2 {
			t.Fatal("actual node samples lost or history changed scope")
		}
	})
	t.Run("baseline_separates_setup_and_real_day_access_loss", func(t *testing.T) {
		var r nativeDayNodeRecorder
		reading := nativeMixedNodeReading{HeapAlloc: 100, Lanes: make([]nativeMixedNodeLane, 4), Access: nativeMixedAccess{Available: 1, Limit: 4096, Private: 7, Stale: 7}}
		for owner := range 10 {
			r.observe(owner, reading)
		}
		if !r.begin(time.Now()) {
			t.Fatal("quiescent setup counters did not bind")
		}
		reading.Access.Dropped = 1
		reading.Access.Private = 8
		r.observe(0, reading)
		rows, ok := r.snapshot()
		if !ok || rows[0].AccessDropped != 1 || rows[0].AccessStale != 0 || r.baseline()[0].Stale != 7 {
			t.Fatal("day observer borrowed setup loss or hid new loss")
		}
		reading.Access.Stale = 6
		r.observe(0, reading)
		if _, ok := r.snapshot(); ok {
			t.Fatal("regressed actual counters were accepted")
		}
	})
}
