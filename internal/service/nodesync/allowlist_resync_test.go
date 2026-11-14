package nodesync

import (
	"context"
	"testing"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

// A node that starts or stops being able to enforce an allowlist changes which
// groups' members may have a client on it, so the members are resynced. Only
// the two capabilities that decide that count: task capabilities come and go
// from report to report and are not a reason to resync anyone.
func TestCapabilityFlipTriggersAllowlistResync(t *testing.T) {
	f := newConfigAppliedFixture(t)
	var panels []int64
	f.service.SetAllowlistResyncer(func(_ context.Context, panelID int64) { panels = append(panels, panelID) })

	report := func(caps ...string) {
		t.Helper()
		if _, err := f.syncWith(t, func(r *nodeprotocol.NodeReport) { r.Capabilities = caps }); err != nil {
			t.Fatal(err)
		}
	}
	expect := func(step string, want int) {
		t.Helper()
		if len(panels) != want {
			t.Fatalf("%s: %d resyncs so far, want %d", step, len(panels), want)
		}
	}

	report() // never declared either: nothing changed
	expect("no capabilities", 0)

	report(nodeprotocol.CapabilityDestinationPolicy) // appears
	expect("policy capability appears", 1)
	if panels[0] != f.agent.PanelID {
		t.Fatalf("resynced panel %d, want the agent's panel %d", panels[0], f.agent.PanelID)
	}

	report(nodeprotocol.CapabilityDestinationPolicy) // same report again
	expect("unchanged report", 1)

	report(nodeprotocol.CapabilityDestinationPolicy, nodeprotocol.CapabilityTaskExecutionV1) // a task capability arrives
	expect("task capability appears", 1)

	report(nodeprotocol.CapabilityDestinationPolicy, nodeprotocol.CapabilityDestinationGroups) // groups appears
	expect("groups capability appears", 2)

	report(nodeprotocol.CapabilityDestinationPolicy, nodeprotocol.CapabilityHostTelemetry) // groups disappears
	expect("groups capability disappears", 3)

	report() // policy disappears
	expect("policy capability disappears", 4)
}

// With no resyncer wired the report still ingests (the heal sweep converges the
// members).
func TestCapabilityFlipWithoutAResyncerIsHarmless(t *testing.T) {
	f := newConfigAppliedFixture(t)
	if _, err := f.syncWith(t, func(r *nodeprotocol.NodeReport) {
		r.Capabilities = []string{nodeprotocol.CapabilityDestinationPolicy}
	}); err != nil {
		t.Fatal(err)
	}
}
