package route

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

// Note codes: what a compile says about one route.
const (
	NoteListNotReady = "list_not_ready"
	NoteListEmpty    = "list_empty"
	NoteListRegexp   = "list_regexp_skipped"
	NoteSkipped      = "route_skipped"
	// NoteListLastGood: the list gave nothing a route can use just now (empty,
	// regexp only, not ready, data unavailable), and the route is compiled from
	// the last expansion that did.
	NoteListLastGood = "list_last_good"
)

// Note is something a compile says about one route: a list it could not use, a
// list's regexp entries it left out, or the whole route being left out.
type Note struct {
	Route  string
	Code   string
	ListID int64
	List   string
	Count  int
}

// compiled is one node's routes as the wire carries them.
type compiled struct {
	// Rules are the routes that are sent, in order, each with its exit.
	Rules []nodeprotocol.RouteRule
	// Entries is the destination entries (domains and CIDRs) of each sent route.
	Entries map[string]int
	Notes   []Note
}

type cached struct {
	key string
	out compiled
}

// exitKeys maps an exit server to the key of the link from the node's server to
// it.
type exitKeys func(panelID int64) (nodeprotocol.LinkKey, bool)

// listIndex is the lists a compile looks up, by id, without their entries.
type listIndex map[int64]*domain.DestList

func (s *Service) listIndex(ctx context.Context) (listIndex, error) {
	index := listIndex{}
	if s.Lists == nil {
		return index, nil
	}
	lists, err := s.Lists.Lists(ctx)
	if err != nil {
		return nil, fmt.Errorf("route: read lists: %w", err)
	}
	for _, l := range lists {
		if l != nil {
			index[l.ID] = l
		}
	}
	return index, nil
}

// dataKey identifies the data a list expands against: a custom list that names
// a geosite category or a country changes with the release or the database
// without its own text changing.
func (s *Service) dataKey(ctx context.Context) string {
	if s.Data == nil {
		return ""
	}
	state, err := s.Data.Get(ctx)
	if err != nil || state == nil {
		return ""
	}
	return state.GeoIPSHA256 + "|" + state.GeositeRelease
}

// usable reports whether a list can supply a route's match.
func usable(l *domain.DestList) bool {
	return l != nil && l.Side == domain.DestSideDest && l.OwnerGroupID == 0 && l.Ready()
}

