package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Per-line subscription ordering: the admin list and the atomic reorder.
//
// The list is the GLOBAL, unfiltered order a subscriber's output is a subset of:
// every separator, every node's direct line and every relay line, placed with
// the same rule the renderer uses (domain.PlaceFloatingLines). A user's
// subscription is this order with the entries that user may not see removed
// (tag filter, line visibility, unpublished lines), so the list is a superset of
// any one subscription, not a copy of it.

// Row kinds of the order API.
const (
	OrderKindSeparator = "separator"
	OrderKindLine      = "line"
)

// MaxOrderItems bounds a reorder request. 64 lines per node (maxRelayLines)
// times a few hundred nodes is the realistic ceiling; this is far above it and
// far below what would make the single transaction a problem.
const MaxOrderItems = 10000

// OrderRef names one row of the order. Node and separator IDs come from
// separate sequences and collide, so a ref is typed. The direct line of a node
// is LineID domain.DirectLineID, mirroring domain.LineKey.
type OrderRef struct {
	Kind   string
	ID     int64 // separator ID
	NodeID int64 // line rows: the landing node
	LineID string
}

// OrderRow is one row of the admin order list.
type OrderRow struct {
	Ref OrderRef
	// Separator is set on separator rows. Node (the landing) is set on line rows;
	// Line is nil for the direct line.
	Separator *domain.SeparatorEntry
	Node      *domain.Node
	Line      *domain.RelayLine
	// Position is the row's effective place on the shared sort scale; Positioned
	// is false for a relay line that was never positioned (it carries its
	// landing's value and sits right after it).
	Position   int
	Positioned bool
	// Published is whether this row would appear in a subscription as far as the
	// global state goes; Withheld says why not ("" when published): one of
	// hidden_direct, landing_disabled, disabled, egress, probe, source_guard,
	// waiting_entry, cdn_unfit.
	Published bool
	Withheld  string
}

// SubscriptionOrder is the order list plus the token that detects a concurrent
// edit: a reorder is only accepted against the version it was built from.
type SubscriptionOrder struct {
	Version string
	Rows    []OrderRow
}

// OrderStaleError is a reorder refused because the order changed since the
// client read it (another administrator reordered, a line was added or removed,
// a node was deleted). Current is the order as it is now, so the client can
// reload without another round trip. It is a conflict to errors.Is.
type OrderStaleError struct{ Current *SubscriptionOrder }

func (e *OrderStaleError) Error() string {
	return domain.ErrConflict.Error() + ": the subscription order changed; reload it and try again"
}

func (e *OrderStaleError) Unwrap() error { return domain.ErrConflict }

// SubscriptionOrder reads the current global order.
func (s *Service) SubscriptionOrder(ctx context.Context) (*SubscriptionOrder, error) {
	if s.separators == nil {
		return nil, fmt.Errorf("separator repo not configured")
	}
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return nil, err
	}
	seps, err := s.separators.List(ctx)
	if err != nil {
		return nil, err
	}
	rows := buildOrderRows(nodes, seps)
	return &SubscriptionOrder{Version: orderVersion(rows), Rows: rows}, nil
}

