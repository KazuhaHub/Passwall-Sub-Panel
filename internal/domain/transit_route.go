package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
)

// Routing by destination (transit.route.v1). A node (one listener of a
// Passwall Node server) normally sends every connection out through one exit:
// its own server, or the single tunnel its EgressPanelID names. Routes let the
// administrator send some destinations out through another server instead, or
// keep them local, with the node's own exit staying the default for everything
// no route takes.
//
// SETTINGS APPLY LITERALLY. A route is what the administrator saved: its
// match (lists, typed entries, ports, network), its position and its target.
// References to destination lists are compiled to literal entries when the
// config is sent and are never rewritten in the stored route.

// DestRouteTargetKind says where a route sends what it matches.
type DestRouteTargetKind string

const (
	// DestRouteLocal leaves from the node's own server, whatever its default exit is.
	DestRouteLocal DestRouteTargetKind = "local"
	// DestRouteServer leaves through the tunnel from the node's server to another
	// Passwall Node server (PanelID).
	DestRouteServer DestRouteTargetKind = "server"
)

// DestRouteTarget is the exit of one route.
type DestRouteTarget struct {
	Kind    DestRouteTargetKind `json:"kind"`
	PanelID int64               `json:"panel_id,omitempty"`
}

// Limits on what an administrator may write. The wire limits are the
// protocol's (protocol.MaxRouteRulesPerListener and the policy ceilings on
// entries); these bound the stored document.
const (
	DestRouteNameMax    = 64
	DestRouteTypedMax   = 5000
	DestRouteListIDsMax = 64
)

// DestRoute is one route of a node: connections whose destination matches go
// out through Target. The first matching route of the node's ordered list
// decides; what no route matches leaves through the node's default exit.
//
// The match is the alternatives of ListIDs (destination lists, geosite lists
// included), Domains and CIDRs, ANDed with Ports and Network, exactly as a
// destination policy rule reads its own.
type DestRoute struct {
	// ID is r<n>, stable across edits and reorders so that audit rows and logs
	// keep naming the same route; the service assigns it.
	ID      string          `json:"id"`
	Name    string          `json:"name,omitempty"`
	Target  DestRouteTarget `json:"target"`
	ListIDs []int64         `json:"list_ids,omitempty"`
	// Domains are domain:, full: and keyword: entries. A regexp cannot be
	// intersected with a policy rule's matcher, so a route refuses it.
	Domains []string `json:"domains,omitempty"`
	CIDRs   []string `json:"cidrs,omitempty"`
	Ports   string   `json:"ports,omitempty"`
	Network string   `json:"network,omitempty"`
}

// MatchesSomething reports whether the route names any match at all. A route
// that matched everything would swallow the routes after it and the default.
func (r DestRoute) MatchesSomething() bool {
	return len(r.ListIDs) > 0 || len(r.Domains) > 0 || len(r.CIDRs) > 0 || r.Ports != "" || r.Network != ""
}

