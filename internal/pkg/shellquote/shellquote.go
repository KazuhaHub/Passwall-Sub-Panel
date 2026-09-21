// Package shellquote quotes a value so a POSIX shell reads it as one literal
// word.
//
// ONE IMPLEMENTATION, because the failure this prevents is a command injection
// rather than a wrong answer. Two callers had their own copy of this three-line
// rule and a third was about to: they agreed, but a rule copied per call site is a
// rule that stops agreeing the first time somebody "simplifies" one of them, and
// the difference shows up only for a value containing the character that copy
// forgot.
package shellquote

import "strings"

// Quote wraps a value in single quotes, closing and reopening around any single
// quote it contains — the one form POSIX gives for a literal that may contain
// everything else unescaped.
func Quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
