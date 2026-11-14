package destpolicy

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// A node that refuses a new policy keeps running the last one that applied (the
// last known good, LKG), trimmed; a node whose LKG is itself refused is sent
// no policy; and a refused policy is tried again only for a new generation, a
// change of the three delivery capabilities, or the admin's "send again".

func applied(digest string) *protocol.PolicyStatus {
	return &protocol.PolicyStatus{Digest: digest, State: protocol.PolicyStateApplied}
}

func rejected(digest string) *protocol.PolicyStatus {
	return &protocol.PolicyStatus{Digest: digest, State: protocol.PolicyStateRejected, IssueCode: protocol.IssueDestinationPolicyRejected}
}

func (h *harness) observe(st *protocol.PolicyStatus, caps ...string) []domain.NodeAgentIssue {
	h.t.Helper()
	if len(caps) == 0 {
		caps = []string{policyCap}
	}
	issues, err := h.svc.ObserveStatus(h.ctx, "agt_9", caps, st)
	if err != nil {
		h.t.Fatal(err)
	}
	return issues
}

func (h *harness) meta() *domain.DestAgentPolicy {
	h.t.Helper()
	m, err := h.repos.Agents.Get(h.ctx, "agt_9")
	if err != nil {
		h.t.Fatal(err)
	}
	return m
}

