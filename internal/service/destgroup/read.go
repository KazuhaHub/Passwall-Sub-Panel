package destgroup

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

const (
	topSites       = 10
	previewTop     = 5
	reportMaxItems = 500
	day            = 24 * time.Hour
)

func ms(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := t.UTC().UnixMilli()
	return &v
}

func source(gid int64) string { return "g" + strconv.FormatInt(gid, 10) }

// trialDays is the window of the report-only figures: seven days, shortened to
// the retention of the trial rows (a window longer than the data kept would say
// "7 days" about 3).
func (s *Service) trialDays(ctx context.Context) int {
	set := ports.UISettings{}
	if s.Settings != nil {
		if loaded, err := s.Settings.Load(ctx, ports.UISettings{}); err == nil {
			set = loaded
		}
	}
	return min(7, ports.AccessControlEffectiveOf(set).TrialRetentionDays)
}

// hourSince is the start of the hour days days ago: the hit rows are by hour.
func (s *Service) hourSince(days int) int64 {
	return s.Now().UTC().Add(-time.Duration(days) * day).Truncate(time.Hour).UnixMilli()
}

// ---- the nodes of a group ----

// panelInfo is what one panel the group's tags select says about the group.
type panelInfo struct {
	id   int64
	name string
	// reason is why the panel cannot enforce an allowlist, "" when it can.
	reason string
	// canRecord: the panel is a native one that records hits (Xray, declaring
	// audit.hits.v1, recording not switched off).
	canRecord bool
	// applied: the node's applied policy carries the group.
	applied bool
}

// panelInfos reads the panels a group's tags select. A read that fails is an
// error: a partial list would say "all nodes can enforce" for the ones it
// could not read.
func (s *Service) panelInfos(ctx context.Context, g *domain.Group) ([]panelInfo, error) {
	ids, err := s.Eligibility.PanelsFor(ctx, g)
	if err != nil {
		return nil, err
	}
	out := make([]panelInfo, 0, len(ids))
	for _, id := range ids {
		panel, err := s.Panels.GetByID(ctx, id)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		info := panelInfo{id: id, name: panel.Name}
		if info.reason, err = s.Eligibility.PanelEnforceReason(ctx, id); err != nil {
			return nil, err
		}
		agent, err := s.Agents.GetByPanelID(ctx, id)
		switch {
		case errors.Is(err, domain.ErrNotFound):
		case err != nil:
			return nil, err
		default:
			info.canRecord = domain.NormalizeAuditCollect(panel.AuditCollect) != domain.AuditCollectOff &&
				domain.NormalizeNodeCoreEngine(agent.ObservedCoreEngine) == domain.NodeCoreXray &&
				slices.Contains(agent.ObservedCapabilities, protocol.CapabilityAuditHits)
			meta, err := s.Policies.GetMeta(ctx, agent.AgentID)
			switch {
			case errors.Is(err, domain.ErrNotFound):
			case err != nil:
				return nil, err
			default:
				info.applied = slices.Contains(meta.AppliedGroups, g.ID)
			}
		}
		out = append(out, info)
	}
	return out, nil
}

// ---- lists ----

func listState(l *domain.DestList) string {
	switch {
	case !l.Ready():
		return "not_ready"
	case l.EntryCount == 0:
		return "empty"
	}
	return "ready"
}

