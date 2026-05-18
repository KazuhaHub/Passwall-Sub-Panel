// Package version exposes the panel build identity. Single source of
// truth for "what version am I running" — referenced by the boot log
// line, by /api/version, and by the SPA's About dialog.
//
// Set via -ldflags="-X .../internal/version.Version=v3.0.0" in CI so a
// `git describe` value lands in the released binary. Source builds get
// the literal "dev" — useful for local smoke tests without polluting
// release artifacts.
package version

// Version is set at link time. Leave it as "dev" in source so a
// developer build is obviously distinguishable from a release artefact.
var Version = "dev"

// Commit is set at link time too — typically the short Git SHA.
// Empty in source builds.
var Commit = ""

// BuildDate is set at link time (UTC, RFC3339). Empty in source builds.
var BuildDate = ""

// String returns "v3.0.0 (abcdef0)" or just "dev" when no metadata
// has been wired in.
func String() string {
	if Commit == "" {
		return Version
	}
	return Version + " (" + Commit + ")"
}
