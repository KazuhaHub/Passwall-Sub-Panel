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

func TestResolveRouteLineLabels(t *testing.T) {
	prior := VlessRouteLine{ID: "a", Region: "SG", Tags: []string{"stream"}}
	line := VlessRouteLine{ID: "a", RegionUnresolved: true, Tags: []string{}}
	ResolveRouteLineLabels(&line, &prior, "US", []string{"edge"})
	if line.Region != "SG" || len(line.Tags) != 0 || line.RegionUnresolved || line.TagsUnresolved {
		t.Fatalf("existing line: %+v", line)
	}
	fresh := VlessRouteLine{RegionUnresolved: true, TagsUnresolved: true}
	ResolveRouteLineLabels(&fresh, nil, "US", nil)
	if fresh.Region != "US" || fresh.Tags == nil || len(fresh.Tags) != 0 {
		t.Fatalf("new line: %+v", fresh)
	}
	if err := CheckRouteLabelsResolved([]VlessRouteLine{{RegionUnresolved: true}}); !errors.Is(err, ErrUnresolvedLineLabels) {
		t.Fatal("storage guard accepted an unresolved line")
	}
}

// Locks the display-field set of ADR 0040 §5. Every other field, including
// fields added later, must count as rule-relevant so it requires the remote
// path; extending the set means editing splitRouteLineDisplay deliberately.
func TestRouteLineDisplayFieldsAreExactlyNameOnlyNameRegionTags(t *testing.T) {
	display := map[string]bool{"Name": true, "OnlyLineName": true, "Region": true, "Tags": true}
	markers := map[string]bool{"RegionUnresolved": true, "TagsUnresolved": true}
	base := VlessRouteLine{ID: "a", OutboundTag: "direct", Route: 7, Enabled: true, Tags: []string{}}
	typ := reflect.TypeOf(base)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if markers[field.Name] {
			continue
		}
		changed := base
		value := reflect.ValueOf(&changed).Elem().Field(i)
		switch value.Kind() {
		case reflect.String:
			value.SetString(value.String() + "x")
		case reflect.Bool:
			value.SetBool(!value.Bool())
		case reflect.Uint16:
			value.SetUint(value.Uint() + 1)
		case reflect.Slice:
			value.Set(reflect.ValueOf([]string{"x"}))
		default:
			t.Fatalf("unhandled field kind for %s; classify it explicitly", field.Name)
		}
		sameRule, displayChanged := CompareRouteLineDisplay(base, changed)
		if display[field.Name] && (!sameRule || !displayChanged) {
			t.Fatalf("%s should be display-only", field.Name)
		}
		if !display[field.Name] && sameRule {
			t.Fatalf("%s must be rule-relevant", field.Name)
		}
	}
	if sameRule, changed := CompareRouteLineDisplay(base, base); !sameRule || changed {
		t.Fatal("an unchanged line compared as changed")
	}
	nilTags := base
	nilTags.Tags = nil
	if _, changed := CompareRouteLineDisplay(base, nilTags); changed {
		t.Fatal("nil and empty tags must compare equal")
	}
}
