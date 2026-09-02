package xui

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Whether a panel can STORE limitHwid is a property of the panel, not of the
// adapter type. Getting this wrong does not merely mis-report a capability:
// lifecycleWriteReason relies on this list to decide whether comparing the
// field can ever converge. Declaring it on a panel that drops the value makes
// every poll cycle see 0 != N, issue a full client replace, and restart that
// node's Xray core — with no end state.

func capsFor(t *testing.T, panelVersion string) map[ports.PanelCapability]bool {
	t.Helper()
	c, err := New(&domain.XUIPanel{Name: "p", URL: "https://example.invalid", PanelVersion: panelVersion})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	set := map[ports.PanelCapability]bool{}
	for _, cap := range c.Capabilities() {
		set[cap] = true
	}
	return set
}

func TestDeviceLimitCapabilityFollowsPanelVersion(t *testing.T) {
	cases := []struct {
		version string
		want    bool
		why     string
	}{
		{"3.7.0", true, "the release that added limitHwid"},
		{"3.7.1", true, "newer still stores it"},
		{"4.0.0", true, "a future major must not lose it"},
		{"3.6.9", false, "one patch below the floor cannot store it"},
		{"3.4.2", false, "PSP's supported floor predates the field"},
		{"", false, "never probed — unknown must not be read as capable"},
		{"not-a-version", false, "an unparseable string is not evidence of support"},
	}
	for _, tc := range cases {
		got := capsFor(t, tc.version)[ports.CapabilityClientDeviceLimit]
		if got != tc.want {
			t.Errorf("panel %q: device-limit capability = %v, want %v (%s)", tc.version, got, tc.want, tc.why)
		}
	}
}

// The IP limit predates the version gate and must stay unconditional, or an
// unprobed panel would silently stop having its concurrent-IP cap maintained.
func TestIPLimitCapabilityIsNotVersionGated(t *testing.T) {
	for _, v := range []string{"", "3.4.2", "3.7.0"} {
		if !capsFor(t, v)[ports.CapabilityClientIPLimit] {
			t.Errorf("panel %q lost the IP-limit capability; only limitHwid is version-gated", v)
		}
	}
}
