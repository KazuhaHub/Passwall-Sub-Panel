package destpolicy

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func testRoster() RosterInput {
	return RosterInput{UserIDs: []int64{12, 2, 3, 2}, UserGroups: map[int64]int64{2: 8, 3: 9, 12: 8}, Capabilities: []string{protocol.CapabilityDestinationPolicy, "audit.hits.v1", "audit.usage.v1"}, Engine: "xray", Collect: domain.AuditCollectHits, CollectRevision: 3}
}
func readyList(id int64, body string) domain.DestList {
	now := time.UnixMilli(1791000000000)
	count := 0
	if body != "" {
		count = len(strings.Split(strings.TrimSuffix(body, "\n"), "\n"))
	}
	return domain.DestList{ID: id, Kind: domain.DestListCustom, EntryCount: count, Entries: []byte(body), LastFetchedAt: &now}
}
func rulePolicy(id int64, action domain.DestAction, priority int) domain.DestPolicy {
	return domain.DestPolicy{ID: id, Action: action, Scope: domain.DestScopeAll, Enabled: true, Priority: priority, Inline: domain.DestInline{Ports: "443"}}
}

func TestBuildPolicyPreservesActionPriorityAndGroupInternalOrder(t *testing.T) {
	defs := domain.DestDefinitions{
		Lists:      []domain.DestList{readyList(1, "domain:example.com\n"), readyList(2, "domain:base.example.com\n"), readyList(3, "domain:extra.example.com\n")},
		Policies:   []domain.DestPolicy{rulePolicy(31, domain.DestObserve, 1), rulePolicy(9, domain.DestBlock, 2), rulePolicy(2, domain.DestAllow, 4), rulePolicy(8, domain.DestBlock, 1)},
		Groups:     []domain.DestGroupMode{{GroupID: 8, Mode: "allowlist", Stage: "trial", ListIDs: []int64{1}, BaseListID: 2, ExtraListID: 3}},
		Exemptions: []domain.DestExemption{{UserID: 999}, {UserID: 3}},
	}
	p, err := BuildPolicy(defs, testRoster())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, rule := range p.Rules {
		ids = append(ids, rule.ID)
	}
	if !slices.Equal(ids, []string{"p2", "p8", "p9", "g8x1", "g8x2", "g8", "p31"}) {
		t.Fatalf("wrong execution order: %v", ids)
	}
	if !slices.Equal(p.Exempt, []protocol.SubjectKey{"usr_3"}) || !slices.Equal(p.Rules[3].Subjects, []protocol.SubjectKey{"usr_12", "usr_2"}) || !p.Rules[5].CatchAll || p.Rules[5].Action != protocol.RuleObserve {
		t.Fatalf("roster/trial semantics wrong: %+v", p)
	}
	if p.Collect != protocol.CollectHits || p.CollectRevision != 3 {
		t.Fatalf("collection revision lost: %+v", p)
	}
	if err := protocol.ValidateDestinationPolicy(p); err != nil {
		t.Fatal(err)
	}
}

func TestBuildPolicySplitsAddressAndProtocolWithoutLosingConstraints(t *testing.T) {
	policy := rulePolicy(7, domain.DestBlock, 1)
	policy.ListIDs = []int64{1}
	policy.Inline = domain.DestInline{CIDRs: []string{"10.0.0.0/8", "10.0.0.0/8"}, Private: true, Protocols: []string{"bittorrent", "bittorrent"}, Ports: "6881-6889", Network: "tcp"}
	defs := domain.DestDefinitions{Lists: []domain.DestList{readyList(1, "domain:z.example.com\ndomain:a.example.com\ndomain:z.example.com\n192.0.2.0/24\n")}, Policies: []domain.DestPolicy{policy}}
	original, _ := json.Marshal(defs)
	p, err := BuildPolicy(defs, testRoster())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rules) != 2 || p.Rules[0].ID != "p7x1" || p.Rules[1].ID != "p7x2" || len(p.Rules[1].Domains) != 0 || len(p.Rules[1].CIDRs) != 0 || p.Rules[1].Private || len(p.Rules[0].Protocols) != 0 {
		t.Fatalf("bad split: %+v", p)
	}
	for _, r := range p.Rules {
		if r.Ports != "6881-6889" || r.Network != "tcp" {
			t.Fatalf("constraints dropped: %+v", r)
		}
	}
	if !slices.Equal(p.Rules[0].Domains, []string{"domain:a.example.com", "domain:z.example.com"}) || !slices.Equal(p.Rules[0].CIDRs, []string{"10.0.0.0/8", "192.0.2.0/24"}) {
		t.Fatalf("sets not canonical: %+v", p)
	}
	_, err = BuildPolicy(defs, testRoster())
	after, _ := json.Marshal(defs)
	if err != nil || string(original) != string(after) {
		t.Fatal("compiler mutates definitions")
	}
}

func TestBuildPolicyFiltersScopesAndExemptionsToLocalRoster(t *testing.T) {
	one := rulePolicy(1, domain.DestBlock, 1)
	one.Scope = domain.DestScopeGroups
	one.GroupIDs = []int64{8}
	other := rulePolicy(2, domain.DestObserve, 1)
	other.Scope = domain.DestScopeGroups
	other.GroupIDs = []int64{10}
	defs := domain.DestDefinitions{Policies: []domain.DestPolicy{one, other}, Exemptions: []domain.DestExemption{{UserID: 999}, {UserID: 2}}}
	p, err := BuildPolicy(defs, testRoster())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rules) != 1 || !slices.Equal(p.Rules[0].Subjects, []protocol.SubjectKey{"usr_12", "usr_2"}) || !slices.Equal(p.Exempt, []protocol.SubjectKey{"usr_2"}) {
		t.Fatalf("nonlocal subjects leak: %+v", p)
	}
	defs.Exemptions = append(defs.Exemptions, domain.DestExemption{UserID: 1000})
	again, err := BuildPolicy(defs, testRoster())
	if err != nil || protocol.PolicyDigest(p) != protocol.PolicyDigest(again) {
		t.Fatal("nonlocal exemption changed policy")
	}
}

