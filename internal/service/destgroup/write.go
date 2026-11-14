package destgroup

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

// maxBaseHosts bounds the hosts of a new group's base allowance: it is a
// handful of encrypted-DNS names, not a list.
const maxBaseHosts = 64

func badInput(field, msg string, bad ...string) error {
	return &destpolicy.ValidationError{Code: destpolicy.CodePolicyInvalid, Field: field, Bad: bad, Msg: msg}
}

// normalizeListIDs checks the lists an allowlist names: they exist, are lists of
// destinations, belong to no other group, and a custom one is not empty. The ids
// come back sorted and without duplicates.
func (s *Service) normalizeListIDs(ctx context.Context, gid int64, in []int64, probe bool) ([]int64, error) {
	ids := slices.Clone(in)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	for _, id := range ids {
		l, err := s.Lists.GetByID(ctx, id)
		switch {
		case errors.Is(err, domain.ErrNotFound):
			return nil, badInput("list_ids", "no such list", fmt.Sprint(id))
		case err != nil:
			return nil, err
		case l.Side != domain.DestSideDest:
			return nil, badInput("list_ids", "a source list cannot be part of an allowlist", fmt.Sprint(id))
		case l.OwnerGroupID != 0 && l.OwnerGroupID != gid:
			return nil, badInput("list_ids", "a list that belongs to another group cannot be used here", fmt.Sprint(id))
		case l.Kind == domain.DestListCustom && l.EntryCount == 0:
			return nil, badInput("list_ids", "an empty custom list allows nothing", fmt.Sprint(id))
		}
		if probe {
			// An allowlist lets traffic through: an entry that would let through all
			// of a public suffix or nearly every host is refused, whatever wrote it.
			if broad := destlist.BroadEntryOf(l); broad != nil {
				return nil, broad
			}
		}
	}
	return ids, nil
}

// normalizeBaseHost reads one host of the base allowance: a bare, exact host
// name. A URL, an address, a port and a whole top-level domain are not hosts the
// allowance can be made of.
func normalizeBaseHost(h string) (string, bool) {
	h = strings.TrimSpace(h)
	if h == "" || strings.ContainsAny(h, "/\\?#@: \t\r\n") {
		return "", false
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return "", false
	}
	host, ok := destlist.NormalizeDomain(h)
	if !ok {
		return "", false
	}
	parsed, err := destlist.ParseCustom([]byte("full:"+host), domain.DestSideDest)
	if err != nil || parsed.EntryCount != 1 || parsed.Report.Ignored != 0 {
		return "", false
	}
	return host, true
}

// ownedList builds one of a group's own custom lists from exact hosts.
func ownedList(name string, hosts []string, now time.Time) (*domain.DestList, error) {
	var text strings.Builder
	for _, h := range hosts {
		text.WriteString("DOMAIN," + h + "\n")
	}
	parsed, err := destlist.ParseCustom([]byte(text.String()), domain.DestSideDest)
	if err != nil {
		return nil, err
	}
	at := now.UTC()
	return &domain.DestList{
		Name: name, Kind: domain.DestListCustom, Side: domain.DestSideDest, Entries: parsed.Entries, SourceText: []byte(text.String()),
		EntryCount: parsed.EntryCount, RegexpCount: parsed.RegexpCount, ContentSHA256: parsed.SHA256, LastFetchedAt: &at,
	}, nil
}

// notReady are the lists among ids that are not ready, or are gone.
func (s *Service) notReady(ctx context.Context, ids ...int64) ([]ListRef, error) {
	var out []ListRef
	seen := map[int64]bool{}
	for _, id := range ids {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		l, err := s.listOf(ctx, id)
		if err != nil {
			return nil, err
		}
		switch {
		case l == nil:
			out = append(out, ListRef{ID: id})
		case !l.Ready():
			out = append(out, ListRef{ID: id, Name: l.Name})
		}
	}
	return out, nil
}

func refIDs(refs []ListRef) []int64 {
	out := make([]int64, len(refs))
	for i, r := range refs {
		out[i] = r.ID
	}
	return out
}

