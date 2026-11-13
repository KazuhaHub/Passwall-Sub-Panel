package destpolicy

import (
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func smtpPolicy(h *harness, name string) *domain.DestPolicy {
	return h.policy(name, domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.Inline = domain.DestPolicyInline{Ports: "25"}
	})
}

// A burst of edits is one change to the fleet: nothing is published until no
// write has followed for the window, and then everything is published once.
func TestPublish_TrailingDebounce(t *testing.T) {
	h := newHarness(t)
	h.settings.set = ports.UISettings{DestPolicyApplyMinSeconds: 60}
	smtpPolicy(h, "a")
	smtpPolicy(h, "b")

	if res := h.compile(base1()); res.Policy != nil || len(h.publishes()) != 0 {
		t.Fatalf("published inside the window: %+v %v", res.Policy, h.publishes())
	}
	h.backdate(59*time.Second, 59*time.Second)
	if res := h.compile(base1()); res.Policy != nil {
		t.Fatal("published one second early")
	}
	h.backdate(61*time.Second, 61*time.Second)
	res := h.compile(base1())
	if res.Policy == nil || len(res.Policy.Rules) != 2 {
		t.Fatalf("two edits must publish as one change: %+v", res.Policy)
	}
	if got := h.publishes(); len(got) != 1 || got[0] != TriggerDebounced {
		t.Fatalf("publishes = %v, want one debounced", got)
	}
	st := h.state()
	if st.Unpublished() || st.FirstUnpublishedAt != nil {
		t.Fatalf("state after publish = %+v", st)
	}
	// Idle: nothing more happens.
	h.compile(base1())
	if len(h.publishes()) != 1 {
		t.Fatal("an idle compile published again")
	}
}

func TestPublish_DebounceSurvivesARestart(t *testing.T) {
	h := newHarness(t)
	smtpPolicy(h, "a")
	h.restart()
	if res := h.compile(base1()); res.Policy != nil {
		t.Fatal("a restart must not publish an edit that is still inside its window")
	}
	h.backdate(2*time.Minute, 2*time.Minute)
	h.restart()
	if res := h.compile(base1()); res.Policy == nil {
		t.Fatal("the stored stamps decide, so the window must elapse across a restart")
	}
}

func TestPublish_MaxWaitStopsEndlessEditsFromNeverPublishing(t *testing.T) {
	h := newHarness(t)
	h.settings.set = ports.UISettings{DestPolicyApplyMinSeconds: 60}
	smtpPolicy(h, "a")
	// The latest write is recent (the burst is still going), the first one is
	// older than five windows.
	h.backdate(5*time.Second, 301*time.Second)
	if res := h.compile(base1()); res.Policy == nil {
		t.Fatal("never published although the first edit is 5 windows old")
	}
	if got := h.publishes(); len(got) != 1 || got[0] != TriggerMaxWait {
		t.Fatalf("publishes = %v", got)
	}
}

func TestPublish_NowSkipsTheWait(t *testing.T) {
	h := newHarness(t)
	smtpPolicy(h, "a")
	st, err := h.svc.Publish(h.ctx)
	if err != nil || st.Unpublished() {
		t.Fatalf("publish now = (%+v, %v)", st, err)
	}
	if got := h.publishes(); len(got) != 1 || got[0] != TriggerManual {
		t.Fatalf("publishes = %v", got)
	}
	if res := h.compile(base1()); res.Policy == nil {
		t.Fatal("not visible after a manual publish")
	}
}

// Paused takes priority over everything and a pause change publishes what was
// waiting; resuming restores the policy with no new edit.
func TestPublish_PausedIsNilAndPausePublishesPending(t *testing.T) {
	h := newHarness(t)
	smtpPolicy(h, "a")
	h.publishNow()
	if res := h.compile(base1()); res.Policy == nil {
		t.Fatal("not published")
	}
	smtpPolicy(h, "b") // pending
	if err := h.svc.SetPaused(h.ctx, true); err != nil {
		t.Fatal(err)
	}
	if got := h.publishes(); len(got) != 2 || got[1] != TriggerPause {
		t.Fatalf("publishes = %v: a pause change publishes what was waiting", got)
	}
	if res := h.compile(base1()); res.Policy != nil || res.Digest != "" {
		t.Fatalf("a paused panel sent a policy: %+v", res.Policy)
	}
	meta, _ := h.repos.Agents.Get(h.ctx, "agt_9")
	if meta.MintedSHA256 != "" {
		t.Fatalf("minted = %q while paused", meta.MintedSHA256)
	}
	if err := h.svc.SetPaused(h.ctx, false); err != nil {
		t.Fatal(err)
	}
	if res := h.compile(base1()); res.Policy == nil || len(res.Policy.Rules) != 2 {
		t.Fatalf("resumed policy = %+v", res.Policy)
	}
	// No change: no write, no second publish.
	if err := h.svc.SetPaused(h.ctx, false); err != nil || len(h.publishes()) != 2 {
		t.Fatalf("a no-op pause published: %v %v", err, h.publishes())
	}
}

