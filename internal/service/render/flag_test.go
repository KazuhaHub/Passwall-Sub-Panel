package render

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestApplyRegionFlagPrefix(t *testing.T) {
	items := []renderItem{
		{node: &domain.Node{Region: "TW", DisplayName: "TW-01"}, name: "TW-01"},
		{node: &domain.Node{Region: "JP", DisplayName: "JP-01"}, name: "JP-01"},
		{node: &domain.Node{Region: "", DisplayName: "NoRegion"}, name: "NoRegion"}, // no flag
		{node: &domain.Node{Region: "XX", DisplayName: "Bogus"}, name: "Bogus"},     // valid letters, renders as letter-boxes; we still prefix
		{isSeparator: true, name: "── Premium ──"},                                  // untouched
	}
	applyRegionFlagPrefix(items)

	wants := []string{
		"🇹🇼 TW-01",
		"🇯🇵 JP-01",
		"NoRegion",      // empty region → unchanged
		"🇽🇽 Bogus",     // any 2 letters get prefixed; client renders best-effort
		"── Premium ──", // separator untouched
	}
	for i, w := range wants {
		if items[i].name != w {
			t.Errorf("items[%d].name = %q, want %q", i, items[i].name, w)
		}
	}
}

func TestRegionFlagEmoji(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"TW", "🇹🇼"},
		{"tw", "🇹🇼"}, // case-insensitive
		{"JP", "🇯🇵"},
		{"US", "🇺🇸"},
		{"", ""},     // empty
		{"T", ""},    // too short
		{"TWN", ""},  // too long (ISO alpha-3 not supported)
		{"T1", ""},   // non-letter
		{"😀", ""},   // non-ASCII
		{"  ", ""},   // whitespace
	}
	for _, tc := range cases {
		if got := regionFlagEmoji(tc.in); got != tc.want {
			t.Errorf("regionFlagEmoji(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
