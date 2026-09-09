package domain

import (
	"testing"
	"time"
)

// The ConfigSync* set is duplicated in web-react/src/views/admin/configSync.ts,
// because neither language can read the other. This is the Go half; that file
// has the frontend half. A state added on one side and not the other does not
// render as a gap — the admin dot falls back to the "never captured" colour and
// wording, so the node is described as something it is not.
func TestConfigSyncStatesAreExhaustive(t *testing.T) {
	want := map[string]string{
		ConfigSyncNeverCaptured: "uncaptured",
		ConfigSyncSynced:        "synced",
		ConfigSyncPending:       "pending",
		ConfigSyncFailed:        "failed",
		ConfigSyncDrift:         "drift",
	}
	if len(want) != 5 {
		t.Fatalf("states = %d; if you added one, add it to configSync.ts too", len(want))
	}
	// Values are what lands in the database and in the API payload, so a
	// rename is a wire change, not a refactor.
	for state, key := range want {
		if state == ConfigSyncNeverCaptured {
			continue
		}
		if state != key {
			t.Errorf("state %q and its i18n key %q disagree; the frontend keys off the state value", state, key)
		}
	}
}

// The stamp must survive a re-mark, because reconcile re-marks a stuck node
// pending on EVERY cycle. A stamp refreshed on each write would reset the
// clock every fifteen minutes and the age could never grow past one cycle —
// a node stuck for three days would read as fifteen minutes old, forever.
//
// That is the whole reason the column exists, so it is the first thing to pin.
func TestConfigPendingSinceDoesNotResetWhileStuck(t *testing.T) {
	n := &Node{}
	t0 := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)

	n.SetConfigSyncState(ConfigSyncPending, t0)
	first := n.ConfigPendingSince
	if first == nil || !first.Equal(t0) {
		t.Fatalf("first pending did not stamp: %v", first)
	}

	// Fifteen minutes later reconcile marks it pending again; then the retries
	// run out and it becomes failed. Neither may move the stamp.
	n.SetConfigSyncState(ConfigSyncPending, t0.Add(15*time.Minute))
	n.SetConfigSyncState(ConfigSyncFailed, t0.Add(72*time.Hour))
	if !n.ConfigPendingSince.Equal(t0) {
		t.Fatalf("stamp moved to %v; the age would restart on every reconcile cycle", n.ConfigPendingSince)
	}

	lag, ok := n.ConfigSyncLag(t0.Add(72 * time.Hour))
	if !ok || lag != 72*time.Hour {
		t.Fatalf("lag = %v (%v), want 72h — this is the number that says whether to wait or to go look", lag, ok)
	}
}

// Converging clears the stamp, so the next failure starts a fresh clock rather
// than inheriting an old incident's age.
func TestConvergingClearsThePendingStamp(t *testing.T) {
	n := &Node{}
	t0 := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)

	n.SetConfigSyncState(ConfigSyncPending, t0)
	n.SetConfigSyncState(ConfigSyncSynced, t0.Add(time.Hour))
	if n.ConfigPendingSince != nil {
		t.Fatalf("synced left a stamp: %v", n.ConfigPendingSince)
	}
	if _, ok := n.ConfigSyncLag(t0.Add(2 * time.Hour)); ok {
		t.Fatal("a converged node must report no lag at all")
	}

	n.SetConfigSyncState(ConfigSyncPending, t0.Add(2*time.Hour))
	if !n.ConfigPendingSince.Equal(t0.Add(2 * time.Hour)) {
		t.Fatal("the new incident must start its own clock")
	}
}

// A converged node and a row written before this column existed both answer
// "no lag" rather than "zero lag". On a dashboard a zero-length lag and an
// unknown one look identical, and only one of them is reassuring.
func TestUnknownLagIsNotZeroLag(t *testing.T) {
	now := time.Now()
	for _, n := range []*Node{
		{},                                   // never captured
		{ConfigSyncState: ConfigSyncSynced},  // converged
		{ConfigSyncState: ConfigSyncPending}, // pre-migration row: stuck, but no stamp
	} {
		if _, ok := n.ConfigSyncLag(now); ok {
			t.Fatalf("state %q reported a lag it cannot know", n.ConfigSyncState)
		}
	}
}
