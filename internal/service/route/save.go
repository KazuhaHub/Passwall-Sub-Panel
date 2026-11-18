package route

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

// stubLinkBase offsets the key of a link that does not exist yet: a draft is
// compiled and checked as if its links existed, and only the keys' identity
// matters within one check.
const stubLinkBase = 1_000_000_000

// Saved is what a save answers: the routes as stored (ids assigned, entries
// normalised) and what the administrator should read.
type Saved struct {
	Routes   []domain.DestRoute
	Warnings []Warning
	Budget   *BudgetView
}

// BudgetView is the routed budget of the server's compile, in the node's own
// terms.
type BudgetView struct {
	Entries    int   `json:"entries"`
	Rules      int   `json:"rules"`
	Bytes      int64 `json:"bytes"`
	MaxEntries int   `json:"max_entries"`
	MaxRules   int   `json:"max_rules"`
	MaxBytes   int64 `json:"max_bytes"`
	// Engine says how it was counted: xray copies each policy matcher into
	// every segment, sing-box shares it.
	Engine string `json:"engine"`
}

// Save stores a node's routes. It validates everything the node would refuse,
// makes the tunnel to each exit server, and returns the warnings of a save that
// went through. An empty list clears the routes and is always allowed, whether
// or not the server can still run them.
func (s *Service) Save(ctx context.Context, nodeID int64, in []domain.DestRoute) (Saved, error) {
	return s.save(ctx, nodeID, in, true)
}

// Check is Save without writing anything, not even the links: what the editor
// shows while the administrator is still typing.
func (s *Service) Check(ctx context.Context, nodeID int64, in []domain.DestRoute) (Saved, error) {
	return s.save(ctx, nodeID, in, false)
}

func (s *Service) save(ctx context.Context, nodeID int64, in []domain.DestRoute, write bool) (Saved, error) {
	node, err := s.Nodes.GetByID(ctx, nodeID)
	if err != nil {
		return Saved{}, err
	}
	if node.IsSeparator() {
		return Saved{}, refuse(CodeSeparator, "a separator has no routes", nil)
	}
	defer s.Lock(node.PanelID)()
	// Re-read under the lock: the routes of the node are what this save replaces.
	if node, err = s.Nodes.GetByID(ctx, nodeID); err != nil {
		return Saved{}, err
	}
	if len(in) == 0 {
		if write {
			if err := s.Routes.Replace(ctx, node.ID, nil); err != nil {
				return Saved{}, err
			}
			if err := s.dropUnusedLinks(ctx, node, node.DestRoutes, nil); err != nil {
				return Saved{}, err
			}
		}
		return Saved{Routes: []domain.DestRoute{}}, nil
	}
	checked, exits, err := s.check(ctx, node, in)
	if err != nil {
		return Saved{}, err
	}
	if write {
		// The routes first, then their tunnels: a route that cannot be stored
		// leaves no tunnel behind. The tunnels are made under the lock of their
		// exit server (the one fronts take), which is where their port is picked.
		if err := s.Routes.Replace(ctx, node.ID, checked.Routes); err != nil {
			return Saved{}, err
		}
		for _, panel := range sortedPanels(exits) {
			if err := s.ensureLink(ctx, node.PanelID, panel); err != nil {
				return Saved{}, fmt.Errorf("route: make the tunnel to server %d: %w", panel, err)
			}
		}
		if err := s.dropUnusedLinks(ctx, node, node.DestRoutes, checked.Routes); err != nil {
			return Saved{}, err
		}
	}
	return checked, nil
}

// CheckSetup holds a draft listener to the same route constraints as an existing
// listener. The caller already holds the route lock for its server.
func (s *Service) CheckSetup(ctx context.Context, node *domain.Node, in []domain.DestRoute) (Saved, error) {
	draft := *node
	draft.ID = stubLinkBase - 1
	draft.DestRoutes = nil
	checked, _, err := s.check(ctx, &draft, in)
	return checked, err
}

// StoreSetup writes previously checked routes. Node setup makes the confirmed
// links before publishing the listener, under the relevant server locks.
func (s *Service) StoreSetup(ctx context.Context, nodeID int64, routes []domain.DestRoute) error {
	return s.Routes.Replace(ctx, nodeID, routes)
}

