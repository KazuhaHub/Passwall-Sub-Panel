package vlessroute

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func strPtr(v string) *string       { return &v }
func tagsPtr(v ...string) *[]string { return &v }

type remoteCalls struct{ status, reads, writes, inbounds int }

func callsOf(c *testClient) remoteCalls {
	return remoteCalls{c.statusReads, c.reads, c.writes, c.inboundReads}
}

func savedLine(t *testing.T, s *Service, n *testNodeRepo) (*View, domain.VlessRouteLine) {
	t.Helper()
	n.node.Region, n.node.Tags = "US", []string{"edge"}
	view, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{Name: "B", OutboundTag: "direct", Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	return view, view.State.Lines[0]
}

func TestNewRouteLineTakesNodeLabelsWhenOmitted(t *testing.T) {
	s, n, _ := serviceFixture(t)
	_, line := savedLine(t, s, n)
	if line.Region != "US" || !reflect.DeepEqual(line.Tags, []string{"edge"}) {
		t.Fatalf("new line labels %q %v", line.Region, line.Tags)
	}
	n.node.Tags[0] = "mutated"
	if n.node.RouteState.Lines[0].Tags[0] != "edge" {
		t.Fatal("the stored line aliases the node's tags")
	}
}

// ADR 0040 §5 and invariant 3: a display-only change is saved without any
// 3X-UI call and keeps status, error and binding.
func TestDisplayOnlySaveStaysLocalAndKeepsApplied(t *testing.T) {
	s, n, c := serviceFixture(t)
	invalidations := 0
	s.invalidate = func() { invalidations++ }
	view, line := savedLine(t, s, n)
	before, applied, previousInvalidations := callsOf(c), n.node.RouteState, invalidations
	if applied.Status != "applied" {
		t.Fatalf("fixture not applied: %+v", applied)
	}
	view, err := s.Save(context.Background(), 1, SaveRequest{Revision: view.State.Revision, Lines: []LineInput{{
		ID: line.ID, Name: "renamed", OnlyLineName: true, OutboundTag: "direct", Enabled: true,
		Region: strPtr(" JP "), Tags: tagsPtr("b", " a ", "b", ""),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := callsOf(c); got != before {
		t.Fatalf("display-only save contacted 3X-UI: before %+v after %+v", before, got)
	}
	got := n.node.RouteState
	if got.Status != applied.Status || got.Error != applied.Error || got.Binding != applied.Binding || got.Revision != applied.Revision+1 {
		t.Fatalf("local path changed applied state: %+v", got)
	}
	stored := got.Lines[0]
	if stored.Name != "renamed" || !stored.OnlyLineName || stored.Region != "JP" || !reflect.DeepEqual(stored.Tags, []string{"b", "a"}) || stored.Route != line.Route {
		t.Fatalf("stored line %+v", stored)
	}
	if invalidations != previousInvalidations+1 || view.State.Status != "applied" {
		t.Fatalf("subscription cache not invalidated or view stale: %d %+v", invalidations, view)
	}
}

func TestDisplayOnlySaveDoesNotNeedAReachablePanel(t *testing.T) {
	s, n, c := serviceFixture(t)
	view, line := savedLine(t, s, n)
	c.coreState = "stopped" // the remote path refuses a stopped core
	c.readError = true
	if _, err := s.Save(context.Background(), 1, SaveRequest{Revision: view.State.Revision, Lines: []LineInput{{ID: line.ID, Name: "B", OutboundTag: "direct", Enabled: true, Region: strPtr("")}}}); err != nil {
		t.Fatalf("display-only save depended on the panel: %v", err)
	}
	if n.node.RouteState.Lines[0].Region != "" || n.node.RouteState.Status != "applied" {
		t.Fatalf("explicit clear not stored: %+v", n.node.RouteState)
	}
}

// Invariant 2: omitted labels keep the saved value, even after the node's
// labels change; an explicit empty value clears.
func TestOmittedRouteLabelsKeepSavedValuesAfterNodeChanges(t *testing.T) {
	s, n, _ := serviceFixture(t)
	view, line := savedLine(t, s, n)
	n.node.Region, n.node.Tags = "DE", []string{"other"}
	view, err := s.Save(context.Background(), 1, SaveRequest{Revision: view.State.Revision, Lines: []LineInput{{ID: line.ID, Name: "renamed", OutboundTag: "direct", Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := view.State.Lines[0]; got.Region != "US" || !reflect.DeepEqual(got.Tags, []string{"edge"}) {
		t.Fatalf("omitted labels changed: %q %v", got.Region, got.Tags)
	}
	view, err = s.Save(context.Background(), 1, SaveRequest{Revision: view.State.Revision, Lines: []LineInput{{ID: line.ID, Name: "renamed", OutboundTag: "direct", Enabled: true, Tags: tagsPtr()}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := view.State.Lines[0]; got.Region != "US" || got.Tags == nil || len(got.Tags) != 0 {
		t.Fatalf("explicit clear not applied: %q %v", got.Region, got.Tags)
	}
}

func TestUnchangedSaveStillReconfirmsRemotely(t *testing.T) {
	s, n, c := serviceFixture(t)
	view, line := savedLine(t, s, n)
	before := callsOf(c)
	if _, err := s.Save(context.Background(), 1, SaveRequest{Revision: view.State.Revision, Lines: []LineInput{{ID: line.ID, Name: line.Name, OutboundTag: "direct", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	if callsOf(c).status == before.status || callsOf(c).reads == before.reads {
		t.Fatal("an unchanged save skipped the reconfirmation")
	}
}

func TestRuleChangesAndShapeChangesUseTheRemotePath(t *testing.T) {
	cases := map[string]func(line domain.VlessRouteLine) []LineInput{
		"rule and display": func(line domain.VlessRouteLine) []LineInput {
			return []LineInput{{ID: line.ID, Name: "B", OutboundTag: "direct", Enabled: true, Port: "443", Region: strPtr("JP")}}
		},
		"enable toggle": func(line domain.VlessRouteLine) []LineInput {
			return []LineInput{{ID: line.ID, Name: "B", OutboundTag: "direct", Enabled: false, Region: strPtr("JP")}}
		},
		"added line": func(line domain.VlessRouteLine) []LineInput {
			return []LineInput{{ID: line.ID, Name: "B", OutboundTag: "direct", Enabled: true, Region: strPtr("JP")}, {Name: "C", OutboundTag: "direct", Enabled: true}}
		},
		"deleted line": func(domain.VlessRouteLine) []LineInput { return []LineInput{} },
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			s, n, c := serviceFixture(t)
			view, line := savedLine(t, s, n)
			before := callsOf(c)
			if _, err := s.Save(context.Background(), 1, SaveRequest{Revision: view.State.Revision, Lines: input(line)}); err != nil {
				t.Fatal(err)
			}
			if callsOf(c).reads == before.reads {
				t.Fatal("a rule-relevant save took the local path")
			}
		})
	}
}

func TestReorderWithDisplayChangeUsesTheRemotePath(t *testing.T) {
	s, n, c := serviceFixture(t)
	n.node.Region = "US"
	view, err := s.Save(context.Background(), 1, SaveRequest{Lines: []LineInput{{Name: "B", OutboundTag: "direct", Enabled: true}, {Name: "C", OutboundTag: "direct", Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	a, b := view.State.Lines[0], view.State.Lines[1]
	before := callsOf(c)
	if _, err := s.Save(context.Background(), 1, SaveRequest{Revision: view.State.Revision, Lines: []LineInput{
		{ID: b.ID, Name: b.Name, OutboundTag: "direct", Enabled: true, Region: strPtr("JP")},
		{ID: a.ID, Name: a.Name, OutboundTag: "direct", Enabled: true},
	}}); err != nil {
		t.Fatal(err)
	}
	if callsOf(c).reads == before.reads {
		t.Fatal("a reorder took the local path")
	}
}

func TestDisplayOnlySaveChecksRevisionAndLease(t *testing.T) {
	s, n, c := serviceFixture(t)
	view, line := savedLine(t, s, n)
	input := []LineInput{{ID: line.ID, Name: "renamed", OutboundTag: "direct", Enabled: true}}
	before := callsOf(c)
	if _, err := s.Save(context.Background(), 1, SaveRequest{Revision: view.State.Revision - 1, Lines: input}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	n.leaseBusy = true
	if _, err := s.Save(context.Background(), 1, SaveRequest{Revision: view.State.Revision, Lines: input}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("busy lease: %v", err)
	}
	if callsOf(c) != before || n.node.RouteState.Lines[0].Name != "B" {
		t.Fatal("a refused local save changed state or contacted 3X-UI")
	}
}

func TestDisplayOnlySaveValidatesLabels(t *testing.T) {
	s, n, _ := serviceFixture(t)
	view, line := savedLine(t, s, n)
	tooMany := make([]string, domain.MaxLineTags+1)
	for i := range tooMany {
		tooMany[i] = string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	if _, err := s.Save(context.Background(), 1, SaveRequest{Revision: view.State.Revision, Lines: []LineInput{{ID: line.ID, Name: "B", OutboundTag: "direct", Enabled: true, Tags: &tooMany}}}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("over-limit tags: %v", err)
	}
}

// Drift found by maintenance stays visible after a local display edit.
func TestDisplayOnlySaveKeepsNonAppliedStatus(t *testing.T) {
	s, n, _ := serviceFixture(t)
	view, line := savedLine(t, s, n)
	n.node.RouteState.Status, n.node.RouteState.Error = "drift", "remote_rule_changed"
	if _, err := s.Save(context.Background(), 1, SaveRequest{Revision: view.State.Revision, Lines: []LineInput{{ID: line.ID, Name: "renamed", OutboundTag: "direct", Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	if got := n.node.RouteState; got.Status != "drift" || got.Error != "remote_rule_changed" || got.Lines[0].Name != "renamed" {
		t.Fatalf("local path changed status: %+v", got)
	}
}