func TestBuildPolicyEmptyAndPendingListsNeverBecomeCatchAll(t *testing.T) {
	empty := rulePolicy(1, domain.DestBlock, 1)
	empty.Inline = domain.DestInline{}
	empty.ListIDs = []int64{1, 2}
	disabled := rulePolicy(2, domain.DestBlock, 2)
	disabled.Enabled = false
	defs := domain.DestDefinitions{Policies: []domain.DestPolicy{empty, disabled}, Lists: []domain.DestList{readyList(1, ""), {ID: 2, Kind: domain.DestListRemote, EntryCount: 1, Entries: []byte("domain:example.com\n")}}}
	p, err := BuildPolicy(defs, testRoster())
	if err != nil || p != nil {
		t.Fatalf("empty rule emitted: %+v / %v", p, err)
	}
	input := testRoster()
	input.Collect = domain.AuditCollectHitsAndUsage
	p, err = BuildPolicy(defs, input)
	if err != nil || p == nil || len(p.Rules) != 0 || len(p.Exempt) != 0 || p.Collect != protocol.CollectHitsAndUsage {
		t.Fatalf("usage-only policy lost: %+v / %v", p, err)
	}
}

func TestBuildPolicyPauseAndLegacyNodesRemainEmpty(t *testing.T) {
	defs := domain.DestDefinitions{State: domain.DestPolicyState{Paused: true}, Policies: []domain.DestPolicy{rulePolicy(1, domain.DestBlock, 1)}}
	p, err := BuildPolicy(defs, testRoster())
	if err != nil || p != nil {
		t.Fatalf("pause emitted rule: %+v / %v", p, err)
	}
	input := testRoster()
	input.Collect = domain.AuditCollectHitsAndUsage
	p, err = BuildPolicy(defs, input)
	if err != nil || p == nil || len(p.Rules) != 0 || p.CollectRevision != 3 {
		t.Fatalf("pause lost usage/revision: %+v / %v", p, err)
	}
	input.Capabilities = []string{"audit.hits.v1", "audit.usage.v1"}
	p, err = BuildPolicy(defs, input)
	if err != nil || p != nil {
		t.Fatalf("legacy node got policy: %+v / %v", p, err)
	}
}

func TestEffectiveCollectMatrixAndSingboxGate(t *testing.T) {
	for _, level := range []domain.AuditCollect{domain.AuditCollectOff, domain.AuditCollectHits, domain.AuditCollectHitsAndUsage} {
		for mask := range 4 {
			var caps []string
			if mask&1 != 0 {
				caps = append(caps, "audit.hits.v1")
			}
			if mask&2 != 0 {
				caps = append(caps, "audit.usage.v1")
			}
			want := protocol.CollectLevel("")
			if level != domain.AuditCollectOff && mask&1 != 0 {
				want = protocol.CollectHits
				if level == domain.AuditCollectHitsAndUsage && mask == 3 {
					want = protocol.CollectHitsAndUsage
				}
			}
			if got := EffectiveCollect(level, caps, "xray"); got != want {
				t.Fatalf("wrong level %s mask %d: %s", level, mask, got)
			}
			if got := EffectiveCollect(level, caps, "sing-box"); got != "" {
				t.Fatalf("sing-box collecting: %s", got)
			}
		}
	}
}

func TestBuildPolicyRejectsInvalidRulesAndBroadStoredEntries(t *testing.T) {
	for _, change := range []func(*domain.DestDefinitions){
		func(d *domain.DestDefinitions) { d.Policies[0].Inline.Ports = "70000" },
		func(d *domain.DestDefinitions) { d.Policies[0].Inline.CIDRs = []string{"10.0.0.1/8"} },
		func(d *domain.DestDefinitions) {
			d.Lists = []domain.DestList{readyList(1, "domain:com\n")}
			d.Policies[0].ListIDs = []int64{1}
		},
		func(d *domain.DestDefinitions) { d.Policies = append(d.Policies, d.Policies[0]) },
	} {
		defs := domain.DestDefinitions{Policies: []domain.DestPolicy{rulePolicy(1, domain.DestBlock, 1)}}
		change(&defs)
		p, err := BuildPolicy(defs, testRoster())
		if !errors.Is(err, domain.ErrValidation) || p != nil {
			t.Fatalf("invalid snapshot yielded policy: %+v / %v", p, err)
		}
	}
}

func TestBuildPolicyEnforceGroupAndDeterministicInputPermutation(t *testing.T) {
	defs := domain.DestDefinitions{Groups: []domain.DestGroupMode{{GroupID: 9, Mode: "allowlist", Stage: "enforce", BaseListID: 1}, {GroupID: 8, Mode: "allowlist", Stage: "trial", BaseListID: 1}}, Lists: []domain.DestList{readyList(1, "domain:example.com\n")}}
	p, err := BuildPolicy(defs, testRoster())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rules) != 4 || p.Rules[0].ID != "g8x2" || p.Rules[3].Action != protocol.RuleBlock {
		t.Fatalf("group order/stage wrong: %+v", p)
	}
	slices.Reverse(defs.Groups)
	again, err := BuildPolicy(defs, testRoster())
	if err != nil || !reflect.DeepEqual(p, again) {
		t.Fatalf("input order changed output: %+v / %v", again, err)
	}
}