func (s *Service) check(ctx context.Context, node *domain.Node, in []domain.DestRoute) (Saved, map[int64]bool, error) {
	support, err := s.SupportOf(ctx, node.PanelID)
	if err != nil {
		return Saved{}, nil, err
	}
	if !support.Supported {
		return Saved{}, nil, supportRefusal(support.Reason)
	}
	routes, err := s.normalise(in, node.DestRoutes)
	if err != nil {
		return Saved{}, nil, err
	}
	var warnings []Warning
	exits, err := s.checkTargets(ctx, node, routes, &warnings)
	if err != nil {
		return Saved{}, nil, err
	}
	index, err := s.listIndex(ctx)
	if err != nil {
		return Saved{}, nil, err
	}
	if err := checkLists(routes, index); err != nil {
		return Saved{}, nil, err
	}
	keys, err := s.keysFor(ctx, node.PanelID, exits)
	if err != nil {
		return Saved{}, nil, err
	}
	data := s.dataKey(ctx)
	out, err := s.compileNode(ctx, node, routes, func(panel int64) (nodeprotocol.LinkKey, bool) {
		key, ok := keys[panel]
		return key, ok
	}, index, data, false, false)
	if err != nil {
		return Saved{}, nil, err
	}
	for _, note := range out.Notes {
		warnings = append(warnings, noteWarning(node, note))
	}
	if err := s.checkWire(ctx, node, out, keys, index, data); err != nil {
		return Saved{}, nil, err
	}
	warnings = append(warnings, redundancy(node, routes)...)
	var budget *BudgetView
	if s.Policies != nil && s.Desired != nil {
		var analysisWarnings []Warning
		budget, analysisWarnings, err = s.budget(ctx, node, routes, keys, index, data)
		if err != nil {
			return Saved{}, nil, err
		}
		warnings = append(warnings, analysisWarnings...)
	}
	return Saved{Routes: routes, Warnings: warnings, Budget: budget}, exits, nil
}

// normalise gives every route its id and writes the typed fields in the one
// spelling the wire accepts. A route that is already stored keeps its id; a new
// one takes the next number above every id in use.
func (s *Service) normalise(in, previous []domain.DestRoute) ([]domain.DestRoute, error) {
	had := map[string]bool{}
	next := 0
	for _, route := range previous {
		had[route.ID] = true
		next = max(next, domain.RouteNumber(route.ID))
	}
	for _, route := range in {
		next = max(next, domain.RouteNumber(route.ID))
	}
	used := map[string]bool{}
	out := make([]domain.DestRoute, 0, len(in))
	for _, route := range in {
		if !had[route.ID] || used[route.ID] {
			next++
			route.ID = fmt.Sprintf("r%d", next)
		}
		used[route.ID] = true
		route.Name = strings.TrimSpace(route.Name)
		route.ListIDs = uniqueIDs(route.ListIDs)
		route.Domains = normaliseDomains(route.Domains)
		cidrs, err := normaliseCIDRs(route.CIDRs)
		if err != nil {
			return nil, refuse(CodeBadEntry, "route %s: %s", map[string]any{"route": route.ID}, route.ID, err.Error())
		}
		route.CIDRs = cidrs
		route.Network = strings.ToLower(strings.TrimSpace(route.Network))
		if spec := strings.TrimSpace(route.Ports); spec != "" {
			ports, err := destpolicy.NormalizePorts(spec)
			if err != nil {
				return nil, refuse(CodeBadEntry, "route %s: %s", map[string]any{"route": route.ID}, route.ID, err.Error())
			}
			route.Ports = ports
		} else {
			route.Ports = ""
		}
		out = append(out, route)
	}
	if err := domain.ValidateDestRoutes(out); err != nil {
		var regexp *domain.RouteRegexpError
		if errors.As(err, &regexp) {
			return nil, refuse(CodeRegexp, "%s", map[string]any{"route": regexp.RouteID, "entry": regexp.Entry}, regexp.Error())
		}
		return nil, refuse(CodeBadRoute, "%s", nil, strings.TrimPrefix(err.Error(), domain.ErrValidation.Error()+": "))
	}
	return out, nil
}

func uniqueIDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	out := slices.Clone(ids)
	slices.Sort(out)
	return slices.Compact(out)
}

