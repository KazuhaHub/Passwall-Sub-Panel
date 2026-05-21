package clientdetect

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func testFamilies() []ports.SubClientFamily {
	return []ports.SubClientFamily{
		{Name: "Clash / mihomo", Keywords: []string{"clash", "mihomo", "meta"}, RenderFormat: "mihomo", Enabled: true},
		{Name: "sing-box", Keywords: []string{"sing-box", "karing"}, RenderFormat: "sing-box", Enabled: true},
		// V2rayNG before V2RayN so the more specific keyword wins.
		{Name: "V2rayNG", Keywords: []string{"v2rayng"}, RenderFormat: "uri-list", Enabled: true},
		{Name: "V2RayN", Keywords: []string{"v2rayn", "v2ray"}, RenderFormat: "uri-list", Enabled: false},
	}
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name, ua, wantClient, wantFormat string
		wantMatched                      bool
	}{
		{"clash UA", "ClashMetaForAndroid/2.0", "Clash / mihomo", "mihomo", true},
		{"sing-box UA", "sing-box 1.8", "sing-box", "sing-box", true},
		{"karing folds into sing-box", "Karing/1.0", "sing-box", "sing-box", true},
		{"v2rayng wins over v2rayn by order", "v2rayNG/1.8.5", "V2rayNG", "uri-list", true},
		{"v2rayn matches v2rayn family", "v2rayN/6.0", "V2RayN", "uri-list", true},
		{"unknown defaults to other/mihomo", "curl/8.0", "other", "mihomo", false},
		{"empty UA no match", "", "other", "mihomo", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Detect(tc.ua, testFamilies())
			if got.ClientName != tc.wantClient || got.RenderFormat != tc.wantFormat || got.Matched != tc.wantMatched {
				t.Fatalf("Detect(%q) = %+v, want client=%q format=%q matched=%v",
					tc.ua, got, tc.wantClient, tc.wantFormat, tc.wantMatched)
			}
		})
	}
}

// Detect reports a match purely by UA keyword, regardless of the family's
// Enabled flag — the enabled/blocked gate is applied by the caller (sub.go),
// not here. A disabled family must still "match" so the caller can decide to
// block it.
func TestDetectMatchesDisabledFamily(t *testing.T) {
	got := Detect("v2rayN/6.0", testFamilies())
	if !got.Matched || got.ClientName != "V2RayN" || got.Enabled {
		t.Fatalf("disabled family should match by UA with Enabled=false: got %+v", got)
	}
}

// TestClientBlocked covers the two filter modes. Blacklist (default) blocks
// only a matched-but-disabled family and lets unknown clients through;
// whitelist blocks anything that isn't matched-and-enabled, unknown included.
func TestClientBlocked(t *testing.T) {
	matchedEnabled := Result{Matched: true, Enabled: true}
	matchedDisabled := Result{Matched: true, Enabled: false}
	unknown := Result{Matched: false}
	cases := []struct {
		name, mode string
		r          Result
		want       bool
	}{
		{"blacklist: enabled passes", FilterBlacklist, matchedEnabled, false},
		{"blacklist: disabled blocked", FilterBlacklist, matchedDisabled, true},
		{"blacklist: unknown passes", FilterBlacklist, unknown, false},
		{"empty mode defaults to blacklist (disabled blocked)", "", matchedDisabled, true},
		{"empty mode defaults to blacklist (unknown passes)", "", unknown, false},
		{"whitelist: enabled passes", FilterWhitelist, matchedEnabled, false},
		{"whitelist: disabled blocked", FilterWhitelist, matchedDisabled, true},
		{"whitelist: unknown blocked", FilterWhitelist, unknown, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClientBlocked(tc.mode, tc.r); got != tc.want {
				t.Fatalf("ClientBlocked(%q, %+v) = %v, want %v", tc.mode, tc.r, got, tc.want)
			}
		})
	}
}
