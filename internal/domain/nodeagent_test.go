package domain

import (
	"testing"
	"time"
)

func TestNodeAgentOfflineAtUsesOnlyLastSeen(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-20 * time.Second)
	agent := &NodeAgent{LastSeen: &recent, Epoch: 999}
	if agent.OfflineAt(now, time.Minute) {
		t.Fatal("recent last_seen must be online regardless of version-like state")
	}
	stale := now.Add(-2 * time.Minute)
	agent.LastSeen = &stale
	agent.Epoch = 1
	if !agent.OfflineAt(now, time.Minute) {
		t.Fatal("stale last_seen must be offline regardless of version-like state")
	}
}

func TestClientApplyTimeoutIssue(t *testing.T) {
	start := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	deadline := start.Add(time.Hour)
	for _, tc := range []struct {
		state ClientApplyState
		code  string
	}{
		{ClientApplyPending, IssueObjectPendingTimeout},
		{ClientApplyRejected, IssueObjectRejectedTimeout},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			attachment := PSPClientInbound{
				ClientID: 7, NodeID: 9, State: tc.state, FirstFailedAt: &start,
			}
			if issue := attachment.TimeoutIssue(deadline.Add(-time.Nanosecond), time.Hour); issue != nil {
				t.Fatalf("escalated before deadline: %+v", issue)
			}
			issue := attachment.TimeoutIssue(deadline, time.Hour)
			if issue == nil || issue.Code != tc.code || issue.ClientID != 7 || issue.NodeID != 9 {
				t.Fatalf("timeout issue = %+v, want %s for client/node", issue, tc.code)
			}
		})
	}
	for _, state := range []ClientApplyState{ClientApplyApplied, ClientApplyBlocked} {
		attachment := PSPClientInbound{State: state, FirstFailedAt: &start}
		if issue := attachment.TimeoutIssue(deadline, time.Hour); issue != nil {
			t.Fatalf("state %s must not time out: %+v", state, issue)
		}
	}
}