// ApplySubscriptionOrder writes the order the administrator arranged: items is
// the COMPLETE list, top to bottom, and baseVersion the version it was built
// from. Row i gets position (i+1)*10, the codebase's reorder spacing: direct
// lines become nodes.sort_order, separators nodes_separator.sort_order and every
// relay line an explicit position, all in one transaction. Writing every line
// (not only the moved ones) keeps the result fully determined; a line added
// later has no position and attaches under its landing.
//
// 400 (ErrValidation): a malformed list (unknown kind, bad ids, duplicates,
// empty, too long, no version). 409 (*OrderStaleError): well formed but not the
// current set (other version, a row missing or extra, a line or node that no
// longer exists). The two checks run against the same read under orderMu, so
// two simultaneous reorders cannot both win against one version.
func (s *Service) ApplySubscriptionOrder(ctx context.Context, baseVersion string, items []OrderRef) (string, error) {
	if len(items) == 0 {
		return "", fmt.Errorf("%w: no items", domain.ErrValidation)
	}
	if len(items) > MaxOrderItems {
		return "", fmt.Errorf("%w: at most %d items", domain.ErrValidation, MaxOrderItems)
	}
	if baseVersion == "" {
		return "", fmt.Errorf("%w: base_version is required", domain.ErrValidation)
	}
	seen := make(map[OrderRef]bool, len(items))
	for _, it := range items {
		switch it.Kind {
		case OrderKindSeparator:
			if it.ID <= 0 || it.NodeID != 0 || it.LineID != "" {
				return "", fmt.Errorf("%w: a separator item needs only a positive id", domain.ErrValidation)
			}
		case OrderKindLine:
			if it.NodeID <= 0 || it.LineID == "" || len(it.LineID) > 128 || it.ID != 0 {
				return "", fmt.Errorf("%w: a line item needs a positive node_id and a line_id", domain.ErrValidation)
			}
		default:
			return "", fmt.Errorf("%w: unknown item kind %q", domain.ErrValidation, it.Kind)
		}
		if seen[it] {
			return "", fmt.Errorf("%w: duplicate item", domain.ErrValidation)
		}
		seen[it] = true
	}

	s.orderMu.Lock()
	defer s.orderMu.Unlock()
	current, err := s.SubscriptionOrder(ctx)
	if err != nil {
		return "", err
	}
	if baseVersion != current.Version || len(items) != len(current.Rows) {
		return "", &OrderStaleError{Current: current}
	}
	for _, row := range current.Rows {
		if !seen[row.Ref] {
			return "", &OrderStaleError{Current: current}
		}
	}
	// Same length, no duplicates and every current row present: the lists are
	// the same set, so an item that names something that no longer exists was
	// already caught by the missing-row check above.

	var w ports.SubscriptionOrderWrite
	for i, it := range items {
		pos := (i + 1) * 10
		switch {
		case it.Kind == OrderKindSeparator:
			w.Separators = append(w.Separators, ports.SeparatorSortUpdate{SeparatorID: it.ID, SortOrder: pos})
		case it.LineID == domain.DirectLineID:
			w.Nodes = append(w.Nodes, ports.NodeSortUpdate{NodeID: it.NodeID, SortOrder: pos})
		default:
			w.Lines = append(w.Lines, ports.LinePositionWrite{NodeID: it.NodeID, LineID: it.LineID, SortOrder: pos})
		}
	}
	// Invalidation rides this write through the decorated repo (see
	// SetSubscriptionInvalidator): the group node cache and the render cache are
	// both dropped after the commit.
	if err := s.nodes.ApplySubscriptionOrder(ctx, w); err != nil {
		return "", err
	}
	after, err := s.SubscriptionOrder(ctx)
	if err != nil {
		return "", err
	}
	return after.Version, nil
}

