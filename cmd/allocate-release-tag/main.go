// Command allocate-release-tag picks the tag a release should be published under:
// the one this source revision was already allocated, or the next number on the
// line it names.
//
// IT READS THE NUMBERS; IT DOES NOT CREATE THE TAG. The rule is that the release
// job allocates serially and then confirms with the atomic result of creating an
// immutable tag: if the tag already exists the allocation lost a race, and the
// answer is to re-read rather than to overwrite. That confirmation is the
// workflow's, because it is the only place with the repository in hand — this
// command answers the question it can answer, and prints one tag.
//
// IT IS PSP'S OWN, AND THAT IS THE POINT. This used to be Passwall Node's
// command, run with `go run`, which kept the Node module in PSP's go.mod. The
// RULE is the same rule — see internal/version's release-line file — and the
// shared test vectors remain the contract between the two implementations.
//
// A REFUSAL PRINTS NOTHING ON STDOUT, so a caller that reads the output without
// checking the exit status cannot capture an empty string and build with it.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

func main() {
	line := flag.String("line", "", "release line, MAJOR.MINOR (for example 4.0)")
	existing := flag.String("existing", "", "existing release tags, whitespace- or comma-separated")
	onCommit := flag.String("on-commit", "", "tags pointing at the revision being released; one already on this line is resumed rather than replaced")
	flag.Parse()

	parsedLine, err := version.ParseReleaseLine(*line)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// A NUMBER ALREADY BOUND TO THIS SOURCE REVISION IS RESUMED, not replaced: a
	// rerun of a failed release continues its own number, and the tag pointing at
	// the commit is what records which one that is.
	released, err := version.ResumeVersion(parsedLine, splitTags(*onCommit))
	if err == nil {
		tag, ok := version.ReleaseTagFor(released)
		if !ok {
			fmt.Fprintf(os.Stderr, "%q is not a publishable version\n", released)
			os.Exit(1)
		}
		fmt.Println(tag)
		return
	}
	if !errors.Is(err, version.ErrVersionNotAllocated) {
		// AN AMBIGUITY IS A REFUSAL, NOT A LICENCE TO ALLOCATE ANOTHER NUMBER. The
		// commit already carries two tags on this line, and adding a third is the
		// opposite of the decision somebody has to make.
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	allocated, err := version.AllocateVersion(parsedLine, splitTags(*existing))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tag, ok := version.ReleaseTagFor(allocated)
	if !ok {
		fmt.Fprintf(os.Stderr, "%q is not a publishable version\n", allocated)
		os.Exit(1)
	}
	fmt.Println(tag)
}

// splitTags accepts what a shell hands over: newlines from a tag listing, commas
// from a hand-written list, and both at once.
func splitTags(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == ',' || r == ' ' || r == '\t' || r == '\r'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
