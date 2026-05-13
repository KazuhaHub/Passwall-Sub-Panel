// Package render is the subscription rendering pipeline. It composes
// per-protocol Clash proxy blocks, applies a group's layout, expands
// node-ref placeholders inside the template, and emits the final YAML
// body plus the Subscription-Userinfo header.
package render

import (
	"context"
	"fmt"
	"strings"
	"time"

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
//	├── 5. substitute {{ proxies }} / {{ proxy_groups }} / {{ rules_common }} / {{ rules_personal }}
//	├── 6. expand @all / @region / @tag inside proxy-groups
//	└── 7. set Subscription-Userinfo header from traffic + expire
func (s *Service) RenderForUser(ctx context.Context, u *domain.User, ct domain.ClientType) (*Output, error) {
	if ct == "" {
		ct = domain.ClientMihomo
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

	rulesCommon, err := s.resolveRulesCommon(ctx, tpl)
	if err != nil {
		return nil, fmt.Errorf("resolve rules: %w", err)
	}
	proxyGroupsYAML, err := buildProxyGroupsYAML(rulesCommon)
	if err != nil {
		return nil, fmt.Errorf("build proxy groups: %w", err)
	}

	body := substituteBlockPlaceholders(tpl.Content, map[string]string{
		"proxies":        strings.TrimRight(string(proxiesYAML), "\n"),
		"proxy_groups":   proxyGroupsYAML,
		"rules_common":   strings.TrimRight(rulesCommon, "\n"),
		"rules_personal": strings.TrimRight(u.PersonalRules, "\n"),
	})
	body = substituteInlinePlaceholders(body, s.profilePlaceholders(ctx, u))
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

func (s *Service) profilePlaceholders(ctx context.Context, u *domain.User) map[string]string {
	st, _ := s.repos.Settings.Load(ctx, ports.UISettings{
		SiteTitle:   "Passwall",
		LogoURL:     "/images/logo+title-circle.png",
		LogoURLDark: "/images/logo+title-circle-darkmode.png",
	})
	if st.SiteTitle == "" {
		st.SiteTitle = "Passwall"
	}
	if st.LogoURL == "" {
		st.LogoURL = "/images/logo+title-circle.png"
	}
	if st.LogoURLDark == "" {
		st.LogoURLDark = st.LogoURL
	}
	base := strings.TrimRight(st.SubBaseURL, "/")
	logo := absoluteURL(base, st.LogoURL)
	logoDark := absoluteURL(base, st.LogoURLDark)
	displayName := u.DisplayName
	if displayName == "" {
		displayName = u.UPN
	}
	expireAt := "permanent"
	if u.ExpireAt != nil {
		expireAt = u.ExpireAt.Format("2006-01-02 15:04")
	}
	return map[string]string{
		"site_title":    st.SiteTitle,
		"logo_url":      logo,
		"logo_url_dark": logoDark,
		"generated_at":  time.Now().Format("2006-01-02 15:04:05"),
		"upn":           u.UPN,
		"display_name":  displayName,
		"expire_at":     expireAt,
	}
}

func absoluteURL(base, raw string) string {
	if raw == "" || strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "data:") {
		return raw
	}
	if base == "" {
		return raw
	}
	if strings.HasPrefix(raw, "/") {
		return base + raw
	}
	return base + "/" + raw
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
				"node_id", it.node.ID, "panel_id", it.node.PanelID, "inbound_id", it.node.InboundID, "err", err)
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
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		return nil, err
	}
	return c.GetInbound(ctx, n.InboundID)
}

func (s *Service) resolveRulesCommon(ctx context.Context, tpl *domain.Template) (string, error) {
	slugs := tpl.RuleSets
	if len(slugs) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(slugs))
	for _, slug := range slugs {
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