// Put sets a group's state and the lists its allowlist is made of. Any state can
// be set from any other (there is no report-first step, and no table of allowed
// transitions); two admins' writes are told apart by updated_at.
//
// A group's first allowlist is created together with the two lists it owns, in
// one transaction, from base_hosts: the exact hosts every group needs (the
// encrypted-DNS hosts its clients resolve through). An existing allowlist is
// written with the updated_at it was read at.
//
// The one safety check beyond the shape is that an allowlist that is "on" has
// every list ready: an allowlist built on a list that never arrived would deny
// everything. Nothing else is refused (D3): no node available, or lists that are
// empty, is for the admin to read in the preview.
//
// Every write drops the cached eligibility and then tells the member sync to
// re-derive the group's members, in that order.
func (s *Service) Put(ctx context.Context, gid int64, in PutInput) (*View, error) {
	if !in.State.Valid() {
		return nil, badInput("state", "state must be off, report or on", string(in.State))
	}
	_, cur, err := s.groupAndAllowlist(ctx, gid)
	if err != nil {
		return nil, err
	}
	hasRow := cur.UpdatedAt != nil
	if !hasRow && in.State == domain.DestStateOff {
		// Nothing to turn off: no row is made just to say so.
		v, _, _, err := s.viewOf(ctx, gid)
		return v, err
	}
	ids, err := s.normalizeListIDs(ctx, gid, in.ListIDs, in.State != domain.DestStateOff)
	if err != nil {
		return nil, err
	}
	next := &domain.DestAllowlist{State: in.State, ListIDs: ids, BaseListID: cur.BaseListID, ExtraListID: cur.ExtraListID}
	var base, extra *domain.DestList
	hostCount := 0
	switch {
	case hasRow:
		if in.UpdatedAt == nil {
			return nil, badInput("updated_at", "the allowlist's updated_at is required to change it")
		}
		if in.BaseHosts != nil {
			return nil, badInput("base_hosts", "the group's base allowance exists; change it through its list's entries")
		}
	default:
		if in.BaseHosts == nil {
			return nil, badInput("base_hosts", "base_hosts is required for a group's first allowlist (it may be empty)")
		}
		hosts, bad := []string{}, []string{}
		for _, h := range *in.BaseHosts {
			if host, ok := normalizeBaseHost(h); ok {
				hosts = append(hosts, host)
			} else {
				bad = append(bad, h)
			}
		}
		if len(bad) > 0 {
			return nil, badInput("base_hosts", "not an exact host name", bad...)
		}
		slices.Sort(hosts)
		hosts = slices.Compact(hosts)
		if len(hosts) > maxBaseHosts {
			return nil, badInput("base_hosts", fmt.Sprintf("at most %d hosts", maxBaseHosts))
		}
		hostCount = len(hosts)
		now := s.Now()
		if base, err = ownedList(fmt.Sprintf("group %d base allowance", gid), hosts, now); err != nil {
			return nil, err
		}
		if extra, err = ownedList(fmt.Sprintf("group %d extra allowances", gid), nil, now); err != nil {
			return nil, err
		}
	}
	if in.State != domain.DestStateOff {
		entries := hostCount
		for _, id := range []int64{next.BaseListID, next.ExtraListID} {
			if id == 0 {
				continue
			}
			if l, err := s.listOf(ctx, id); err != nil {
				return nil, err
			} else if l != nil {
				entries += l.EntryCount
			}
		}
		if len(ids) == 0 && entries == 0 {
			return nil, badInput("list_ids", "an allowlist needs a list or a host: nothing would be allowed")
		}
	}
	if in.State == domain.DestStateOn {
		bad, err := s.notReady(ctx, append(slices.Clone(ids), next.BaseListID, next.ExtraListID)...)
		if err != nil {
			return nil, err
		}
		if len(bad) > 0 {
			return nil, &destlist.ListNotReadyError{ListIDs: refIDs(bad)}
		}
	}
	if s.Budget != nil && in.State != domain.DestStateOff {
		hypo := destpolicy.GroupHypo{Mode: &domain.DestGroupMode{GroupID: gid, State: in.State, ListIDs: ids, BaseListID: next.BaseListID, ExtraListID: next.ExtraListID}}
		if !hasRow {
			// The lists do not exist yet: the save is judged with them under ids
			// that cannot be a stored list's.
			b, e := *base, *extra
			b.ID, e.ID = -1, -2
			hypo.Mode.BaseListID, hypo.Mode.ExtraListID = b.ID, e.ID
			hypo.Lists = []*domain.DestList{&b, &e}
		}
		if err := s.Budget.CheckGroupBudget(ctx, hypo); err != nil {
			return nil, err
		}
	}

	if hasRow {
		err = s.Allowlists.Save(ctx, gid, next, in.UpdatedAt)
	} else {
		err = s.Allowlists.CreateWithOwnedLists(ctx, gid, next, base, extra)
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return nil, ErrGroupNotFound
	case errors.Is(err, domain.ErrValidation):
		// A list the allowlist names was deleted between the checks and the write.
		return nil, badInput("list_ids", "a list the allowlist names no longer exists")
	case errors.Is(err, domain.ErrAlreadyExists):
		// Another admin made the group's first allowlist between the read and the write.
		return nil, fmt.Errorf("%w: the group's allowlist changed since it was read", domain.ErrConflict)
	case err != nil:
		return nil, err
	}

	// What the members may have a client on changed with the state: drop the
	// cached answer first, then re-derive (the resync reads it), then the
	// subscriptions rendered from the old answer.
	s.Eligibility.InvalidateEligibility(0)
	if s.Resync != nil {
		s.Resync.ResyncGroupMembersInBackground(gid)
	}
	if s.Changed != nil {
		s.Changed(ctx)
	}
	v, _, _, err := s.viewOf(ctx, gid)
	return v, err
}