func normaliseDomains(entries []string) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry != "" {
			out = append(out, entry)
		}
	}
	return sortedUnique(out)
}

func normaliseCIDRs(entries []string) ([]string, error) {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			if addr, aerr := netip.ParseAddr(entry); aerr == nil {
				prefix, err = netip.PrefixFrom(addr, addr.BitLen()), nil
			}
		}
		if err != nil {
			return nil, fmt.Errorf("%q is not a CIDR range", entry)
		}
		if prefix.Addr().Is4In6() {
			return nil, fmt.Errorf("%q is an IPv4-mapped IPv6 range; write the IPv4 range", entry)
		}
		out = append(out, prefix.Masked().String())
	}
	return sortedUnique(out), nil
}

// checkTargets validates the exit server of every route and returns the set of
// exit servers. A server that is not a Passwall Node server, or is the node's
// own, is refused; one that cannot be dialed is refused; one that has not synced
// or lacks transit.link.v1 is a warning, because it can be fixed after the
// save and the sync withholds nothing for it.
func (s *Service) checkTargets(ctx context.Context, node *domain.Node, routes []domain.DestRoute, warnings *[]Warning) (map[int64]bool, error) {
	exits := map[int64]bool{}
	for _, route := range routes {
		if route.Target.Kind != domain.DestRouteServer {
			continue
		}
		panel := route.Target.PanelID
		if panel == node.PanelID {
			return nil, refuse(CodeBadTarget, "route %s: the exit server must differ from the node's own server; use local to leave from it",
				map[string]any{"route": route.ID}, route.ID)
		}
		if exits[panel] {
			continue
		}
		exits[panel] = true
		target, err := s.Panels.GetByID(ctx, panel)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return nil, refuse(CodeBadTarget, "route %s: no such server", map[string]any{"route": route.ID}, route.ID)
			}
			return nil, err
		}
		if target == nil || domain.NormalizePanelKind(target.Kind) != domain.PanelKindPSP {
			return nil, refuse(CodeBadTarget, "route %s: the exit server must be a Passwall Node server", map[string]any{"route": route.ID}, route.ID)
		}
		address, err := s.Links.ServerAddress(ctx, panel)
		if err != nil {
			return nil, err
		}
		own := ""
		if link, err := s.Links.LinkRow(ctx, node.PanelID, panel); err == nil {
			own = strings.TrimSpace(link.ExitAddress)
		} else if !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
		if strings.TrimSpace(address) == "" && own == "" {
			return nil, refuse(CodeNoAddress, "route %s: server %s has not reported yet, so its address is unknown; set the tunnel's exit address or wait for it to sync",
				map[string]any{"route": route.ID, "server": target.Name, "panel_id": panel}, route.ID, target.Name)
		}
		agent, err := s.Agents.GetByPanelID(ctx, panel)
		switch {
		case err != nil && !errors.Is(err, domain.ErrNotFound):
			return nil, err
		case err != nil || agent == nil:
			*warnings = append(*warnings, Warning{Code: WarnExitNotReady, RouteID: route.ID,
				Message: fmt.Sprintf("server %s has no agent that has reported yet; the route works once it does", target.Name),
				Params:  map[string]any{"server": target.Name, "panel_id": panel}})
		case !slices.Contains(agent.ObservedCapabilities, nodeprotocol.CapabilityTransitLink):
			*warnings = append(*warnings, Warning{Code: WarnExitNoLink, RouteID: route.ID,
				Message: fmt.Sprintf("server %s has not declared transit.link.v1, so it cannot take the tunnel; upgrade its agent", target.Name),
				Params:  map[string]any{"server": target.Name, "panel_id": panel}})
		}
	}
	return exits, nil
}

// checkLists refuses a route that names a list it cannot use.
func checkLists(routes []domain.DestRoute, index listIndex) error {
	for _, route := range routes {
		for _, id := range route.ListIDs {
			l, known := index[id]
			switch {
			case !known:
				return refuse(CodeBadList, "route %s: list %d does not exist", map[string]any{"route": route.ID, "list_id": id}, route.ID, id)
			case l.Side != domain.DestSideDest:
				return refuse(CodeBadList, "route %s: list %q lists sources, and a route matches destinations", map[string]any{"route": route.ID, "list_id": id, "list": l.Name}, route.ID, l.Name)
			case l.OwnerGroupID != 0:
				return refuse(CodeBadList, "route %s: list %q belongs to a group and cannot be used elsewhere", map[string]any{"route": route.ID, "list_id": id, "list": l.Name}, route.ID, l.Name)
			}
		}
	}
	return nil
}

