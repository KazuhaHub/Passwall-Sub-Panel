package destpolicy

import (
	"context"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// fallbackLog records the agents FallbackChanged was told about.
type fallbackLog struct {
	mu     sync.Mutex
	agents []string
}

func (f *fallbackLog) hook(_ context.Context, agentID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agents = append(f.agents, agentID)
}

func (f *fallbackLog) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.agents)
}

// A native node that enters or leaves a refusal or an over-limit fallback
// stops or starts being able to enforce an allowlist, which changes which
// members may have a client on it: whoever owns the groups is told, once per
// change.
func TestFallbackChanged_RejectedEnteredAndLeft(t *testing.T) {
	h, a, first := withLKG(t)
	log := &fallbackLog{}
	h.svc.fallbackChanged = log.hook

	h.editPolicy(a, "DOMAIN-SUFFIX,b.example.com")
	second := h.compile(base1())
	if log.count() != 0 {
		t.Fatalf("told %d times before anything was refused", log.count())
	}
	h.observe(rejected(second.Digest))
	if log.count() != 1 || log.agents[0] != "agt_9" {
		t.Fatalf("a refusal told %v, want agt_9 once", log.agents)
	}
	// The held refusal is stable: syncing again changes nothing.
	for i := 0; i < 3; i++ {
		h.compile(base1())
	}
	if log.count() != 1 {
		t.Fatalf("a held refusal told %d times", log.count())
	}
	// A new generation retries the policy and the refusal ends.
	smtpPolicy(h, "other")
	h.publishNow()
	h.compile(base1())
	if m := h.meta(); m.FallbackReason != domain.DestFallbackNone {
		t.Fatalf("setup: still in fallback: %+v", m)
	}
	if log.count() != 2 {
		t.Fatalf("the end of the refusal told %d times in all, want 2", log.count())
	}
	_ = first
}

func TestFallbackChanged_OverLimitEnteredAndLeft(t *testing.T) {
	h := newHarness(t)
	log := &fallbackLog{}
	h.svc.fallbackChanged = log.hook
	l := h.list("l", "DOMAIN-SUFFIX,x.example.com")
	h.policy("grp", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.ListIDs, p.Scope, p.GroupIDs = []int64{l.ID}, domain.DestScopeGroups, []int64{5}
	})
	h.publishNow()
	h.defs.Definitions = bigGroup{Definitions: h.repos, group: 5}

	users := make([]int64, 0, 50_001)
	for i := int64(1); i <= 50_001; i++ {
		users = append(users, i)
	}
	f := fleet{panelID: 9, nodes: []*domain.Node{node(1, goodSniffing)}, users: users}
	h.compile(f)
	h.compile(f)
	if log.count() != 1 {
		t.Fatalf("entering over-limit told %d times, want 1", log.count())
	}
	f.users = users[:100]
	h.compile(f)
	h.compile(f)
	if log.count() != 2 {
		t.Fatalf("leaving over-limit told %d times in all, want 2", log.count())
	}
}

// The sniffing fallback depends on the roster, which the allowlist itself
// shrinks: it is judged on the inbounds directly (group.Service.Eligible), so
// it must not be what triggers a resync, or entering and leaving it would
// loop.
func TestFallbackChanged_SniffingIsNotATrigger(t *testing.T) {
	h := newHarness(t)
	log := &fallbackLog{}
	h.svc.fallbackChanged = log.hook
	l := h.list("l", "DOMAIN-SUFFIX,x.example.com")
	h.policy("p", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{l.ID} })
	h.publishNow()
	bad := fleet{panelID: 9, nodes: []*domain.Node{node(1, `{"enabled":true,"destOverride":["http"]}`)}, users: []int64{10}}
	h.compile(bad)
	if m := h.meta(); m.FallbackReason != domain.DestFallbackSniffing {
		t.Fatalf("setup: not in the sniffing fallback: %+v", m)
	}
	good := fleet{panelID: 9, nodes: []*domain.Node{node(1, goodSniffing)}, users: []int64{10}}
	h.compile(good)
	if log.count() != 0 {
		t.Fatalf("the sniffing fallback told %d times, want 0", log.count())
	}
}
