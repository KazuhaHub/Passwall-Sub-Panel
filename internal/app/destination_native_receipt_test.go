package app

import (
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ReportedAt records status transitions, not every sync. A following accepted
// sync proves liveness even when an unchanged receipt retains its old timestamp.
// Empty and paused candidates preserve the historical LKG by design.
func nativeReceiptMatches(r domain.DestAgentPolicy, generation int64, lastSeen *time.Time, after time.Time) bool {
	if r.MintedGeneration != generation || r.MintedAt == nil || r.MintedAt.Before(after) || lastSeen == nil || !lastSeen.After(*r.MintedAt) || r.ReportedAt == nil || r.ReportedState != "applied" || r.ReportedSHA256 != r.MintedSHA256 || r.ReportedIssue != "" || len(r.ReportedListeners) != 0 || r.FallbackReason != "" || r.FallbackExhausted || r.OverLimit != nil || len(r.PrecheckListeners) != 0 || r.RejectedGeneration != 0 || r.RejectedContext != "" {
		return false
	}
	switch r.MintedKind {
	case domain.DestCandidateEmpty, domain.DestCandidatePaused:
		return true
	case domain.DestCandidateDesired:
		return r.MintedSHA256 != "" && r.DesiredSHA256 == r.MintedSHA256 && r.AppliedAt != nil && r.AppliedSHA256 == r.MintedSHA256
	default:
		return false
	}
}

// Fault scenarios declare the exact source and exhaustion state they expect.
// A historical LKG is not enough: the current fallback must itself be applied.
func nativeFaultReceiptMatches(r domain.DestAgentPolicy, generation int64, lastSeen *time.Time, after time.Time, kind domain.DestCandidateKind, exhausted bool) bool {
	if r.MintedKind != kind || r.MintedGeneration != generation || r.MintedAt == nil || r.MintedAt.Before(after) || lastSeen == nil || !lastSeen.After(*r.MintedAt) || r.ReportedAt == nil || r.ReportedState != "applied" || r.ReportedSHA256 != r.MintedSHA256 || r.ReportedIssue != "" || len(r.ReportedListeners) != 0 || r.FallbackReason != "sniffing" || r.FallbackExhausted != exhausted || r.OverLimit != nil || len(r.PrecheckListeners) == 0 || r.RejectedGeneration != 0 || r.RejectedContext != "" {
		return false
	}
	switch kind {
	case domain.DestCandidateFallback:
		return !exhausted && r.MintedSHA256 != "" && r.AppliedAt != nil && r.AppliedSHA256 == r.MintedSHA256
	case domain.DestCandidateEmpty, domain.DestCandidatePaused:
		return true
	default:
		return false
	}
}

func TestNativeDestinationReceiptRequiresCurrentObservedCandidate(t *testing.T) {
	after := time.UnixMilli(1000)
	minted, seen := after.Add(time.Second), after.Add(2*time.Second)
	base := domain.DestAgentPolicy{MintedKind: domain.DestCandidateDesired, MintedGeneration: 7, MintedAt: &minted, MintedSHA256: "current", DesiredSHA256: "current", ReportedSHA256: "current", ReportedState: "applied", AppliedSHA256: "current", AppliedAt: &seen, ReportedAt: &seen}
	for _, tc := range []struct {
		name   string
		change func(*domain.DestAgentPolicy, **time.Time)
		want   bool
	}{
		{"desired", func(*domain.DestAgentPolicy, **time.Time) {}, true},
		{"empty-with-no-lkg", func(r *domain.DestAgentPolicy, _ **time.Time) {
			r.MintedKind = domain.DestCandidateEmpty
			r.AppliedAt = nil
			r.AppliedSHA256 = ""
		}, true},
		{"paused-retains-historical-lkg", func(r *domain.DestAgentPolicy, _ **time.Time) {
			r.MintedKind = domain.DestCandidatePaused
			r.AppliedSHA256 = "historical"
		}, true},
		{"unchanged-status-timestamp", func(r *domain.DestAgentPolicy, _ **time.Time) { r.ReportedAt = &after }, true},
		{"stale-generation", func(r *domain.DestAgentPolicy, _ **time.Time) { r.MintedGeneration-- }, false},
		{"old-mint", func(r *domain.DestAgentPolicy, _ **time.Time) { old := after.Add(-time.Second); r.MintedAt = &old }, false},
		{"no-following-sync", func(_ *domain.DestAgentPolicy, at **time.Time) { *at = &minted }, false},
		{"missing-report", func(r *domain.DestAgentPolicy, _ **time.Time) { r.ReportedAt = nil }, false},
		{"wrong-receipt", func(r *domain.DestAgentPolicy, _ **time.Time) { r.ReportedSHA256 = "historical" }, false},
		{"desired-old-lkg", func(r *domain.DestAgentPolicy, _ **time.Time) { r.AppliedSHA256 = "historical" }, false},
		{"fallback", func(r *domain.DestAgentPolicy, _ **time.Time) { r.MintedKind = domain.DestCandidateFallback }, false},
		{"exhausted", func(r *domain.DestAgentPolicy, _ **time.Time) { r.FallbackExhausted = true }, false},
		{"rejected", func(r *domain.DestAgentPolicy, _ **time.Time) { r.ReportedState = "rejected" }, false},
		{"precheck", func(r *domain.DestAgentPolicy, _ **time.Time) { r.PrecheckListeners = []string{"listener"} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, lastSeen := base, &seen
			tc.change(&r, &lastSeen)
			if got := nativeReceiptMatches(r, 7, lastSeen, after); got != tc.want {
				t.Fatalf("receipt readiness=%t, want %t", got, tc.want)
			}
		})
	}
}

func TestNativeFaultReceiptRequiresExactCandidateAndCurrentAcknowledgement(t *testing.T) {
	after := time.UnixMilli(1000)
	minted, seen := after.Add(time.Second), after.Add(2*time.Second)
	base := domain.DestAgentPolicy{MintedKind: domain.DestCandidateFallback, MintedGeneration: 7, MintedAt: &minted, MintedSHA256: "port-lkg", DesiredSHA256: "new-domain-rule", ReportedSHA256: "port-lkg", ReportedState: "applied", AppliedSHA256: "port-lkg", AppliedAt: &seen, ReportedAt: &seen, FallbackReason: "sniffing", PrecheckListeners: []string{"l1"}}
	for _, tc := range []struct {
		name      string
		kind      domain.DestCandidateKind
		exhausted bool
		change    func(*domain.DestAgentPolicy)
		want      bool
	}{
		{"confirmed-port-fallback", domain.DestCandidateFallback, false, func(*domain.DestAgentPolicy) {}, true},
		{"paused-retains-fallback-history", domain.DestCandidatePaused, false, func(r *domain.DestAgentPolicy) {
			r.MintedKind = domain.DestCandidatePaused
			r.MintedSHA256 = "empty"
			r.ReportedSHA256 = "empty"
		}, true},
		{"pruned-empty-retains-lkg", domain.DestCandidateEmpty, false, func(r *domain.DestAgentPolicy) {
			r.MintedKind = domain.DestCandidateEmpty
			r.MintedSHA256 = "empty"
			r.ReportedSHA256 = "empty"
		}, true},
		{"exhausted-empty-retains-lkg", domain.DestCandidateEmpty, true, func(r *domain.DestAgentPolicy) {
			r.MintedKind = domain.DestCandidateEmpty
			r.MintedSHA256 = "empty"
			r.ReportedSHA256 = "empty"
			r.FallbackExhausted = true
		}, true},
		{"wrong-candidate-kind", domain.DestCandidatePaused, false, func(*domain.DestAgentPolicy) {}, false},
		{"old-generation", domain.DestCandidateFallback, false, func(r *domain.DestAgentPolicy) { r.MintedGeneration-- }, false},
		{"wrong-reported-digest", domain.DestCandidateFallback, false, func(r *domain.DestAgentPolicy) { r.ReportedSHA256 = "old" }, false},
		{"wrong-applied-digest", domain.DestCandidateFallback, false, func(r *domain.DestAgentPolicy) { r.AppliedSHA256 = "old" }, false},
		{"missing-applied-time", domain.DestCandidateFallback, false, func(r *domain.DestAgentPolicy) { r.AppliedAt = nil }, false},
		{"wrong-fallback-reason", domain.DestCandidateFallback, false, func(r *domain.DestAgentPolicy) { r.FallbackReason = "rejected" }, false},
		{"no-sniffing-precheck", domain.DestCandidateFallback, false, func(r *domain.DestAgentPolicy) { r.PrecheckListeners = nil }, false},
		{"unexpected-exhaustion", domain.DestCandidateFallback, false, func(r *domain.DestAgentPolicy) { r.FallbackExhausted = true }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.change(&r)
			if got := nativeFaultReceiptMatches(r, 7, &seen, after, tc.kind, tc.exhausted); got != tc.want {
				t.Fatalf("fault receipt readiness=%t, want %t", got, tc.want)
			}
		})
	}
}
