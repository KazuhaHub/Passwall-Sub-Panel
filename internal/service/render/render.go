// Package render is the subscription rendering pipeline. It composes
// per-protocol Clash proxy blocks, applies a group's layout, expands
// node-ref placeholders inside the template, and emits the final YAML
// body plus the Subscription-Userinfo header.
package render

import (
	"context"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// GroupResolver is the subset of group.Service the renderer needs. Declared
// here as an interface so the render package never imports group and stays
// trivially testable.
type GroupResolver interface {
	NodesFor(ctx context.Context, g *domain.Group) ([]*domain.Node, error)
}

type Service struct {
	repos    ports.Repos
	pool     ports.XUIPool
	groupSvc GroupResolver
}

func New(repos ports.Repos, pool ports.XUIPool, groupSvc GroupResolver) *Service {
	return &Service{repos: repos, pool: pool, groupSvc: groupSvc}
}

// Output bundles the rendered body with the headers the HTTP layer should set.
type Output struct {
	Body        []byte
	ContentType string
	Headers     map[string]string
}

// RenderForUser produces the subscription document for u.
//
//	┌── 1. load default template for ct
//	├── 2. resolve user group → matched nodes
//	├── 3. apply group.Layout (sort + insert separators)
//	├── 4. fetch each inbound, emit per-protocol Clash proxy block
//	├── 5. substitute {{ proxies }} / {{ rules_common }} / {{ rules_personal }}
//	├── 6. expand @all / @region / @tag inside proxy-groups
//	└── 7. set Subscription-Userinfo header from traffic + expire
func (s *Service) RenderForUser(ctx context.Context, u *domain.User, ct domain.ClientType) (*Output, error) {
	if ct == "" {
		ct = domain.ClientClashMeta
	}
	tpl, err := s.repos.Template.GetDefault(ctx, ct)
	if err != nil {
		return nil, fmt.Errorf("load template: %w", err)
	}
	g, err := s.repos.Group.GetByID(ctx, u.GroupID)
	if err != nil {
		return nil, fmt.Errorf("load group: %w", err)
	}
	nodes, err := s.groupSvc.NodesFor(ctx, g)
	if err != nil {
		return nil, fmt.Errorf("resolve nodes: %w", err)
	}

	items := applyLayout(nodes, g.Layout)
	proxies := s.buildProxies(ctx, u, items)

	proxiesYAML, err := yaml.Marshal(proxies)
	if err != nil {
		return nil, fmt.Errorf("marshal proxies: %w", err)
	}

	rulesCommon, err := s.resolveRulesCommon(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("resolve rules: %w", err)
	}

	body := substituteBlockPlaceholders(tpl.Content, map[string]string{
		"proxies":        strings.TrimRight(string(proxiesYAML), "\n"),
		"rules_common":   strings.TrimRight(rulesCommon, "\n"),
		"rules_personal": strings.TrimRight(u.PersonalRules, "\n"),
	})
	body = expandNodeRefs(body, items)

	headers := map[string]string{
		"Content-Type":            "text/yaml; charset=utf-8",
		"Profile-Update-Interval": "24",
	}
	if info := s.buildSubInfo(ctx, u); info != "" {
		headers["Subscription-Userinfo"] = info
	}

	return &Output{
		Body:        []byte(body),
		ContentType: "text/yaml; charset=utf-8",
		Headers:     headers,
	}, nil
}

func (s *Service) buildProxies(ctx context.Context, u *domain.User, items []renderItem) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if it.isSeparator {
			out = append(out, emitSeparator(it.name))
			continue
		}
		inb, err := s.fetchInbound(ctx, it.node)
		if err != nil {
			log.Warn("render: skip node, fetch inbound failed",
				"node_id", it.node.ID, "panel", it.node.PanelName, "inbound_id", it.node.InboundID, "err", err)
			continue
		}
		block, err := emitProxy(it.node.DisplayName, it.node, u, inb)
		if err != nil {
			log.Warn("render: skip node, emit failed", "node_id", it.node.ID, "err", err)
			continue
		}
		if block == nil {
			log.Warn("render: skip node, unsupported protocol",
				"node_id", it.node.ID, "protocol", inb.Protocol)
			continue
		}
		out = append(out, block)
	}
	return out
}

func (s *Service) fetchInbound(ctx context.Context, n *domain.Node) (*ports.Inbound, error) {
	c, err := s.pool.Get(n.PanelName)
	if err != nil {
		return nil, err
	}
	return c.GetInbound(ctx, n.InboundID)
}

func (s *Service) resolveRulesCommon(ctx context.Context, u *domain.User) (string, error) {
	if len(u.EnabledRuleSets) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(u.EnabledRuleSets))
	for _, slug := range u.EnabledRuleSets {
		rs, err := s.repos.RuleSet.GetBySlug(ctx, slug)
		if err != nil {
			log.Warn("render: skip rule_set, lookup failed", "slug", slug, "err", err)
			continue
		}
		if !rs.Enabled {
			continue
		}
		parts = append(parts, strings.TrimRight(rs.Content, "\n"))
	}
	return strings.Join(parts, "\n"), nil
}

// buildSubInfo produces the Subscription-Userinfo header value. Bytes are
// taken from the most recent traffic snapshot; total reflects the user's
// configured cap (0 = unlimited); expire is unix seconds or 0.
//
// Format spec: https://github.com/Dreamacro/clash/wiki/managing-providers
func (s *Service) buildSubInfo(ctx context.Context, u *domain.User) string {
	var up, down int64
	if snap, err := s.repos.Traffic.LatestForUser(ctx, u.ID); err == nil && snap != nil {
		up = snap.UpBytes
		down = snap.DownBytes
	}
	var expire int64
	if u.ExpireAt != nil {
		expire = u.ExpireAt.Unix()
	}
	return fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d",
		up, down, u.TrafficLimitBytes, expire)
}