// RouteNumber is n of an id r<n>, 0 when the id is not of that form.
func RouteNumber(id string) int {
	rest, ok := strings.CutPrefix(id, "r")
	if !ok || rest == "" || rest[0] == '0' || len(rest) > 9 {
		return 0
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 1 {
		return 0
	}
	return n
}

// ValidateDestRoutes checks the shape of a node's routes: what needs no other
// data. Whether a target server exists, the lists are usable and the compiled
// routes fit the budget is the service's.
func ValidateDestRoutes(routes []DestRoute) error {
	if len(routes) > protocol.MaxRouteRulesPerListener {
		return fmt.Errorf("%w: a node holds at most %d routes", ErrValidation, protocol.MaxRouteRulesPerListener)
	}
	seen := make(map[string]bool, len(routes))
	for _, route := range routes {
		if RouteNumber(route.ID) == 0 {
			return fmt.Errorf("%w: route id %q is not r<number>", ErrValidation, route.ID)
		}
		if seen[route.ID] {
			return fmt.Errorf("%w: route id %q is used twice", ErrValidation, route.ID)
		}
		seen[route.ID] = true
		if len(route.Name) > DestRouteNameMax || strings.TrimSpace(route.Name) != route.Name {
			return fmt.Errorf("%w: route %s: the name must be at most %d bytes without surrounding space", ErrValidation, route.ID, DestRouteNameMax)
		}
		switch route.Target.Kind {
		case DestRouteLocal:
			if route.Target.PanelID != 0 {
				return fmt.Errorf("%w: route %s: a local target names no server", ErrValidation, route.ID)
			}
		case DestRouteServer:
			if route.Target.PanelID <= 0 {
				return fmt.Errorf("%w: route %s: choose the exit server", ErrValidation, route.ID)
			}
		default:
			return fmt.Errorf("%w: route %s: the target is local or a server", ErrValidation, route.ID)
		}
		if !route.MatchesSomething() {
			return fmt.Errorf("%w: route %s matches nothing: the default exit already covers everything else", ErrValidation, route.ID)
		}
		if len(route.ListIDs) > DestRouteListIDsMax {
			return fmt.Errorf("%w: route %s names more than %d lists", ErrValidation, route.ID, DestRouteListIDsMax)
		}
		if len(route.Domains)+len(route.CIDRs) > DestRouteTypedMax {
			return fmt.Errorf("%w: route %s has more than %d typed entries; put them in a list", ErrValidation, route.ID, DestRouteTypedMax)
		}
		for _, id := range route.ListIDs {
			if id <= 0 {
				return fmt.Errorf("%w: route %s: invalid list id %d", ErrValidation, route.ID, id)
			}
		}
		for _, entry := range route.Domains {
			if strings.HasPrefix(entry, "regexp:") {
				return &RouteRegexpError{RouteID: route.ID, Entry: entry}
			}
			prefix, value, ok := strings.Cut(entry, ":")
			if !ok || (prefix != "domain" && prefix != "full" && prefix != "keyword") || strings.TrimSpace(value) == "" {
				return fmt.Errorf("%w: route %s: %q is not a domain:, full: or keyword: entry", ErrValidation, route.ID, entry)
			}
		}
		for _, cidr := range route.CIDRs {
			if _, err := netip.ParsePrefix(cidr); err != nil {
				return fmt.Errorf("%w: route %s: %q is not a CIDR range", ErrValidation, route.ID, cidr)
			}
		}
		switch route.Network {
		case "", "tcp", "udp":
		default:
			return fmt.Errorf("%w: route %s: the network is tcp, udp or empty", ErrValidation, route.ID)
		}
	}
	return nil
}

// RouteRegexpError says a route holds a regexp entry, which a route refuses: it
// cannot be intersected with the destination policy that applies per exit.
type RouteRegexpError struct {
	RouteID string
	Entry   string
}

func (e *RouteRegexpError) Error() string {
	return fmt.Sprintf("route %s: %q is a regexp, and a route takes domain:, full: and keyword: entries only (a regexp cannot be combined with the destination policy of each exit)", e.RouteID, e.Entry)
}

func (e *RouteRegexpError) Is(target error) bool { return target == ErrValidation }

// NodeRoutes is a node's routes with what the panel knows of their delivery.
type NodeRoutes struct {
	NodeID int64
	Routes []DestRoute
	// Sent is the digest of the routes last put in the server's config (""
	// when none were sent); Applied is the digest the server last confirmed
	// holding with this node applied. The routes run when both are equal and
	// not empty.
	Sent    string
	Applied string
	// Withheld is why the node is not being sent ("" while it is): see
	// Node.RoutesWithheld.
	Withheld  string
	SentAt    *time.Time
	AppliedAt *time.Time
}

// Running reports whether the server confirmed the routes the panel last sent.
func (n NodeRoutes) Running() bool { return n.Sent != "" && n.Sent == n.Applied }

// TransitRoutesDigest identifies exactly the routes of one listener that were
// sent, so a confirmation counts only for the content it confirmed.
func TransitRoutesDigest(routes protocol.ListenerRoutes) string {
	encoded, _ := json.Marshal(routes)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
