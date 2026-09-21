package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Tested by RUNNING the command that ships, for the same reason the Node side's
// equivalent is: the caller is a workflow, and there the only observable is what
// the process does.
var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "release-tag")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "release-tag")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		os.RemoveAll(dir)
		panic("building release-tag: " + err.Error())
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return 0, stdout.String(), stderr.String()
	}
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("running release-tag %v: %v", args, err)
	}
	return exit.ExitCode(), stdout.String(), stderr.String()
}

// A REFUSAL PRINTS NOTHING ON STDOUT, so a caller that ignores the exit status
// cannot capture an empty string and build with it.
func TestItAllocatesTheNextNumberOnTheLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"the first fix", []string{"-line", "4.0", "-existing", "v4.0.0"}, "v4.0.0.1"},
		// THE CASE THE RELEASE PATH HITS TODAY. The tags on the line are the four
		// published before the address changed, and the number allocated is
		// published under the current namespace: an allocation is always a release
		// that has not gone out yet.
		{"above a published patch", []string{"-line", "4.0", "-existing", "release/4.0.0\nrelease/4.0.1\n"}, "v4.0.1.1"},
		{"a shell hands over commas", []string{"-line", "4.0", "-existing", "v4.0.0,v4.0.0.1"}, "v4.0.0.2"},
		{"and spaces", []string{"-line", "4.0", "-existing", "v4.0.0 v4.0.0.1"}, "v4.0.0.2"},
		// A RERUN CONTINUES ITS OWN ADDRESS, not a derived one: the number was
		// bound to a revision by a tag, and that tag is what the release is
		// addressed by.
		{"a rerun resumes its own number", []string{"-line", "4.0", "-existing", "v4.0.0", "-on-commit", "v4.0.0.1"}, "v4.0.0.1"},
		{"a rerun of a release published before the address changed", []string{"-line", "4.0", "-existing", "release/4.0.1\n", "-on-commit", "release/4.0.1.1"}, "release/4.0.1.1"},
		// THE TWO NAMESPACES ARE ONE LINE, so the numbering runs through the
		// address change rather than restarting at it.
		{"across the address change", []string{"-line", "4.0", "-existing", "release/4.0.0 release/4.0.1 release/4.0.1.1 v4.0.1.2"}, "v4.0.1.3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := run(t, tc.args...)
			if code != 0 {
				t.Fatalf("refused: %s", stderr)
			}
			if stdout != tc.want+"\n" {
				t.Fatalf("printed %q, want exactly %q", stdout, tc.want+"\n")
			}
		})
	}
}

func TestARefusalPrintsNothingOnStdout(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"a line with nothing on it", []string{"-line", "4.0", "-existing", "release/4.1.0"}},
		{"no line", []string{"-existing", "release/4.0.0"}},
		{"a version where a line belongs", []string{"-line", "4.0.1", "-existing", "release/4.0.0"}},
		{"two numbers on one revision", []string{"-line", "4.0", "-on-commit", "release/4.0.0.1,release/4.0.0.2", "-existing", "release/4.0.0.1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := run(t, tc.args...)
			if code == 0 {
				t.Fatalf("accepted, printing %q", stdout)
			}
			if strings.TrimSpace(stdout) != "" {
				t.Fatalf("a refusal printed %q on stdout, which a caller would read as a tag", stdout)
			}
			if strings.TrimSpace(stderr) == "" {
				t.Fatal("a refusal must say why; the maintainer sees only this")
			}
		})
	}
}
