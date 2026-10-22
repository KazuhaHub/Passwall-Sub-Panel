package version

import "testing"

func TestOldNodeRequiresPlainBridgeOrScriptForCandidate(t *testing.T) {
	for _, current := range []string{"4.0.1.11", "4.0.1.22 (abcdef0)", "v0.0.1-beta11", "dev"} {
		if NodeCanInstallVersion(current, "4.1.0-beta.1") {
			t.Errorf("old helper %s accepted suffix", current)
		}
		if !NodeCanInstallVersion(current, "4.0.2") {
			t.Errorf("bridge blocked for %s", current)
		}
	}
	for _, current := range []string{"4.0.2", "4.1.0-beta.1", "4.1.0-rc.1"} {
		if !NodeCanInstallVersion(current, "4.1.0-beta.2") {
			t.Errorf("modern helper %s rejected suffix", current)
		}
	}
}