// keysFor maps each exit server to the key of the link from entry to it: the
// key of the stored link, or a stand-in for one that does not exist yet.
func (s *Service) keysFor(ctx context.Context, entry int64, exits map[int64]bool) (map[int64]nodeprotocol.LinkKey, error) {
	keys := make(map[int64]nodeprotocol.LinkKey, len(exits))
	for panel := range exits {
		link, err := s.Links.LinkRow(ctx, entry, panel)
		switch {
		case err == nil:
			keys[panel] = nodeprotocol.NewLinkKey(link.ID)
		case errors.Is(err, domain.ErrNotFound):
			keys[panel] = nodeprotocol.NewLinkKey(stubLinkBase + panel)
		default:
			return nil, err
		}
	}
	return keys, nil
}

func stubLinkOut(key nodeprotocol.LinkKey) nodeprotocol.LinkOut {
	return nodeprotocol.LinkOut{Key: key, Address: "192.0.2.1", Port: 1, Protocol: nodeprotocol.LinkProtocolSS2022,
		SS: &nodeprotocol.LinkSS{Method: "2022-blake3-aes-128-gcm", Password: "AAAAAAAAAAAAAAAAAAAAAA=="}}
}

// checkWire checks what would be sent against the wire contract: the node's
// routes alone, and together with the routes of the server's other nodes (the
// entry limits are per server).
func (s *Service) checkWire(ctx context.Context, node *domain.Node, out compiled, keys map[int64]nodeprotocol.LinkKey, index listIndex, data string) error {
	if len(out.Rules) == 0 {
		return nil
	}
	var links []nodeprotocol.LinkOut
	seen := map[nodeprotocol.LinkKey]bool{}
	for _, key := range keys {
		if !seen[key] {
			seen[key] = true
			links = append(links, stubLinkOut(key))
		}
	}
	slices.SortFunc(links, func(a, b nodeprotocol.LinkOut) int { return strings.Compare(string(a.Key), string(b.Key)) })
	mine := nodeprotocol.ListenerRoutes{Listener: nodeprotocol.NewListenerKey(node.ID), Rules: out.Rules}
	alone := nodeprotocol.Transit{LinksOut: links, Routes: []nodeprotocol.ListenerRoutes{mine}}
	if err := nodeprotocol.ValidateTransit(&alone); err != nil {
		return refuse(CodeBadEntry, "%s", nil, err.Error())
	}
	// The other routed nodes of the server count against the same ceilings.
	domains, cidrs, rules := 0, 0, 0
	for _, rule := range out.Rules {
		domains += len(rule.Domains)
		cidrs += len(rule.CIDRs)
	}
	rules = len(out.Rules)
	fleet, err := s.Nodes.List(ctx)
	if err != nil {
		return err
	}
	for _, other := range fleet {
		if other == nil || other.ID == node.ID || other.PanelID != node.PanelID || !other.Enabled || len(other.DestRoutes) == 0 {
			continue
		}
		panels := map[int64]bool{}
		for _, route := range other.DestRoutes {
			if route.Target.Kind == domain.DestRouteServer {
				panels[route.Target.PanelID] = true
			}
		}
		otherKeys, err := s.keysFor(ctx, node.PanelID, panels)
		if err != nil {
			return err
		}
		compiledOther, err := s.compileNode(ctx, other, other.DestRoutes, func(panel int64) (nodeprotocol.LinkKey, bool) {
			key, ok := otherKeys[panel]
			return key, ok
		}, index, data, true, false)
		if err != nil {
			var missing *MissingExitError
			var listFailed *ListError
			if errors.As(err, &missing) || errors.As(err, &listFailed) {
				continue
			}
			return err
		}
		for _, rule := range compiledOther.Rules {
			domains += len(rule.Domains)
			cidrs += len(rule.CIDRs)
		}
		rules += len(compiledOther.Rules)
	}
	if domains > nodeprotocol.MaxPolicyDomains || cidrs > nodeprotocol.MaxPolicyCIDRs || rules > nodeprotocol.MaxRouteRules {
		return refuse(CodeTooManyEntries,
			"the routes of this server together hold %d domains (at most %d), %d ranges (at most %d) and %d routes (at most %d); shorten a list or remove a route",
			map[string]any{"domains": domains, "max_domains": nodeprotocol.MaxPolicyDomains, "cidrs": cidrs, "max_cidrs": nodeprotocol.MaxPolicyCIDRs, "routes": rules, "max_routes": nodeprotocol.MaxRouteRules},
			domains, nodeprotocol.MaxPolicyDomains, cidrs, nodeprotocol.MaxPolicyCIDRs, rules, nodeprotocol.MaxRouteRules)
	}
	return nil
}

