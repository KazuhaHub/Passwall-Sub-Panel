package node

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/front"
)

type setupLinks struct {
	linkRecorder
	fail bool
}

func (l *setupLinks) EnsureLink(ctx context.Context, entry, exit int64) (domain.TransitLink, error) {
	_, _ = l.linkRecorder.EnsureLink(ctx, entry, exit)
	if l.fail {
		return domain.TransitLink{}, errors.New("link write failed")
	}
	return domain.TransitLink{Port: 31001}, nil
}

func setupDraft() (*domain.Node, ports.InboundSpec, SetupOptions) {
	n := &domain.Node{PanelID: 7, DisplayName: "new", Region: "JP", ServerAddress: "jp.example.com",
		HideDirect: true, Flow: "xtls-rprx-vision", Relays: []domain.RelayLine{tunnelLine(9, 24443)}}
	spec := ports.InboundSpec{Port: 4443, Remark: n.DisplayName, Enable: true, Protocol: "vless", StreamSettings: landingReality}
	opts := SetupOptions{Tunnels: map[int64]SetupEntry{9: {SNI: "example.com", Target: "example.com:443", Listen: "0.0.0.0"}}}
	return n, spec, opts
}

func TestSetupInboundChecksConfirmedPortsBeforeWriting(t *testing.T) {
	ctx := context.Background()
	env := newTunnelEnv(t, nil)
	links := &setupLinks{}
	env.svc.SetTunnelLinks(links)
	n, spec, opts := setupDraft()
	plan, err := env.svc.SetupInbound(ctx, n, spec, opts, true)
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != 0 || len(links.pairs) != 0 || len(plan.Bindings) != 3 {
		t.Fatalf("preview wrote data or omitted bindings: %+v", plan)
	}
	if p := plan.Bindings[1]; p.Port != 24443 || p.Listen != "0.0.0.0" || len(p.SNI) != 1 || p.SNI[0] != "example.com" {
		t.Fatalf("tunnel binding = %+v", p)
	}
	n, spec, opts = setupDraft()
	opts.LinkPorts = map[string]int{"9/7": spec.Port}
	if _, err = env.svc.SetupInbound(ctx, n, spec, opts, true); !errors.Is(err, domain.ErrValidation) || !strings.Contains(err.Error(), n.DisplayName) {
		t.Fatalf("staged listener collision: err = %v", err)
	}
	n, spec, opts = setupDraft()
	opts.LinkPorts = map[string]int{"9/7": 31888}
	opts.Confirm = plan.Token
	if _, err = env.svc.SetupInbound(ctx, n, spec, opts, false); !errors.Is(err, domain.ErrConflict) || n.ID != 0 {
		t.Fatalf("unreviewed transit port: id %d, err = %v", n.ID, err)
	}
	n, spec, opts = setupDraft()
	opts.LinkPorts = map[string]int{"9/7": 31888}
	revised, err := env.svc.SetupInbound(ctx, n, spec, opts, true)
	if err != nil || revised.Token == plan.Token || revised.Bindings[2].Port != 31888 {
		t.Fatalf("changed transit port: plan %+v, err %v", revised, err)
	}
	n, spec, opts = setupDraft()
	opts.Confirm = plan.Token
	spec.Port++
	if _, err = env.svc.SetupInbound(ctx, n, spec, opts, false); !errors.Is(err, domain.ErrConflict) || n.ID != 0 {
		t.Fatalf("changed plan: id %d, err = %v", n.ID, err)
	}
	n, spec, opts = setupDraft()
	opts.Confirm = plan.Token
	if _, err = env.svc.SetupInbound(ctx, n, spec, opts, false); err != nil {
		t.Fatal(err)
	}
	landing, err := env.repo.GetByID(ctx, n.ID)
	if err != nil || len(landing.Relays) != 1 || !landing.HideDirect || !landing.Enabled {
		t.Fatalf("created landing: %+v, %v", landing, err)
	}
	child := env.repo.child(n.ID, landing.Relays[0].ID)
	if child == nil || !child.Enabled || child.DesiredPort != 24443 || child.InboundListen != "0.0.0.0" ||
		!strings.Contains(child.StreamSettings, `"serverNames":["example.com"]`) {
		t.Fatalf("created listener: %+v", child)
	}
}

func TestSetupInboundRollsBackWhenLinkCannotBeCreated(t *testing.T) {
	ctx := context.Background()
	env := newTunnelEnv(t, nil)
	env.svc.SetTunnelLinks(&setupLinks{fail: true})
	n, spec, opts := setupDraft()
	plan, err := env.svc.SetupInbound(ctx, n, spec, opts, true)
	if err != nil {
		t.Fatal(err)
	}
	n, spec, opts = setupDraft()
	opts.Confirm = plan.Token
	if _, err = env.svc.SetupInbound(ctx, n, spec, opts, false); err == nil || !strings.Contains(err.Error(), "link write failed") {
		t.Fatalf("err = %v", err)
	}
	nodes, err := env.repo.List(ctx)
	if err != nil || len(nodes) != 2 || env.repo.children() != 0 {
		t.Fatalf("failed setup left nodes behind: %+v, %v", nodes, err)
	}
}

func TestSetupInboundConfirmsSharedForwardWithLandingSNI(t *testing.T) {
	ctx := context.Background()
	env := newTunnelEnv(t, nil)
	owner := fronted(domain.Node{ID: 3, PanelID: 9, DisplayName: "existing", DesiredPort: 443, Enabled: true})
	env.repo.nodes[3] = &owner
	fronts := &joinFronts{fixedFronts{rows: []domain.TransitFront{{
		ID: 1, PanelID: 9, Port: 443,
		Listeners: []domain.FrontListener{{NodeID: 3, LoopbackPort: 30001}},
		Routes:    []domain.FrontRoute{{ServerNames: []string{"a.example.com"}, Target: domain.FrontTarget{Kind: domain.FrontTargetListener, NodeID: 3}}},
		Default:   domain.FrontTarget{Kind: domain.FrontTargetExternal, Host: "www.example.com", Port: 443},
	}}}}
	env.svc.SetFronts(fronts, front.New(fronts, env.repo, panelOK{}, agentOK{}, noForwards{}, nil, nil))
	draft := func() (*domain.Node, ports.InboundSpec) {
		n, spec, _ := setupDraft()
		n.Relays[0].Mode, n.Relays[0].Port = "", 443
		return n, spec
	}
	n, spec := draft()
	plan, err := env.svc.SetupInbound(ctx, n, spec, SetupOptions{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(fronts.rows[0].Routes) != 1 || len(plan.Bindings) != 2 || plan.Bindings[1].FrontID != 1 {
		t.Fatalf("preview: %+v, routes %+v", plan, fronts.rows[0].Routes)
	}
	n, spec = draft()
	if _, err = env.svc.SetupInbound(ctx, n, spec, SetupOptions{Confirm: plan.Token}, false); err != nil {
		t.Fatal(err)
	}
	routes := fronts.rows[0].Routes
	if len(routes) != 2 || routes[1].Target.NodeID != n.ID || routes[1].ServerNames[0] != "www.microsoft.com" {
		t.Fatalf("saved routes: %+v", routes)
	}
}
