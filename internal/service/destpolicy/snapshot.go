package destpolicy

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// snapshotDoc is the published serialisation of every definition Compile reads.
// It holds only what is IN FORCE: policies that are not off, the lists they
// (and the source rules) reference, the exemptions that have not expired, and
// the source rules that are on. Everything else is irrelevant to a node and
// would make an edit of a policy nobody enabled look like a change.
//
// It is canonical: every slice is in a fixed order and the struct field order
// is the key order, so the same definitions marshal to the same bytes.
type snapshotDoc struct {
	Version    int    `json:"v"`
	Generation int64  `json:"generation"`
	GeoIP      string `json:"geoip_sha256,omitempty"`
	// Geosite is the geosite release the lists in force were derived from.
	Geosite string       `json:"geosite_release,omitempty"`
	Lists   []snapList   `json:"lists"`
	Policy  []snapPolicy `json:"policies"`
	Exempt  []int64      `json:"exempt"`
	Sources []snapSource `json:"sources"`
}

type snapList struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Side    string `json:"side"`
	Ready   bool   `json:"ready"`
	Entries string `json:"entries"`
}

type snapPolicy struct {
	ID       int64                   `json:"id"`
	Name     string                  `json:"name"`
	Action   string                  `json:"action"`
	Priority int                     `json:"priority"`
	Scope    string                  `json:"scope"`
	Groups   []int64                 `json:"groups,omitempty"`
	Panels   []int64                 `json:"panels,omitempty"`
	Lists    []int64                 `json:"lists,omitempty"`
	Inline   domain.DestPolicyInline `json:"inline"`
}

type snapSource struct {
	Node      int64    `json:"node"`
	Countries []string `json:"countries,omitempty"`
	CIDRs     []string `json:"cidrs,omitempty"`
}

// newSnapshotDoc selects what is in force from a consistent read of the
// definitions. now decides which exemptions have expired: an expired one is
// left out even though the cleanup has not deleted it yet.
func newSnapshotDoc(defs *domain.DestDefinitions, state *domain.DestPolicyState, now time.Time) *snapshotDoc {
	doc := &snapshotDoc{
		Version: snapshotFormatVersion, Generation: defs.Generation,
		Lists: []snapList{}, Policy: []snapPolicy{}, Exempt: []int64{}, Sources: []snapSource{},
	}
	if state != nil {
		doc.GeoIP, doc.Geosite = state.GeoIPSHA256, state.GeositeRelease
	}
	used := map[int64]bool{}
	// Allow policies before block ones, each by priority then id: the order
	// the rules are emitted in. ReadDefinitions already orders by (action,
	// priority, id); sorting again keeps the document independent of that.
	policies := slices.Clone(defs.Policies)
	slices.SortStableFunc(policies, func(a, b *domain.DestPolicy) int {
		if a.Action != b.Action {
			if a.Action == domain.DestActionAllow {
				return -1
			}
			return 1
		}
		if a.Priority != b.Priority {
			return a.Priority - b.Priority
		}
		return int(a.ID - b.ID)
	})
	for _, p := range policies {
		if p.State != domain.DestStateOn {
			continue
		}
		sp := snapPolicy{
			ID: p.ID, Name: p.Name, Action: string(p.Action), Priority: p.Priority, Scope: string(p.Scope),
			Lists: slices.Clone(p.ListIDs), Inline: p.Inline,
		}
		slices.Sort(sp.Lists)
		switch p.Scope {
		case domain.DestScopeGroups:
			sp.Groups = slices.Clone(p.GroupIDs)
			slices.Sort(sp.Groups)
		case domain.DestScopePanels:
			sp.Panels = slices.Clone(p.PanelIDs)
			slices.Sort(sp.Panels)
		}
		for _, id := range sp.Lists {
			used[id] = true
		}
		doc.Policy = append(doc.Policy, sp)
	}
	for _, r := range defs.SourceRules {
		if r.State != domain.DestStateOn {
			continue
		}
		doc.Sources = append(doc.Sources, snapSource{Node: r.NodeID, Countries: slices.Clone(r.Countries), CIDRs: slices.Clone(r.CIDRs)})
	}
	for _, l := range defs.Lists {
		if !used[l.ID] {
			continue
		}
		doc.Lists = append(doc.Lists, snapList{ID: l.ID, Name: l.Name, Side: string(l.Side), Ready: l.Ready(), Entries: string(l.Entries)})
	}
	for _, e := range defs.Exemptions {
		if e.ExpiresAt != nil && !e.ExpiresAt.After(now) {
			continue
		}
		doc.Exempt = append(doc.Exempt, e.UserID)
	}
	slices.Sort(doc.Exempt)
	return doc
}

// empty reports whether nothing in the document reaches a node.
func (d *snapshotDoc) empty() bool { return len(d.Policy) == 0 && len(d.Sources) == 0 }

func (d *snapshotDoc) marshal() ([]byte, error) { return json.Marshal(d) }

func parseSnapshotDoc(body []byte) (*snapshotDoc, error) {
	var doc snapshotDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("destpolicy: the published snapshot does not decode: %w", err)
	}
	if doc.Version != snapshotFormatVersion {
		return nil, fmt.Errorf("destpolicy: snapshot format %d is not understood", doc.Version)
	}
	return &doc, nil
}