func noteWarning(node *domain.Node, note Note) Warning {
	w := Warning{RouteID: note.Route, Listener: string(nodeprotocol.NewListenerKey(node.ID))}
	switch note.Code {
	case NoteListRegexp:
		w.Code = WarnListRegexp
		w.Message = fmt.Sprintf("route %s: %d regexp entries of list %q are not used; a route takes domain:, full: and keyword: entries only", note.Route, note.Count, note.List)
		w.Params = map[string]any{"route": note.Route, "list_id": note.ListID, "list": note.List, "count": note.Count}
	case NoteListLastGood:
		w.Code = WarnListLastGood
		w.Message = fmt.Sprintf("route %s: list %q gives nothing a route can use right now; the route keeps using the last good list", note.Route, note.List)
		w.Params = map[string]any{"route": note.Route, "list_id": note.ListID, "list": note.List}
	case NoteListNotReady, NoteListEmpty:
		w.Code = WarnListNotReady
		w.Message = fmt.Sprintf("route %s: list %q is not ready or empty, so it adds nothing to the route", note.Route, note.List)
		w.Params = map[string]any{"route": note.Route, "list_id": note.ListID, "list": note.List}
	default:
		w.Code = WarnRouteSkipped
		w.Message = fmt.Sprintf("route %s is not sent: none of the destinations it names are available", note.Route)
		w.Params = map[string]any{"route": note.Route}
	}
	return w
}

// redundancy warns about routes that cannot change anything: one that sends to
// the exit the node already leaves through, and any route behind one that
// matches only a port or network the earlier route also covers entirely.
func redundancy(node *domain.Node, routes []domain.DestRoute) []Warning {
	var out []Warning
	for _, route := range routes {
		same := (route.Target.Kind == domain.DestRouteLocal && node.EgressPanelID == 0) ||
			(route.Target.Kind == domain.DestRouteServer && route.Target.PanelID == node.EgressPanelID)
		if same {
			out = append(out, Warning{Code: WarnRedundant, RouteID: route.ID, Listener: string(nodeprotocol.NewListenerKey(node.ID)),
				Message: fmt.Sprintf("route %s sends to the exit this node already leaves through, so it changes nothing", route.ID),
				Params:  map[string]any{"route": route.ID}})
		}
	}
	return out
}

