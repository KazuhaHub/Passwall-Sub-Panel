package destpolicy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// PublishRefusedError is a publish the validation before it refused: the
// previous snapshot stays in force. It classifies as a conflict (HTTP 409).
type PublishRefusedError struct{ Detail domain.DestPublishError }

func (e *PublishRefusedError) Error() string {
	if e.Detail.Kind == "invalid" {
		return fmt.Sprintf("%v: the policy is not valid (%s)", domain.ErrConflict, e.Detail.Field)
	}
	return fmt.Sprintf("%v: the policy is over its %s limit (%d of %d)", domain.ErrConflict, e.Detail.Kind, e.Detail.Used, e.Detail.Limit)
}

func (e *PublishRefusedError) Unwrap() error { return domain.ErrConflict }

func (s *Service) effective(ctx context.Context) ports.AccessControlEffective {
	if s.settings == nil {
		return ports.AccessControlEffectiveOf(ports.UISettings{})
	}
	set, err := s.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return ports.AccessControlEffectiveOf(ports.UISettings{})
	}
	return ports.AccessControlEffectiveOf(set)
}

// ensurePublished runs at the top of every Compile and costs one single-row
// read when nothing is pending. It publishes when a definition changed and
//
//   - no write has followed it for the debounce window (the trailing
//     debounce: a burst of edits is one publish), or
//   - the first unpublished write is older than five windows (a run of edits
//     that never pauses must still reach the nodes).
//
// An explicit publish (Publish) and a pause change (SetPaused) skip the wait.
func (s *Service) ensurePublished(ctx context.Context) (*domain.DestPolicyState, error) {
	st, err := s.state.Get(ctx)
	if err != nil {
		return nil, err
	}
	if !st.Unpublished() || st.Generation == s.failedGen.Load() {
		return st, nil
	}
	window := s.effective(ctx).PolicyApplyMin()
	now := s.now()
	var trigger string
	switch {
	case st.LastWriteAt == nil || now.Sub(*st.LastWriteAt) >= window:
		trigger = TriggerDebounced
	case st.FirstUnpublishedAt != nil && now.Sub(*st.FirstUnpublishedAt) >= maxWaitFactor*window:
		trigger = TriggerMaxWait
	default:
		return st, nil
	}
	// A publish already running is not waited for: validation expands every
	// list, which takes a while, and every node's compile would queue behind
	// it. The compile answers from the snapshot that is published now.
	if !s.pubMu.TryLock() {
		return st, nil
	}
	next, err := s.publishLocked(ctx, trigger)
	s.pubMu.Unlock()
	var refused *PublishRefusedError
	if errors.As(err, &refused) {
		// The old snapshot stays in force; the status carries why.
		return s.state.Get(ctx)
	}
	if err != nil {
		return nil, err
	}
	return next, nil
}

// Publish publishes now, whatever the debounce says, and returns the state
// after. When the validation refuses it the error is a *PublishRefusedError and
// nothing moves.
func (s *Service) Publish(ctx context.Context) (*domain.DestPolicyState, error) {
	return s.publish(ctx, TriggerManual)
}

// publish snapshots the definitions at their current generation, validates the
// result, stores the snapshot and moves published_generation to it.
func (s *Service) publish(ctx context.Context, trigger string) (*domain.DestPolicyState, error) {
	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	return s.publishLocked(ctx, trigger)
}

// publishLocked is publish with pubMu held by the caller.
func (s *Service) publishLocked(ctx context.Context, trigger string) (*domain.DestPolicyState, error) {

	st, err := s.state.Get(ctx)
	if err != nil {
		return nil, err
	}
	if !st.Unpublished() {
		return st, nil
	}
	defs, err := s.defs.ReadDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	if defs.Generation <= st.PublishedGeneration {
		return st, nil
	}
	now := s.now()
	doc := newSnapshotDoc(defs, st, now)
	if refusal := s.validateDoc(ctx, doc); refusal != nil {
		s.failedGen.Store(defs.Generation)
		if s.metrics.PublishRejected != nil {
			s.metrics.PublishRejected()
		}
		if err := s.state.SetPublishError(ctx, &refusal.Detail, now); err != nil {
			log.Warn("destpolicy: could not record the publish error", "err", err)
		}
		return nil, refusal
	} else if err := ctx.Err(); err != nil {
		return nil, err
	}
	body, err := doc.marshal()
	if err != nil {
		return nil, fmt.Errorf("destpolicy: encode snapshot: %w", err)
	}
	if err := s.state.SaveSnapshot(ctx, defs.Generation, body); err != nil {
		return nil, err
	}
	won, err := s.state.CompletePublish(ctx, defs.Generation, st.PublishedGeneration)
	if err != nil {
		return nil, err
	}
	s.failedGen.Store(0)
	if won {
		if s.metrics.Publish != nil {
			s.metrics.Publish(trigger)
		}
		if _, err := s.state.PruneSnapshotsExcept(ctx, defs.Generation); err != nil {
			log.Warn("destpolicy: could not prune old snapshots", "err", err)
		}
		log.Info("destination policy published", "generation", defs.Generation, "trigger", trigger)
	}
	return s.state.Get(ctx)
}

