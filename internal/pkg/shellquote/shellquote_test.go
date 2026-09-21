package shellquote_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/shellquote"
)

// QUOTING IS TESTED BY THE SHELL, not by comparing strings: what matters is what
// a shell reads back, and a table of expected literals would only restate the
// implementation.
func TestAShellReadsBackTheValueItWasGiven(t *testing.T) {
	for _, value := range []string{
		"plain",
		"",
		"with space",
		"with'quote",
		"'''",
		"`backtick`",
		"$(command)",
		"; rm -rf /",
		`"double"`,
		"back\\slash",
		"new\nline",
		"tab\there",
		"${VAR}",
		"a'b\"c`d$e;f&g|h",
	} {
		quoted := shellquote.Quote(value)
		// `printf %s` prints the argument the shell produced, with no trailing
		// newline of its own, so this compares exactly what the shell read.
		out, err := exec.Command("sh", "-c", "printf %s "+quoted).Output()
		if err != nil {
			t.Fatalf("sh refused %q quoted as %s: %v", value, quoted, err)
		}
		if string(out) != value {
			t.Fatalf("a shell read %q back as %q", value, string(out))
		}
	}
}

// AND THE SHELL DID NOT INTERPRET IT. A value that expands, substitutes or
// separates commands would change the output above only if the command were
// something other than a plain `printf`; this asserts the shape of the shell line
// itself, so a caller's own concatenation cannot smuggle an operator in.
func TestTheQuotedFormCarriesNoUnquotedOperator(t *testing.T) {
	quoted := shellquote.Quote("x; echo injected")
	if strings.Contains(strings.Trim(quoted, "'"), ";") == false {
		t.Fatalf("the fixture no longer contains an operator, so this case tests nothing: %q", quoted)
	}
	out, err := exec.Command("sh", "-c", "printf %s "+quoted).Output()
	if err != nil || string(out) != "x; echo injected" {
		t.Fatalf("an operator survived quoting: %q (%v)", string(out), err)
	}
}
