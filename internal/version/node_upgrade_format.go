package version

import "strings"

// NodeCanInstallVersion accounts for the parser compiled into the installed
// upgrade helper. 4.0.2 is the first migration release with suffix support;
// older nodes can reach that plain version, or use a generated install script.
// The opaque expected-version identity is still checked by the node itself.
func NodeCanInstallVersion(current, target string) bool {
	if !strings.Contains(target, "-") {
		return true
	}
	current = strings.SplitN(current, " ", 2)[0]
	return IsReleaseVersion(current) && CompareRelease(current, "4.0.2-beta.1") >= 0
}