// SetPaused flips the emergency switch. A pause change takes effect on every
// node's next sync (paused is part of what Compile keys on) and publishes
// whatever was waiting, so a resumed policy is not stale.
func (s *Service) SetPaused(ctx context.Context, paused bool) error {
	st, err := s.state.Get(ctx)
	if err != nil {
		return err
	}
	if st.Paused == paused {
		return nil
	}
	if err := s.state.SetPaused(ctx, paused); err != nil {
		return err
	}
	if st.Unpublished() {
		if _, err := s.publish(ctx, TriggerPause); err != nil {
			var refused *PublishRefusedError
			if !errors.As(err, &refused) {
				return err
			}
		}
	}
	return nil
}

// validateDoc compiles the UPPER BOUND of the document and validates it: every
// rule, no subjects, no exemptions, every source group on every inbound it
// names. Rule counts, domains, regexps, CIDRs and the serialised size are the
// same for every node, so they are judged once here; what is specific to a node
// (its roster, its sniffing) is judged per node in Compile.
func (s *Service) validateDoc(ctx context.Context, doc *snapshotDoc) *PublishRefusedError {
	b, err := s.buildBase(ctx, doc)
	if err != nil {
		return &PublishRefusedError{Detail: domain.DestPublishError{Kind: "invalid", Field: truncate(err.Error(), 200)}}
	}
	return refusalFor(b.assemble(nodeView{bound: true}))
}

// refusalFor is the verdict on a policy: nil when it validates, else which
// limit it is over, or that it is invalid.
func refusalFor(p *protocol.DestinationPolicy) *PublishRefusedError {
	if p == nil {
		return nil
	}
	if over := usageOf(p).over(); over != nil {
		return &PublishRefusedError{Detail: *over}
	}
	if err := protocol.ValidateDestinationPolicy(p); err != nil {
		return &PublishRefusedError{Detail: domain.DestPublishError{Kind: "invalid", Field: truncate(err.Error(), 200)}}
	}
	return nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Usage is how much of each limit a policy uses.
type Usage struct {
	Rules, Domains, Regexps, CIDRs, SourceCIDRs, Subjects, Inbounds, Bytes int
}

// Limits is the protocol's limits, for the budget the admin page shows.
var Limits = Usage{
	Rules: protocol.MaxPolicyRules, Domains: protocol.MaxPolicyDomains, Regexps: protocol.MaxPolicyRegexps,
	CIDRs: protocol.MaxPolicyCIDRs, SourceCIDRs: protocol.MaxPolicySourceCIDRs, Subjects: protocol.MaxPolicySubjects,
	Inbounds: protocol.MaxPolicyInbounds, Bytes: protocol.MaxPolicyBytes,
}

// usageOf measures a policy the way ValidateDestinationPolicy does.
func usageOf(p *protocol.DestinationPolicy) Usage {
	u := Usage{Rules: len(p.Rules), Subjects: len(p.Exempt)}
	for _, r := range p.Rules {
		u.Domains += len(r.Domains)
		for _, d := range r.Domains {
			if strings.HasPrefix(d, "regexp:") {
				u.Regexps++
			}
		}
		u.CIDRs += len(r.CIDRs)
		u.SourceCIDRs += len(r.SourceCIDRs)
		u.Subjects += len(r.Subjects)
		u.Inbounds += len(r.Inbounds)
	}
	if b, err := jsonMarshal(p); err == nil {
		u.Bytes = len(b)
	}
	return u
}

// over names the first limit the usage passes. The order is the order an admin
// can most easily do something about it.
func (u Usage) over() *domain.DestPublishError {
	for _, c := range []struct {
		kind       string
		used, most int
	}{
		{"rules", u.Rules, Limits.Rules}, {"domains", u.Domains, Limits.Domains}, {"regexps", u.Regexps, Limits.Regexps},
		{"cidrs", u.CIDRs, Limits.CIDRs}, {"source_cidrs", u.SourceCIDRs, Limits.SourceCIDRs},
		{"subjects", u.Subjects, Limits.Subjects}, {"inbounds", u.Inbounds, Limits.Inbounds}, {"bytes", u.Bytes, Limits.Bytes},
	} {
		if c.used > c.most {
			return &domain.DestPublishError{Kind: c.kind, Used: c.used, Limit: c.most}
		}
	}
	return nil
}
