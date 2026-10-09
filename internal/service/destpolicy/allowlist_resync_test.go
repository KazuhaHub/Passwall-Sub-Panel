package destpolicy

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestCompilerAllowlistResyncOccursAfterCommitIncludingCacheHits(t *testing.T) {
	c, _, runtime, agent, snapshot, base := cachedCompilerFixture(t)
	caps := []string{protocol.CapabilityDestinationPolicy}
	calls := 0
	c.SetAllowlistResyncer(func(_ context.Context, id string) {
		if id != agent.AgentID {
			t.Fatal("resync crossed agent identity")
		}
		calls++
	})
	desired, err := c.Compile(t.Context(), agent, snapshot, caps, base)
	if err != nil {
		t.Fatal(err)
	}
	runtime.state.RejectedGeneration, runtime.state.RejectedContext = 1, desired.Mint.Context
	if _, err := c.Compile(t.Context(), agent, snapshot, caps, base); err != nil || calls != 1 || runtime.state.FallbackReason != "rejected" {
		t.Fatalf("committed rejection: calls=%d state=%+v / %v", calls, runtime.state, err)
	}
	if _, err := c.Compile(t.Context(), agent, snapshot, caps, base); err != nil || calls != 1 {
		t.Fatalf("idle resynced: calls=%d / %v", calls, err)
	}
	// Reuse the cached pre-transition selection, while the owner transaction
	// fails: no side effect may be published for the uncommitted decision.
	runtime.state.FallbackReason = ""
	runtime.fail = true
	if _, err := c.Compile(t.Context(), agent, snapshot, caps, base); !errors.Is(err, domain.ErrUnavailable) || calls != 1 {
		t.Fatalf("failed cached transaction resynced: calls=%d / %v", calls, err)
	}
	runtime.fail = false
	if _, err := c.Compile(t.Context(), agent, snapshot, caps, base); err != nil || calls != 2 || runtime.state.FallbackReason != "rejected" {
		t.Fatalf("cached transition recovery: calls=%d / %v", calls, err)
	}
	runtime.state.RejectedGeneration, runtime.state.RejectedContext = 0, ""
	runtime.state.FallbackReason = "over_limit"
	if _, err := c.Compile(t.Context(), agent, snapshot, caps, base); err != nil || calls != 3 || runtime.state.FallbackReason != "" {
		t.Fatalf("quota recovery: calls=%d / %v", calls, err)
	}
}
