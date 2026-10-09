package destpolicy

import (
	"context"
	"github.com/KazuhaHub/passwall-protocol/protocol"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type compilerRetryRuntime struct {
	*cacheCompilerRuntime
	failRetry bool
}

func (r *compilerRetryRuntime) RetryDestinationPolicy(context.Context, string, time.Time) (bool, error) {
	if r.failRetry {
		return false, domain.ErrUnavailable
	}
	if r.state.RejectedGeneration == 0 && !r.state.FallbackExhausted && r.state.FallbackReason != "rejected" {
		return false, nil
	}
	r.state.RejectedGeneration = 0
	r.state.RejectedContext = ""
	r.state.FallbackExhausted = false
	if r.state.FallbackReason == "rejected" {
		r.state.FallbackReason = ""
	}
	r.state.MintedAt = nil
	return true, nil
}

func TestCompilerRetryInvalidatesOnlyAfterCommitAndRebuildsWarmSelection(t *testing.T) {
	c, _, runtime, agent, snapshot, base := cachedCompilerFixture(t)
	store := &compilerRetryRuntime{cacheCompilerRuntime: runtime}
	c.runtime = store
	prepares := 0
	c.prepare = func(defs domain.DestDefinitions, roster, quota RosterInput, baseBody protocol.ConfigBody) (*PreparedCandidate, error) {
		prepares++
		return PrepareCandidate(defs, roster, quota, baseBody)
	}
	caps := []string{protocol.CapabilityDestinationPolicy}
	first, err := c.Compile(t.Context(), agent, snapshot, caps, base)
	if err != nil {
		t.Fatal(err)
	}
	runtime.state.RejectedGeneration, runtime.state.RejectedContext = 1, first.Mint.Context
	runtime.state.FallbackReason = "rejected"
	if _, err := c.Compile(t.Context(), agent, snapshot, caps, base); err != nil {
		t.Fatal(err)
	}
	invalidations, resyncs := 0, 0
	c.SetInvalidator(func(id string) {
		invalidations++
		if id != agent.AgentID || runtime.state.RejectedGeneration != 0 {
			t.Error("retry notified before committed reset")
		}
	})
	c.SetAllowlistResyncer(func(_ context.Context, id string) {
		resyncs++
		if id != agent.AgentID || invalidations == 0 || runtime.state.FallbackReason != "" {
			t.Error("retry resynced before invalidation/reset")
		}
	})
	store.failRetry = true
	if changed, err := c.RetryDestinationPolicy(t.Context(), agent.AgentID); err == nil || changed || invalidations != 0 || resyncs != 0 {
		t.Fatal("failed retry notified or hid failure")
	}
	if _, err := c.Compile(t.Context(), agent, snapshot, caps, base); err != nil || prepares != 1 {
		t.Fatal("failed retry discarded warm immutable inputs")
	}
	store.failRetry = false
	if changed, err := c.RetryDestinationPolicy(t.Context(), agent.AgentID); err != nil || !changed || invalidations != 1 || resyncs != 1 {
		t.Fatal("committed retry did not notify exactly once")
	}
	rebuilt, err := c.Compile(t.Context(), agent, snapshot, caps, base)
	if err != nil || rebuilt.Mint.Kind != domain.DestCandidateDesired || prepares != 2 {
		t.Fatal("retry reused locked warm selection or failed to rebuild")
	}
	if changed, err := c.RetryDestinationPolicy(t.Context(), agent.AgentID); err != nil || changed || resyncs != 1 {
		t.Fatal("idempotent retry repeated membership work")
	}
}
