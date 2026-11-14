package destpolicy

import (
	"encoding/json"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// WITHOUT ANY GROUP ROW NOTHING CHANGES. A panel upgraded to a version that has
// allowlist groups, and no group set, publishes the snapshot bytes it published
// before and sends every node the policy bytes it sent before, so no config
// ETag moves and no core restarts. The literals below were taken before the
// feature existed.
func TestCompile_NoGroupsIsByteIdentical(t *testing.T) {
	h := newHarness(t)
	h.setGroups(map[int64]int64{10: 5})
	sites := h.list("sites", "DOMAIN-SUFFIX,b.example.com\nDOMAIN-SUFFIX,a.example.com")
	ok := h.list("ok", "DOMAIN-SUFFIX,fine.example.com")
	h.policy("bt", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.ListIDs = []int64{sites.ID}
		p.Inline = domain.DestPolicyInline{Protocols: []string{"bittorrent"}, Ports: "6881-6889"}
	})
	h.policy("scoped", domain.DestActionBlock, domain.DestStateOn, func(p *domain.DestPolicy) {
		p.Inline = domain.DestPolicyInline{Ports: "25"}
		p.Scope, p.GroupIDs = domain.DestScopeGroups, []int64{5}
	})
	h.policy("allow", domain.DestActionAllow, domain.DestStateOn, func(p *domain.DestPolicy) { p.ListIDs = []int64{ok.ID} })
	h.policy("watch", domain.DestActionBlock, domain.DestStateReport, func(p *domain.DestPolicy) { p.ListIDs = []int64{sites.ID} })
	if err := h.repos.SourceRules.Upsert(h.ctx, &domain.DestSourceRule{NodeID: 1, CIDRs: []string{"203.0.113.0/24"}, State: domain.DestStateOn}, nil); err != nil {
		t.Fatal(err)
	}
	h.publishNow()

	st := h.state()
	snap, err := h.repos.State.Snapshot(h.ctx, st.PublishedGeneration)
	if err != nil {
		t.Fatal(err)
	}
	res := h.compile(base1(), policyCap, groupsCap)
	body, err := json.Marshal(res.Policy)
	if err != nil {
		t.Fatal(err)
	}
	if string(snap.Body) != goldenSnapshot {
		t.Errorf("snapshot bytes moved:\n%s\nwant\n%s", snap.Body, goldenSnapshot)
	}
	if string(body) != goldenPolicy {
		t.Errorf("policy bytes moved:\n%s\nwant\n%s", body, goldenPolicy)
	}
	if res.Digest != goldenDigest {
		t.Errorf("digest = %s, want %s", res.Digest, goldenDigest)
	}
	// The same agent without the groups capability gets the very same bytes.
	if again := h.compile(base1(), policyCap); again.Digest != res.Digest {
		t.Errorf("the groups capability changed a policy that has no group: %s vs %s", again.Digest, res.Digest)
	}
}

const (
	goldenSnapshot = `{"v":1,"generation":5,"lists":[{"id":1,"name":"sites","side":"dest","ready":true,"entries":"domain:a.example.com\ndomain:b.example.com"},{"id":2,"name":"ok","side":"dest","ready":true,"entries":"domain:fine.example.com"}],"policies":[{"id":3,"name":"allow","action":"allow","priority":1,"scope":"all","lists":[2],"inline":{}},{"id":1,"name":"bt","action":"block","priority":1,"scope":"all","lists":[1],"inline":{"ports":"6881-6889","protocols":["bittorrent"]}},{"id":2,"name":"scoped","action":"block","priority":2,"scope":"groups","groups":[5],"inline":{"ports":"25"}},{"id":4,"name":"watch","action":"block","priority":3,"scope":"all","state":"report","lists":[1],"inline":{}}],"exempt":[],"sources":[{"node":1,"cidrs":["203.0.113.0/24"]}]}`
	goldenPolicy   = `{"rules":[{"id":"s1","action":"block","inbounds":["lst_1"],"source_cidrs":["203.0.113.0/24"]},{"id":"p3","action":"allow","domains":["domain:fine.example.com"]},{"id":"p1x1","action":"block","domains":["domain:a.example.com","domain:b.example.com"],"ports":"6881-6889"},{"id":"p1x2","action":"block","ports":"6881-6889","protocols":["bittorrent"]},{"id":"p2","action":"block","subjects":["usr_10"],"ports":"25"},{"id":"p4","action":"observe","domains":["domain:a.example.com","domain:b.example.com"]}]}`
	goldenDigest   = "dc05b41c36ee1c667005a0e7a5a5ced4e11b3e23f70259bb2ec699ccc6721b4e"
)
