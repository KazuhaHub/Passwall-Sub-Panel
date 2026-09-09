package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// The tri-state encoding is the one §8 calls out by name, because the repo
// already has the collision it forbids: traffic_cap.go returns 0 for "no
// headroom" and the panel reads 0 as unlimited, so "exhausted" and "no cap"
// are one value. In JSON the trap is `omitempty`, which would erase the
// difference between 0 and absent.
func TestHeadroomTriStateSurvivesJSON(t *testing.T) {
	zero := int64(0)
	n := int64(5368709120)
	cases := []struct {
		name string
		in   *int64
		want string
	}{
		{"no limit configured", nil, `"headroom_bytes":null`},
		{"configured and exhausted", &zero, `"headroom_bytes":0`},
		{"bytes remaining", &n, `"headroom_bytes":5368709120`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(QuotaEntry{Client: NewClientKey(1), HeadroomBytes: tc.in})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !strings.Contains(string(b), tc.want) {
				t.Fatalf("got %s, want it to contain %s — the three states must stay distinguishable on the wire", b, tc.want)
			}
		})
	}

	// And back: absent must not decode as zero.
	var got QuotaEntry
	if err := json.Unmarshal([]byte(`{"client":"cli_1"}`), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.HeadroomBytes != nil {
		t.Fatalf("absent headroom decoded as %v — 'never configured' became a number", *got.HeadroomBytes)
	}
}

// Freshness must not be able to reach an ETag. The structural guarantee is that
// these fields live on Envelope; this pins that nobody later moves one into a
// segment body, where it would re-mint every round and cancel the skip.
func TestSegmentBodiesCarryNoTimestamps(t *testing.T) {
	for _, tc := range []struct {
		name string
		body any
	}{
		{"config", ConfigBody{}},
		{"roster", RosterBody{}},
		{"directives", DirectivesBody{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.body)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			for _, banned := range []string{"_at_ms", "_age_ms", "timestamp", "computed_at"} {
				if strings.Contains(string(b), banned) {
					t.Errorf("%s body contains %q: a per-round value inside a segment changes its digest every round, re-mints it every round, and cancels the zero-payload steady state", tc.name, banned)
				}
			}
		})
	}
}

// The agent reports observations; it never states desired configuration. This
// is §5's second hard constraint, and here it is checkable by looking at the
// serialized shape rather than by reviewing every future change.
func TestNodeReportStatesNoDesiredConfig(t *testing.T) {
	b, err := json.Marshal(NodeReport{AgentID: "a"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Probe target fields especially: ADR 0025 debt 3(d) is exactly a column
	// that became both desired and observed. The protocol must not offer a
	// place to say them.
	for _, banned := range []string{"port", "protocol", "listen", "server_address", "config", "desired"} {
		if strings.Contains(string(b), banned) {
			t.Errorf("NodeReport can express %q — PSP would swallow it as a new desired value and confirmation would degenerate into agreeing with itself", banned)
		}
	}
}

// A bare monotonic counter has no way back from a database restore. The pair
// does, and this is the case that motivated it.
func TestVersionEpochRecoversFromARestore(t *testing.T) {
	applied := Version{Epoch: 1, Version: 412}
	// PSP restored from backup: same epoch, counter back to 1.
	if (Version{Epoch: 1, Version: 1}).Newer(applied) {
		t.Fatal("a lower version in the same epoch must not be accepted")
	}
	// PSP rebuilt the document row, so the epoch advanced.
	if !(Version{Epoch: 2, Version: 1}).Newer(applied) {
		t.Fatal("a higher epoch must be accepted even at version 1 — without this the agent rejects every future version and serves stale config until someone reinstalls it")
	}
}

// Convergence is judged on content, not on the version number, so a rollback to
// previously-seen content does not report drift that does not exist.
func TestConvergenceIsJudgedOnContent(t *testing.T) {
	if !Converged("sha-A", "sha-A") {
		t.Error("equal content must read as converged even when versions differ")
	}
	if Converged("", "sha-A") {
		t.Error("an agent that holds nothing must never read as converged")
	}
	if Converged("sha-A", "sha-B") {
		t.Error("different content must read as not converged")
	}
}

// Keys are row ids. A key that does not parse means the agent is talking about
// something PSP never minted — reportable, never guessable.
func TestKeysRoundTripAndRejectNonCanonical(t *testing.T) {
	if got, err := NewClientKey(10234).RowID(); err != nil || got != 10234 {
		t.Fatalf("round trip = (%d, %v), want (10234, nil)", got, err)
	}
	for _, bad := range []ClientKey{"cli_", "cli_007", "cli_+7", "cli_x", "lst_7", "7"} {
		if _, err := bad.RowID(); err == nil {
			t.Errorf("%q parsed — two spellings of one row id would let one object hold two identities in a membership set, and membership is how deletion is expressed", bad)
		}
	}
}