// Preview says what saving this state and these lists would do, and changes
// nothing: how many members, which nodes the group could still use and which it
// would lose (and why), which lists are not ready, the hosts the base allowance
// is suggested from, and what the group's report-only rows say it would have
// denied lately. The figures are of the lists the group has now: the rows record
// what the current lists denied, and are not re-judged against the edited ones.
func (s *Service) Preview(ctx context.Context, gid int64, in PreviewInput) (*Preview, error) {
	if !in.State.Valid() {
		return nil, badInput("state", "state must be off, report or on", string(in.State))
	}
	g, cur, err := s.groupAndAllowlist(ctx, gid)
	if err != nil {
		return nil, err
	}
	ids, err := s.normalizeListIDs(ctx, gid, in.ListIDs, in.State != domain.DestStateOff)
	if err != nil {
		return nil, err
	}
	members, err := s.Groups.CountMembersByGroups(ctx, []int64{gid})
	if err != nil {
		return nil, err
	}
	infos, err := s.panelInfos(ctx, g)
	if err != nil {
		return nil, err
	}
	p := &Preview{
		Members: members[gid], TotalNodes: len(infos), Ineligible: []Ineligible{}, NodesLost: []Ineligible{},
		NotReadyLists: []ListRef{}, BaseHosts: []string{}, BaseHostsSource: "none",
	}
	nowInForce, willBe := stateOf(cur) != domain.DestStateOff, in.State != domain.DestStateOff
	for _, pi := range infos {
		if !willBe || pi.reason == "" {
			p.EligibleNodes++
			if willBe && !pi.canRecord {
				p.NonRecordingNodes++
			}
			continue
		}
		x := Ineligible{PanelID: pi.id, Name: pi.name, Reason: pi.reason}
		p.Ineligible = append(p.Ineligible, x)
		if !nowInForce {
			p.NodesLost = append(p.NodesLost, x)
		}
	}
	if willBe {
		bad, err := s.notReady(ctx, append(slices.Clone(ids), cur.BaseListID, cur.ExtraListID)...)
		if err != nil {
			return nil, err
		}
		p.NotReadyLists = append(p.NotReadyLists, bad...)
	}
	if s.BaseHosts != nil {
		if hosts, err := s.BaseHosts(ctx); err == nil && len(hosts) > 0 {
			p.BaseHosts, p.BaseHostsSource = hosts, "template"
		}
	}
	if s.Hits != nil {
		days := s.trialDays(ctx)
		sites, err := s.Hits.TrialSites(ctx, source(gid), s.hourSince(days))
		if err != nil {
			return nil, err
		}
		if len(sites) > 0 {
			w := &WouldDenyTop{WouldDeny: WouldDeny{Days: days, Sites: len(sites)}, Top: []SiteCount{}}
			owned, err := s.ownedEntries(ctx, cur)
			if err != nil {
				return nil, err
			}
			for i, site := range sites {
				w.Count += site.Count
				if i < previewTop {
					w.Top = append(w.Top, SiteCount{Site: site.Site, Count: site.Count, Added: addedBy(owned, site.Site)})
				}
			}
			p.WouldDeny = w
		}
	}
	return p, nil
}

// AddException appends a destination the group's members went to, as the report
// shows it, to the group's extra list: the site (a domain and everything under
// it) or the host (exactly that name). It is the list's own entry edit, and one
// change is one advance of the generation.
func (s *Service) AddException(ctx context.Context, gid int64, target, match string) (*ExceptionResult, error) {
	line, err := destpolicy.ExceptionLine(target, match)
	if err != nil {
		return nil, err
	}
	_, a, err := s.groupAndAllowlist(ctx, gid)
	if err != nil {
		return nil, err
	}
	if a.ExtraListID == 0 {
		return nil, badInput("group_id", "the group has no allowlist to add to")
	}
	if s.Editor == nil {
		return nil, fmt.Errorf("%w: exceptions are not available in this build", domain.ErrUnavailable)
	}
	if _, _, err := s.Editor.AddEntries(ctx, a.ExtraListID, []string{line}, nil); err != nil {
		return nil, err
	}
	return &ExceptionResult{Entry: line, GroupID: gid, ListID: a.ExtraListID}, nil
}
