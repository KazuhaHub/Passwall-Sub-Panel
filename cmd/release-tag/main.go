// Command release-tag prints the VERSION a release tag names.
//
// IT EXISTS SO PSP DOES NOT HAVE TO ASK PASSWALL NODE. The release workflow used
// to run the Node module's equivalent, which made PSP's publication path a
// consumer of the Node module — and a `go run <module>/pkg` keeps that module in
// PSP's go.mod, so the dependency could not be dropped while this was outsourced.
//
// ON SUCCESS IT PRINTS THE VERSION AND NOTHING ELSE. The workflow needs both
// identities — the tag for the git ref and the download URL, the version for the
// build stamp, the archive name and the image tag — and the second is derivable
// from the first. Deriving it here rather than in shell keeps one implementation
// of a rule whose whole point is that there is one, and printing only the version
// means `$(...)` cannot capture a banner into a filename.
//
// A REFUSAL PRINTS NOTHING ON STDOUT, so a caller that ignores the exit status
// still cannot build with an empty or invented version.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

func main() {
	args := os.Args[1:]
	publish := len(args) == 2 && args[0] == "--publish"
	if publish {
		args = args[1:]
	}
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "release requires an explicit tag: vMAJOR.MINOR.PATCH[.BUILD]")
		os.Exit(1)
	}
	released, ok := version.VersionOfReleaseTag(args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "not a publishable release tag: %q\n", args[0])
		os.Exit(1)
	}
	if publish && (strings.HasPrefix(args[0], "release/") || len(strings.Split(strings.SplitN(released, "-", 2)[0], ".")) != 3) {
		fmt.Fprintln(os.Stderr, "new releases require vMAJOR.MINOR.PATCH with optional -beta.N or -rc.N")
		os.Exit(1)
	}
	fmt.Println(released)
}
