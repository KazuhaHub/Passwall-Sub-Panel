package domain_test

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// A CORE VERSION IS AN IDENTITY THE PANEL COMPARES TWO WAYS: against the release a
// document publishes, and against the version a node reports about itself. One
// normalizer, because two would eventually disagree about a shape — and the
// disagreement would read as "the node is running an unknown core".
//
// IT IS NOT THE RELEASE RULE. `4.0.0.1` is a product release and is not a core
// version; a core version is three segments, and the leading v upstream tags
// releases with is accepted on input because operators paste what they see.
func TestNormalizeCoreVersionAcceptsWhatUpstreamPublishes(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"26.6.27", "26.6.27"},
		{"v26.6.27", "26.6.27"},
		// Surrounding whitespace is not the caller getting the identity wrong — it
		// is a form field, and the release it names is unambiguous.
		{" 26.6.27 ", "26.6.27"},
		{"1.14.0", "1.14.0"},
		{"0.0.0", "0.0.0"},
	} {
		got, err := domain.NormalizeCoreVersion(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("NormalizeCoreVersion(%q) = (%q, %v), want %q", tc.in, got, err, tc.want)
		}
	}
}

// EVERYTHING ELSE IS REFUSED, because a version this cannot read is a version the
// panel cannot compare — and a comparison that silently fails is how a node ends
// up offered a release nobody reviewed.
func TestNormalizeCoreVersionRefusesEverythingElse(t *testing.T) {
	for _, bad := range []string{
		"", "latest", "26.6", "26", "26.6.27.1", "v", "26.6.x", "026.6.27", "26.06.27",
		"26.6.-1", "26.6.27-beta", "-26.6.27", "2 6.6.27",
	} {
		if got, err := domain.NormalizeCoreVersion(bad); err == nil {
			t.Errorf("NormalizeCoreVersion(%q) accepted %q", bad, got)
		}
	}
}
