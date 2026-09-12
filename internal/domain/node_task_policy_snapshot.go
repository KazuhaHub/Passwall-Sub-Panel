package domain

import (
	"fmt"
	"math"
)

const nodeTaskLifecycleDayMS int64 = 24 * 60 * 60 * 1000

// NodeTaskLifecycleSnapshot freezes the original authorization and support
// policy for one task. It does not authorize execution: expiry wire capability,
// both execution gates, retention and restore-finalize remain separate work.
//
// FullResultRetainUntilMS is the immutable minimum protection floor, measured
// from the original latest-start deadline, not from mutable settings. A future
// late-result protection deadline may only extend this floor (for example to
// completion + the original retention period), never rewrite this snapshot.
// Passing the floor is not itself permission to delete identity or evidence.
type NodeTaskLifecycleSnapshot struct {
	IssuedAtMS              int64                   `json:"issued_at_ms"`
	NotAfterMS              int64                   `json:"not_after_ms"`
	Policy                  NodeTaskLifecyclePolicy `json:"policy"`
	FullResultRetainUntilMS int64                   `json:"full_result_retain_until_ms"`
}

// NewNodeTaskLifecycleSnapshot is pure: the producer chooses issued/deadline
// and captures the policy once before task creation. Retries must reuse the
// stored snapshot rather than call this with a new time to extend an old task.
func NewNodeTaskLifecycleSnapshot(issuedAtMS, notAfterMS int64, policy NodeTaskLifecyclePolicy) (*NodeTaskLifecycleSnapshot, error) {
	floor, err := nodeTaskLifecycleRetentionFloor(issuedAtMS, notAfterMS, policy)
	if err != nil {
		return nil, err
	}
	return &NodeTaskLifecycleSnapshot{
		IssuedAtMS: issuedAtMS, NotAfterMS: notAfterMS, Policy: policy,
		FullResultRetainUntilMS: floor,
	}, nil
}

// Validate rejects a missing snapshot and exact-floor drift. Legacy task
// compatibility belongs to the optional Lifecycle pointer on NodeAgentTask:
// nil is protected unknown history, not a guessed deadline or zero-day policy.
func (s *NodeTaskLifecycleSnapshot) Validate() error {
	if s == nil {
		return fmt.Errorf("%w: native task lifecycle snapshot is required", ErrValidation)
	}
	floor, err := nodeTaskLifecycleRetentionFloor(s.IssuedAtMS, s.NotAfterMS, s.Policy)
	if err != nil {
		return err
	}
	if s.FullResultRetainUntilMS != floor {
		return fmt.Errorf("%w: native task lifecycle retention floor must match the original deadline and policy exactly", ErrValidation)
	}
	return nil
}

func nodeTaskLifecycleRetentionFloor(issuedAtMS, notAfterMS int64, policy NodeTaskLifecyclePolicy) (int64, error) {
	if err := policy.Validate(); err != nil {
		return 0, err
	}
	if issuedAtMS <= 0 || notAfterMS <= issuedAtMS {
		return 0, fmt.Errorf("%w: native task lifecycle requires positive issued time and a later deadline", ErrValidation)
	}
	// Validate bounds the day count before conversion/multiplication, including
	// 32-bit builds. Subtraction avoids int64 addition overflow opening a floor.
	retentionMS := int64(policy.ResultRetentionDays) * nodeTaskLifecycleDayMS
	if notAfterMS > math.MaxInt64-retentionMS {
		return 0, fmt.Errorf("%w: native task lifecycle retention floor overflows int64 milliseconds", ErrValidation)
	}
	return notAfterMS + retentionMS, nil
}

func (s *NodeTaskLifecycleSnapshot) Clone() *NodeTaskLifecycleSnapshot {
	if s == nil {
		return nil
	}
	copy := *s // Policy and timestamps are values; no caller-owned pointers remain.
	return &copy
}
