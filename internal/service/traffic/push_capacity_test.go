package traffic

import (
	"fmt"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/paneltz"
)

// The diagnostics page shows a metric's help string to the operator verbatim,
// and psp_push_sem_capacity's states the number. The capacity is NOT a
// setting: New fixes it at the default panel concurrency before any settings
// are attached. So a change to that default has to change the help in the
// same commit, or the page states one capacity while the gauge reads another
// (the help used to call it "configured", which sent operators looking for a
// setting that does not exist).
func TestNew_PushSemCapacityHelpStatesTheCapacityNewPublishes(t *testing.T) {
	metrics.Reset()
	svc := New(nil, nil, nil, nil, nil, nil, nil)
	want := paneltz.ResolveMaxPanelConcurrency(0)
	if got := cap(svc.pushSem); got != want {
		t.Fatalf("push semaphore capacity = %d, want the default panel concurrency %d", got, want)
	}

	var found bool
	for _, g := range metrics.Take().Gauges {
		if g.Name != "psp_push_sem_capacity" {
			continue
		}
		found = true
		if g.Value != int64(want) {
			t.Errorf("psp_push_sem_capacity = %d, want %d", g.Value, want)
		}
		if !strings.Contains(g.Help, fmt.Sprintf("(%d)", want)) {
			t.Errorf("help %q does not state the capacity (%d) the gauge reads", g.Help, want)
		}
		if strings.Contains(strings.ToLower(g.Help), "configured") {
			t.Errorf("help %q calls a fixed capacity configured", g.Help)
		}
	}
	if !found {
		t.Fatal("psp_push_sem_capacity is not registered")
	}
}