// budget computes the routed budget of the node's server with the draft routes
// in place, refuses what the node would reject, and warns about what is close or
// approximate. It follows the node's own count (see analyse).
func (s *Service) budget(ctx context.Context, node *domain.Node, routes []domain.DestRoute, keys map[int64]nodeprotocol.LinkKey, index listIndex, data string) (*BudgetView, []Warning, error) {
	result, names, err := s.analyseServer(ctx, node, routes, keys, index, data)
	if err != nil || result == nil {
		return nil, nil, err
	}
	view := &BudgetView{Entries: result.Analysis.Entries, Rules: result.Analysis.Rules, Bytes: result.Analysis.ServerBytes,
		MaxEntries: MaxRoutedEntries, MaxRules: MaxRoutedRules, MaxBytes: MaxRoutedConfigBytes, Engine: result.Engine}
	listener := nodeprotocol.NewListenerKey(node.ID)
	var warnings []Warning
	if over := result.Analysis.Over; over != nil {
		name := names[over.Listener]
		params := map[string]any{"listener": string(over.Listener), "node": name, "limit": over.Limit, "used": over.Used, "max": over.Max}
		// Another listener that was already over the limit before this save is
		// not made the refusal of it: the save only says so.
		baseline := false
		if routesChanged(node.DestRoutes, routes) {
			if before, _, berr := s.analyseServer(ctx, node, node.DestRoutes, keys, index, data); berr == nil && before != nil && before.Analysis.Over != nil {
				baseline = true
			}
		}
		if !baseline {
			return view, nil, refuse(CodeOverBudget,
				"the routes would make %s over the limit Passwall Node holds a routed listener to (%s: %d of %d); it would reject the listener. Remove a route, shorten a list or narrow the destination policy",
				params, name, over.Limit, over.Used, over.Max)
		}
		warnings = append(warnings, Warning{Code: WarnBudget, Listener: string(over.Listener), Params: params,
			Message: fmt.Sprintf("%s is over the limit Passwall Node holds a routed listener to (%s: %d of %d); the node rejects it until the routes or the policy shrink", name, over.Limit, over.Used, over.Max)})
	} else if warn := result.Analysis.Warn; warn != nil {
		name := names[warn.Listener]
		warnings = append(warnings, Warning{Code: WarnBudget, Listener: string(warn.Listener),
			Params:  map[string]any{"listener": string(warn.Listener), "node": name, "limit": warn.Limit, "used": warn.Used, "max": warn.Max},
			Message: fmt.Sprintf("%s is using %d of the %d %s Passwall Node allows a routed listener; one more route or a longer list may be rejected", name, warn.Used, warn.Max, warn.Limit)})
	}
	if result.Analysis.ServerBytes >= int64(bytesWarnAt*MaxRoutedConfigBytes) {
		server := ""
		if panel, perr := s.Panels.GetByID(ctx, node.PanelID); perr == nil && panel != nil {
			server = panel.Name
		}
		warnings = append(warnings, Warning{Code: WarnBudget, Listener: string(listener),
			Params:  map[string]any{"server": server, "limit": "bytes", "used": result.Analysis.ServerBytes, "max": int64(MaxRoutedConfigBytes)},
			Message: fmt.Sprintf("the config of server %s is estimated at %d of the %d bytes Passwall Node accepts for a routed listener; it rejects a listener past that, which the panel cannot see exactly", server, result.Analysis.ServerBytes, int64(MaxRoutedConfigBytes))})
	}
	// Approximations, for the group this node is in.
	var mine []Approximation
	for _, item := range result.Analysis.Approximate {
		if slices.Contains(result.groupOf(listener), item.Listener) {
			mine = append(mine, item)
		}
	}
	if len(mine) > 0 {
		warnings = append(warnings, approximateWarning(node, mine))
	}
	return view, warnings, nil
}

func approximateWarning(node *domain.Node, items []Approximation) Warning {
	type entry struct {
		Rule   string `json:"rule"`
		Route  string `json:"route"`
		Action string `json:"action"`
	}
	list := make([]entry, 0, len(items))
	names := make([]string, 0, len(items))
	for _, item := range items {
		list = append(list, entry{Rule: item.Rule, Route: item.Route, Action: string(item.Action)})
		if len(names) < 5 {
			names = append(names, item.Rule+" on "+item.Route)
		}
	}
	more := ""
	if len(items) > len(names) {
		more = fmt.Sprintf(" and %d more", len(items)-len(names))
	}
	return Warning{Code: WarnApproximate, Listener: string(nodeprotocol.NewListenerKey(node.ID)),
		Params: map[string]any{"node": node.DisplayName, "items": list, "count": len(items)},
		Message: fmt.Sprintf("On Xray a destination policy rule with a regexp or a keyword cannot be combined exactly with a route: its copy is left out of the route's segment when it allows or only records, and widened when it blocks (%s%s). Those destinations are therefore refused or unrecorded on %s",
			strings.Join(names, ", "), more, node.DisplayName)}
}

func routesChanged(a, b []domain.DestRoute) bool {
	if len(a) != len(b) {
		return true
	}
	for i := range a {
		if fmt.Sprint(a[i]) != fmt.Sprint(b[i]) {
			return true
		}
	}
	return false
}

// serverAnalysis is the analysis of one server's routed compile.
type serverAnalysis struct {
	Analysis Analysis
	Engine   string
	groups   []routeGroup
}

