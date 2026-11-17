package domain

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeLineLabelsTrimDedupeKeepOrderAndCase(t *testing.T) {
	region, err := NormalizeLineRegion("  hk ")
	if err != nil || region != "hk" {
		t.Fatalf("region %q %v", region, err)
	}
	tags, err := NormalizeLineTags([]string{" b ", "", "A", "b", "a", "  "})
	if err != nil || !reflect.DeepEqual(tags, []string{"b", "A", "a"}) {
		t.Fatalf("tags %v %v", tags, err)
	}
	if tags, err := NormalizeLineTags(nil); err != nil || tags == nil || len(tags) != 0 {
		t.Fatal("an explicit empty list must normalize to a non-nil empty slice")
	}
}

func TestNormalizeLineLabelsBounds(t *testing.T) {
	if _, err := NormalizeLineRegion(strings.Repeat("x", MaxLineRegionBytes)); err != nil {
		t.Fatal(err)
	}
	if _, err := NormalizeLineRegion(strings.Repeat("x", MaxLineRegionBytes+1)); !errors.Is(err, ErrValidation) {
		t.Fatal("over-long region accepted")
	}
	if _, err := NormalizeLineTags([]string{strings.Repeat("y", MaxLineTagBytes+1)}); !errors.Is(err, ErrValidation) {
		t.Fatal("over-long tag accepted")
	}
	many := make([]string, MaxLineTags+1)
	for i := range many {
		many[i] = strings.Repeat("t", i+1)
	}
	if _, err := NormalizeLineTags(many[:MaxLineTags]); err != nil {
		t.Fatal(err)
	}
	if _, err := NormalizeLineTags(many); !errors.Is(err, ErrValidation) {
		t.Fatal("too many tags accepted")
	}
	// Duplicates do not count against the limit.
	if _, err := NormalizeLineTags(append(many[:MaxLineTags:MaxLineTags], many[0])); err != nil {
		t.Fatal(err)
	}
}

// Invariant 2: an omitted field never clears a saved value; a new line takes
// the node's value; an explicit value always wins.
func TestResolveRelayLabels(t *testing.T) {
	previous := []RelayLine{{ID: "old", Region: "JP", Tags: []string{"cmcc"}}}
	next := []RelayLine{
		{ID: "old", RegionUnresolved: true, TagsUnresolved: true},
		{ID: "new", RegionUnresolved: true, TagsUnresolved: true},
		{ID: "explicit", Region: "", Tags: []string{}},
	}
	nodeTags := []string{"edge"}
	ResolveRelayLabels(previous, next, "US", nodeTags)
	want := []RelayLine{
		{ID: "old", Region: "JP", Tags: []string{"cmcc"}},
		{ID: "new", Region: "US", Tags: []string{"edge"}},
		{ID: "explicit", Region: "", Tags: []string{}},
	}
	if !reflect.DeepEqual(next, want) {
		t.Fatalf("got %+v", next)
	}
	nodeTags[0] = "mutated"
	if next[1].Tags[0] != "edge" {
		t.Fatal("a new line aliases the node's tag slice")
	}
	if err := CheckRelayLabelsResolved(next); err != nil {
		t.Fatal(err)
	}
	if err := CheckRelayLabelsResolved([]RelayLine{{TagsUnresolved: true}}); !errors.Is(err, ErrUnresolvedLineLabels) {
		t.Fatal("storage guard accepted an unresolved line")
	}
}
