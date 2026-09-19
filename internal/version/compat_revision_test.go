package version

import "testing"

// R09 §10 step 6: "未知 schema、损坏数据、异常回退不能替换有效缓存".
//
// The schema and major checks already refuse a malformed or wrong-major
// document. What was missing is the third: a manifest that is perfectly valid
// and perfectly well-formed, but OLDER than the one already applied. Nothing
// compared them, so a stale CDN edge, a reverted commit, or a rollback of the
// published file would silently replace a newer tested range with an older one —
// and the panel would then refuse an upgrade an admin had already been told was
// supported, with nothing recording why.
//
// The comparison is a pure function so the rule is testable without a server,
// and so the ordering lives in one place rather than at each call site.
func TestRevisionRegressed(t *testing.T) {
	cases := []struct {
		name    string
		applied string
		fetched string
		want    bool
		wantErr bool
	}{
		{"nothing applied yet accepts anything well-formed", "", "2026-09-19", false, false},
		{"a newer manifest is accepted", "2026-09-16", "2026-09-19", false, false},
		{"re-applying the same revision is accepted", "2026-09-19", "2026-09-19", false, false},
		{"a full timestamp is accepted too, and compares as an instant", "2026-09-19", "2026-09-19T10:00:00Z", false, false},
		{"an older manifest is a rollback", "2026-09-19", "2026-09-16", true, false},
		{"an old date across a year boundary is still older", "2026-01-01", "2025-12-31", true, false},
		// A manifest that cannot be ordered cannot be applied. Refusing is the
		// fail-closed direction: the last good range stays in force.
		{"a manifest with no revision is refused", "2026-09-19", "", false, true},
		{"an unparseable revision is refused", "2026-09-19", "not-a-time", false, true},
		{"a plausible but invalid date is refused", "2026-09-19", "2026-13-45", false, true},
		{"an unparseable APPLIED revision is refused rather than trusted", "garbage", "2026-09-19", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := revisionRegressed(tc.applied, tc.fetched)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("revisionRegressed(%q, %q) accepted; a manifest that cannot be ordered must be refused", tc.applied, tc.fetched)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("revisionRegressed(%q, %q) = %v, want %v", tc.applied, tc.fetched, got, tc.want)
			}
		})
	}
}
