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

// THE VERSION IS THE THING THE BUILD NEEDS; the tag is the address it came from.
// Printing only the version means `$(...)` cannot capture a banner into an
// archive name, and a refusal prints nothing so a caller that ignores the exit
// status still cannot build with an invented version.
func TestItPrintsTheVersionAndNothingElse(t *testing.T) {
	for _, tc := range []struct{ tag, want string }{
		{"release/4.0.0", "4.0.0"},
		{"release/4.0.1", "4.0.1"},
		{"release/4.0.1.1", "4.0.1.1"},
		{"release/102.1.0", "102.1.0"},
	} {
		code, stdout, stderr := run(t, tc.tag)
		if code != 0 {
			t.Fatalf("%s: refused: %s", tc.tag, stderr)
		}
		if stdout != tc.want+"\n" {
			t.Fatalf("%s printed %q, want exactly %q", tc.tag, stdout, tc.want+"\n")
		}
	}
}

func TestARefusalPrintsNothingOnStdout(t *testing.T) {
	for _, tag := range []string{
		"4.0.1",           // a version where a tag belongs
		"latest",          // not an address at all
		"release/4.0",     // shorthand is not an identity
		"release/04.0.0",  // a leading zero
		"release/4.0.0.0", // a literal zero build
		"v4.0.1",          // the scheme this project does not publish
		"",                // nothing
	} {
		code, stdout, stderr := run(t, tag)
		if code == 0 {
			t.Errorf("%q was accepted, printing %q", tag, stdout)
		}
		if strings.TrimSpace(stdout) != "" {
			t.Errorf("%q printed %q on stdout, which a caller would read as a version", tag, stdout)
		}
		if strings.TrimSpace(stderr) == "" {
			t.Errorf("%q was refused with no reason; the maintainer sees only this", tag)
		}
	}
	// AND AN ARGUMENT IS REQUIRED: defaulting to something would derive a version
	// from nothing.
	if code, _, _ := run(t); code == 0 {
		t.Error("the command accepted no argument at all")
	}
}