// groupOf are the listeners that route like the given one.
func (a *serverAnalysis) groupOf(listener nodeprotocol.ListenerKey) []nodeprotocol.ListenerKey {
	for _, group := range a.groups {
		if slices.Contains(group.Listeners, listener) {
			return group.Listeners[:1]
		}
	}
	return nil
}

// analyseServer builds what the node's compile would see for the node's server
// with draft in place of the node's routes, and counts it. names maps a listener
// to its node's name. The result is nil when there is nothing routed.
func (s *Service) analyseServer(ctx context.Context, node *domain.Node, draft []domain.DestRoute, draftKeys map[int64]nodeprotocol.LinkKey, index listIndex, data string) (*serverAnalysis, map[nodeprotocol.ListenerKey]string, error) {
	agent, err := s.Agents.GetByPanelID(ctx, node.PanelID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	panel, err := s.Panels.GetByID(ctx, node.PanelID)
	if err != nil {
		return nil, nil, err
	}
	snapshot, err := s.Desired.Load(ctx, node.PanelID)
	if err != nil {
		return nil, nil, fmt.Errorf("route: load the server's config: %w", err)
	}
	fleet, err := s.Nodes.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	byID := map[int64]*domain.Node{}
	for _, n := range fleet {
		if n != nil {
			byID[n.ID] = n
		}
	}
	keyFor := func(exit int64) nodeprotocol.LinkKey {
		if key, ok := draftKeys[exit]; ok {
			return key
		}
		if link, err := s.Links.LinkRow(ctx, node.PanelID, exit); err == nil {
			return nodeprotocol.NewLinkKey(link.ID)
		}
		return nodeprotocol.NewLinkKey(stubLinkBase + exit)
	}
	names := map[nodeprotocol.ListenerKey]string{}
	var order []nodeprotocol.ListenerKey
	rules := map[nodeprotocol.ListenerKey][]nodeprotocol.RouteRule{}
	defaults := map[nodeprotocol.ListenerKey]nodeprotocol.LinkKey{}
	present := map[nodeprotocol.ListenerKey]bool{}
	routeExits := map[int64][]destpolicy.RouteExit{}
	egress := map[int64]int64{}
	candidates := snapshot.Nodes
	if _, present := byID[node.ID]; !present {
		candidates = append(slices.Clone(candidates), node)
		byID[node.ID] = node
	}
	for _, n := range candidates {
		if n == nil {
			continue
		}
		key := nodeprotocol.NewListenerKey(n.ID)
		present[key] = true
		names[key] = n.DisplayName
		full := byID[n.ID]
		if full == nil {
			full = n
		}
		if full.EgressPanelID != 0 && full.EgressPanelID != full.PanelID && full.Enabled {
			egress[n.ID] = full.EgressPanelID
			defaults[key] = keyFor(full.EgressPanelID)
		}
		routes := full.DestRoutes
		if n.ID == node.ID {
			routes = draft
		}
		if !full.Enabled || full.IsSeparator() || len(routes) == 0 {
			continue
		}
		panels := map[int64]bool{}
		for _, route := range routes {
			if route.Target.Kind == domain.DestRouteServer {
				panels[route.Target.PanelID] = true
			}
		}
		keys := map[int64]nodeprotocol.LinkKey{}
		for panel := range panels {
			keys[panel] = keyFor(panel)
		}
		out, err := s.compileNode(ctx, full, routes, func(panel int64) (nodeprotocol.LinkKey, bool) {
			key, ok := keys[panel]
			return key, ok
		}, index, data, n.ID != node.ID, false)
		if err != nil {
			var missing *MissingExitError
			var listFailed *ListError
			if errors.As(err, &missing) || errors.As(err, &listFailed) {
				continue
			}
			return nil, nil, err
		}
		if len(out.Rules) == 0 {
			continue
		}
		order = append(order, key)
		rules[key] = out.Rules
		in := PlanInput{Nodes: nil}
		in.RouteOut = keys
		in.Egress = nil
		in.EgressExit = map[int64]int64{n.ID: egress[n.ID]}
		def := defaults[key]
		routeExits[n.ID] = exitsOf(full, out.Rules, def, in)
	}
	if len(order) == 0 {
		return nil, names, nil
	}
	cin := destpolicy.CompileInput{
		PanelID: node.PanelID, AgentID: agent.AgentID, Capabilities: agent.ObservedCapabilities, Snapshot: snapshot,
		Config:       nodeprotocol.ConfigBody{Core: nodeprotocol.CoreSelection{Engine: string(domain.NormalizeNodeCoreEngine(agent.DesiredCoreEngine))}},
		AuditCollect: panel.AuditCollect, DestResolve: panel.DestResolve, Egress: egress, RouteExits: routeExits,
	}
	plan, err := s.Policies.PlanPolicy(ctx, cin)
	if err != nil {
		return nil, nil, fmt.Errorf("route: plan the destination policy: %w", err)
	}
	groups := groupRoutes(order, rules, defaults)
	analysis := analyse(analysisInput{Policy: plan.Policy, Resolve: plan.Resolve, Engine: plan.Engine, Clients: plan.Clients, Present: present, Groups: groups})
	return &serverAnalysis{Analysis: analysis, Engine: plan.Engine, groups: groups}, names, nil
}

func sortedPanels(set map[int64]bool) []int64 {
	out := make([]int64, 0, len(set))
	for panel := range set {
		out = append(out, panel)
	}
	slices.Sort(out)
	return out
}

// lockServers takes the fronts' lock of both servers of a tunnel, lower id first
// (the order node saves take theirs in, so the two never wait on each other): the
// exit's, where the tunnel's port is picked, and the entry's, which a node save
// binding a default exit holds.
func (s *Service) lockServers(a, b int64) func() {
	if s.ServerLock == nil {
		return func() {}
	}
	if a > b {
		a, b = b, a
	}
	first := s.ServerLock(a)
	if a == b {
		return first
	}
	second := s.ServerLock(b)
	return func() { second(); first() }
}

func (s *Service) ensureLink(ctx context.Context, entry, exit int64) error {
	defer s.lockServers(entry, exit)()
	_, err := s.Links.EnsureLink(ctx, entry, exit)
	return err
}

// dropUnusedLinks removes the tunnels the routes no longer need: an exit server
// that was in the old routes and is not in the new ones, and that no node of the
// entry server uses as its default exit or reaches through a route of its own.
// The link goes with its port, which is freed on the exit server.
func (s *Service) dropUnusedLinks(ctx context.Context, node *domain.Node, old, current []domain.DestRoute) error {
	candidates := map[int64]bool{}
	for _, route := range old {
		if route.Target.Kind == domain.DestRouteServer {
			candidates[route.Target.PanelID] = true
		}
	}
	for _, route := range current {
		delete(candidates, route.Target.PanelID)
	}
	if len(candidates) == 0 {
		return nil
	}
	for _, exit := range sortedPanels(candidates) {
		if err := s.dropLinkIfUnused(ctx, node, exit); err != nil {
			return fmt.Errorf("route: remove the unused tunnel to server %d: %w", exit, err)
		}
	}
	return nil
}

// dropLinkIfUnused removes the tunnel from node's server to exit when nothing of
// the server uses it. The fleet is read UNDER the servers' locks, the one a node
// save holds while it binds a default exit: a binding made after the read would
// otherwise have its tunnel deleted from under it.
func (s *Service) dropLinkIfUnused(ctx context.Context, node *domain.Node, exit int64) error {
	defer s.lockServers(node.PanelID, exit)()
	fleet, err := s.Nodes.List(ctx)
	if err != nil {
		return err
	}
	for _, other := range fleet {
		if other == nil || other.PanelID != node.PanelID {
			continue
		}
		if other.EgressPanelID == exit {
			return nil
		}
		if other.ID == node.ID {
			continue
		}
		for _, route := range other.DestRoutes {
			if route.Target.Kind == domain.DestRouteServer && route.Target.PanelID == exit {
				return nil
			}
		}
	}
	return s.Links.RemoveLink(ctx, node.PanelID, exit)
}

// NodeDeleted removes the tunnels only the deleted node's routes used, by the
// rule a save of routes follows (see dropUnusedLinks). The node is already gone
// from the fleet, so nothing of it counts as a user.
func (s *Service) NodeDeleted(ctx context.Context, node *domain.Node) error {
	if node == nil || len(node.DestRoutes) == 0 {
		return nil
	}
	return s.dropUnusedLinks(ctx, node, node.DestRoutes, nil)
}
