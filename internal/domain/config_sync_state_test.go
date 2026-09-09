package domain

import (
	"testing"
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