// buildOrderRows lays out the global order. The base sequence is what a group
// with no layout of its own renders: nodes and standalone separators merged by
// sort_order, a separator above a node it ties with, equal nodes by region then
// ID (the default sort strategy's tie-break; group weights and strategies only
// ever reorder ties, and a reorder through this API removes every tie), each
// node followed by its lines in declaration order. Lines with a stored position
// are then placed by domain.PlaceFloatingLines, the rule the renderer applies.
func buildOrderRows(nodes []*domain.Node, seps []*domain.SeparatorEntry) []OrderRow {
	type entry struct {
		key    int
		sep    *domain.SeparatorEntry
		node   *domain.Node
		region string
		id     int64
	}
	entries := make([]entry, 0, len(nodes)+len(seps))
	for _, n := range nodes {
		// A tunnel line's hidden listener is reached through its parent's line.
		if n == nil || n.IsTunnelChild() || n.IsSeparator() {
			continue
		}
		entries = append(entries, entry{key: n.SortOrder, node: n, region: n.Region, id: n.ID})
	}
	for _, sep := range seps {
		entries = append(entries, entry{key: sep.SortOrder, sep: sep, id: sep.ID})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.key != b.key {
			return a.key < b.key
		}
		if (a.sep != nil) != (b.sep != nil) {
			return a.sep != nil
		}
		if a.sep == nil && a.region != b.region {
			return a.region < b.region
		}
		return a.id < b.id
	})

	var rows []OrderRow
	var slots []domain.LineSlot
	for _, e := range entries {
		if e.sep != nil {
			rows = append(rows, OrderRow{Ref: OrderRef{Kind: OrderKindSeparator, ID: e.sep.ID}, Separator: e.sep,
				Position: e.sep.SortOrder, Positioned: true, Published: e.sep.Enabled})
			slots = append(slots, domain.LineSlot{Key: e.sep.SortOrder, Keyed: true})
			continue
		}
		n := e.node
		withheld := directWithheld(n)
		rows = append(rows, OrderRow{Ref: OrderRef{Kind: OrderKindLine, NodeID: n.ID, LineID: domain.DirectLineID}, Node: n,
			Position: n.SortOrder, Positioned: true, Published: withheld == "", Withheld: withheld})
		slots = append(slots, domain.LineSlot{Key: n.SortOrder, Keyed: true})
		for i := range n.Relays {
			line := &n.Relays[i]
			row := OrderRow{Ref: OrderRef{Kind: OrderKindLine, NodeID: n.ID, LineID: line.ID}, Node: n, Line: line,
				Position: n.SortOrder, Withheld: relayWithheld(n, line)}
			row.Published = row.Withheld == ""
			slot := domain.LineSlot{Key: n.SortOrder, Keyed: true}
			if line.Position != nil {
				row.Position, row.Positioned = *line.Position, true
				slot = domain.LineSlot{Key: *line.Position, Keyed: true, Floating: true}
			}
			rows = append(rows, row)
			slots = append(slots, slot)
		}
	}
	order := domain.PlaceFloatingLines(slots)
	out := make([]OrderRow, len(rows))
	for i, from := range order {
		out[i] = rows[from]
	}
	return out
}

// directWithheld says why a node's direct line is not published, "" if it is.
func directWithheld(n *domain.Node) string {
	switch {
	case !n.Enabled:
		return "landing_disabled"
	case n.EffectiveHideDirect():
		return "hidden_direct"
	case !n.EgressPublishable() || n.EgressWithheld:
		return "egress"
	}
	return ""
}

// relayWithheld mirrors the conditions that keep a relay line out of a
// subscription (the node is published, RelayLine.Publishable, and a CDN line's
// landing is fit), in the order an administrator would want them explained.
func relayWithheld(n *domain.Node, line *domain.RelayLine) string {
	switch {
	case !n.Enabled:
		return "landing_disabled"
	case !n.EgressPublishable() || n.EgressWithheld:
		return "egress"
	case !line.Enabled:
		return "disabled"
	case line.ProbeWithheld:
		return "probe"
	case line.SourceWithheld != "":
		return "source_guard"
	case line.Gated() && !line.ManagedReady:
		return "waiting_entry"
	case line.CDN():
		if f := domain.CDNLandingFault(n); f != nil && f.Code != domain.CDNFaultUnreadable {
			return "cdn_unfit"
		}
	}
	return ""
}

// orderVersion is a hex SHA-256 over what a reorder is built from: each row's
// identity, its effective position and whether it was ever positioned, in
// order. Names, enabled flags and the like are deliberately not in it: editing
// them cannot make a drag land in the wrong place.
func orderVersion(rows []OrderRow) string {
	h := sha256.New()
	for _, r := range rows {
		if r.Ref.Kind == OrderKindSeparator {
			h.Write([]byte("s:" + strconv.FormatInt(r.Ref.ID, 10)))
		} else {
			h.Write([]byte("l:" + strconv.FormatInt(r.Ref.NodeID, 10) + ":" + r.Ref.LineID))
		}
		h.Write([]byte(":" + strconv.Itoa(r.Position)))
		if r.Positioned {
			h.Write([]byte(":p"))
		}
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}
