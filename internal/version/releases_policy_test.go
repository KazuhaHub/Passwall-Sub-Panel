package version

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A policy is a permission to upgrade. These rejections are the ones whose
// absence would turn a bad policy into an offered upgrade, so each gets a case
// that would pass if the check were dropped.
func TestReleasePolicyRefusals(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	valid := `{
	  "schema_version": 1,
	  "revision": 7,
	  "issued_at": "2026-09-19T00:00:00Z",
	  "expires_at": "2026-09-26T00:00:00Z",
	  "applies_to_psp": {"min": "4.0.0", "max": "4.99.99"},
	  "releases": [{"version": "v0.0.1-beta11", "release_tag": "v0.0.1-beta11", "scheme": "legacy", "evidence": ["node-wire-v1"]}],
	  "upgrade_edges": [{"from": "v0.0.1-beta3", "to": "v0.0.1-beta11", "evidence": ["upgrade-mechanism"]}],
	  "refusals": [{"version": "v0.0.1-beta1", "reason": "never reviewed"}]
	}`

	if _, err := ParseReleasesPolicy([]byte(valid), now); err != nil {
		t.Fatalf("the reference document must parse: %v", err)
	}

	for _, tc := range []struct {
		name    string
		doc     string
		wantErr error
		detail  string
	}{
		{
			name:    "unknown schema",
			doc:     strings.Replace(valid, `"schema_version": 1`, `"schema_version": 99`, 1),
			wantErr: ErrPolicySchema,
		},
		{
			name:    "expired window",
			doc:     strings.Replace(valid, `"expires_at": "2026-09-26T00:00:00Z"`, `"expires_at": "2026-09-19T06:00:00Z"`, 1),
			wantErr: ErrPolicyExpired,
		},
		{
			name:    "window that ends before it starts",
			doc:     strings.Replace(valid, `"issued_at": "2026-09-19T00:00:00Z"`, `"issued_at": "2026-10-01T00:00:00Z"`, 1),
			wantErr: ErrPolicyMalformed,
		},
		{
			name:    "no validity window",
			doc:     strings.Replace(valid, `"expires_at": "2026-09-26T00:00:00Z",`, ``, 1),
			wantErr: ErrPolicyMalformed,
		},
		{
			name:    "inverted PSP range",
			doc:     strings.Replace(valid, `{"min": "4.0.0", "max": "4.99.99"}`, `{"min": "5.0.0", "max": "4.99.99"}`, 1),
			wantErr: ErrPolicyMalformed,
		},
		{
			name:    "unparseable PSP range",
			doc:     strings.Replace(valid, `{"min": "4.0.0", "max": "4.99.99"}`, `{"min": "latest", "max": "4.99.99"}`, 1),
			wantErr: ErrPolicyMalformed,
		},
		{
			name:    "zero release line",
			doc:     strings.Replace(valid, `{"min": "4.0.0", "max": "4.99.99"}`, `{"min": "0.0.0", "max": "4.99.99"}`, 1),
			wantErr: ErrPolicyMalformed,
		},
		{
			name:    "a release offered with no evidence",
			doc:     strings.Replace(valid, `"evidence": ["node-wire-v1"]`, `"evidence": []`, 1),
			wantErr: ErrPolicyMalformed,
			detail:  "no evidence",
		},
		{
			name:    "an edge missing an end",
			doc:     strings.Replace(valid, `{"from": "v0.0.1-beta3", "to": "v0.0.1-beta11", "evidence": ["upgrade-mechanism"]}`, `{"from": "v0.0.1-beta3", "evidence": ["upgrade-mechanism"]}`, 1),
			wantErr: ErrPolicyMalformed,
			detail:  "both ends",
		},
		{
			name:    "an edge with no evidence",
			doc:     strings.Replace(valid, `{"from": "v0.0.1-beta3", "to": "v0.0.1-beta11", "evidence": ["upgrade-mechanism"]}`, `{"from": "v0.0.1-beta3", "to": "v0.0.1-beta11"}`, 1),
			wantErr: ErrPolicyMalformed,
			detail:  "no evidence",
		},
		{
			name:    "a refusal with no reason",
			doc:     strings.Replace(valid, `{"version": "v0.0.1-beta1", "reason": "never reviewed"}`, `{"version": "v0.0.1-beta1"}`, 1),
			wantErr: ErrPolicyMalformed,
			detail:  "reason",
		},
		{
			name: "a release both offered and refused",
			doc: strings.Replace(valid,
				`"refusals": [{"version": "v0.0.1-beta1", "reason": "never reviewed"}]`,
				`"refusals": [{"version": "v0.0.1-beta11", "reason": "never reviewed"}]`, 1),
			wantErr: ErrPolicyMalformed,
			detail:  "both offered and refused",
		},
		{
			name: "the same release offered twice",
			doc: strings.Replace(valid,
				`"releases": [{"version": "v0.0.1-beta11", "release_tag": "v0.0.1-beta11", "scheme": "legacy", "evidence": ["node-wire-v1"]}]`,
				`"releases": [{"version": "v0.0.1-beta11", "release_tag": "v0.0.1-beta11", "scheme": "legacy", "evidence": ["node-wire-v1"]},{"version": "v0.0.1-beta11", "release_tag": "v0.0.1-beta11", "scheme": "legacy", "evidence": ["node-wire-v1"]}]`, 1),
			wantErr: ErrPolicyMalformed,
			detail:  "twice",
		},
		{
			name:    "not a policy document at all",
			doc:     `{"schema_version": 1, "revision":`,
			wantErr: ErrPolicyMalformed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseReleasesPolicy([]byte(tc.doc), now)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if tc.detail != "" && !strings.Contains(err.Error(), tc.detail) {
				t.Fatalf("error %q does not name the reason %q", err, tc.detail)
			}
		})
	}
}

