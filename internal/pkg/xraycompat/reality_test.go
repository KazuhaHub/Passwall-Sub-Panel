package xraycompat

import (
	"strings"
	"testing"
)

func TestMihomoNeedsMLKEM(t *testing.T) {
	t.Parallel()
	tests := []struct {
		version string
		want    bool
	}{
		{version: "26.6.27", want: false},
		{version: "v26.7.28", want: false},
		{version: "26.9.7", want: false},
		{version: "26.9.8", want: true},
		{version: "v26.9.9", want: true},
		{version: "Xray 26.9.9", want: true},
		{version: "26.10.0", want: true},
		{version: "", want: false},
		{version: "latest", want: false},
		{version: "26.09.8", want: false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.version, func(t *testing.T) {
			t.Parallel()
			if got := MihomoNeedsMLKEM(test.version); got != test.want {
				t.Fatalf("MihomoNeedsMLKEM(%q) = %v, want %v", test.version, got, test.want)
			}
		})
	}
}

func TestNormalizeRealityFingerprint(t *testing.T) {
	tests := []struct {
		name, version, input string
		wantChanged          bool
		wantFingerprint      string
	}{
		{
			name: "new xray forces chrome", version: "26.9.8", wantChanged: true, wantFingerprint: `"fingerprint":"chrome"`,
			input: `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["example.com"],"settings":{"fingerprint":"firefox","spiderX":"/"}}}`,
		},
		{
			name: "already chrome is stable", version: "v26.9.9", wantChanged: false, wantFingerprint: `"fingerprint":"chrome"`,
			input: `{"security":"reality","realitySettings":{"settings":{"fingerprint":"chrome"}}}`,
		},
		{
			name: "old xray preserves choice", version: "26.7.28", wantChanged: false, wantFingerprint: `"fingerprint":"firefox"`,
			input: `{"security":"reality","realitySettings":{"settings":{"fingerprint":"firefox"}}}`,
		},
		{
			name: "tls is untouched", version: "26.9.9", wantChanged: false, wantFingerprint: `"fingerprint":"firefox"`,
			input: `{"security":"tls","realitySettings":{"settings":{"fingerprint":"firefox"}}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed, err := NormalizeRealityFingerprint(tt.input, tt.version)
			if err != nil {
				t.Fatal(err)
			}
			if changed != tt.wantChanged {
				t.Fatalf("changed = %v, want %v", changed, tt.wantChanged)
			}
			if !strings.Contains(got, tt.wantFingerprint) {
				t.Fatalf("output = %s, want %s", got, tt.wantFingerprint)
			}
		})
	}
}

func TestNormalizeRealityFingerprintRejectsMalformedRequiredConfig(t *testing.T) {
	if _, _, err := NormalizeRealityFingerprint(`{"security":"reality"`, "26.9.9"); err == nil {
		t.Fatal("malformed REALITY config accepted")
	}
}
