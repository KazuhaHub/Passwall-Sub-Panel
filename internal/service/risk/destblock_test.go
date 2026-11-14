package risk

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// fakeDest answers the destination-hit read: one input per account, or err.
type fakeDest struct {
	inputs map[int64]domain.DestBlockInput
	err    error
	since  []int64
}

func (f *fakeDest) DestBlockInputs(_ context.Context, sinceMS int64) (map[int64]domain.DestBlockInput, error) {
	f.since = append(f.since, sinceMS)
	return f.inputs, f.err
}

func destRows(t *testing.T, h *harness) map[int64]domain.RiskSignal {
	out := map[int64]domain.RiskSignal{}
	for uid, kinds := range h.store.saved(t) {
		if r, ok := kinds[domain.RiskKindDestBlock]; ok {
			out[uid] = r
		}
	}
	return out
}

func in(nodes int, counts ...int64) domain.DestBlockInput {
	d := domain.DestBlockInput{RecordingNodes: nodes}
	for i, c := range counts {
		d.Sources = append(d.Sources, domain.DestBlockSourceCount{Source: "p" + string(rune('1'+i)), Count: c})
	}
	return d
}

func newDestHarness(users []*domain.User, dest *fakeDest) (*harness, *Service) {
	h := newHarness(users)
	svc := h.service()
	svc.d.Dest = dest
	return h, svc
}

func TestRefresh_DestBlockJudgesEachAccountFromItsOwnInput(t *testing.T) {
	dest := &fakeDest{inputs: map[int64]domain.DestBlockInput{
		1: in(1, 3),    // clean
		2: in(2, 6, 6), // suspect (12 of 20)
		3: in(1, 25),   // flagged
		4: in(1),       // idle: recording, nothing blocked
		// 5: absent: no recording node
	}}
	h, svc := newDestHarness(usersInGroups(0, 0, 0, 0, 0), dest)
	if err := svc.RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows := destRows(t, h)
	for uid, want := range map[int64][2]string{
		1: {"clean", "within"}, 2: {"suspect", "over_building"}, 3: {"flagged", "over"}, 4: {"idle", "no_hits"}, 5: {"unknown", "no_collector"},
	} {
		r, ok := rows[uid]
		if !ok || string(r.State) != want[0] || string(r.Code) != want[1] {
			t.Errorf("user %d = %+v (present %v), want %v", uid, r, ok, want)
		}
	}
	if len(dest.since) != 1 {
		t.Fatalf("the destination read ran %d times, want once for the whole run", len(dest.since))
	}
	// The window is the current hour and the 23 before it: 24 hourly buckets.
	want := refreshNow.Truncate(time.Hour).Add(-23 * time.Hour).UnixMilli()
	if dest.since[0] != want {
		t.Fatalf("window starts at %d, want %d", dest.since[0], want)
	}
}

func TestRefresh_DestBlockUsesTheGroupsSwitchAndThreshold(t *testing.T) {
	dest := &fakeDest{inputs: map[int64]domain.DestBlockInput{1: in(1, 5), 2: in(1, 5), 3: in(1, 5)}}
	h, svc := newDestHarness(usersInGroups(0, 1, 2), dest)
	h.settings.groups = map[int64]ports.UISettings{
		1: {RiskDestBlockOff: true},
		2: {RiskDestBlockThreshold: 5},
	}
	if err := svc.RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows := destRows(t, h)
	if rows[1].State != domain.GeoStateClean || rows[2].State != domain.GeoStateDisabled || rows[2].Code != domain.RiskCodeSignalOff || rows[3].State != domain.GeoStateFlagged {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[2].Evidence != nil {
		t.Fatalf("a switched-off signal has evidence %s", rows[2].Evidence)
	}
}

// A source that cannot be read costs the rows it feeds: they keep their
// previous values, and the run is partial.
func TestRefresh_DestBlockUnreadableKeepsThePreviousRows(t *testing.T) {
	dest := &fakeDest{err: errors.New("db down")}
	h, svc := newDestHarness(usersInGroups(0), dest)
	before := outcomes()
	if err := svc.RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(destRows(t, h)) != 0 {
		t.Fatal("dest_block was judged from a read that failed")
	}
	wantOutcome(t, before, "partial")
}

func TestRefresh_DestBlockIsNotComputedWithoutTheReader(t *testing.T) {
	h := newHarness(usersInGroups(0))
	if err := h.service().RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(destRows(t, h)) != 0 {
		t.Fatal("dest_block rows without a reader")
	}
}

func TestRefresh_DestBlockEvidenceHasNoAddress(t *testing.T) {
	dest := &fakeDest{inputs: map[int64]domain.DestBlockInput{1: in(2, 30, 4)}}
	h, svc := newDestHarness(usersInGroups(0), dest)
	if err := svc.RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	r := destRows(t, h)[1]
	if r.Evidence == nil {
		t.Fatal("no evidence")
	}
	for _, tok := range strings.FieldsFunc(string(r.Evidence), func(c rune) bool { return strings.ContainsRune(`{}[]",: `, c) }) {
		if _, err := netip.ParseAddr(tok); err == nil {
			t.Errorf("evidence holds an address %q: %s", tok, r.Evidence)
		}
		if _, err := netip.ParsePrefix(tok); err == nil {
			t.Errorf("evidence holds a network %q", tok)
		}
	}
}