// listOf reads a list; a list that is gone (nil, nil) is not ready.
func (s *Service) listOf(ctx context.Context, id int64) (*domain.DestList, error) {
	l, err := s.Lists.GetByID(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	return l, err
}

// covers reports whether a canonical list covers a recorded destination: a
// domain entry covers the name and everything under it, a full entry only the
// name. "(ip)" is no name.
func covers(entries []byte, site string) bool {
	if site == "" || site == "(ip)" {
		return false
	}
	for _, line := range bytes.Split(entries, []byte("\n")) {
		e := string(line)
		switch {
		case strings.HasPrefix(e, "domain:"):
			d := strings.TrimPrefix(e, "domain:")
			if site == d || strings.HasSuffix(site, "."+d) {
				return true
			}
		case strings.HasPrefix(e, "full:"):
			if site == strings.TrimPrefix(e, "full:") {
				return true
			}
		}
	}
	return false
}

// ownedEntries are the entries of the group's base and extra lists, to say which
// recorded sites are already allowed there.
func (s *Service) ownedEntries(ctx context.Context, a *domain.DestAllowlist) ([][]byte, error) {
	var out [][]byte
	for _, id := range []int64{a.BaseListID, a.ExtraListID} {
		if id == 0 {
			continue
		}
		l, err := s.listOf(ctx, id)
		if err != nil {
			return nil, err
		}
		if l != nil {
			out = append(out, l.Entries)
		}
	}
	return out, nil
}

func addedBy(owned [][]byte, site string) bool {
	for _, e := range owned {
		if covers(e, site) {
			return true
		}
	}
	return false
}

// ---- views ----

// state is the group's allowlist state, off for a group that never had one.
func stateOf(a *domain.DestAllowlist) domain.DestState {
	if a == nil || a.State == "" {
		return domain.DestStateOff
	}
	return a.State
}

// view builds one group's view from what was read for it.
func (s *Service) view(ctx context.Context, g *domain.Group, a *domain.DestAllowlist, members int64, infos []panelInfo, blocked map[string]int64) (*View, error) {
	state := stateOf(a)
	v := &View{
		GroupID: g.ID, GroupName: g.Name, Members: members, State: string(state),
		StateChangedAt: ms(a.StateChangedAt), UpdatedAt: ms(a.UpdatedAt),
		ListIDs: []int64{}, Lists: []ListView{}, BaseListID: a.BaseListID, ExtraListID: a.ExtraListID,
		Ineligible: []Ineligible{}, PendingNodes: []PanelRef{}, NotReadyLists: []ListRef{},
		TotalNodes: len(infos),
	}
	if a.ListIDs != nil {
		v.ListIDs = slices.Clone(a.ListIDs)
	}
	seen := map[int64]bool{}
	all := append(slices.Clone(a.ListIDs), a.BaseListID, a.ExtraListID)
	for _, id := range all {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		l, err := s.listOf(ctx, id)
		if err != nil {
			return nil, err
		}
		inNamed := slices.Contains(a.ListIDs, id)
		switch {
		case l == nil:
			if inNamed {
				v.Lists = append(v.Lists, ListView{ID: id, State: "not_ready"})
			}
			v.NotReadyLists = append(v.NotReadyLists, ListRef{ID: id})
			continue
		case inNamed:
			v.Lists = append(v.Lists, ListView{ID: id, Name: l.Name, EntryCount: l.EntryCount, State: listState(l)})
		}
		if id == a.BaseListID {
			v.BaseCount = l.EntryCount
		}
		if !l.Ready() {
			v.NotReadyLists = append(v.NotReadyLists, ListRef{ID: id, Name: l.Name})
		}
	}
	slices.SortFunc(v.NotReadyLists, func(x, y ListRef) int { return int(x.ID - y.ID) })

	inForce := state != domain.DestStateOff
	for _, p := range infos {
		switch {
		case !inForce || p.reason == "":
			v.EligibleNodes++
			if inForce {
				if p.applied {
					v.AppliedNodes++
				} else if members > 0 {
					v.PendingNodes = append(v.PendingNodes, PanelRef{PanelID: p.id, Name: p.name})
				}
			}
		default:
			v.Ineligible = append(v.Ineligible, Ineligible{PanelID: p.id, Name: p.name, Reason: p.reason})
		}
	}

	if s.Hits != nil {
		since := s.hourSince(s.trialDays(ctx))
		sites, err := s.Hits.TrialSites(ctx, source(g.ID), since)
		if err != nil {
			return nil, err
		}
		if len(sites) > 0 {
			w := WouldDeny{Days: s.trialDays(ctx), Sites: len(sites)}
			for _, site := range sites {
				w.Count += site.Count
			}
			v.WouldDeny = &w
		}
		if n, ok := blocked[source(g.ID)]; ok || state == domain.DestStateOn {
			v.Denied24h = &n
		}
	}
	return v, nil
}

// blockedLastDay is, per group source, the connections the groups refused in the
// last 24 hours.
func (s *Service) blockedLastDay(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	if s.Hits == nil {
		return out, nil
	}
	counts, err := s.Hits.SourceCounts(ctx, s.hourSince(1))
	if err != nil {
		return nil, err
	}
	for _, c := range counts {
		if c.Action == domain.DestHitBlock && strings.HasPrefix(c.Source, "g") {
			out[c.Source] = c.Count
		}
	}
	return out, nil
}

// List reads every group, with the allowlist it has (a group that never had one
// is off, with no lists): the allowlist tab shows all of them.
func (s *Service) List(ctx context.Context) ([]*View, error) {
	groups, err := s.Groups.List(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := s.Allowlists.List(ctx)
	if err != nil {
		return nil, err
	}
	rows := make(map[int64]*domain.DestAllowlist, len(entries))
	for _, e := range entries {
		a := e.Allowlist
		rows[e.GroupID] = &a
	}
	ids := make([]int64, len(groups))
	for i, g := range groups {
		ids[i] = g.ID
	}
	members, err := s.Groups.CountMembersByGroups(ctx, ids)
	if err != nil {
		return nil, err
	}
	blocked, err := s.blockedLastDay(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*View, 0, len(groups))
	for _, g := range groups {
		a := rows[g.ID]
		if a == nil {
			a = &domain.DestAllowlist{}
		}
		infos, err := s.panelInfos(ctx, g)
		if err != nil {
			return nil, err
		}
		v, err := s.view(ctx, g, a, members[g.ID], infos, blocked)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// viewOf reads one group's view.
func (s *Service) viewOf(ctx context.Context, gid int64) (*View, *domain.DestAllowlist, []panelInfo, error) {
	g, err := s.Groups.GetByID(ctx, gid)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil, nil, ErrGroupNotFound
	}
	if err != nil {
		return nil, nil, nil, err
	}
	a, err := s.Allowlists.Get(ctx, gid)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil, nil, ErrGroupNotFound
	}
	if err != nil {
		return nil, nil, nil, err
	}
	members, err := s.Groups.CountMembersByGroups(ctx, []int64{gid})
	if err != nil {
		return nil, nil, nil, err
	}
	blocked, err := s.blockedLastDay(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	infos, err := s.panelInfos(ctx, g)
	if err != nil {
		return nil, nil, nil, err
	}
	v, err := s.view(ctx, g, a, members[gid], infos, blocked)
	return v, a, infos, err
}

// Get reads one group with what the turn-on dialog lists.
func (s *Service) Get(ctx context.Context, gid int64) (*Detail, error) {
	v, a, _, err := s.viewOf(ctx, gid)
	if err != nil {
		return nil, err
	}
	d := &Detail{View: v, Readiness: Readiness{
		ListsReady: len(v.NotReadyLists) == 0, NotReadyLists: v.NotReadyLists, UnappliedNodes: len(v.PendingNodes),
		BaseNonempty: v.BaseCount > 0, TopSites: []SiteCount{},
	}}
	if s.Hits == nil {
		return d, nil
	}
	sites, err := s.Hits.TrialSites(ctx, source(gid), s.hourSince(1))
	if err != nil {
		return nil, err
	}
	owned, err := s.ownedEntries(ctx, a)
	if err != nil {
		return nil, err
	}
	for _, site := range sites {
		added := addedBy(owned, site.Site)
		if !added {
			d.Readiness.RemainingSites24h++
		}
		if len(d.Readiness.TopSites) < topSites {
			d.Readiness.TopSites = append(d.Readiness.TopSites, SiteCount{Site: site.Site, Count: site.Count, Added: added})
		}
	}
	return d, nil
}

// Summaries are the groups in force (report or on), by group id: what the
// policy list says about the allowlist.
func (s *Service) Summaries(ctx context.Context) ([]Summary, error) {
	entries, err := s.Allowlists.List(ctx)
	if err != nil {
		return nil, err
	}
	out := []Summary{}
	for _, e := range entries {
		if e.Allowlist.State != domain.DestStateReport && e.Allowlist.State != domain.DestStateOn {
			continue
		}
		days := 0
		if at := e.Allowlist.StateChangedAt; at != nil && s.Now().After(*at) {
			days = int(s.Now().Sub(*at) / day)
		}
		out = append(out, Summary{GroupID: e.GroupID, Name: e.GroupName, State: string(e.Allowlist.State), StateDays: days})
	}
	return out, nil
}

// Report reads what the group's report-only rows say: the destinations its
// members would have been denied, folded by site, the most seen first, and
// which of them the group's own base and extra lists already allow. days is
// clamped to what is kept (0: the default window).
func (s *Service) Report(ctx context.Context, gid int64, days int) (*Report, error) {
	_, a, err := s.groupAndAllowlist(ctx, gid)
	if err != nil {
		return nil, err
	}
	keep := s.trialDays(ctx)
	if days <= 0 || days > keep {
		days = keep
	}
	r := &Report{EffectiveDays: days, State: string(stateOf(a)), SinceAt: ms(a.StateChangedAt), Items: []ReportItem{}}
	if s.Hits == nil {
		return r, nil
	}
	since := s.hourSince(days)
	sites, err := s.Hits.TrialSites(ctx, source(gid), since)
	if err != nil {
		return nil, err
	}
	owned, err := s.ownedEntries(ctx, a)
	if err != nil {
		return nil, err
	}
	for i, site := range sites {
		if i >= reportMaxItems {
			break
		}
		r.Items = append(r.Items, ReportItem{Site: site.Site, Count: site.Count, LastAt: site.LastMS, Added: addedBy(owned, site.Site)})
	}
	if r.DroppedInRange, err = s.Hits.TrialDropped(ctx, since, s.Now().UTC().Add(time.Hour).Truncate(time.Hour).UnixMilli()); err != nil {
		return nil, err
	}
	return r, nil
}

// groupAndAllowlist is the group and its allowlist, without the node reads a
// view needs.
func (s *Service) groupAndAllowlist(ctx context.Context, gid int64) (*domain.Group, *domain.DestAllowlist, error) {
	g, err := s.Groups.GetByID(ctx, gid)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil, ErrGroupNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	a, err := s.Allowlists.Get(ctx, gid)
	if err != nil {
		return nil, nil, err
	}
	return g, a, nil
}
