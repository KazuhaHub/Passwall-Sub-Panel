// Package render is the subscription rendering pipeline. It composes
// per-protocol Clash proxy blocks, applies a group's layout, expands
// node-ref placeholders inside the template, and emits the final YAML
// body plus the Subscription-Userinfo header.
package render

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/paneltz"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safego"
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
	g, err := s.repos.Group.GetByID(ctx, u.GroupID)
	if err != nil {
		return nil, fmt.Errorf("load group: %w", err)
	}
	nodes, err := s.groupSvc.NodesFor(ctx, g)
	if err != nil {
		return nil, fmt.Errorf("resolve nodes: %w", err)
	}

	// Separators are loaded fresh per-render; the table is small (single-
	// digit rows in practice) so we don't memoize, and a stale list would
	// make a freshly-added separator invisible until restart. Visibility
	// is decided by SeparatorEntry.VisibleForNodes — global separators
	// always pass; node-bound ones pass when this group's node set
	// intersects their NodeIDs.
	groupNodeIDs := make([]int64, len(nodes))
	for i, n := range nodes {
		groupNodeIDs[i] = n.ID
	}
	separators, err := s.resolveSeparators(ctx, groupNodeIDs)
	if err != nil {
		return nil, fmt.Errorf("resolve separators: %w", err)
	}

	items := applyLayout(nodes, separators, g.Layout)
	// Region-flag prefix is a render-time knob from UISettings. We load the
	// settings once here for the flag toggle; downstream callers do their
	// own Load when they need other fields.
	if st, err := s.repos.Settings.Load(ctx, ports.UISettings{}); err == nil && st.SubRegionFlagPrefix {
		applyRegionFlagPrefix(items)
	}

	// URI list path is template-free — V2rayN / Passwall / Shadowrocket
	// only consume nodes and do their own local routing. Short-circuit
	// BEFORE Template.GetDefault so a missing uri-list template (which
	// is the seeded default — only mihomo + sing-box templates ship)
	// doesn't propagate ErrNotFound up to the sub handler and produce
	// a misleading 404 to UAs that match a uri-list rule.
	if ct == domain.ClientURIList {
		return s.renderURIList(ctx, u, items)
	}

	tpl, err := s.repos.Template.GetDefault(ctx, ct)
	if err != nil {
		return nil, fmt.Errorf("load template: %w", err)
	}
	proxies := s.buildProxies(ctx, u, items)

	proxiesYAML, err := yaml.Marshal(proxies)
	if err != nil {
		return nil, fmt.Errorf("marshal proxies: %w", err)
	}

	rulesCommon, proxyGroupOrder, err := s.resolveRulesCommon(ctx, tpl)
	if err != nil {
		return nil, fmt.Errorf("resolve rules: %w", err)
	}
	if len(proxyGroupOrder) == 0 {
		proxyGroupOrder = tpl.ProxyGroupOrder
	}
	if ct == domain.ClientSingBox {
		return s.renderSingBox(ctx, u, tpl, items, rulesCommon, proxyGroupOrder)
	}
	proxyGroupsYAML, err := buildProxyGroupsYAML(strings.Join([]string{u.PersonalRules, rulesCommon}, "\n"), proxyGroupOrder)
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

	// Build profile name for Content-Disposition header.
	profileName := s.buildProfileName(ctx, u)
	encodedName := url.PathEscape(profileName)

	// Get update interval from settings.
	updateInterval := 24
	if st, err := s.repos.Settings.Load(ctx, ports.UISettings{}); err == nil && st.SubUpdateIntervalHours > 0 {
		updateInterval = st.SubUpdateIntervalHours
	}

	headers := map[string]string{
		"Content-Type":            "text/yaml; charset=utf-8",
		"Profile-Update-Interval": strconv.Itoa(updateInterval),
		"Content-Disposition":     `attachment; filename*=UTF-8''` + encodedName,
		"Profile-Title":           profileName,
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

// resolveSeparators returns the separators that should appear when the
// group being rendered contains the supplied node IDs. Backed by the
// SeparatorRepo.ListEnabled hot path (returns enabled rows in sort_order);
// we then filter via SeparatorEntry.VisibleForNodes:
//   - global separators always pass
//   - node-bound separators pass when groupNodeIDs intersects their NodeIDs
//
// Returns nil (not an error) when the repo isn't wired — tests that
// construct Service without a SeparatorRepo still work.
func (s *Service) resolveSeparators(ctx context.Context, groupNodeIDs []int64) ([]*domain.SeparatorEntry, error) {
	if s.repos.Separator == nil {
		return nil, nil
	}
	all, err := s.repos.Separator.ListEnabled(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*domain.SeparatorEntry, 0, len(all))
	for _, e := range all {
		if e.VisibleForNodes(groupNodeIDs) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *Service) profilePlaceholders(ctx context.Context, u *domain.User) map[string]string {
	st, _ := s.repos.Settings.Load(ctx, ports.UISettings{
		SiteTitle:   "Kazuha Hub Passwall",
		LogoURL:     "/images/logo+title-circle.png",
		LogoURLDark: "/images/logo+title-circle-darkmode.png",
	})
	if st.SiteTitle == "" {
		st.SiteTitle = "Kazuha Hub Passwall"
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
	// Pre-resolve EmailRules once per render. ClientEmail is per-(user,
	// node), so the rules can be reused across the loop.
	st, _ := s.repos.Settings.Load(ctx, ports.UISettings{})
	emailRules := domain.EmailRules{Domain: st.EmailDomain}

	// Captured nodes render from the local snapshot (zero 3X-UI calls), so a
	// subscription still renders while 3X-UI is unreachable; un-captured nodes
	// (transition window) batch into one ListInbounds per panel. See
	// resolveInbounds.
	inboundByNode := s.resolveInbounds(ctx, items)

	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if it.isSeparator {
			out = append(out, emitSeparator(it.name))
			continue
		}
		inb := inboundByNode[it.node.ID]
		if inb == nil {
			log.Warn("render: skip node, inbound config unavailable (no local snapshot and live fetch failed)",
				"node_id", it.node.ID, "panel_id", it.node.PanelID, "inbound_id", it.node.InboundID)
			continue
		}
		userEmail := u.ClientEmail(it.node.ID, emailRules)
		block, err := emitProxy(it.name, it.node, u, inb, userEmail)
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
	// Fall back to a sentinel when the loop produced nothing (no nodes
	// matched the group, every inbound fetch failed, every protocol was
	// unsupported, …). Without this the rendered template's "proxies:"
	// key serializes to "[]", which CMfA rejects with "profile does not
	// contain `proxies` or `proxy-providers`". See sentinel.go.
	if len(out) == 0 {
		log.Warn("render: no usable proxies, injecting sentinel",
			"user_id", u.ID, "items_considered", len(items))
	}
	return withSentinelIfEmpty(out)
}

func (s *Service) fetchInbound(ctx context.Context, n *domain.Node) (*ports.Inbound, error) {
	c, err := s.pool.Get(n.PanelID)
	if err != nil {
		return nil, err
	}
	return c.GetInbound(ctx, n.InboundID)
}

// prefetchInboundsForRender pulls every inbound the proxy-block builder
// will need in one ListInbounds call per panel, then returns a node→
// inbound map. This collapses the previous "fetchInbound on every node"
// N+1 pattern: for a 10-node user spread across 2 panels, render now
// makes 2 ListInbounds calls instead of 10 GetInbound calls. Failures
// (panel unreachable, inbound deleted server-side) leave the affected
// entries absent from the map; the caller logs and skips them, matching
// the prior fall-through behaviour.
func (s *Service) prefetchInboundsForRender(ctx context.Context, items []renderItem) map[int64]*ports.Inbound {
	// Bucket node IDs by their owning panel so each panel is touched once.
	panelInboundIDs := map[int64]map[int]struct{}{}
	for _, it := range items {
		if it.isSeparator || it.node == nil {
			continue
		}
		ids, ok := panelInboundIDs[it.node.PanelID]
		if !ok {
			ids = map[int]struct{}{}
			panelInboundIDs[it.node.PanelID] = ids
		}
		ids[it.node.InboundID] = struct{}{}
	}
	if len(panelInboundIDs) == 0 {
		return nil
	}

	// One ListInbounds per panel. Errors degrade gracefully — affected
	// inbounds just don't appear in the result, and the caller treats
	// "missing" as "skip this node + warn", same as the old fetchInbound
	// failure path.
	type panelResult struct {
		panelID     int64
		byInboundID map[int]*ports.Inbound
		err         error
	}
	// Bound the per-render fan-out with the same semaphore size every
	// other panel-touching loop uses (traffic.PollOnce, reconcile.RunOnce
	// — default 8 from MaxPanelConcurrency). Pre-v3.6.1-beta.4 this
	// loop spawned ONE goroutine per panel with no cap, so a group
	// spanning N panels triggered N concurrent ListInbounds against
	// 3X-UI per /sub render — and /sub is the only public endpoint, so
	// a coordinated polling fleet could effectively DoS 3X-UI from
	// inside PSP. With the cap the worst-case concurrent panel hits
	// match what the background loops already established as the
	// reasonable 3X-UI load ceiling.
	maxConcurrency := paneltz.ResolveMaxPanelConcurrency(0)
	sem := make(chan struct{}, maxConcurrency)
	resultsCh := make(chan panelResult, len(panelInboundIDs))
	for pid := range panelInboundIDs {
		go func(p int64) {
			defer safego.Recover("render.prefetchInbounds")
			sem <- struct{}{}
			defer func() { <-sem }()
			c, err := s.pool.Get(p)
			if err != nil {
				resultsCh <- panelResult{panelID: p, err: err}
				return
			}
			list, lerr := c.ListInbounds(ctx)
			if lerr != nil {
				resultsCh <- panelResult{panelID: p, err: lerr}
				return
			}
			idx := make(map[int]*ports.Inbound, len(list))
			for i := range list {
				idx[list[i].ID] = &list[i]
			}
			resultsCh <- panelResult{panelID: p, byInboundID: idx}
		}(pid)
	}

	byPanel := make(map[int64]map[int]*ports.Inbound, len(panelInboundIDs))
	for i := 0; i < len(panelInboundIDs); i++ {
		r := <-resultsCh
		if r.err != nil {
			log.Warn("render: panel inbound prefetch failed",
				"panel_id", r.panelID, "err", r.err)
			continue
		}
		byPanel[r.panelID] = r.byInboundID
	}

	out := make(map[int64]*ports.Inbound, len(items))
	for _, it := range items {
		if it.isSeparator || it.node == nil {
			continue
		}
		if panel, ok := byPanel[it.node.PanelID]; ok {
			if inb, found := panel[it.node.InboundID]; found {
				out[it.node.ID] = inb
			}
		}
	}
	return out
}

func (s *Service) resolveRulesCommon(ctx context.Context, tpl *domain.Template) (string, []string, error) {
	slugs := tpl.RuleSets
	if len(slugs) == 0 {
		log.Debug("render: no rule_sets configured for template", "template", tpl.Slug)
		return "", nil, nil
	}
	parts := make([]string, 0, len(slugs))
	proxyGroupOrder := []string{}
	seenOrder := map[string]bool{}
	for _, slug := range slugs {
		rs, err := s.repos.RuleSet.GetBySlug(ctx, slug)
		if err != nil {
			log.Warn("render: skip rule_set, lookup failed", "slug", slug, "err", err)
			continue
		}
		if !rs.Enabled {
			log.Debug("render: skip disabled rule_set", "slug", slug)
			continue
		}
		content := strings.TrimRight(rs.Content, "\n")
		if content == "" {
			log.Warn("render: rule_set content is empty", "slug", slug)
			continue
		}
		parts = append(parts, content)
		for _, target := range rs.ProxyGroupOrder {
			target = strings.TrimSpace(target)
			if target == "" || seenOrder[target] {
				continue
			}
			seenOrder[target] = true
			proxyGroupOrder = append(proxyGroupOrder, target)
		}
		log.Debug("render: loaded rule_set", "slug", slug, "lines", strings.Count(content, "\n")+1)
	}
	result := strings.Join(parts, "\n")
	log.Debug("render: rules_common resolved", "total_length", len(result), "rule_sets", len(parts))
	return result, proxyGroupOrder, nil
}

// DefaultSubProfileNameTemplate is the compiled-in fallback for
// UISettings.SubProfileNameTemplate. Mirrored by the frontend's
// MeView so the deep-link &name= and the response Profile-Title /
// Content-Disposition headers stay in sync.
const DefaultSubProfileNameTemplate = "{{ site_title }} - {{ user }}"

// RenderProfileName resolves the admin-configured profile-name template
// against a user. Pure (no DB / network), so both the render layer and
// the /api/user/me handler call it directly to avoid drift between the
// Content-Disposition / Profile-Title strings on the subscription
// response and the &name= value baked into one-click-import deep links.
//
// Supported placeholders:
//
//	{{ site_title }}   — admin's panel SiteTitle (falls back to a hard
//	                     default if both settings and arg are empty)
//	{{ app_title }}    — short brand name
//	{{ display_name }} — user's display name (may be empty)
//	{{ upn }}          — user's UPN
//	{{ user }}         — display_name with UPN fallback (the most
//	                     useful placeholder — covers the 99% case)
//
// Result is post-processed to strip characters that would break
// filename safety (forward slash, quote, etc.) and to collapse runs
// of whitespace introduced by an empty placeholder.
func RenderProfileName(settings ports.UISettings, u *domain.User) string {
	siteTitle := settings.SiteTitle
	if siteTitle == "" {
		siteTitle = "Kazuha Hub Passwall"
	}
	appTitle := settings.AppTitle
	if appTitle == "" {
		appTitle = "Passwall"
	}
	displayName := u.DisplayName
	user := displayName
	if user == "" {
		user = u.UPN
	}
	tpl := strings.TrimSpace(settings.SubProfileNameTemplate)
	if tpl == "" {
		tpl = DefaultSubProfileNameTemplate
	}
	name := tpl
	for from, to := range map[string]string{
		"{{ site_title }}":   siteTitle,
		"{{ app_title }}":    appTitle,
		"{{ display_name }}": displayName,
		"{{ upn }}":          u.UPN,
		"{{ user }}":         user,
	} {
		name = strings.ReplaceAll(name, from, to)
	}
	// Filename safety — only stripped, not URL-escaped. Callers wrap
	// with url.PathEscape before emitting via Content-Disposition.
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, `\`, "-")
	name = strings.ReplaceAll(name, `"`, "")
	name = strings.ReplaceAll(name, "?", "")
	name = strings.ReplaceAll(name, "*", "")
	name = strings.ReplaceAll(name, ":", " -")
	name = strings.Join(strings.Fields(name), " ")
	return name
}

// buildProfileName is the render-layer convenience wrapper that loads
// settings via the repo before delegating to RenderProfileName.
func (s *Service) buildProfileName(ctx context.Context, u *domain.User) string {
	st, _ := s.repos.Settings.Load(ctx, ports.UISettings{SiteTitle: "Kazuha Hub Passwall"})
	return RenderProfileName(st, u)
}

// buildSubInfo produces the Subscription-Userinfo header value. Bytes are
// taken from the most recent traffic snapshot; total reflects the user's
// configured cap (0 = unlimited); expire is appended ONLY when the user
// has a real expiry — both ClashMi and ClashMetaForAndroid parse a literal
// "expire=0" as the Unix epoch (1969/1970 in negative-offset timezones)
// rather than as "no expiry", so the field must be absent entirely when
// there's nothing to communicate.
//
// Format spec: https://github.com/Dreamacro/clash/wiki/managing-providers
func (s *Service) buildSubInfo(ctx context.Context, u *domain.User) string {
	var up, down int64
	if snap, err := s.repos.Traffic.LatestForUser(ctx, u.ID); err == nil && snap != nil {
		up = snap.UpBytes
		down = snap.DownBytes
	}
	out := fmt.Sprintf("upload=%d; download=%d; total=%d", up, down, u.TrafficLimitBytes)
	if u.ExpireAt != nil && !u.ExpireAt.IsZero() {
		out += fmt.Sprintf("; expire=%d", u.ExpireAt.Unix())
	}
	return out
}