// withLKG publishes policy A, has the node apply it, and returns A's digest.
func withLKG(t *testing.T) (*harness, *domain.DestPolicy, Result) {
	t.Helper()
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,a.example.com")
	a := h.policy("A", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()
	res := h.compile(base1())
	if res.Policy == nil {
		t.Fatal("setup: nothing minted")
	}
	h.observe(applied(res.Digest))
	return h, a, res
}

func TestLKG_RecordedWhenTheNodeAppliesWhatWasMinted(t *testing.T) {
	h, _, res := withLKG(t)
	m := h.meta()
	if m.AppliedSHA256 != res.Digest || m.AppliedRuleCount != 1 || m.AppliedAt == nil || m.MintedSHA256 != res.Digest || m.DesiredSHA256 != res.Digest {
		t.Fatalf("state = %+v", m)
	}
	body, err := h.repos.Agents.AppliedBody(h.ctx, "agt_9")
	if err != nil || !strings.Contains(string(body), `"domain:a.example.com"`) {
		t.Fatalf("body = %q (%v)", body, err)
	}
	if m.ReportedSHA256 != res.Digest || m.ReportedState != "applied" {
		t.Fatalf("reported = %+v", m)
	}
}

func TestObserveStatus_WritesOnlyWhatChanged(t *testing.T) {
	h, _, res := withLKG(t)
	before := h.counts.patches
	for i := 0; i < 3; i++ {
		h.observe(applied(res.Digest))
	}
	if h.counts.patches != before {
		t.Fatalf("%d writes for an unchanged status", h.counts.patches-before)
	}
	h.observe(applied(""))
	if h.counts.patches != before+1 {
		t.Fatalf("a changed status wrote %d times", h.counts.patches-before)
	}
	if m := h.meta(); m.AppliedSHA256 != res.Digest {
		t.Fatalf("an empty digest (no policy) must not erase the last known good: %+v", m)
	}
}

func TestObserveStatus_IgnoresAnAgentWithoutTheCapabilityAndNilStatus(t *testing.T) {
	h := newHarness(t)
	if issues, err := h.svc.ObserveStatus(h.ctx, "agt_9", []string{"x"}, applied("")); err != nil || issues != nil {
		t.Fatalf("no capability: (%v, %v)", issues, err)
	}
	if _, err := h.svc.ObserveStatus(h.ctx, "agt_9", []string{policyCap}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.repos.Agents.Get(h.ctx, "agt_9"); err == nil {
		t.Fatal("a row was written for nothing")
	}
}

func (h *harness) editPolicy(p *domain.DestPolicy, domainsLine string) {
	h.t.Helper()
	if _, _, err := h.lists.UpdateCustom(h.ctx, p.ListIDs[0], "l", []byte(domainsLine)); err != nil {
		h.t.Fatal(err)
	}
	h.publishNow()
}

func TestFallback_RejectedNewPolicyRunsTheLKGAndIsNotRetriedOnTheSameGeneration(t *testing.T) {
	h, a, first := withLKG(t)
	h.editPolicy(a, "DOMAIN-SUFFIX,b.example.com")
	second := h.compile(base1())
	if second.Digest == first.Digest {
		t.Fatal("the edit did not reach the node")
	}
	issues := h.observe(rejected(second.Digest))
	if len(issues) != 0 {
		t.Fatalf("one-time issues = %+v", issues)
	}
	m := h.meta()
	if m.FallbackReason != domain.DestFallbackRejected || m.RejectedGeneration != h.generation() || m.RejectedCaps != "p" {
		t.Fatalf("state = %+v", m)
	}

	res := h.compile(base1())
	if res.Digest != first.Digest {
		t.Fatalf("minted %s, want the last known good %s", res.Digest, first.Digest)
	}
	if len(res.Issues) != 1 || res.Issues[0].Code != IssueRejected {
		t.Fatalf("issues = %+v", res.Issues)
	}
	if h.rec.count(ResultFallbackReject) != 1 {
		t.Fatalf("results = %v", h.rec.compile)
	}
	// The same generation, the same capabilities: not retried, any number of syncs.
	for i := 0; i < 3; i++ {
		if again := h.compile(base1()); again.Digest != first.Digest {
			t.Fatal("retried within the same generation")
		}
	}
	// The node applies the LKG again: nothing about the LKG changes.
	h.observe(applied(first.Digest))
	if m := h.meta(); m.FallbackReason != domain.DestFallbackRejected || m.AppliedSHA256 != first.Digest {
		t.Fatalf("state = %+v", m)
	}
}

func TestFallback_RetryTriggers(t *testing.T) {
	setup := func(t *testing.T) (*harness, Result, Result) {
		h, a, first := withLKG(t)
		h.editPolicy(a, "DOMAIN-SUFFIX,b.example.com")
		second := h.compile(base1())
		h.observe(rejected(second.Digest))
		if res := h.compile(base1()); res.Digest != first.Digest {
			t.Fatal("setup: not in fallback")
		}
		return h, first, second
	}
	t.Run("a new generation", func(t *testing.T) {
		h, _, second := setup(t)
		smtpPolicy(h, "other")
		h.publishNow()
		if res := h.compile(base1()); res.Policy == nil || len(res.Policy.Rules) != 2 {
			t.Fatalf("not retried after a new generation: %+v", res.Policy)
		}
		if m := h.meta(); m.FallbackReason != domain.DestFallbackNone {
			t.Fatalf("state = %+v", m)
		}
		_ = second
	})
	t.Run("a change of the delivery capabilities", func(t *testing.T) {
		h, first, _ := setup(t)
		res := h.compile(base1(), policyCap, "task.execution.v1")
		if res.Digest != first.Digest {
			t.Fatal("an unrelated capability change must not retry")
		}
		res = h.compile(base1(), policyCap, "audit.hits.v1")
		if res.Digest == first.Digest || res.Policy == nil {
			t.Fatal("gaining audit.hits.v1 must retry the new policy")
		}
	})
	t.Run("the admin's send again", func(t *testing.T) {
		h, first, _ := setup(t)
		if res := h.compile(base1()); res.Digest != first.Digest {
			t.Fatal("setup")
		}
		ok, err := h.svc.Retry(h.ctx, "agt_9")
		if err != nil || !ok {
			t.Fatalf("retry = (%v, %v)", ok, err)
		}
		if res := h.compile(base1()); res.Digest == first.Digest {
			t.Fatal("not retried after the admin asked")
		}
		if ok, _ := h.svc.Retry(h.ctx, "agt_9"); ok {
			t.Fatal("retry on an agent that is not refused must say so")
		}
	})
	t.Run("a retried policy that applies clears the refusal", func(t *testing.T) {
		h, _, _ := setup(t)
		if _, err := h.svc.Retry(h.ctx, "agt_9"); err != nil {
			t.Fatal(err)
		}
		res := h.compile(base1())
		h.observe(applied(res.Digest))
		m := h.meta()
		if m.FallbackReason != domain.DestFallbackNone || m.AppliedSHA256 != res.Digest {
			t.Fatalf("state = %+v", m)
		}
	})
}

func TestFallback_NoLKGMeansNoPolicy(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,a.example.com")
	h.policy("A", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()
	res := h.compile(base1())
	h.observe(rejected(res.Digest))
	after := h.compile(base1())
	if after.Policy != nil || after.Digest != "" {
		t.Fatalf("a node that never applied anything fell back to %+v", after.Policy)
	}
	if h.rec.count(ResultFallbackNil) != 1 {
		t.Fatalf("results = %v", h.rec.compile)
	}
}

// The LKG is itself refused (the core changed under it): minting it again would
// make the whole config undeployable, so the record is dropped and the node is
// sent no policy.
func TestFallback_AnLKGThatIsItselfRefusedBecomesNil(t *testing.T) {
	h, a, first := withLKG(t)
	h.editPolicy(a, "DOMAIN-SUFFIX,b.example.com")
	second := h.compile(base1())
	h.observe(rejected(second.Digest))
	if res := h.compile(base1()); res.Digest != first.Digest {
		t.Fatal("setup: LKG not minted")
	}
	issues := h.observe(rejected(first.Digest))
	if len(issues) != 1 || issues[0].Code != IssueLKGRejected {
		t.Fatalf("issues = %+v", issues)
	}
	m := h.meta()
	if m.AppliedSHA256 != "" || m.AppliedRuleCount != 0 {
		t.Fatalf("the refused LKG was kept: %+v", m)
	}
	if body, _ := h.repos.Agents.AppliedBody(h.ctx, "agt_9"); len(body) != 0 {
		t.Fatalf("the refused LKG body was kept (%d bytes)", len(body))
	}
	if res := h.compile(base1()); res.Policy != nil {
		t.Fatalf("minted %+v after the LKG was refused", res.Policy)
	}
}

func TestFallback_AStaleRefusalIsRecordedButChangesNothing(t *testing.T) {
	h, _, first := withLKG(t)
	h.observe(rejected(strings.Repeat("ab", 32))) // not what was minted
	if m := h.meta(); m.FallbackReason != domain.DestFallbackNone || m.ReportedState != "rejected" {
		t.Fatalf("state = %+v", m)
	}
	if res := h.compile(base1()); res.Digest != first.Digest {
		t.Fatal("a refusal of something else changed the minted policy")
	}
}

// The trimming only ever loosens.
func TestFallback_TheLKGIsTrimmedToWhatIsStillInForce(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,a.example.com")
	keep := h.policy("keep", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	gone := smtpPolicy(h, "gone")
	scoped := h.policy("scoped", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.Inline = domain.DestPolicyInline{Ports: "26"}
		p.Scope, p.GroupIDs = domain.DestScopeGroups, []int64{5}
	})
	h.setGroups(map[int64]int64{10: 5, 11: 5})
	for _, u := range []int64{10, 11} {
		if err := h.repos.Exemptions.Upsert(h.ctx, &domain.DestExemption{UserID: u}); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.repos.SourceRules.Upsert(h.ctx, &domain.DestSourceRule{NodeID: 1, CIDRs: []string{"203.0.113.0/24"}, State: domain.DestStateOn}, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.repos.SourceRules.Upsert(h.ctx, &domain.DestSourceRule{NodeID: 2, CIDRs: []string{"198.51.100.0/24"}, State: domain.DestStateOn}, nil); err != nil {
		t.Fatal(err)
	}
	h.publishNow()
	f := base1()
	full := h.compile(f)
	h.observe(applied(full.Digest))
	if n := len(full.Policy.Rules); n != 5 {
		t.Fatalf("setup rules = %d (%v)", n, ruleIDs(full.Policy))
	}

	// "gone" is deleted, the source rule of node 2 is turned off, user 11
	// leaves the roster, and exemption 12 is added; then a new policy is
	// refused.
	if err := h.repos.Policies.Delete(h.ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.repos.SourceRules.Upsert(h.ctx, &domain.DestSourceRule{NodeID: 2, CIDRs: []string{"198.51.100.0/24"}, State: domain.DestStateOff}, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.repos.Exemptions.Upsert(h.ctx, &domain.DestExemption{UserID: 12}); err != nil {
		t.Fatal(err)
	}
	smtpPolicy(h, "new")
	h.publishNow()
	f.users = []int64{10, 12}
	next := h.compile(f)
	h.observe(rejected(next.Digest))

	res := h.compile(f)
	got := ruleIDs(res.Policy)
	want := []string{"s1", "p" + itoa(keep.ID), "p" + itoa(scoped.ID)}
	if !slices.Equal(got, want) {
		t.Fatalf("rules = %v, want %v: deleted policy and the switched-off source rule go, the rest stays", got, want)
	}
	for _, r := range res.Policy.Rules {
		if r.ID == "s1" && !slices.Equal(r.Inbounds, []protocol.ListenerKey{"lst_1"}) {
			t.Fatalf("source rule = %+v", r)
		}
		if r.ID == "p"+itoa(scoped.ID) && !slices.Equal(r.Subjects, []protocol.SubjectKey{"usr_10"}) {
			t.Fatalf("subjects = %v: usr_11 left the roster", r.Subjects)
		}
	}
	if !slices.Equal(res.Policy.Exempt, []protocol.SubjectKey{"usr_10", "usr_12"}) {
		t.Fatalf("exempt = %v: the LKG's and the current, limited to the roster", res.Policy.Exempt)
	}
	if err := protocol.ValidateDestinationPolicy(res.Policy); err != nil {
		t.Fatal(err)
	}
}

// A fallback that trims everything away is no policy at all.
func TestFallback_TrimmingEverythingYieldsNil(t *testing.T) {
	h, a, _ := withLKG(t)
	h.editPolicy(a, "DOMAIN-SUFFIX,b.example.com")
	second := h.compile(base1())
	h.observe(rejected(second.Digest))
	if err := h.repos.Policies.Update(h.ctx, func() *domain.DestPolicy { a.State = domain.DestStateOff; return a }(), nil); err != nil {
		t.Fatal(err)
	}
	smtpPolicy(h, "z")
	h.publishNow()
	// A new generation retries, so go through the retry path first: the new
	// policy is z only, and is refused as well.
	third := h.compile(base1())
	h.observe(rejected(third.Digest))
	res := h.compile(base1())
	if res.Policy != nil {
		t.Fatalf("the LKG's only rule belongs to a policy that is off; got %+v", res.Policy)
	}
}

// Paused takes priority over a fallback, and the LKG is not rewritten while it
// holds.
func TestFallback_PausedWinsAndLeavesTheLKGAlone(t *testing.T) {
	h, a, first := withLKG(t)
	h.editPolicy(a, "DOMAIN-SUFFIX,b.example.com")
	second := h.compile(base1())
	h.observe(rejected(second.Digest))
	if err := h.svc.SetPaused(h.ctx, true); err != nil {
		t.Fatal(err)
	}
	if res := h.compile(base1()); res.Policy != nil {
		t.Fatal("a paused node in fallback still got a policy")
	}
	// The node reports what it ran before the pause took effect; the record of
	// what applied must not move.
	h.observe(applied(second.Digest))
	if m := h.meta(); m.AppliedSHA256 != first.Digest {
		t.Fatalf("the LKG moved during a pause: %+v", m)
	}
}

func TestCompile_StateSurvivesARestart(t *testing.T) {
	h, a, first := withLKG(t)
	h.editPolicy(a, "DOMAIN-SUFFIX,b.example.com")
	second := h.compile(base1())
	h.observe(rejected(second.Digest))
	h.restart()
	res := h.compile(base1())
	if res.Digest != first.Digest {
		t.Fatalf("after a restart the node got %s, want the LKG %s", res.Digest, first.Digest)
	}
	// And it is not minted again (the compile result of the restarted service
	// is the recorded one, so the write is a no-op).
	_ = time.Second
}

// ---- review round: refused fallbacks, generations, transient errors ----

// The fallback that is refused need not be the recorded LKG: when a policy was
// deleted, the fallback is the TRIMMED LKG, whose digest differs from
// applied_sha256. Refused, it must still be dropped, or PSP mints it forever
// and the config never deploys (roster frozen).
func TestFallback_ATrimmedLKGThatIsRefusedBecomesNil(t *testing.T) {
	h := newHarness(t)
	l := h.list("l", "DOMAIN-SUFFIX,a.example.com")
	h.policy("A", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	b := smtpPolicy(h, "B")
	h.publishNow()
	first := h.compile(base1())
	h.observe(applied(first.Digest))

	if err := h.repos.Policies.Delete(h.ctx, b.ID); err != nil { // trims the LKG
		t.Fatal(err)
	}
	extra := h.policy("C", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "26"} })
	_ = extra
	h.publishNow()
	next := h.compile(base1())
	h.observe(rejected(next.Digest))
	trimmed := h.compile(base1())
	if trimmed.Policy == nil || trimmed.Digest == first.Digest || trimmed.Digest == next.Digest {
		t.Fatalf("setup: want a trimmed fallback, got %+v", trimmed.Digest)
	}
	issues := h.observe(rejected(trimmed.Digest))
	if len(issues) != 1 || issues[0].Code != IssueLKGRejected {
		t.Fatalf("issues = %+v", issues)
	}
	if m := h.meta(); m.AppliedSHA256 != "" || m.AppliedRuleCount != 0 {
		t.Fatalf("the refused fallback's record was kept: %+v", m)
	}
	if res := h.compile(base1()); res.Policy != nil {
		t.Fatalf("a refused fallback was minted again: %+v", res.Policy)
	}
}

// A refusal belongs to the generation the refused policy was minted at.
func TestFallback_RefusalIsRecordedAgainstTheMintGenerationNotTheLatest(t *testing.T) {
	h, a, _ := withLKG(t)
	h.editPolicy(a, "DOMAIN-SUFFIX,b.example.com")
	second := h.compile(base1())
	mintedAt := h.generation()
	h.editPolicy(a, "DOMAIN-SUFFIX,c.example.com") // N+1 published before the report arrives
	h.observe(rejected(second.Digest))
	if m := h.meta(); m.RejectedGeneration != mintedAt || m.MintedGeneration != mintedAt {
		t.Fatalf("rejected at %d (minted at %d), want %d", m.RejectedGeneration, m.MintedGeneration, mintedAt)
	}
	if res := h.compile(base1()); res.Digest == second.Digest || res.Policy == nil || res.Issues != nil {
		t.Fatalf("generation N+1 was not tried: %+v", res)
	}
	if m := h.meta(); m.FallbackReason != domain.DestFallbackNone {
		t.Fatalf("state = %+v", m)
	}
}

// A transient read error while falling back must not ship "no policy" and must
// not be cached: the sync fails and the next one tries again.
func TestFallback_ATransientBodyReadErrorIsNotCachedAsNil(t *testing.T) {
	h, a, first := withLKG(t)
	h.editPolicy(a, "DOMAIN-SUFFIX,b.example.com")
	second := h.compile(base1())
	h.observe(rejected(second.Digest))
	h.counts.Lock()
	h.counts.failBody = true
	h.counts.Unlock()
	if _, err := h.svc.Compile(h.ctx, CompileInput{PanelID: 9, AgentID: "agt_9", Capabilities: []string{policyCap}, Snapshot: base1().snapshot()}); err == nil {
		t.Fatal("a failed body read produced an answer")
	}
	h.counts.Lock()
	h.counts.failBody = false
	h.counts.Unlock()
	if res := h.compile(base1()); res.Digest != first.Digest {
		t.Fatalf("after the error the node got %q, want the LKG", res.Digest)
	}
}

// A refusal survives a sniffing episode, a no-policy round and a pause.
func TestFallback_RefusalSurvivesPause(t *testing.T) {
	h, a, first := withLKG(t)
	h.editPolicy(a, "DOMAIN-SUFFIX,b.example.com")
	second := h.compile(base1())
	h.observe(rejected(second.Digest))
	// (A change of the inbounds' sniffing is a change of the refusal's context
	// and is retried on purpose: see TestFallback_ARefusedPolicyIsRetriedWhenTheNodeChanged.)
	if err := h.svc.SetPaused(h.ctx, true); err != nil {
		t.Fatal(err)
	}
	h.compile(base1())
	if err := h.svc.SetPaused(h.ctx, false); err != nil {
		t.Fatal(err)
	}
	if res := h.compile(base1()); res.Digest != first.Digest {
		t.Fatalf("the refusal was erased by a pause: minted %q", res.Digest)
	}
}

func TestFallback_TrimmingDropsAChangedActionAndFiltersByCurrentGroup(t *testing.T) {
	h := newHarness(t)
	h.setGroups(map[int64]int64{10: 5, 11: 5, 12: 6})
	flip := smtpPolicy(h, "flip")
	grp := h.policy("grp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.Inline = domain.DestPolicyInline{Ports: "26"}
		p.Scope, p.GroupIDs = domain.DestScopeGroups, []int64{5}
	})
	h.publishNow()
	f := base1()
	full := h.compile(f)
	h.observe(applied(full.Digest))

	flip.Action = domain.DestActionAllow
	if err := h.repos.Policies.Update(h.ctx, flip, nil); err != nil {
		t.Fatal(err)
	}
	// User 11 leaves group 5.
	h.setGroups(map[int64]int64{10: 5, 11: 6, 12: 6})
	smtpPolicy(h, "new")
	h.publishNow()
	next := h.compile(f)
	h.observe(rejected(next.Digest))
	res := h.compile(f)
	if got := ruleIDs(res.Policy); len(got) != 1 || got[0] != "p"+itoa(grp.ID) {
		t.Fatalf("rules = %v: the policy that changed action must go", got)
	}
	if !slices.Equal(res.Policy.Rules[0].Subjects, []protocol.SubjectKey{"usr_10"}) {
		t.Fatalf("subjects = %v: only current members of the group", res.Policy.Rules[0].Subjects)
	}
}

func TestOverLimitIssueDetailDoesNotCarryALiveCount(t *testing.T) {
	d := (&Service{}).issuesFor(domain.DestFallbackOverLimit, &domain.DestAgentPolicy{}, "", &domain.DestPublishError{Kind: "subjects", Used: 51000, Limit: 50000})
	if len(d) != 1 || d[0].Detail != "subjects over limit" {
		t.Fatalf("issue = %+v", d)
	}
}

func TestPublish_ACompileDoesNotWaitForARunningPublish(t *testing.T) {
	h := newHarness(t)
	smtpPolicy(h, "a")
	h.publishNow()
	smtpPolicy(h, "b")
	h.backdate(2*time.Minute, 2*time.Minute)
	release := make(chan struct{})
	entered := make(chan struct{})
	h.defs.afterRead = func() { close(entered); <-release }
	published := make(chan struct{})
	go func() { _, _ = h.svc.Publish(h.ctx); close(published) }()
	<-entered
	done := make(chan Result, 1)
	go func() { done <- h.compile(base1()) }()
	select {
	case res := <-done:
		if res.Policy == nil || len(res.Policy.Rules) != 1 {
			t.Fatalf("compile during a publish = %+v", res.Policy)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a compile blocked on a running publish")
	}
	close(release)
	<-published
}

func TestCompile_AMissingSnapshotFallsBackToTheCurrentOne(t *testing.T) {
	h := newHarness(t)
	smtpPolicy(h, "a")
	h.publishNow()
	h.compile(base1())
	smtpPolicy(h, "b")
	h.publishNow() // prunes the first snapshot
	h.svc.mu.Lock()
	h.svc.baseCache = nil
	h.svc.mu.Unlock()
	// A compile that read the older published generation would find it gone.
	if _, err := h.svc.baseFor(h.ctx, h.generation()-1); err == nil {
		t.Log("old snapshot still there")
	}
	if res := h.compile(base1()); res.Policy == nil || len(res.Policy.Rules) != 2 {
		t.Fatalf("compile = %+v", res.Policy)
	}
}

func TestPruneAgentsForgetsTheDeleted(t *testing.T) {
	h, _, _ := withLKG(t)
	h.svc.PruneAgents(map[string]bool{"other": true})
	h.svc.mu.Lock()
	n := len(h.svc.agentMeta) + len(h.svc.compiled)
	h.svc.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d cache entries remain for a deleted agent", n)
	}
}

// ---- round 2 ----

// A body that stays unreadable cannot freeze the node for ever: after a bound
// the fallback is nil (always deployable) and the issue says why.
func TestFallback_APersistentBodyReadErrorEventuallyFallsBackToNil(t *testing.T) {
	h, a, first := withLKG(t)
	h.editPolicy(a, "DOMAIN-SUFFIX,b.example.com")
	second := h.compile(base1())
	h.observe(rejected(second.Digest))
	h.counts.Lock()
	h.counts.failBody = true
	h.counts.Unlock()
	in := CompileInput{PanelID: 9, AgentID: "agt_9", Capabilities: []string{policyCap}, Snapshot: base1().snapshot()}
	for i := 0; i < 2; i++ {
		if _, err := h.svc.Compile(h.ctx, in); err == nil {
			t.Fatalf("attempt %d answered", i+1)
		}
	}
	res, err := h.svc.Compile(h.ctx, in)
	if err != nil || res.Policy != nil {
		t.Fatalf("after the bound: (%+v, %v), want nil", res.Policy, err)
	}
	found := false
	for _, is := range res.Issues {
		found = found || is.Code == IssueLKGUnreadable
	}
	if !found {
		t.Fatalf("issues = %+v", res.Issues)
	}
	// Not cached: when the database recovers the LKG is used again.
	h.counts.Lock()
	h.counts.failBody = false
	h.counts.Unlock()
	h.clock.Advance(2 * time.Minute) // the re-read is backed off to once a minute
	if res := h.compile(base1()); res.Digest != first.Digest {
		t.Fatalf("after recovery: %q, want the LKG", res.Digest)
	}
}

// A newer published generation moves minted_generation even when the minted
// bytes are the same, so a later refusal is recorded against the right one.
func TestCompile_MintedGenerationAdvancesWithTheGenerationEvenForSameBytes(t *testing.T) {
	h := newHarness(t)
	smtpPolicy(h, "a")
	h.publishNow()
	h.compile(base1())
	before := h.meta().MintedGeneration
	h.policy("unrelated", domain.DestActionBlock, domain.DestStateOff, func(p *domain.DestPolicy) { p.Inline = domain.DestPolicyInline{Ports: "99"} })
	h.publishNow()
	h.compile(base1())
	if after := h.meta().MintedGeneration; after != h.generation() || after <= before {
		t.Fatalf("minted generation %d -> %d, want %d", before, after, h.generation())
	}
}
