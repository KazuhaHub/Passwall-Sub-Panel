package group

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// node helpers — short factory so each test case spells its intent inline.
// Note: tags here are the literal node.Tags entries, NOT condition strings.
// A condition like "tag:reality" matches a node whose Tags contains "reality"
// (the matcher strips the "tag:" prefix when looking up).
func n(region string, tags ...string) *domain.Node {
	return &domain.Node{Region: region, Tags: tags}
}

func TestMatches_All_AlwaysTrue(t *testing.T) {
	cases := []*domain.Node{
		n(""),
		n("TW"),
		n("JP", "reality"),
	}
	for _, node := range cases {
		if !Matches(node, domain.TagFilter{All: true}) {
			t.Fatalf("All=true should match node %+v", node)
		}
	}
}

func TestMatches_AND_RequiresEveryCondition(t *testing.T) {
	tw := n("TW", "reality", "premium")
	jp := n("JP", "reality")
	twWs := n("TW", "ws")

	cases := []struct {
		name string
		f    domain.TagFilter
		want map[string]bool // node label → expected
	}{
		{
			name: "region+tag both required (default mode)",
			f:    domain.TagFilter{Tags: []string{"region:TW", "tag:reality"}},
			want: map[string]bool{"tw": true, "jp": false, "twWs": false},
		},
		{
			name: "explicit mode=all behaves the same",
			f:    domain.TagFilter{Mode: "all", Tags: []string{"region:TW", "tag:reality"}},
			want: map[string]bool{"tw": true, "jp": false, "twWs": false},
		},
		{
			name: "empty Tags under AND is vacuously true",
			f:    domain.TagFilter{Tags: nil},
			want: map[string]bool{"tw": true, "jp": true, "twWs": true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nodes := map[string]*domain.Node{"tw": tw, "jp": jp, "twWs": twWs}
			for label, node := range nodes {
				if got := Matches(node, tc.f); got != tc.want[label] {
					t.Errorf("Matches(%s) = %v, want %v", label, got, tc.want[label])
				}
			}
		})
	}
}

func TestMatches_OR_AnyConditionSuffices(t *testing.T) {
	tw := n("TW", "reality")
	jp := n("JP", "reality")
	hk := n("HK", "ws")

	f := domain.TagFilter{Mode: "any", Tags: []string{"region:TW", "region:JP"}}
	wants := map[*domain.Node]bool{tw: true, jp: true, hk: false}
	for node, want := range wants {
		if got := Matches(node, f); got != want {
			t.Errorf("Matches(%v) = %v, want %v", node, got, want)
		}
	}
}

func TestMatches_OR_EmptyTagsMatchesNothing(t *testing.T) {
	// Vacuous OR over an empty list should be false — opposite of vacuous
	// AND. Otherwise an admin saving an empty OR filter would silently
	// match every enabled node, which is the AND-all semantics and would
	// surprise the admin who explicitly picked OR.
	f := domain.TagFilter{Mode: "any", Tags: nil}
	if Matches(n("TW", "reality"), f) {
		t.Fatal("empty OR filter should match nothing")
	}
}

func TestMatches_CondTypes(t *testing.T) {
	// Node has plain tag "reality", server-prefixed tag "server:tw-hinet",
	// and a bare tag "premium". Conditions exercise each match path.
	node := n("TW", "reality", "server:tw-hinet", "premium")
	cases := []struct {
		cond string
		want bool
	}{
		{"region:TW", true},
		{"region:tw", true},     // region is case-insensitive
		{"region:JP", false},    // wrong region
		{"tag:reality", true},   // tag: prefix strips, looks up "reality"
		{"tag:missing", false},
		{"server:tw-hinet", true}, // unknown prefix → literal tag match (full string in node.Tags)
		{"server:other", false},
		{"premium", true}, // no colon → literal tag lookup
		{"none", false},
	}
	for _, tc := range cases {
		f := domain.TagFilter{Tags: []string{tc.cond}}
		if got := Matches(node, f); got != tc.want {
			t.Errorf("cond %q: got %v, want %v", tc.cond, got, tc.want)
		}
	}
}
