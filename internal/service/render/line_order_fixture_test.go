package render

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The fixtures in this file build a small but deliberately awkward fleet: equal
// sort_order values, a hidden direct entry, a standalone separator tied with a
// node, a positional group separator and group sort weights. They exist so the
// ordering tests and the byte-for-byte compatibility golden exercise every
// branch of the pipeline that decides where an entry lands.

type orderGroupRepo struct {
	ports.GroupRepo
	group domain.Group
}

func (r orderGroupRepo) GetByID(context.Context, int64) (*domain.Group, error) {
	g := r.group
	return &g, nil
}

// lineOrderNode is a VLESS node with applied credentials for its direct entry
// and for every relay in relays. The relay IDs double as their display names.
func lineOrderNode(id int64, name string, sortOrder int, region string, relays ...string) (*domain.Node, []*domain.PSPClient, map[int64][]domain.PSPClientInbound) {
	n := vlessRealityNode(true)
	n.ID, n.DisplayName, n.SortOrder, n.Region = id, name, sortOrder, region
	n.ServerAddress = fmt.Sprintf("n%d.example.com", id)
	clients := []*domain.PSPClient{{ID: id * 100, UserID: 5, NodeID: id, LineID: domain.DirectLineID, UUID: "87654321-4321-4def-8abc-cba987654321"}}
	attach := map[int64][]domain.PSPClientInbound{id * 100: {{ClientID: id * 100, NodeID: id,
		AppliedUUID: "12345678-1234-4abc-8def-123456789abc", AppliedEmail: fmt.Sprintf("u5-%d@example.test", id)}}}
	for i, line := range relays {
		cid := id*100 + int64(i) + 1
		n.Relays = append(n.Relays, domain.RelayLine{ID: line, Name: line, Address: fmt.Sprintf("%s.relay.example", line), Port: 8443 + i, Enabled: true, Region: region})
		clients = append(clients, &domain.PSPClient{ID: cid, UserID: 5, NodeID: id, LineID: line, UUID: "12345678-1234-4abc-8def-123456789abc"})
		attach[cid] = []domain.PSPClientInbound{{ClientID: cid, NodeID: id,
			AppliedUUID: "12345678-1234-4abc-8def-123456789abc", AppliedEmail: fmt.Sprintf("u5-%d-%s@example.test", id, line)}}
	}
	return n, clients, attach
}

// lineOrderRender wires a Service over the given nodes. Every node's direct
// entry and relays carry applied credentials, so nothing reads a panel.
type lineOrderRender struct {
	*Service
	nodes []*domain.Node
	user  *domain.User
	group *orderGroupRepo
	rule  *domain.RuleSet
}

func newLineOrderRender(t *testing.T, seps []*domain.SeparatorEntry, specs ...func() (*domain.Node, []*domain.PSPClient, map[int64][]domain.PSPClientInbound)) *lineOrderRender {
	t.Helper()
	var nodes []*domain.Node
	var clients []*domain.PSPClient
	attachments := map[int64][]domain.PSPClientInbound{}
	for _, spec := range specs {
		n, c, a := spec()
		nodes = append(nodes, n)
		clients = append(clients, c...)
		for k, v := range a {
			attachments[k] = v
		}
	}
	rule := &domain.RuleSet{Slug: "test", Enabled: true, Content: "- MATCH,国外媒体", ProxyGroupMembers: map[string][]domain.ProxyGroupMember{
		"国外媒体": {{Kind: "node_set", Value: "remaining"}},
	}}
	groups := &orderGroupRepo{group: domain.Group{ID: 1}}
	s := &Service{repos: ports.Repos{
		Settings:  fakeSettings{},
		PSPClient: lineCredentialRepo{clients: clients, attachments: attachments},
		Group:     groups,
		Template: referenceTemplateRepo{content: map[domain.ClientType]string{
			domain.ClientMihomo: referenceMihomoTemplate, domain.ClientSingBox: referenceSingBoxTemplate}},
		RuleSet: referenceRuleRepo{rule: rule},
	}, pool: panicPool{}}
	if seps != nil {
		s.repos.Separator = lineNameSeparatorRepo{entries: seps}
	}
	s.groupSvc = referenceGroupResolver{nodes: nodes}
	s.cache = newRenderCache(subRenderCacheTTL)
	return &lineOrderRender{Service: s, nodes: nodes, user: &domain.User{ID: 5, UUID: "87654321-4321-4def-8abc-cba987654321", GroupID: 1}, group: groups, rule: rule}
}

func spec(id int64, name string, sortOrder int, region string, relays ...string) func() (*domain.Node, []*domain.PSPClient, map[int64][]domain.PSPClientInbound) {
	return func() (*domain.Node, []*domain.PSPClient, map[int64][]domain.PSPClientInbound) {
		return lineOrderNode(id, name, sortOrder, region, relays...)
	}
}

// awkwardFleet is the fleet the compatibility golden was captured from.
func awkwardFleet(t *testing.T) *lineOrderRender {
	t.Helper()
	seps := []*domain.SeparatorEntry{
		{ID: 1, DisplayName: "---- early ----", SortOrder: 5, Enabled: true, Mode: domain.SeparatorModeGlobal},
		{ID: 2, DisplayName: "---- tied with D ----", SortOrder: 30, Enabled: true, Mode: domain.SeparatorModeGlobal},
	}
	r := newLineOrderRender(t, seps,
		spec(7, "A", 10, "US", "a1", "a2"),
		spec(8, "C", 20, "JP", "c1"),
		spec(9, "D", 30, "HK", "d1", "d2"),
		spec(10, "E", 30, "AU"),
		spec(11, "F", 40, "US", "f1"),
	)
	r.nodes[1].HideDirect = true // C: only its line is published
	r.group.group.Layout = domain.Layout{
		Separators: []domain.Separator{{Position: 3, Name: "---- positional ----"}},
		Sort:       []domain.SortEntry{{NodeID: 9, Weight: 1}},
	}
	return r
}

// renderedOrder returns the emitted entry names in output order, whatever the
// format. Separators come back under their placeholder names.
func renderedOrder(t *testing.T, r *lineOrderRender, ct domain.ClientType) []string {
	t.Helper()
	out, err := r.RenderForUser(context.Background(), r.user, ct)
	if err != nil {
		t.Fatal(err)
	}
	if ct == domain.ClientURIList {
		body, err := base64.StdEncoding.DecodeString(string(out.Body))
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, line := range strings.Fields(string(body)) {
			u, err := url.Parse(line)
			if err != nil {
				t.Fatal(err)
			}
			names = append(names, u.Fragment)
		}
		return names
	}
	key := "name"
	if ct == domain.ClientSingBox {
		key = "tag"
	}
	var names []string
	for _, proxy := range subscriptionProxyMaps(t, out, ct) {
		if name, ok := proxy[key].(string); ok && !singBoxBuiltin[name] {
			names = append(names, name)
		}
	}
	return names
}

// singBoxBuiltin are the outbounds every sing-box template carries that are not
// subscription entries.
var singBoxBuiltin = map[string]bool{"direct": true, "block": true, "国外媒体": true}

var allClientTypes = []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox, domain.ClientURIList}
