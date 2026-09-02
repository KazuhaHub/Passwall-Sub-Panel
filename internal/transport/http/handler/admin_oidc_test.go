package handler

import "testing"

// TestOIDCIssuerHTTPS pins that an enabled OIDC config requires an https issuer
// (the issuer drives discovery + token exchange; http invites downgrade / SSRF).
func TestOIDCIssuerHTTPS(t *testing.T) {
	cases := []struct {
		enabled bool
		issuer  string
		want    bool
	}{
		{true, "https://login.example.com", true},
		{true, "https://login.example.com/", true},
		{true, "http://login.example.com", false}, // downgrade
		{true, "http://169.254.169.254", false},   // metadata SSRF over http
		{true, "ftp://x", false},
		{true, "not a url", false},
		{true, "", false},
		{false, "", true}, // disabled → any issuer ok
		{false, "http://whatever", true},
	}
	for _, tc := range cases {
		if got := oidcIssuerHTTPS(tc.enabled, tc.issuer); got != tc.want {
			t.Errorf("oidcIssuerHTTPS(%v, %q) = %v, want %v", tc.enabled, tc.issuer, got, tc.want)
		}
	}
}
