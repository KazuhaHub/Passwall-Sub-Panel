package version_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// ALLOCATING A RELEASE NUMBER, IN THIS REPOSITORY.
//
// PSP used to run Passwall Node's allocator for this (`go run
// github.com/KazuhaHub/passwall-node/deployment/cmd/allocate-release-tag`), which
// made its release workflow a consumer of the Node module. The rule is ported
// here rather than moved into the shared protocol module: it is a version tool,
// not a wire contract, and the protocol repository is pure wire data by design.
//
// THE RULE ITSELF IS UNCHANGED, and these cases are the ones the Node side
// already asserts: a number, once bound to a source revision, is never reused; a
// failed build may leave a gap; an incremental fix takes the FOURTH segment; and
// a line's first release is NAMED rather than derived, because nothing in a
// repository knows whether the next release belongs on 4.0 or 4.1.
func TestParseReleaseLineReadsOnlyALine(t *testing.T) {
	for _, ok := range []string{"4.0", "0.1", "102.1", "4.99"} {
		line, err := version.ParseReleaseLine(ok)
		if err != nil {
			t.Fatalf("ParseReleaseLine(%q) = %v", ok, err)
		}
		if line.String() != ok {
			t.Fatalf("ParseReleaseLine(%q) round-tripped to %q", ok, line.String())
		}
	}
	for _, bad := range []string{"4.0.1", "v4.0", "04.0", "4", "", "4.0 ", " 4.0", "4.x", "-4.0", "4.0.0.1"} {
		if _, err := version.ParseReleaseLine(bad); err == nil {
			t.Errorf("ParseReleaseLine(%q) accepted a version or a typo as a line", bad)
		}
	}
}

func TestAllocatingTheNextNumberOnALine(t *testing.T) {
	line, err := version.ParseReleaseLine("4.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		existing []string
		want     string
	}{
		{"the first fix on the released patch", []string{"release/4.0.0"}, "4.0.0.1"},
		{"the next fix", []string{"release/4.0.0", "release/4.0.0.1"}, "4.0.0.2"},
		// THE CASE THE RELEASE PATH ACTUALLY HITS TODAY: 4.0.1 is published, and
		// the next number on the line is a BUILD on it.
		{"above a named patch", []string{"release/4.0.0", "release/4.0.1"}, "4.0.1.1"},
		{"past a two-digit build", []string{"release/4.0.0", "release/4.0.0.9", "release/4.0.0.10"}, "4.0.0.11"},
		// A GAP IS NOT FILLED. Filling one gives two source revisions the same
		// identity, which is what a release number exists to prevent.
		{"past a gap", []string{"release/4.0.0.1", "release/4.0.0.3"}, "4.0.0.4"},
		// OTHER LINES HAVE THEIR OWN NUMBERING — including one that is NUMERICALLY
		// AHEAD of this one, which a rule that counted the highest tag in the
		// repository rather than on the line would get wrong.
		{"other lines are ignored", []string{"release/4.0.0", "release/4.1.0", "release/5.0.0", "v1.2.3"}, "4.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := version.AllocateVersion(line, tc.existing)
			if err != nil {
				t.Fatalf("AllocateVersion = %v", err)
			}
			if got != tc.want {
				t.Fatalf("AllocateVersion = %q, want %q", got, tc.want)
			}
		})
	}
}

// A LINE WITH NOTHING ON IT IS REFUSED rather than given a first number: that
// decision has to be made by a person, and a guess publishes to the wrong line.
func TestAllocatingRefusesWhatItCannotCountFrom(t *testing.T) {
	line, err := version.ParseReleaseLine("4.0")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := version.AllocateVersion(line, []string{"release/4.1.0", "release/5.0.0"})
	if err == nil {
		t.Fatalf("a line with no release on it was allocated %q", empty)
	}
	// AND A TAG THAT IS NOT OURS TAKES NO NUMBER. `release/4.0.oops` cannot be read
	// as a release version, so it is not a release on this line either: it is
	// skipped and the count proceeds from the ones that can be read. Refusing the
	// whole allocation instead would make one stray tag on a remote freeze every
	// future release.
	if got, err := version.AllocateVersion(line, []string{"release/4.0.0", "release/4.0.oops"}); err != nil || got != "4.0.0.1" {
		t.Fatalf("AllocateVersion = %q, %v; want it to skip what it cannot read", got, err)
	}
}

// THE BUILD SEGMENT HAS A CEILING, and reaching it is a refusal rather than a
// rollover into the next patch: the patch is a decision, not an overflow.
func TestAllocatingRefusesToRollTheBuildSegmentOver(t *testing.T) {
	line, err := version.ParseReleaseLine("4.0")
	if err != nil {
		t.Fatal(err)
	}
	atCeiling := fmt.Sprintf("release/4.0.0.%d", version.MaxVersionSegmentBounds)
	if got, err := version.AllocateVersion(line, []string{atCeiling}); err == nil {
		t.Fatalf("the build segment rolled over into %q", got)
	}
}

// A RERUN CONTINUES ITS OWN NUMBER. What records it is the source revision: a tag
// points at a commit, so a rerun asks which tags point at the commit it is about
// to build, and nothing else has to be written down.
func TestResumingTheNumberBoundToASourceRevision(t *testing.T) {
	line, err := version.ParseReleaseLine("4.0")
	if err != nil {
		t.Fatal(err)
	}
	got, err := version.ResumeVersion(line, []string{"release/4.0.0.1", "unrelated-tag"})
	if err != nil || got != "4.0.0.1" {
		t.Fatalf("ResumeVersion = %q, %v; want the number bound to this revision", got, err)
	}
	// NOTHING BOUND TO IT is not an error but a distinct answer: allocate one.
	if _, err := version.ResumeVersion(line, []string{"release/4.1.0"}); !errors.Is(err, version.ErrVersionNotAllocated) {
		t.Fatalf("an unallocated revision reported %v, want ErrVersionNotAllocated", err)
	}
	// TWO CANDIDATES IS REFUSED RATHER THAN RESOLVED. Two tags on one commit, on
	// one line, are two releases given the same source, and choosing between them
	// is deciding which of somebody's releases does not count.
	if _, err := version.ResumeVersion(line, []string{"release/4.0.0.1", "release/4.0.0.2"}); !errors.Is(err, version.ErrAmbiguousLineRevision) {
		t.Fatalf("two candidates reported %v, want ErrAmbiguousLineRevision", err)
	}
}