// Being well-formed is not the same as being about THIS build. The two are
// separate decisions so the caller can report "this policy is not for you"
// rather than treat it as a broken document.
func TestReleasePolicyAppliesOnlyToItsReviewedBuilds(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	policy, err := ParseReleasesPolicy([]byte(`{
	  "schema_version": 1, "revision": 7,
	  "issued_at": "2026-09-19T00:00:00Z", "expires_at": "2026-09-26T00:00:00Z",
	  "applies_to_psp": {"min": "4.0.0", "max": "4.99.99"},
	  "releases": [], "upgrade_edges": [], "refusals": []
	}`), now)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		build string
		want  bool
	}{
		{"v4.0.0-beta.25", true},
		{"v4.0.0", true},
		{"v4.99.99", true},
		{"v5.0.0", false},
		{"v3.9.2-beta.20", false},
		{"dev", false},
	} {
		if got := policy.AppliesTo(tc.build); got != tc.want {
			t.Errorf("AppliesTo(%q) = %v, want %v", tc.build, got, tc.want)
		}
	}
}

func TestReleasePolicyRevisionOnlyMovesForward(t *testing.T) {
	policy := ReleasesPolicy{Revision: 7}
	if ok, err := policy.Supersedes(6); err != nil || !ok {
		t.Fatalf("a newer revision must supersede: ok=%v err=%v", ok, err)
	}
	if ok, err := policy.Supersedes(7); err != nil || ok {
		t.Fatalf("an equal revision must be reused, not re-applied: ok=%v err=%v", ok, err)
	}
	if ok, err := policy.Supersedes(8); err == nil || ok {
		t.Fatalf("an older revision must be refused: ok=%v err=%v", ok, err)
	}
	if _, err := policy.Supersedes(-1); err == nil {
		t.Fatal("a negative revision must be refused")
	}
}

// The document this repository actually ships has to satisfy its own validator.
// A policy that fails its own rules would be refused at runtime by the very
// build that published it.
func TestShippedReleasePolicyValidates(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "compat", "releases-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	policy, err := ParseReleasesPolicy(raw, now)
	if err != nil {
		t.Fatalf("the shipped policy does not validate: %v", err)
	}
	if len(policy.Releases) == 0 {
		t.Fatal("the shipped policy offers no release")
	}
	if !policy.AppliesTo("v4.0.0-beta.25") {
		t.Fatal("the shipped policy does not apply to the build it ships with")
	}
}