// compileKey identifies everything a node's compiled routes depend on.
func compileKey(node *domain.Node, routes []domain.DestRoute, keys map[int64]nodeprotocol.LinkKey, index listIndex, data string) string {
	h := sha256.New()
	spec, _ := json.Marshal(routes)
	h.Write(spec)
	fmt.Fprintf(h, "|%s|", data)
	panels := make([]int64, 0, len(keys))
	for panel := range keys {
		panels = append(panels, panel)
	}
	slices.Sort(panels)
	for _, panel := range panels {
		fmt.Fprintf(h, "k%d=%s;", panel, keys[panel])
	}
	var ids []int64
	for _, route := range routes {
		ids = append(ids, route.ListIDs...)
	}
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		if l := index[id]; l != nil {
			fmt.Fprintf(h, "l%d:%s:%t:%d:%d;", id, l.ContentSHA256, l.Ready(), l.OwnerGroupID, len(l.Side))
		} else {
			fmt.Fprintf(h, "l%d:missing;", id)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ListError says a list a route names could not be read or expanded for a reason
// that is not the list being empty or its data missing (a store or decoding
// failure), and that no last good content of it exists. The route cannot be sent
// and the node must not be sent without it, so the listener is withheld.
type ListError struct {
	RouteID string
	ListID  int64
	Err     error
}

func (e *ListError) Error() string {
	return fmt.Sprintf("route %s: list %d could not be read: %v", e.RouteID, e.ListID, e.Err)
}
func (e *ListError) Unwrap() error { return e.Err }

// compileNode compiles a node's routes to the wire: lists become literal
// entries, regexp entries are left out and counted, and a route whose address
// side the lists were supposed to supply and did not is left out whole, so that
// the narrowing fields (ports, network) are not left to stand in for it. exit
// maps an exit server to its link key; a route whose server it cannot map is
// reported as an error the caller turns into a withheld listener.
func (s *Service) compileNode(ctx context.Context, node *domain.Node, routes []domain.DestRoute, exit exitKeys, index listIndex, data string, useCache, persist bool) (compiled, error) {
	keys := map[int64]nodeprotocol.LinkKey{}
	for _, route := range routes {
		if route.Target.Kind == domain.DestRouteServer {
			if key, ok := exit(route.Target.PanelID); ok {
				keys[route.Target.PanelID] = key
			}
		}
	}
	key := compileKey(node, routes, keys, index, data)
	if useCache {
		s.mu.Lock()
		hit, ok := s.cache[node.ID]
		s.mu.Unlock()
		if ok && hit.key == key {
			return hit.out, nil
		}
	}
	out := compiled{Entries: map[string]int{}}
	for _, route := range routes {
		target := nodeprotocol.LocalExit
		if route.Target.Kind == domain.DestRouteServer {
			link, ok := keys[route.Target.PanelID]
			if !ok {
				return compiled{}, &MissingExitError{RouteID: route.ID, PanelID: route.Target.PanelID}
			}
			target = link
		}
		var domains, cidrs []string
		domains = append(domains, route.Domains...)
		cidrs = append(cidrs, route.CIDRs...)
		addressIntent := len(route.Domains)+len(route.CIDRs) > 0
		skip := false
		for _, id := range route.ListIDs {
			addressIntent = true
			l := index[id]
			name := ""
			if l != nil {
				name = l.Name
			}
			var listDomains, listCIDRs []string
			var listErr error
			code, skipped := "", 0
			switch {
			case !usable(l):
				code = NoteListNotReady
			default:
				full, err := s.Lists.GetList(ctx, id)
				var expanded *destlist.Expanded
				if err == nil {
					expanded, err = s.Lists.Expand(ctx, full)
				}
				switch {
				case err == nil:
					for _, entry := range expanded.Domains {
						if strings.HasPrefix(entry, "regexp:") {
							skipped++
							continue
						}
						listDomains = append(listDomains, entry)
					}
					listCIDRs = expanded.CIDRs
					if len(expanded.Domains)+len(expanded.CIDRs) == 0 {
						code = NoteListEmpty
					}
				case errors.Is(err, destlist.ErrGeoIPUnavailable), errors.Is(err, destlist.ErrGeositeUnavailable):
					code = NoteListNotReady
				default:
					// Any other failure is not allowed to fail the server's whole
					// sync, nor to send the node without the route: it goes on from
					// the list's last good content, or its listener is withheld.
					listErr = err
					code = NoteListNotReady
				}
			}
			if skipped > 0 {
				out.Notes = append(out.Notes, Note{Route: route.ID, Code: NoteListRegexp, ListID: id, List: name, Count: skipped})
			}
			if len(listDomains)+len(listCIDRs) > 0 {
				// Something a route can use: it is the list's last good expansion.
				if persist {
					if err := s.rememberList(ctx, id, listDomains, listCIDRs); err != nil {
						return compiled{}, err
					}
				}
				domains = append(domains, listDomains...)
				cidrs = append(cidrs, listCIDRs...)
				continue
			}
			if code == "" {
				code = NoteListEmpty // regexp entries only
			}
			// Nothing a route can use. An automatic refresh must not send the
			// destinations the list gave to the default exit: the route keeps
			// the last good expansion, and says so. An administrator's confirmed
			// edit forgets it (destlist.RouteUses.Forget).
			if good, goodCIDRs, found, err := s.Routes.GoodList(ctx, id); err != nil {
				return compiled{}, fmt.Errorf("route: read the last good list %d: %w", id, err)
			} else if found {
				domains = append(domains, good...)
				cidrs = append(cidrs, goodCIDRs...)
				out.Notes = append(out.Notes, Note{Route: route.ID, Code: NoteListLastGood, ListID: id, List: name})
				continue
			}
			if listErr != nil {
				return compiled{}, &ListError{RouteID: route.ID, ListID: id, Err: listErr}
			}
			out.Notes = append(out.Notes, Note{Route: route.ID, Code: code, ListID: id, List: name})
		}
		domains = sortedUnique(domains)
		cidrs = mergeCIDRs(cidrs)
		if addressIntent && len(domains) == 0 && len(cidrs) == 0 {
			skip = true
		}
		if skip {
			out.Notes = append(out.Notes, Note{Route: route.ID, Code: NoteSkipped})
			continue
		}
		out.Rules = append(out.Rules, nodeprotocol.RouteRule{
			ID: route.ID, Domains: domains, CIDRs: cidrs, Ports: route.Ports, Network: route.Network, Exit: target,
		})
		out.Entries[route.ID] = len(domains) + len(cidrs)
	}
	if persist {
		s.mu.Lock()
		s.cache[node.ID] = cached{key: key, out: out}
		s.mu.Unlock()
	}
	return out, nil
}

// MissingExitError says a route names an exit server there is no link to.
type MissingExitError struct {
	RouteID string
	PanelID int64
}

func (e *MissingExitError) Error() string {
	return fmt.Sprintf("route %s leaves through server %d, which has no usable tunnel from this server", e.RouteID, e.PanelID)
}

func sortedUnique(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := slices.Clone(items)
	slices.Sort(out)
	return slices.Compact(out)
}

// mergeCIDRs is the canonical, merged form of a list of ranges, which is the
// one spelling the wire accepts.
func mergeCIDRs(items []string) []string {
	var prefixes []netip.Prefix
	for _, item := range items {
		if p, err := netip.ParsePrefix(item); err == nil {
			prefixes = append(prefixes, p.Masked())
		}
	}
	if len(prefixes) == 0 {
		return nil
	}
	merged := destlist.MergePrefixes(prefixes)
	out := make([]string, 0, len(merged))
	for _, p := range merged {
		out = append(out, p.String())
	}
	slices.Sort(out)
	return out
}

// Withheld is a listener that must not be sent in this config, and why: its
// routes cannot be sent, and sending it without them would send the destinations
// they take through its default exit.
type Withheld struct {
	NodeID int64
	Name   string
	Reason string
	Detail string
}

// Issue codes recorded against the agent, and the reasons a listener is withheld.
const (
	IssueRoutesNotSent      = "routes_not_sent"
	IssueRouteListenerHeld  = "route_listener_withheld"
	IssueRouteSkipped       = "route_skipped"
	IssueRouteRegexpSkipped = "route_regexp_skipped"
	WithheldNoLink          = "no_link"
	WithheldNoCapability    = "no_capability"
	WithheldListError       = "list_error"
	WithheldContract        = "contract_refused"
	WithheldTargetGone      = "target_gone"
	WithheldTooManyEntries  = "too_many_entries"
)

// PlanInput is what a sync gives the plan.
type PlanInput struct {
	PanelID      int64
	Capabilities []string
	// Nodes are the listeners the server is sent, in config order.
	Nodes []*domain.Node
	// RouteOut maps an exit server to the key of the outgoing link from this
	// server that is part of the config (transit.LinkPlan.RouteOut), and
	// RouteKeys the key of the link row whether it is sent or not.
	RouteOut  map[int64]nodeprotocol.LinkKey
	RouteKeys map[int64]nodeprotocol.LinkKey
	// Out are the outgoing links of the config, for validating the routes
	// against.
	Out []nodeprotocol.LinkOut
	// Egress maps each listener bound to a tunnel to its link, and EgressExit to
	// the server that link leaves from (transit.LinkPlan).
	Egress     []nodeprotocol.EgressBinding
	EgressExit map[int64]int64
}

// Plan is what a sync sends for routes.
type Plan struct {
	// Routes go in Transit.Routes, in listener key order.
	Routes []nodeprotocol.ListenerRoutes
	// Exits are, per routed listener, the exits its traffic can take, for the
	// destination policy compile.
	Exits map[int64][]destpolicy.RouteExit
	// Withheld are the listeners left out of the config.
	Withheld []Withheld
	// Issues are recorded against the agent.
	Issues []domain.NodeAgentIssue
}

// PlanFor compiles the routes of the nodes a server is sent. A server that did
// not declare transit.route.v1 is sent none, and an issue says that routes are
// saved and not sent.
func (s *Service) PlanFor(ctx context.Context, in PlanInput) (Plan, error) {
	var plan Plan
	var routed []*domain.Node
	for _, node := range in.Nodes {
		if node != nil && node.Enabled && !node.IsSeparator() && len(node.DestRoutes) > 0 {
			routed = append(routed, node)
		}
	}
	if len(routed) == 0 {
		return plan, nil
	}
	if !slices.Contains(in.Capabilities, nodeprotocol.CapabilityTransitRoute) || !slices.Contains(in.Capabilities, nodeprotocol.CapabilityTransitLink) {
		// An agent that does not know routes would run the listener and send every
		// destination through its default exit, with no error anywhere. The
		// listener is not sent, and the node is not published, until the server
		// can run its routes (or the routes are removed).
		for _, node := range routed {
			plan.Withheld = append(plan.Withheld, Withheld{NodeID: node.ID, Name: node.DisplayName, Reason: WithheldNoCapability,
				Detail: "this server does not declare transit.route.v1 and transit.link.v1"})
			plan.Issues = append(plan.Issues, domain.NodeAgentIssue{
				Code: IssueRoutesNotSent, Key: string(nodeprotocol.NewListenerKey(node.ID)),
				Detail: WithheldNoCapability + ": " + node.DisplayName + " is not sent and not published: its routes are saved, but this server does not declare transit.route.v1 and transit.link.v1, so it would send their destinations through the default exit",
			})
		}
		return plan, nil
	}
	index, err := s.listIndex(ctx)
	if err != nil {
		return Plan{}, err
	}
	data := s.dataKey(ctx)
	defaults := map[nodeprotocol.ListenerKey]nodeprotocol.LinkKey{}
	for _, binding := range in.Egress {
		defaults[binding.Listener] = binding.Link
	}
	sentLinks := map[nodeprotocol.LinkKey]bool{}
	for _, out := range in.Out {
		sentLinks[out.Key] = true
	}
	withhold := func(node *domain.Node, reason, detail string) {
		plan.Withheld = append(plan.Withheld, Withheld{NodeID: node.ID, Name: node.DisplayName, Reason: reason, Detail: detail})
		plan.Issues = append(plan.Issues, domain.NodeAgentIssue{
			Code: IssueRouteListenerHeld, Key: string(nodeprotocol.NewListenerKey(node.ID)), Detail: reason + ": " + detail,
		})
	}
	domainsTotal, cidrsTotal, rulesTotal := 0, 0, 0
	plan.Exits = map[int64][]destpolicy.RouteExit{}
	for _, node := range routed {
		listener := nodeprotocol.NewListenerKey(node.ID)
		out, err := s.compileNode(ctx, node, node.DestRoutes, func(panel int64) (nodeprotocol.LinkKey, bool) {
			key, ok := in.RouteOut[panel]
			return key, ok
		}, index, data, true, true)
		var missing *MissingExitError
		var listFailed *ListError
		switch {
		case errors.As(err, &listFailed):
			withhold(node, WithheldListError, listFailed.Error())
			continue
		case errors.As(err, &missing):
			reason := WithheldNoLink
			if _, known := in.RouteKeys[missing.PanelID]; !known {
				reason = WithheldTargetGone
			}
			withhold(node, reason, missing.Error())
			continue
		case err != nil:
			return Plan{}, err
		}
		for _, note := range out.Notes {
			plan.Issues = append(plan.Issues, noteIssue(listener, node, note))
		}
		if len(out.Rules) == 0 {
			continue
		}
		lr := nodeprotocol.ListenerRoutes{Listener: listener, Rules: out.Rules}
		stub := nodeprotocol.Transit{LinksOut: in.Out, Routes: []nodeprotocol.ListenerRoutes{lr}}
		if err := nodeprotocol.ValidateTransit(&stub); err != nil {
			withhold(node, WithheldContract, err.Error())
			continue
		}
		d, c := 0, 0
		for _, rule := range out.Rules {
			d += len(rule.Domains)
			c += len(rule.CIDRs)
		}
		if domainsTotal+d > nodeprotocol.MaxPolicyDomains || cidrsTotal+c > nodeprotocol.MaxPolicyCIDRs || rulesTotal+len(out.Rules) > nodeprotocol.MaxRouteRules {
			withhold(node, WithheldTooManyEntries, fmt.Sprintf("the routes of the server together are over the wire limits (%d domains, %d ranges, %d routes)",
				nodeprotocol.MaxPolicyDomains, nodeprotocol.MaxPolicyCIDRs, nodeprotocol.MaxRouteRules))
			continue
		}
		domainsTotal, cidrsTotal, rulesTotal = domainsTotal+d, cidrsTotal+c, rulesTotal+len(out.Rules)
		plan.Routes = append(plan.Routes, lr)
		plan.Exits[node.ID] = exitsOf(node, out.Rules, defaults[listener], in)
	}
	sort.Slice(plan.Routes, func(i, j int) bool { return plan.Routes[i].Listener < plan.Routes[j].Listener })
	if len(plan.Exits) == 0 {
		plan.Exits = nil
	}
	return plan, nil
}

// exitsOf are the distinct exits a routed listener's traffic can take: the exit
// of each route that is sent and its default exit, in key order.
func exitsOf(node *domain.Node, rules []nodeprotocol.RouteRule, def nodeprotocol.LinkKey, in PlanInput) []destpolicy.RouteExit {
	panelOf := map[nodeprotocol.LinkKey]int64{nodeprotocol.LocalExit: node.PanelID}
	for panel, key := range in.RouteOut {
		panelOf[key] = panel
	}
	var out []destpolicy.RouteExit
	seen := map[nodeprotocol.LinkKey]bool{}
	add := func(key nodeprotocol.LinkKey, panel int64) {
		if !seen[key] {
			seen[key] = true
			out = append(out, destpolicy.RouteExit{Key: key, PanelID: panel})
		}
	}
	for _, rule := range rules {
		add(rule.Exit, panelOf[rule.Exit])
	}
	if def == "" {
		add(nodeprotocol.LocalExit, node.PanelID)
	} else {
		add(def, in.EgressExit[node.ID])
	}
	slices.SortFunc(out, func(a, b destpolicy.RouteExit) int { return strings.Compare(string(a.Key), string(b.Key)) })
	return out
}

func noteIssue(listener nodeprotocol.ListenerKey, node *domain.Node, note Note) domain.NodeAgentIssue {
	issue := domain.NodeAgentIssue{Key: string(listener)}
	switch note.Code {
	case NoteListRegexp:
		issue.Code = IssueRouteRegexpSkipped
		issue.Detail = fmt.Sprintf("%s, route %s: %d regexp entries of list %q are not used: a route takes domain:, full: and keyword: entries only", node.DisplayName, note.Route, note.Count, note.List)
	case NoteListLastGood:
		issue.Code = IssueRouteSkipped
		issue.Detail = fmt.Sprintf("%s, route %s: list %q gives nothing a route can use right now (empty, not ready or its data is unavailable); the route keeps using the last good list, so those destinations still leave through its exit", node.DisplayName, note.Route, note.List)
	case NoteListNotReady:
		issue.Code = IssueRouteSkipped
		issue.Detail = fmt.Sprintf("%s, route %s: list %q is not ready, so it adds nothing to the route", node.DisplayName, note.Route, note.List)
	case NoteListEmpty:
		issue.Code = IssueRouteSkipped
		issue.Detail = fmt.Sprintf("%s, route %s: list %q has no entries, so it adds nothing to the route", node.DisplayName, note.Route, note.List)
	default:
		issue.Code = IssueRouteSkipped
		issue.Detail = fmt.Sprintf("%s, route %s is not sent: none of the destinations it names are available, and its ports and network alone must not stand in for them", node.DisplayName, note.Route)
	}
	return issue
}

// RecordSent stores, for every node with routes on the server, the digest of
// what the config now holds, and "" for the nodes whose routes are not in it, so
// an earlier confirmation cannot make routes that are not being sent look
// applied, and why a node is withheld (a withheld node is not published). It
// reports whether anything changed, which changes what is published.
func (s *Service) RecordSent(ctx context.Context, nodes []*domain.Node, sent []nodeprotocol.ListenerRoutes, withheld map[int64]string) (bool, error) {
	byListener := make(map[nodeprotocol.ListenerKey]nodeprotocol.ListenerRoutes, len(sent))
	for _, lr := range sent {
		byListener[lr.Listener] = lr
	}
	now := s.Now().UTC()
	changed := false
	for _, node := range nodes {
		if node == nil || (len(node.DestRoutes) == 0 && node.RoutesSent == "" && node.RoutesWithheld == "") {
			continue
		}
		digest := ""
		if lr, ok := byListener[nodeprotocol.NewListenerKey(node.ID)]; ok {
			digest = domain.TransitRoutesDigest(lr)
		}
		moved, err := s.Routes.RecordSent(ctx, node.ID, digest, withheld[node.ID], now)
		if err != nil {
			return changed, fmt.Errorf("route: record sent routes of node %d: %w", node.ID, err)
		}
		changed = changed || moved
	}
	return changed, nil
}

// Confirm records what a server confirmed of the routes in the config it holds:
// the routes of a listener are applied when the listener is. A listener that is
// rejected, or missing from the report, withdraws the confirmation. held is the
// config the server holds and statuses its object statuses, so the body is
// exactly what they describe. It reports whether anything changed.
func (s *Service) Confirm(ctx context.Context, held *nodeprotocol.Transit, statuses map[string]nodeprotocol.ObjectStatus, now time.Time) (bool, error) {
	if held == nil {
		return false, nil
	}
	changed := false
	for _, lr := range held.Routes {
		id, err := lr.Listener.RowID()
		if err != nil {
			continue
		}
		digest := domain.TransitRoutesDigest(lr)
		if status, ok := statuses[string(lr.Listener)]; !ok || status.State != nodeprotocol.ObjectApplied {
			digest = ""
		}
		moved, err := s.Routes.SetApplied(ctx, id, digest, now)
		if err != nil {
			return changed, fmt.Errorf("route: confirm routes of %s: %w", lr.Listener, err)
		}
		changed = changed || moved
	}
	return changed, nil
}

// Early decides which listeners cannot be sent with their routes before
// anything else is planned from the config (shared ports, links), so a listener
// that is left out is treated like one that was never there: it drops out of a
// shared port with its routes instead of being named by a front the server
// would then refuse. It plans with what is stored: the tunnels that exist (or
// would be made) and whether each can be dialed. PlanFor, with the real tunnels,
// still has the last word.
func (s *Service) Early(ctx context.Context, panelID int64, caps []string, nodes []*domain.Node) (Plan, error) {
	in := PlanInput{PanelID: panelID, Capabilities: caps, Nodes: nodes, RouteOut: map[int64]nodeprotocol.LinkKey{}, RouteKeys: map[int64]nodeprotocol.LinkKey{}}
	seen := map[int64]bool{}
	for _, node := range nodes {
		if node == nil || !node.Enabled {
			continue
		}
		for _, route := range node.DestRoutes {
			panel := route.Target.PanelID
			if route.Target.Kind != domain.DestRouteServer || panel <= 0 || panel == node.PanelID || seen[panel] {
				continue
			}
			seen[panel] = true
			if _, err := s.Panels.GetByID(ctx, panel); err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					continue
				}
				return Plan{}, err
			}
			key := nodeprotocol.NewLinkKey(stubLinkBase + panel)
			if link, err := s.Links.LinkRow(ctx, node.PanelID, panel); err == nil {
				key = nodeprotocol.NewLinkKey(link.ID)
			} else if !errors.Is(err, domain.ErrNotFound) {
				return Plan{}, err
			}
			in.RouteKeys[panel] = key
			usable, err := s.Links.OutUsable(ctx, node.PanelID, panel)
			if err != nil {
				return Plan{}, err
			}
			if usable {
				in.RouteOut[panel] = key
				in.Out = append(in.Out, stubLinkOut(key))
			}
		}
	}
	return s.PlanFor(ctx, in)
}

// rememberList stores a list's expansion as its last good one; the store writes
// only when it differs from what it holds, so a sync that changes nothing writes
// nothing.
func (s *Service) rememberList(ctx context.Context, id int64, domains, cidrs []string) error {
	sum := sha256.New()
	for _, d := range domains {
		sum.Write([]byte(d))
		sum.Write([]byte{'\n'})
	}
	sum.Write([]byte{0})
	for _, c := range cidrs {
		sum.Write([]byte(c))
		sum.Write([]byte{'\n'})
	}
	if err := s.Routes.SaveGoodList(ctx, id, hex.EncodeToString(sum.Sum(nil)), domains, cidrs); err != nil {
		return fmt.Errorf("route: keep the last good list %d: %w", id, err)
	}
	return nil
}

// Forget drops the last good expansion of a list: the administrator confirmed an
// edit that leaves routes with nothing from it, so they stop matching it
// (destlist.RouteUses).
func (s *Service) Forget(ctx context.Context, listID int64) error {
	return s.Routes.ForgetGoodList(ctx, listID)
}
