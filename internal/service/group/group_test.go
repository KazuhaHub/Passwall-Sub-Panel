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
		{"region:tw", true},   // region is case-insensitive
		{"region:JP", false},  // wrong region
		{"tag:reality", true}, // tag: prefix strips, looks up "reality"
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

// TestMatches_TrimsSpaces guards the user-reported bug where typing
// "tag: Premium" (with a space after the colon) silently matched nothing
// — historically the val side kept its leading space and HasTag would
// compare " Premium" to the stored "Premium". Whole-condition,
// pre-colon and post-colon all need to tolerate stray whitespace.
func TestMatches_TrimsSpaces(t *testing.T) {
	node := n("TW", "reality", "server:tw-hinet", "Premium")
	cases := []struct {
		cond string
		want bool
	}{
		{"tag: reality", true},      // space after colon (the reported bug)
		{"tag :reality", true},      // space before colon
		{"  tag : reality  ", true}, // spaces all over
		{"region: TW", true},        // region prefix with space
		{"tag: Premium", true},      // capitalized tag with space (literal report)
		{"server: tw-hinet", true},  // unknown prefix path also normalises
		{" Premium ", true},         // no-colon path: outer whitespace trimmed
		{"tag: missing", false},     // still false when value truly absent
	}
	for _, tc := range cases {
		f := domain.TagFilter{Tags: []string{tc.cond}}
		if got := Matches(node, f); got != tc.want {
			t.Errorf("cond %q: got %v, want %v", tc.cond, got, tc.want)
		}
	}
}

// SameFilter decides whether a group save re-pushes every member to the
// panels, so it must hold exactly where the matcher cannot tell two filters
// apart and must not hold anywhere the selection can differ.
func TestSameFilter(t *testing.T) {
	and := func(tags ...string) domain.TagFilter { return domain.TagFilter{Tags: tags} }
	withMode := func(mode string, tags ...string) domain.TagFilter { return domain.TagFilter{Mode: mode, Tags: tags} }
	for _, tc := range []struct {
		name string
		a, b domain.TagFilter
		same bool
	}{
		{"identical", and("region:JP", "tag:reality"), and("region:JP", "tag:reality"), true},
		{"empty mode is all", withMode("", "tag:a"), withMode("all", "tag:a"), true},
		{"unknown mode is all", withMode("both", "tag:a"), withMode("all", "tag:a"), true},
		{"trimmed like matchOne", and("tag:a", "server:x", "plain"), and("  tag : a ", "server: x", " plain "), true},
		{"order ignored", and("tag:a", "region:JP"), and("region:JP", "tag:a"), true},
		{"repetition ignored", withMode("any", "tag:a"), withMode("any", "tag:a", " tag:a"), true},
		{"nil and empty tags", and(), domain.TagFilter{Tags: []string{}}, true},
		{"all ignores leftover tags", domain.TagFilter{All: true, Tags: []string{"tag:a"}}, domain.TagFilter{All: true, Mode: "any"}, true},

		{"all to conditions", domain.TagFilter{All: true}, and("tag:a"), false},
		{"conditions to all", and("tag:a"), domain.TagFilter{All: true, Tags: []string{"tag:a"}}, false},
		{"all is not folded into empty AND", domain.TagFilter{All: true}, and(), false},
		{"and to any", withMode("all", "tag:a", "tag:b"), withMode("any", "tag:a", "tag:b"), false},
		{"empty mode to any", withMode("", "tag:a"), withMode("any", "tag:a"), false},
		{"condition added", and("tag:a"), and("tag:a", "tag:b"), false},
		{"condition removed", and("tag:a", "tag:b"), and("tag:a"), false},
		{"value changed", and("region:JP"), and("region:US"), false},
		{"case kept", and("tag:A"), and("tag:a"), false},
		{"inner spaces kept", and("tag:a b"), and("tag:ab"), false},
		{"leading colon is a bare tag", and(":a"), and("a"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SameFilter(tc.a, tc.b); got != tc.same {
				t.Fatalf("SameFilter(%+v, %+v) = %v, want %v", tc.a, tc.b, got, tc.same)
			}
			if got := SameFilter(tc.b, tc.a); got != tc.same {
				t.Fatalf("SameFilter is not symmetric for %+v / %+v", tc.a, tc.b)
			}
		})
	}
}

// Every pair SameFilter calls equal must select the same nodes; checked
// against a spread of nodes so the equivalence rests on the matcher itself.
func TestSameFilterAgreesWithMatches(t *testing.T) {
	nodes := []*domain.Node{
		{Region: "JP", Tags: []string{"a", "server:x"}},
		{Region: "US", Tags: []string{"a"}},
		{Region: "JP", Tags: []string{"plain"}},
		{Region: "SG"},
	}
	pairs := [][2]domain.TagFilter{
		{{Tags: []string{"region:JP", "tag:a"}}, {Mode: "all", Tags: []string{" tag : a", "region:JP", "region: JP"}}},
		{{Mode: "any", Tags: []string{"plain", "server:x"}}, {Mode: "any", Tags: []string{"server: x ", " plain"}}},
		{{All: true}, {All: true, Mode: "any"}},
	}
	for i, p := range pairs {
		if !SameFilter(p[0], p[1]) {
			t.Fatalf("pair %d: SameFilter = false, want true", i)
		}
		for j, n := range nodes {
			if Matches(n, p[0]) != Matches(n, p[1]) {
				t.Errorf("pair %d node %d: filters SameFilter calls equal select differently", i, j)
			}
		}
	}
}