// The publish reads the generation FIRST and the definitions after it, and
// completes with that generation. A write that lands in between is therefore
// not lost: the next round sees generation > published and publishes again.
// Reading the definitions first would record the later generation as published
// with a body that lacks the write, and nothing would ever send it.
func TestPublish_AWriteBetweenTheReadAndTheCompletionIsPublishedNextRound(t *testing.T) {
	h := newHarness(t)
	smtpPolicy(h, "a")
	h.defs.afterRead = func() { smtpPolicy(h, "late") }
	if _, err := h.svc.Publish(h.ctx); err != nil {
		t.Fatal(err)
	}
	if st := h.state(); !st.Unpublished() {
		t.Fatalf("state = %+v: the late write's generation must still be ahead of the published one", st)
	}
	if res := h.compile(base1()); res.Policy == nil || len(res.Policy.Rules) != 1 {
		t.Fatalf("the first publish carries what was committed before its generation: %+v", res.Policy)
	}
	h.backdate(2*time.Minute, 2*time.Minute)
	if res := h.compile(base1()); res.Policy == nil || len(res.Policy.Rules) != 2 {
		t.Fatalf("the late write was never published: %+v", res.Policy)
	}
}

func TestPublish_OnlyTheNewestSnapshotIsKept(t *testing.T) {
	h := newHarness(t)
	smtpPolicy(h, "a")
	h.publishNow()
	smtpPolicy(h, "b")
	h.publishNow()
	var n int64
	if err := h.db.Table("dest_policy_snapshots").Count(&n).Error; err != nil || n != 1 {
		t.Fatalf("snapshots = %d (%v), want only the published one", n, err)
	}
}

// Over a limit that is the same for every node the publish is refused: the
// previous snapshot stays in force, the reason is recorded, and the same
// generation is not retried on every sync of every node.
func TestPublish_PreValidationRefusesAndKeepsThePreviousSnapshot(t *testing.T) {
	h := newHarness(t)
	keep := smtpPolicy(h, "keep")
	h.publishNow()
	before := h.compile(base1())
	if before.Policy == nil {
		t.Fatal("setup")
	}

	var extra []*domain.DestPolicy
	for i := 0; i < 256; i++ { // 257 rules with "keep": one over MaxPolicyRules
		extra = append(extra, h.policy("p"+itoa(int64(i)), domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
			p.Inline = domain.DestPolicyInline{Ports: "26"}
		}))
	}
	_, err := h.svc.Publish(h.ctx)
	var refused *PublishRefusedError
	if !errors.As(err, &refused) || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("publish error = %v, want a PublishRefusedError (409)", err)
	}
	if refused.Detail.Kind != "rules" || refused.Detail.Used != 257 || refused.Detail.Limit != 256 {
		t.Fatalf("detail = %+v", refused.Detail)
	}
	st := h.state()
	if st.PublishError == nil || st.PublishError.Kind != "rules" || st.PublishErrorAt == nil {
		t.Fatalf("publish error not recorded: %+v", st)
	}
	if st.PublishedGeneration == st.Generation {
		t.Fatal("published_generation must not move")
	}
	if h.rec.rejected != 1 {
		t.Fatalf("rejected counter = %d", h.rec.rejected)
	}
	if after := h.compile(base1()); after.Digest != before.Digest {
		t.Fatal("a refused publish changed what the nodes get")
	}

	// Not retried on every sync (the validation expands every list).
	h.backdate(2*time.Minute, 2*time.Minute)
	reads := h.counts.defsReads
	for i := 0; i < 3; i++ {
		h.compile(base1())
	}
	if h.counts.defsReads != reads {
		t.Fatalf("definition reads went %d -> %d: the failed generation was retried on the next syncs", reads, h.counts.defsReads)
	}

	// Fixing it publishes on the next round and clears the error.
	if err := h.repos.Policies.Delete(h.ctx, extra[0].ID); err != nil {
		t.Fatal(err)
	}
	h.backdate(2*time.Minute, 2*time.Minute)
	if res := h.compile(base1()); res.Policy == nil || len(res.Policy.Rules) != 256 {
		t.Fatalf("after the fix: %+v", res.Policy)
	}
	if st := h.state(); st.PublishError != nil || st.Unpublished() {
		t.Fatalf("state after the fix = %+v", st)
	}
	_ = keep
}

// An empty custom list does not make the policy unpublishable: the rule is
// skipped and the rest is published.
func TestPublish_AListEditedToNothingSkipsTheRuleAndStillPublishes(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,a.example.com")
	h.policy("p", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	smtpPolicy(h, "smtp")
	if _, _, err := h.lists.UpdateCustom(h.ctx, l.ID, "l", []byte("# emptied")); err != nil {
		t.Fatal(err)
	}
	h.publishNow()
	res := h.compile(base1())
	if res.Policy == nil || len(res.Policy.Rules) != 1 || res.Policy.Rules[0].Ports != "25" {
		t.Fatalf("policy = %+v", res.Policy)
	}
	if st := h.state(); st.PublishError != nil {
		t.Fatalf("an empty list is not a publish error: %+v", st.PublishError)
	}
}

func TestPublish_NothingPendingIsANoOp(t *testing.T) {
	h := newHarness(t)
	st, err := h.svc.Publish(h.ctx)
	if err != nil || st.Unpublished() || len(h.publishes()) != 0 {
		t.Fatalf("empty publish = (%+v, %v) %v", st, err, h.publishes())
	}
}
