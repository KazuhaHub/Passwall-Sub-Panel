package destpolicy

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type Observer struct {
	store           ports.DestAgentPolicyRepo
	now             func() time.Time
	invalidate      func(string)
	allowlistResync func(context.Context, string)
}

func NewObserver(store ports.DestAgentPolicyRepo, now func() time.Time) *Observer {
	if now == nil {
		now = time.Now
	}
	return &Observer{store: store, now: now}
}
func (o *Observer) SetInvalidator(invalidate func(string)) { o.invalidate = invalidate }
func (o *Observer) SetAllowlistResyncer(resync func(context.Context, string)) {
	o.allowlistResync = resync
}
func (o *Observer) ObserveStatus(ctx context.Context, agentID string, status *protocol.PolicyStatus, capabilities []string) error {
	if status == nil || !slices.Contains(capabilities, protocol.CapabilityDestinationPolicy) || protocol.ValidatePolicyStatus(*status) != nil {
		return nil
	}
	if o == nil || o.store == nil {
		return domain.ErrUnavailable
	}
	now := o.now().UTC()
	invalidate := false
	resync := false
	_, err := o.store.Update(ctx, agentID, now, func(state *domain.DestAgentPolicy, loadBodies func() error) (bool, error) {
		invalidate, resync = false, false
		blocked := fallbackBlocksAllowlist(*state)
		oldDigest, oldReason, oldExhausted := state.AppliedSHA256, state.FallbackReason, state.FallbackExhausted
		oldRejected, oldLimit, oldListeners := state.RejectedGeneration, state.OverLimit, state.PrecheckListeners
		oldRejectedContext := state.RejectedContext
		changed, err := ApplyPolicyStatus(state, status, capabilities, now)
		if errors.Is(err, errPolicyBodiesRequired) {
			if err := loadBodies(); err != nil {
				return false, err
			}
			changed, err = ApplyPolicyStatus(state, status, capabilities, now)
			if errors.Is(err, errPolicyBodiesRequired) {
				return false, fmt.Errorf("%w: missing exact minted policy body", domain.ErrUnavailable)
			}
		}
		if err == nil && changed {
			invalidate = oldDigest != state.AppliedSHA256 || oldReason != state.FallbackReason || oldExhausted != state.FallbackExhausted || oldRejected != state.RejectedGeneration || oldRejectedContext != state.RejectedContext || !reflect.DeepEqual(oldLimit, state.OverLimit) || !slices.Equal(oldListeners, state.PrecheckListeners)
			resync = blocked != fallbackBlocksAllowlist(*state)
		}
		return changed, err
	})
	if err != nil {
		return err
	}
	if invalidate && o.invalidate != nil {
		o.invalidate(agentID)
	}
	if resync && o.allowlistResync != nil {
		o.allowlistResync(ctx, agentID)
	}
	return nil
}

func fallbackBlocksAllowlist(state domain.DestAgentPolicy) bool {
	return state.FallbackReason != "" || state.FallbackExhausted
}
