package app

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func nativeOpenRosterReceiptMatches(stream domain.NodeAgentStream, epoch uint64, revoked protocol.SubjectKey, canary protocol.ClientKey, listener protocol.ListenerKey) bool {
	var roster protocol.RosterBody
	if epoch == 0 || stream.Stream != domain.NodeAgentStreamRoster || stream.AppliedEpoch != epoch || !stream.Converged() || json.Unmarshal(stream.DesiredBody, &roster) != nil {
		return false
	}
	retained, active := 0, 0
	for _, c := range roster.Clients {
		if c.Subject == revoked {
			if c.Enabled {
				return false
			}
			retained++
		}
		if c.Key == canary {
			if !c.Enabled || len(c.Listeners) != 1 || c.Listeners[0] != listener {
				return false
			}
			active++
		}
	}
	return retained == 1 && active == 1
}

func TestNativeOpenRosterReceiptRequiresCurrentAttachmentAndPreservedRevocation(t *testing.T) {
	roster := protocol.RosterBody{Clients: []protocol.Client{
		{Key: protocol.NewClientKey(1), Subject: protocol.NewSubjectKey(1), Enabled: false},
		{Key: protocol.NewClientKey(2), Subject: protocol.NewSubjectKey(2), Enabled: true, Listeners: []protocol.ListenerKey{protocol.NewListenerKey(3)}},
	}}
	for _, tc := range []struct {
		name   string
		change func(*protocol.RosterBody, *domain.NodeAgentStream)
		want   bool
	}{
		{"current-attached-canary", func(*protocol.RosterBody, *domain.NodeAgentStream) {}, true},
		{"unacknowledged", func(_ *protocol.RosterBody, s *domain.NodeAgentStream) { s.AppliedETag = "old" }, false},
		{"old-epoch", func(_ *protocol.RosterBody, s *domain.NodeAgentStream) { s.AppliedEpoch = 1 }, false},
		{"unattached-canary", func(r *protocol.RosterBody, _ *domain.NodeAgentStream) { r.Clients[1].Listeners = nil }, false},
		{"different-listener", func(r *protocol.RosterBody, _ *domain.NodeAgentStream) {
			r.Clients[1].Listeners[0] = protocol.NewListenerKey(4)
		}, false},
		{"disabled-canary", func(r *protocol.RosterBody, _ *domain.NodeAgentStream) { r.Clients[1].Enabled = false }, false},
		{"replaced-canary", func(r *protocol.RosterBody, _ *domain.NodeAgentStream) { r.Clients[1].Key = protocol.NewClientKey(4) }, false},
		{"revoked-user-reenabled", func(r *protocol.RosterBody, _ *domain.NodeAgentStream) { r.Clients[0].Enabled = true }, false},
		{"revoked-user-deleted", func(r *protocol.RosterBody, _ *domain.NodeAgentStream) { r.Clients = r.Clients[1:] }, false},
		{"duplicate-canary", func(r *protocol.RosterBody, _ *domain.NodeAgentStream) { r.Clients = append(r.Clients, r.Clients[1]) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := roster
			current.Clients = append([]protocol.Client(nil), roster.Clients...)
			current.Clients[1].Listeners = append([]protocol.ListenerKey(nil), roster.Clients[1].Listeners...)
			stream := domain.NodeAgentStream{Stream: domain.NodeAgentStreamRoster, DesiredETag: "current", AppliedETag: "current", AppliedEpoch: 2}
			tc.change(&current, &stream)
			body, err := json.Marshal(current)
			if err != nil {
				t.Fatal(err)
			}
			stream.DesiredBody = body
			if got := nativeOpenRosterReceiptMatches(stream, 2, protocol.NewSubjectKey(1), protocol.NewClientKey(2), protocol.NewListenerKey(3)); got != tc.want {
				t.Fatalf("open roster readiness=%t, want %t", got, tc.want)
			}
		})
	}
}

