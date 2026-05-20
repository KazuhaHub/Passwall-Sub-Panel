package mailer

import "testing"

// resolveLogoURL must only ever yield an absolute http(s) URL (or empty) for
// email <img> src. data: URIs render in the in-panel preview but are blocked
// by Gmail and most webmail clients, and relative paths can't be fetched at
// all — both were the cause of the "preview fine, received logo broken" bug.
func TestResolveLogoURL(t *testing.T) {
	const base = "https://panel.example.com"
	publicFallback := base + emailLogoAssetPath

	cases := []struct {
		name        string
		base        string
		light, dark string
		want        string
	}{
		{"no config falls back to public asset", base, "", "", publicFallback},
		{"dark preferred over light", base, "https://cdn.example.com/light.png", "https://cdn.example.com/dark.png", "https://cdn.example.com/dark.png"},
		{"light used when no dark", base, "https://cdn.example.com/light.png", "", "https://cdn.example.com/light.png"},
		{"relative configured logo resolves against base", base, "/brand/logo.png", "", base + "/brand/logo.png"},
		{"data URI configured logo is rejected, public fallback wins", base, "data:image/png;base64,AAAA", "", publicFallback},
		{"data URI dark rejected, falls back to light http", base, "https://cdn.example.com/light.png", "data:image/png;base64,AAAA", "https://cdn.example.com/light.png"},
		{"no base and only relative logo yields empty (skip img)", "", "/brand/logo.png", "", ""},
		{"no base and data URI yields empty (skip img)", "", "data:image/png;base64,AAAA", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveLogoURL(tc.base, tc.light, tc.dark)
			if got != tc.want {
				t.Fatalf("resolveLogoURL(%q, %q, %q) = %q, want %q", tc.base, tc.light, tc.dark, got, tc.want)
			}
		})
	}
}
