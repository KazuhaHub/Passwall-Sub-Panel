package group

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// PanelEnforceReason is Eligible's answer for one panel with the reason: the
// admin API tells an administrator why a node is not open to an allowlist group.
// It must agree with Eligible in every case.
func TestPanelEnforceReason(t *testing.T) {
	cases := []struct {
		name  string
		setup func(e *eligEnv)
		want  string
	}{
		{"healthy", func(e *eligEnv) {}, ""},
		{"3x-ui", func(e *eligEnv) { e.panels.kinds[1] = domain.PanelKind3XUI }, EnforceXUI},
		{"legacy kind is 3x-ui", func(e *eligEnv) { e.panels.kinds[1] = "" }, EnforceXUI},
		{"s-ui", func(e *eligEnv) { e.panels.kinds[1] = domain.PanelKindSUI }, EnforceSUI},
		{"no agent enrolled", func(e *eligEnv) { delete(e.agents.byPanel, 1) }, EnforceCapability},
		{"no groups capability", func(e *eligEnv) {
			e.agents.byPanel[1].ObservedCapabilities = []string{protocol.CapabilityDestinationPolicy}
		}, EnforceCapability},
		{"rejected", func(e *eligEnv) {
			e.policies.byAgent["agt_1"] = &domain.DestAgentPolicy{FallbackReason: domain.DestFallbackRejected}
		}, EnforceRejected},
		{"over limit", func(e *eligEnv) {
			e.policies.byAgent["agt_1"] = &domain.DestAgentPolicy{FallbackReason: domain.DestFallbackOverLimit}
		}, EnforceOverLimit},
		{"weak sniffing", func(e *eligEnv) { e.node.Sniffing = `{"enabled":true,"metadataOnly":true}` }, EnforceSniffing},
		{"sing-box needs no sniffing", func(e *eligEnv) {
			e.agents.byPanel[1].DesiredCoreEngine = domain.NodeCoreSingBox
			e.node.Sniffing = `{"enabled":true,"metadataOnly":true}`
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEligEnv(t)
			tc.setup(e)
			got, err := e.svc.PanelEnforceReason(context.Background(), 1)
			if err != nil || got != tc.want {
				t.Fatalf("reason = %q, %v, want %q", got, err, tc.want)
			}
			if ok := e.eligible(t, allowlistGroup(domain.DestStateOn)); ok != (tc.want == "") {
				t.Fatalf("Eligible = %v disagrees with the reason %q", ok, tc.want)
			}
		})
	}
}

func TestPanelEnforceReason_ReadFailuresAreErrors(t *testing.T) {
	boom := errors.New("db down")
	for name, fail := range map[string]func(e *eligEnv){
		"panel":    func(e *eligEnv) { e.panels.err = boom },
		"agent":    func(e *eligEnv) { e.agents.err = boom },
		"policy":   func(e *eligEnv) { e.policies.err = boom },
		"nodelist": func(e *eligEnv) { e.nodes.err = boom },
	} {
		e := newEligEnv(t)
		fail(e)
		if got, err := e.svc.PanelEnforceReason(context.Background(), 1); !errors.Is(err, boom) || got != "" {
			t.Fatalf("%s: reason = %q, %v", name, got, err)
		}
	}
}

// The panels a group's tags select: those with an enabled inbound the filter
// matches, once each, by id.
func TestPanelsFor(t *testing.T) {
	e := newEligEnv(t)
	e.nodes.nodes = []*domain.Node{
		{ID: 10, PanelID: 3, Enabled: true, Region: "TW"},
		{ID: 11, PanelID: 3, Enabled: true, Region: "TW"},
		{ID: 12, PanelID: 1, Enabled: true, Region: "TW"},
		{ID: 13, PanelID: 2, Enabled: true, Region: "JP"},
	}
	g := &domain.Group{ID: 5, TagFilter: domain.TagFilter{Tags: []string{"region:TW"}}}
	got, err := e.svc.PanelsFor(context.Background(), g)
	if err != nil || !slices.Equal(got, []int64{1, 3}) {
		t.Fatalf("panels = %v, %v, want [1 3]", got, err)
	}
	all, _ := e.svc.PanelsFor(context.Background(), &domain.Group{ID: 6, TagFilter: domain.TagFilter{All: true}})
	if !slices.Equal(all, []int64{1, 2, 3}) {
		t.Fatalf("all = %v", all)
	}
}

// Deleting a group drops what is cached about it: its member count and the
// eligibility its allowlist state made true.
func TestDelete_DropsTheCachedMemberCount(t *testing.T) {
	e := newEligEnv(t)
	e.groups.groups = []*domain.Group{allowlistGroup(domain.DestStateOn)}
	if _, err := e.svc.TagMatchedMembers(context.Background(), 1, 5); err != nil {
		t.Fatal(err)
	}
	before := e.groups.counts.Load()
	if err := e.svc.Delete(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	e.groups.groups = []*domain.Group{allowlistGroup(domain.DestStateOn)}
	if _, err := e.svc.TagMatchedMembers(context.Background(), 1, 5); err != nil {
		t.Fatal(err)
	}
	// Delete itself counts members once; the cached count was dropped, so the
	// lookup afterwards counts again.
	if got := e.groups.counts.Load() - before; got != 2 {
		t.Fatalf("member counts after the delete = %d, want 2 (the delete's own and a fresh one)", got)
	}
}