func TestNativeListenerFaultReceiptRequiresUnchangedPolicyAndFailedCurrentConfig(t *testing.T) {
	minted, mutation, seen := time.UnixMilli(1000), time.UnixMilli(2000), time.UnixMilli(3000)
	prior := domain.DestAgentPolicy{AgentID: "native", MintedKind: domain.DestCandidateDesired, MintedGeneration: 7, MintedAt: &minted, MintedSHA256: "confirmed", DesiredSHA256: "confirmed", ReportedSHA256: "confirmed", ReportedState: "applied", ReportedAt: &minted, AppliedSHA256: "confirmed", AppliedAt: &minted, MintedBody: []byte("policy"), AppliedBody: []byte("policy")}
	// Stream acknowledgement means received and durably stored, not deployed.
	stream := domain.NodeAgentStream{AgentID: "native", Stream: domain.NodeAgentStreamConfig, DesiredETag: "invalid-listener", AppliedETag: "invalid-listener", AppliedEpoch: 1, LastSeen: &seen}
	for _, tc := range []struct {
		name   string
		change func(*domain.DestAgentPolicy, *domain.NodeAgentStream)
		want   bool
	}{
		{"unchanged-policy-with-fresh-config-failure", func(*domain.DestAgentPolicy, *domain.NodeAgentStream) {}, true},
		{"no-current-config-mutation", func(_ *domain.DestAgentPolicy, s *domain.NodeAgentStream) { s.DesiredETag = "good-listener" }, false},
		{"old-config-acknowledgement", func(_ *domain.DestAgentPolicy, s *domain.NodeAgentStream) { s.AppliedETag = "good-listener" }, false},
		{"different-received-config", func(_ *domain.DestAgentPolicy, s *domain.NodeAgentStream) { s.AppliedETag = "other" }, false},
		{"old-epoch", func(_ *domain.DestAgentPolicy, s *domain.NodeAgentStream) { s.AppliedEpoch = 0 }, false},
		{"stale-config-report", func(_ *domain.DestAgentPolicy, s *domain.NodeAgentStream) { s.LastSeen = &minted }, false},
		{"policy-rejected", func(r *domain.DestAgentPolicy, _ *domain.NodeAgentStream) { r.ReportedState = "rejected" }, false},
		{"policy-fallback", func(r *domain.DestAgentPolicy, _ *domain.NodeAgentStream) { r.FallbackReason = "rejected" }, false},
		{"policy-generation-changed", func(r *domain.DestAgentPolicy, _ *domain.NodeAgentStream) { r.MintedGeneration++ }, false},
		{"policy-body-changed", func(r *domain.DestAgentPolicy, _ *domain.NodeAgentStream) { r.MintedBody = []byte("different") }, false},
		{"lkg-body-changed", func(r *domain.DestAgentPolicy, _ *domain.NodeAgentStream) { r.AppliedBody = []byte("different") }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current, cfg := prior, stream
			tc.change(&current, &cfg)
			if got := nativeListenerFaultReceiptMatches(prior, current, cfg, "good-listener", 1, &seen, mutation); got != tc.want {
				t.Fatalf("listener fault receipt=%t, want %t", got, tc.want)
			}
		})
	}
	// The test must observe an accepted sync after the mutation, not a cached
	// policy receipt and an old core error.
	if nativeListenerFaultReceiptMatches(prior, prior, stream, "good-listener", 1, &minted, mutation) {
		t.Fatal("stale agent report proved a new listener failure")
	}
}

// A failed listener deployment must not be mistaken for a policy rejection or
// for deployment of the invalid config. Streams acknowledge received documents;
// the driver separately requires the production listener intent to stay pending
// and proves the previous core is still serving real traffic.
func nativeListenerFaultReceiptMatches(prior, current domain.DestAgentPolicy, cfg domain.NodeAgentStream, confirmedConfig string, epoch uint64, lastSeen *time.Time, after time.Time) bool {
	return prior.AgentID != "" && current.AgentID == prior.AgentID && prior.MintedAt != nil &&
		lastSeen != nil && lastSeen.After(after) && nativeReceiptMatches(current, prior.MintedGeneration, lastSeen, *prior.MintedAt) &&
		prior.MintedKind == domain.DestCandidateDesired && current.MintedKind == prior.MintedKind &&
		current.MintedSHA256 == prior.MintedSHA256 && current.DesiredSHA256 == prior.DesiredSHA256 &&
		current.AppliedSHA256 == prior.AppliedSHA256 && current.ReportedSHA256 == prior.ReportedSHA256 &&
		bytes.Equal(current.MintedBody, prior.MintedBody) && bytes.Equal(current.AppliedBody, prior.AppliedBody) &&
		cfg.AgentID == prior.AgentID && cfg.Stream == domain.NodeAgentStreamConfig && epoch > 0 && cfg.AppliedEpoch == epoch &&
		confirmedConfig != "" && cfg.DesiredETag != "" && cfg.DesiredETag != confirmedConfig && cfg.Converged() &&
		cfg.LastSeen != nil && cfg.LastSeen.After(after)
}

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
