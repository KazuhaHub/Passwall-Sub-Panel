package destgroup

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

// ---- reading ----

func TestList_EveryGroupWithItsStateListsAndNodes(t *testing.T) {
	h := newHarness(t)
	plain := h.group("Plain", 3) // never had an allowlist
	guests := h.group("Guests", 7)
	l := h.list("news", "DOMAIN-SUFFIX,news.example")
	if _, err := h.put(guests.ID, PutInput{State: domain.DestStateReport, ListIDs: []int64{l.ID}, BaseHosts: hosts("doh.example", "dns.example")}); err != nil {
		t.Fatal(err)
	}
	// Panel 1 runs the group's rules; panel 2 cannot enforce; 3 is a 3X-UI panel.
	h.meta[nativeAgent] = &domain.DestAgentPolicy{AgentID: nativeAgent, AppliedGroups: []int64{guests.ID}}

	got, err := h.svc.List(h.ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("list = %+v, %v: every group, the one without an allowlist too", got, err)
	}
	p, g := got[0], got[1]
	if p.GroupID != plain.ID || p.State != "off" || p.Members != 3 || p.UpdatedAt != nil || p.StateChangedAt != nil || len(p.Lists) != 0 || p.ListIDs == nil {
		t.Fatalf("plain = %+v: off, no row, and a list that is [] not null", p)
	}
	if p.EligibleNodes != p.TotalNodes || len(p.Ineligible) != 0 {
		t.Fatalf("plain nodes: %d/%d, %+v: an off group is not limited to nodes that enforce", p.EligibleNodes, p.TotalNodes, p.Ineligible)
	}
	if g.GroupID != guests.ID || g.GroupName != "Guests" || g.State != "report" || g.Members != 7 || g.UpdatedAt == nil || g.StateChangedAt == nil {
		t.Fatalf("guests = %+v", g)
	}
	if !slices.Equal(g.ListIDs, []int64{l.ID}) || len(g.Lists) != 1 || g.Lists[0] != (ListView{ID: l.ID, Name: "news", EntryCount: 1, State: "ready"}) {
		t.Fatalf("guests lists = %+v / %+v", g.ListIDs, g.Lists)
	}
	if g.BaseListID == 0 || g.ExtraListID == 0 || g.BaseCount != 2 {
		t.Fatalf("guests base = %d extra = %d count = %d", g.BaseListID, g.ExtraListID, g.BaseCount)
	}
	if g.TotalNodes != 3 || g.EligibleNodes != 1 {
		t.Fatalf("nodes = %d/%d, want 1 of 3", g.EligibleNodes, g.TotalNodes)
	}
	wantIneligible := []Ineligible{{PanelID: 2, Name: "osaka", Reason: "capability"}, {PanelID: 3, Name: "legacy", Reason: "xui"}}
	if !slices.Equal(g.Ineligible, wantIneligible) {
		t.Fatalf("ineligible = %+v, want %+v", g.Ineligible, wantIneligible)
	}
	if g.AppliedNodes != 1 || len(g.PendingNodes) != 0 || len(g.NotReadyLists) != 0 {
		t.Fatalf("applied %d pending %+v not ready %+v", g.AppliedNodes, g.PendingNodes, g.NotReadyLists)
	}
}

func TestList_PendingNodesAreTheEligibleOnesThatDoNotRunTheGroupYet(t *testing.T) {
	h := newHarness(t)
	g := h.group("Guests", 4)
	l := h.list("news", "DOMAIN-SUFFIX,news.example")
	if _, err := h.put(g.ID, PutInput{State: domain.DestStateOn, ListIDs: []int64{l.ID}, BaseHosts: hosts()}); err != nil {
		t.Fatal(err)
	}
	h.elig.reasons = map[int64]string{3: "xui"} // panels 1 and 2 can both enforce
	h.meta[nativeAgent] = &domain.DestAgentPolicy{AgentID: nativeAgent, AppliedGroups: []int64{g.ID}}
	got, _ := h.svc.List(h.ctx)
	if got[0].AppliedNodes != 1 || !slices.Equal(got[0].PendingNodes, []PanelRef{{PanelID: 2, Name: "osaka"}}) {
		t.Fatalf("applied %d pending %+v, want panel 2 pending", got[0].AppliedNodes, got[0].PendingNodes)
	}
	// With nobody in the group there is nothing to apply.
	h.groups.members[g.ID] = 0
	if got, _ := h.svc.List(h.ctx); len(got[0].PendingNodes) != 0 {
		t.Fatalf("pending %+v for a group with no members", got[0].PendingNodes)
	}
}

func TestList_NotReadyListsAndTheTrialFigures(t *testing.T) {
	h := newHarness(t)
	g := h.group("Guests", 4)
	remote := h.remoteList("feed") // never fetched
	if _, err := h.put(g.ID, PutInput{State: domain.DestStateReport, ListIDs: []int64{remote.ID}, BaseHosts: hosts()}); err != nil {
		t.Fatal(err)
	}
	got, _ := h.svc.List(h.ctx)
	if !slices.Equal(got[0].NotReadyLists, []ListRef{{ID: remote.ID, Name: "feed"}}) || got[0].Lists[0].State != "not_ready" {
		t.Fatalf("not ready = %+v, lists = %+v", got[0].NotReadyLists, got[0].Lists)
	}
	if got[0].WouldDeny != nil || got[0].Denied24h != nil {
		t.Fatalf("would_deny %+v denied %v: no row was ever recorded, so null and not zero", got[0].WouldDeny, got[0].Denied24h)
	}
	src := "g" + itoa(g.ID)
	h.hit(0, src, "observe", "a.example", 10, 0)
	h.hit(0, src, "observe", "b.example", 5, 0)
	h.hit(3, src, "observe", "c.example", 100, 0) // in the retention, past the 7-day window? no: 3 days
	h.hit(0, src, "block", "x.example", 8, 7)
	got, _ = h.svc.List(h.ctx)
	if w := got[0].WouldDeny; w == nil || *w != (WouldDeny{Days: 7, Sites: 3, Count: 115}) {
		t.Fatalf("would_deny = %+v, want 3 sites 115 over 7 days", w)
	}
	if got[0].Denied24h == nil || *got[0].Denied24h != 8 {
		t.Fatalf("denied_24h = %v, want 8", got[0].Denied24h)
	}
}

func TestGet_ReadinessForTheTurnOnDialog(t *testing.T) {
	h := newHarness(t)
	g := h.group("Guests", 4)
	l := h.list("news", "DOMAIN-SUFFIX,news.example")
	if _, err := h.put(g.ID, PutInput{State: domain.DestStateReport, ListIDs: []int64{l.ID}, BaseHosts: hosts()}); err != nil {
		t.Fatal(err)
	}
	src := "g" + itoa(g.ID)
	h.hit(0, src, "observe", "news.example", 50, 0) // already allowed by the list... but only extra and base count as added
	h.hit(0, src, "observe", "cdn.example", 30, 0)
	h.hit(0, src, "observe", "api.example", 20, 0)
	h.hit(2, src, "observe", "old.example", 99, 0) // not in the last 24 hours
	al, _ := h.dest.GroupAllowlists.Get(h.ctx, g.ID)
	if _, _, err := h.lists.AddEntries(h.ctx, al.ExtraListID, []string{"DOMAIN-SUFFIX,cdn.example"}, nil); err != nil {
		t.Fatal(err)
	}
	d, err := h.svc.Get(h.ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := d.Readiness
	if !r.ListsReady || len(r.NotReadyLists) != 0 || r.BaseNonempty || r.UnappliedNodes != 1 {
		t.Fatalf("readiness = %+v: the base is empty and one eligible node does not run the group yet", r)
	}
	// 3 sites in 24 hours, one of them (cdn) already added to the group's extra list.
	if r.RemainingSites24h != 2 {
		t.Fatalf("remaining = %d, want 2", r.RemainingSites24h)
	}
	want := []SiteCount{{Site: "news.example", Count: 50}, {Site: "cdn.example", Count: 30, Added: true}, {Site: "api.example", Count: 20}}
	if !slices.Equal(r.TopSites, want) {
		t.Fatalf("top sites = %+v, want %+v", r.TopSites, want)
	}
	if _, err := h.svc.Get(h.ctx, 9999); !errors.Is(err, ErrGroupNotFound) || !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing group = %v", err)
	}
}

// ---- writing ----

// Any state can be set from any other, and the first write can be "on": there is
// no report-first step.
func TestPut_OffToOnDirectlyAndTheMembersAreToldInOrder(t *testing.T) {
	h := newHarness(t)
	g := h.group("Guests", 4)
	l := h.list("news", "DOMAIN-SUFFIX,news.example")
	gen := h.generation()
	v, err := h.put(g.ID, PutInput{State: domain.DestStateOn, ListIDs: []int64{l.ID, l.ID}, BaseHosts: hosts("DoH.Example.", "doh.example")})
	if err != nil {
		t.Fatal(err)
	}
	if v.State != "on" || !slices.Equal(v.ListIDs, []int64{l.ID}) || v.UpdatedAt == nil || v.BaseCount != 1 {
		t.Fatalf("view = %+v", v)
	}
	if h.generation() != gen+1 {
		t.Fatalf("generation %d -> %d: the lists and the state are one advance", gen, h.generation())
	}
	al, _ := h.dest.GroupAllowlists.Get(h.ctx, g.ID)
	base, err := h.dest.Lists.GetByID(h.ctx, al.BaseListID)
	if err != nil || base.OwnerGroupID != g.ID || string(base.Entries) != "full:doh.example" {
		t.Fatalf("base list = %+v, %v: an exact host, owned by the group", base, err)
	}
	extra, _ := h.dest.Lists.GetByID(h.ctx, al.ExtraListID)
	if extra.OwnerGroupID != g.ID || extra.EntryCount != 0 {
		t.Fatalf("extra list = %+v", extra)
	}
	// The eligibility is dropped, then the members re-derived, then the render cache.
	if want := []string{"invalidate:0", "resync:" + itoa(g.ID), "changed"}; !slices.Equal(h.ev.all(), want) {
		t.Fatalf("events = %v, want %v", h.ev.all(), want)
	}
	// The save was measured, with the lists it brings along under ids it has not got yet.
	if len(h.budget.got) != 1 || h.budget.got[0].Mode.State != domain.DestStateOn || len(h.budget.got[0].Lists) != 2 {
		t.Fatalf("budget = %+v", h.budget.got)
	}
}

func TestPut_EveryTransitionIsAllowedAndOffKeepsTheLists(t *testing.T) {
	h := newHarness(t)
	g := h.group("Guests", 4)
	l := h.list("news", "DOMAIN-SUFFIX,news.example")
	v, err := h.put(g.ID, PutInput{State: domain.DestStateReport, ListIDs: []int64{l.ID}, BaseHosts: hosts()})
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []domain.DestState{domain.DestStateOn, domain.DestStateReport, domain.DestStateOff, domain.DestStateOn, domain.DestStateOff, domain.DestStateReport} {
		v, err = h.put(g.ID, h.edit(v, to))
		if err != nil || v.State != string(to) {
			t.Fatalf("to %s: %+v, %v", to, v, err)
		}
	}
	if v.State != "report" || !slices.Equal(v.ListIDs, []int64{l.ID}) || v.BaseListID == 0 {
		t.Fatalf("after the round trip: %+v: off keeps the row and the lists", v)
	}
}

// edit is the PUT a page sends to change only the state of what it read.
func (h *harness) edit(v *View, to domain.DestState) PutInput {
	at := time.UnixMilli(*v.UpdatedAt).UTC()
	return PutInput{State: to, ListIDs: v.ListIDs, UpdatedAt: &at}
}

func TestPut_OffForAGroupThatNeverHadAnAllowlistWritesNothing(t *testing.T) {
	h := newHarness(t)
	g := h.group("Guests", 4)
	gen := h.generation()
	v, err := h.put(g.ID, PutInput{State: domain.DestStateOff})
	if err != nil || v.State != "off" || v.UpdatedAt != nil {
		t.Fatalf("view = %+v, %v", v, err)
	}
	if h.generation() != gen || len(h.ev.all()) != 0 {
		t.Fatalf("a no-op wrote: generation %d -> %d, events %v", gen, h.generation(), h.ev.all())
	}
}

func TestPut_RefusalsAndTheirCodes(t *testing.T) {
	h := newHarness(t)
	g, other := h.group("Guests", 4), h.group("Other", 1)
	good := h.list("news", "DOMAIN-SUFFIX,news.example")
	empty := h.list("empty", "")
	src, _, err := h.lists.CreateCustom(h.ctx, "src", domain.DestSideSource, []byte("SRC-GEOIP,CN"))
	if err != nil {
		t.Fatal(err)
	}
	// A list another group owns.
	if _, err := h.put(other.ID, PutInput{State: domain.DestStateReport, ListIDs: []int64{good.ID}, BaseHosts: hosts()}); err != nil {
		t.Fatal(err)
	}
	otherAl, _ := h.dest.GroupAllowlists.Get(h.ctx, other.ID)
	remote := h.remoteList("feed")

	type tc struct {
		name  string
		in    PutInput
		code  string
		field string
	}
	for _, c := range []tc{
		{"unknown state", PutInput{State: "maybe", ListIDs: []int64{good.ID}, BaseHosts: hosts()}, destpolicy.CodePolicyInvalid, "state"},
		{"a list that does not exist", PutInput{State: domain.DestStateReport, ListIDs: []int64{99999}, BaseHosts: hosts()}, destpolicy.CodePolicyInvalid, "list_ids"},
		{"a source list", PutInput{State: domain.DestStateReport, ListIDs: []int64{src.ID}, BaseHosts: hosts()}, destpolicy.CodePolicyInvalid, "list_ids"},
		{"another group's own list", PutInput{State: domain.DestStateReport, ListIDs: []int64{otherAl.BaseListID}, BaseHosts: hosts()}, destpolicy.CodePolicyInvalid, "list_ids"},
		{"an empty custom list", PutInput{State: domain.DestStateReport, ListIDs: []int64{empty.ID}, BaseHosts: hosts()}, destpolicy.CodePolicyInvalid, "list_ids"},
		{"no list and no host", PutInput{State: domain.DestStateReport, BaseHosts: hosts()}, destpolicy.CodePolicyInvalid, "list_ids"},
		{"a new allowlist without base_hosts", PutInput{State: domain.DestStateReport, ListIDs: []int64{good.ID}}, destpolicy.CodePolicyInvalid, "base_hosts"},
		{"a url as a base host", PutInput{State: domain.DestStateReport, ListIDs: []int64{good.ID}, BaseHosts: hosts("https://doh.example/dns-query")}, destpolicy.CodePolicyInvalid, "base_hosts"},
		{"an address as a base host", PutInput{State: domain.DestStateReport, ListIDs: []int64{good.ID}, BaseHosts: hosts("1.1.1.1")}, destpolicy.CodePolicyInvalid, "base_hosts"},
		{"a whole top level domain as a base host", PutInput{State: domain.DestStateReport, ListIDs: []int64{good.ID}, BaseHosts: hosts("com")}, destpolicy.CodePolicyInvalid, "base_hosts"},
	} {
		t.Run(c.name, func(t *testing.T) {
			gen := h.generation()
			_, err := h.put(g.ID, c.in)
			var v *destpolicy.ValidationError
			if !errors.As(err, &v) || v.Code != c.code || v.Field != c.field {
				t.Fatalf("err = %v, want %s on %s", err, c.code, c.field)
			}
			if h.generation() != gen {
				t.Fatal("a refused save advanced the generation")
			}
			if al, _ := h.dest.GroupAllowlists.Get(h.ctx, g.ID); al.UpdatedAt != nil {
				t.Fatalf("a refused save wrote the group: %+v", al)
			}
			if lists, _ := h.dest.Lists.List(h.ctx, false); len(lists) != 6 {
				t.Fatalf("%d lists: a refused first save left its owned lists behind", len(lists))
			}
		})
	}

	// "on" with a list that is not ready is the safety check: 409, and only on.
	_, err = h.put(g.ID, PutInput{State: domain.DestStateOn, ListIDs: []int64{remote.ID}, BaseHosts: hosts()})
	var nr *destlist.ListNotReadyError
	if !errors.As(err, &nr) || !slices.Equal(nr.ListIDs, []int64{remote.ID}) {
		t.Fatalf("on with a list that is not ready: %v", err)
	}
	if _, err := h.put(g.ID, PutInput{State: domain.DestStateReport, ListIDs: []int64{remote.ID}, BaseHosts: hosts()}); err != nil {
		t.Fatalf("report with a list that is not ready is allowed: %v", err)
	}
	if _, err := h.put(9999, PutInput{State: domain.DestStateOff}); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("missing group = %v", err)
	}
}

func TestPut_ExistingRowNeedsItsUpdatedAtAndAStaleOneIsAConflict(t *testing.T) {
	h := newHarness(t)
	g := h.group("Guests", 4)
	l := h.list("news", "DOMAIN-SUFFIX,news.example")
	v, err := h.put(g.ID, PutInput{State: domain.DestStateReport, ListIDs: []int64{l.ID}, BaseHosts: hosts()})
	if err != nil {
		t.Fatal(err)
	}
	var verr *destpolicy.ValidationError
	if _, err := h.put(g.ID, PutInput{State: domain.DestStateOn, ListIDs: []int64{l.ID}}); !errors.As(err, &verr) || verr.Field != "updated_at" {
		t.Fatalf("no updated_at on an existing row: %v", err)
	}
	if _, err := h.put(g.ID, PutInput{State: domain.DestStateOn, ListIDs: []int64{l.ID}, BaseHosts: hosts("x.example"), UpdatedAt: new(time.Time)}); !errors.As(err, &verr) || verr.Field != "base_hosts" {
		t.Fatalf("base_hosts on an existing row: %v: its base list is edited through its entries", err)
	}
	// Somebody else saves first.
	if _, err := h.put(g.ID, h.edit(v, domain.DestStateOn)); err != nil {
		t.Fatal(err)
	}
	gen := h.generation()
	_, err = h.put(g.ID, h.edit(v, domain.DestStateOff))
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("a stale write = %v, want a conflict", err)
	}
	if h.generation() != gen {
		t.Fatal("a stale write advanced the generation")
	}
}

func TestPut_OverTheLimitIsRefusedAndLeavesNothing(t *testing.T) {
	h := newHarness(t)
	g := h.group("Guests", 4)
	l := h.list("news", "DOMAIN-SUFFIX,news.example")
	h.budget.err = &destpolicy.ValidationError{Code: destpolicy.CodePolicyOverLimit, Field: "subjects", Kind: "subjects", Used: 60000, Limit: 50000}
	gen := h.generation()
	_, err := h.put(g.ID, PutInput{State: domain.DestStateOn, ListIDs: []int64{l.ID}, BaseHosts: hosts("doh.example")})
	var v *destpolicy.ValidationError
	if !errors.As(err, &v) || v.Code != destpolicy.CodePolicyOverLimit || v.Kind != "subjects" {
		t.Fatalf("err = %v", err)
	}
	if lists, _ := h.dest.Lists.List(h.ctx, false); len(lists) != 1 || h.generation() != gen || len(h.ev.all()) != 0 {
		t.Fatalf("lists %d generation %d events %v: a refused save leaves no owned list and tells nobody", len(lists), h.generation(), h.ev.all())
	}
}

// ---- preview ----

func TestPreview_WhatASaveWouldDoWithoutSavingIt(t *testing.T) {
	h := newHarness(t)
	g := h.group("Guests", 6)
	remote := h.remoteList("feed")
	gen := h.generation()
	p, err := h.svc.Preview(h.ctx, g.ID, PreviewInput{State: domain.DestStateOn, ListIDs: []int64{remote.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Members != 6 || p.TotalNodes != 3 || p.EligibleNodes != 1 {
		t.Fatalf("members %d nodes %d/%d", p.Members, p.EligibleNodes, p.TotalNodes)
	}
	want := []Ineligible{{PanelID: 2, Name: "osaka", Reason: "capability"}, {PanelID: 3, Name: "legacy", Reason: "xui"}}
	if !slices.Equal(p.Ineligible, want) || !slices.Equal(p.NodesLost, want) {
		t.Fatalf("ineligible %+v lost %+v: the group has no allowlist now, so every one of them is a loss", p.Ineligible, p.NodesLost)
	}
	if !slices.Equal(p.NotReadyLists, []ListRef{{ID: remote.ID, Name: "feed"}}) {
		t.Fatalf("not ready = %+v", p.NotReadyLists)
	}
	if !slices.Equal(p.BaseHosts, []string{"dns.alidns.com", "doh.example"}) || p.BaseHostsSource != "template" {
		t.Fatalf("base hosts %v from %q", p.BaseHosts, p.BaseHostsSource)
	}
	if p.WouldDeny != nil {
		t.Fatalf("would deny = %+v, want null: nothing was recorded", p.WouldDeny)
	}
	// Only panel 1 is a native node that records hits.
	if p.NonRecordingNodes != 0 {
		t.Fatalf("non-recording = %d", p.NonRecordingNodes)
	}
	if h.generation() != gen || len(h.ev.all()) != 0 {
		t.Fatal("a preview wrote or told somebody")
	}
}

func TestPreview_NodesLostAreOnlyThoseTheGroupHasNow(t *testing.T) {
	h := newHarness(t)
	g := h.group("Guests", 6)
	l := h.list("news", "DOMAIN-SUFFIX,news.example")
	if _, err := h.put(g.ID, PutInput{State: domain.DestStateReport, ListIDs: []int64{l.ID}, BaseHosts: hosts()}); err != nil {
		t.Fatal(err)
	}
	// Already in force, panels 2 and 3 are not the group's now: nothing is lost by saving "on".
	p, _ := h.svc.Preview(h.ctx, g.ID, PreviewInput{State: domain.DestStateOn, ListIDs: []int64{l.ID}})
	if len(p.Ineligible) != 2 || len(p.NodesLost) != 0 {
		t.Fatalf("ineligible %+v lost %+v", p.Ineligible, p.NodesLost)
	}
	// And a preview of "off" loses nothing and limits nothing.
	p, _ = h.svc.Preview(h.ctx, g.ID, PreviewInput{State: domain.DestStateOff})
	if len(p.Ineligible) != 0 || len(p.NodesLost) != 0 || p.EligibleNodes != p.TotalNodes {
		t.Fatalf("off: %+v", p)
	}
}

func TestPreview_NodesThatEnforceButDoNotRecord(t *testing.T) {
	h := newHarness(t)
	g := h.group("Guests", 6)
	h.elig.reasons = map[int64]string{3: "xui"}
	h.agents[2].ObservedCapabilities = h.agents[1].ObservedCapabilities // panel 2 can enforce, and can record
	h.panels[2].AuditCollect = domain.AuditCollectOff                   // ... but its record setting is off
	p, err := h.svc.Preview(h.ctx, g.ID, PreviewInput{State: domain.DestStateReport})
	if err != nil || p.EligibleNodes != 2 || p.NonRecordingNodes != 1 {
		t.Fatalf("eligible %d non-recording %d, %v", p.EligibleNodes, p.NonRecordingNodes, err)
	}
	// A sing-box node enforces but records nothing (D2: still eligible).
	h.panels[2].AuditCollect = domain.AuditCollectHits
	h.agents[2].DesiredCoreEngine, h.agents[2].ObservedCoreEngine = domain.NodeCoreSingBox, domain.NodeCoreSingBox
	if p, _ = h.svc.Preview(h.ctx, g.ID, PreviewInput{State: domain.DestStateReport}); p.NonRecordingNodes != 1 || p.EligibleNodes != 2 {
		t.Fatalf("sing-box: eligible %d non-recording %d", p.EligibleNodes, p.NonRecordingNodes)
	}
}

func TestPreview_NoTemplateSuggestionIsSaidSo(t *testing.T) {
	h := newHarness(t)
	g := h.group("Guests", 6)
	h.hostsErr = errors.New("templates unreadable")
	p, err := h.svc.Preview(h.ctx, g.ID, PreviewInput{State: domain.DestStateReport})
	if err != nil || p.BaseHostsSource != "none" || p.BaseHosts == nil || len(p.BaseHosts) != 0 {
		t.Fatalf("base hosts = %v from %q, %v: an unreadable template is no suggestion, and the rest of the preview stands", p.BaseHosts, p.BaseHostsSource, err)
	}
	if _, err := h.svc.Preview(h.ctx, g.ID, PreviewInput{State: "maybe"}); err == nil {
		t.Fatal("an unknown state was previewed")
	}
	if _, err := h.svc.Preview(h.ctx, 9999, PreviewInput{State: domain.DestStateOn}); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("missing group = %v", err)
	}
}

// ---- the report ----

func TestReport_FoldsTheTrialRowsBySite(t *testing.T) {
	h := newHarness(t)
	g := h.group("Guests", 6)
	l := h.list("news", "DOMAIN-SUFFIX,news.example")
	if _, err := h.put(g.ID, PutInput{State: domain.DestStateReport, ListIDs: []int64{l.ID}, BaseHosts: hosts("doh.example")}); err != nil {
		t.Fatal(err)
	}
	al, _ := h.dest.GroupAllowlists.Get(h.ctx, g.ID)
	if _, _, err := h.lists.AddEntries(h.ctx, al.ExtraListID, []string{"DOMAIN-SUFFIX,cdn.example"}, nil); err != nil {
		t.Fatal(err)
	}
	src := "g" + itoa(g.ID)
	h.hit(0, src, "observe", "cdn.example", 10, 0)
	h.hit(1, src, "observe", "cdn.example", 5, 0)
	h.hit(0, src, "observe", "api.example", 40, 0)
	h.hit(0, src, "observe", "(ip)", 3, 0)
	h.hit(0, src, "observe", "doh.example", 2, 0) // the base list allows it: added
	h.hit(0, "g999", "observe", "other.example", 70, 0)
	h.hit(0, src, "observe", "user.example", 70, 7) // an account's row is not a trial row
	h.drop(0, domain.DestDropNodeTrial, 11)
	h.drop(0, domain.DestDropQueueFull, 99)

	r, err := h.svc.Report(h.ctx, g.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != "report" || r.EffectiveDays != 7 || r.SinceAt == nil || r.DroppedInRange != 11 {
		t.Fatalf("report = %+v", r)
	}
	var got []string
	for _, it := range r.Items {
		got = append(got, it.Site+":"+itoa(it.Count)+":"+map[bool]string{true: "added", false: "-"}[it.Added])
	}
	want := []string{"api.example:40:-", "cdn.example:15:added", "(ip):3:-", "doh.example:2:added"}
	if !slices.Equal(got, want) {
		t.Fatalf("items = %v, want %v: by count, folded over hours, the added ones marked", got, want)
	}
	if r.Items[1].LastAt == 0 {
		t.Fatal("last_at is missing")
	}
	// Days are clamped to what is kept; the figure says what was used.
	if r, _ := h.svc.Report(h.ctx, g.ID, 90); r.EffectiveDays != 7 {
		t.Fatalf("90 days -> %d, want the 7 kept", r.EffectiveDays)
	}
	if r, _ := h.svc.Report(h.ctx, g.ID, 1); r.EffectiveDays != 1 || len(r.Items) != 4 {
		t.Fatalf("1 day -> %d days %d items", r.EffectiveDays, len(r.Items))
	}
	if _, err := h.svc.Report(h.ctx, 9999, 0); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("missing group = %v", err)
	}
	// A group that never had an allowlist reports nothing, and says it is off.
	plain := h.group("Plain", 1)
	if r, err := h.svc.Report(h.ctx, plain.ID, 0); err != nil || r.State != "off" || r.SinceAt != nil || len(r.Items) != 0 || r.Items == nil {
		t.Fatalf("plain report = %+v, %v", r, err)
	}
}

// ---- exceptions ----

func TestAddException_AppendsToTheGroupsExtraList(t *testing.T) {
	h := newHarness(t)
	g := h.group("Guests", 6)
	l := h.list("news", "DOMAIN-SUFFIX,news.example")
	if _, err := h.put(g.ID, PutInput{State: domain.DestStateReport, ListIDs: []int64{l.ID}, BaseHosts: hosts()}); err != nil {
		t.Fatal(err)
	}
	al, _ := h.dest.GroupAllowlists.Get(h.ctx, g.ID)
	gen := h.generation()
	res, err := h.svc.AddException(h.ctx, g.ID, "https://CDN.Example/x", "site")
	if err != nil || res.Entry != "DOMAIN-SUFFIX,cdn.example" || res.GroupID != g.ID || res.ListID != al.ExtraListID {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if h.generation() != gen+1 {
		t.Fatalf("generation %d -> %d: one change is one advance", gen, h.generation())
	}
	if extra, _ := h.dest.Lists.GetByID(h.ctx, al.ExtraListID); string(extra.Entries) != "domain:cdn.example" {
		t.Fatalf("extra entries = %q", extra.Entries)
	}
	if _, err := h.svc.AddException(h.ctx, g.ID, "api.example", "host"); err != nil {
		t.Fatal(err)
	}
	var v *destpolicy.ValidationError
	if _, err := h.svc.AddException(h.ctx, g.ID, "not a host", "site"); !errors.As(err, &v) || v.Code != destpolicy.CodeExceptionInvalid {
		t.Fatalf("a bad target = %v", err)
	}
	if _, err := h.svc.AddException(h.ctx, g.ID, "a.example", "wild"); !errors.As(err, &v) || v.Field != "match" {
		t.Fatalf("a bad match = %v", err)
	}
	plain := h.group("Plain", 1)
	if _, err := h.svc.AddException(h.ctx, plain.ID, "a.example", "site"); !errors.As(err, &v) || v.Field != "group_id" {
		t.Fatalf("a group with no allowlist = %v", err)
	}
	if _, err := h.svc.AddException(h.ctx, 9999, "a.example", "site"); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("missing group = %v", err)
	}
}

// ---- the policy list's view of the groups in force ----

func TestSummaries_TheGroupsInForceAndForHowLong(t *testing.T) {
	h := newHarness(t)
	a, b := h.group("Guests", 1), h.group("Staff", 1)
	h.group("Plain", 1)
	l := h.list("news", "DOMAIN-SUFFIX,news.example")
	if _, err := h.put(b.ID, PutInput{State: domain.DestStateOn, ListIDs: []int64{l.ID}, BaseHosts: hosts()}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.put(a.ID, PutInput{State: domain.DestStateReport, ListIDs: []int64{l.ID}, BaseHosts: hosts()}); err != nil {
		t.Fatal(err)
	}
	c := h.group("Off", 1)
	if _, err := h.put(c.ID, PutInput{State: domain.DestStateReport, ListIDs: []int64{l.ID}, BaseHosts: hosts()}); err != nil {
		t.Fatal(err)
	}
	cv, _ := h.svc.Get(h.ctx, c.ID)
	if _, err := h.put(c.ID, h.edit(cv.View, domain.DestStateOff)); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(50 * time.Hour)
	h.rebuild()
	got, err := h.svc.Summaries(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The harness clock is fixed in 2026, the rows were stamped by the real clock;
	// what is asserted is who is in force, in id order, and the days is not negative.
	if len(got) != 2 || got[0].GroupID != a.ID || got[0].State != "report" || got[1].GroupID != b.ID || got[1].State != "on" || got[0].Name != "Guests" {
		t.Fatalf("summaries = %+v: off groups and groups without a row are not in force", got)
	}
	for _, s := range got {
		if s.StateDays < 0 {
			t.Fatalf("state_days = %d", s.StateDays)
		}
	}
}

// A list an allowlist names is probed like any list that lets traffic through: an
// entry that would allow all of a public suffix, or nearly every host, is refused
// (dest_list_too_broad), whatever wrote it into the list.
func TestPut_RefusesAListWithAnEntryThatAllowsTooMuch(t *testing.T) {
	h := newHarness(t)
	g := h.group("Guests", 4)
	wide := &domain.DestList{
		Name: "wide", Kind: domain.DestListCustom, Side: domain.DestSideDest, Entries: []byte("domain:com\ndomain:example.com"),
		EntryCount: 2, ContentSHA256: strings.Repeat("c", 64),
	}
	if err := h.dest.Lists.Create(h.ctx, wide); err != nil {
		t.Fatal(err)
	}
	gen := h.generation()
	for _, state := range []domain.DestState{domain.DestStateReport, domain.DestStateOn} {
		_, err := h.put(g.ID, PutInput{State: state, ListIDs: []int64{wide.ID}, BaseHosts: hosts()})
		var broad *destlist.BroadEntryError
		if !errors.As(err, &broad) || broad.Entry != "domain:com" || broad.Reason != destlist.ReasonBroadSuffix {
			t.Fatalf("%s: err = %v, want the broad entry named", state, err)
		}
	}
	if h.generation() != gen {
		t.Fatal("a refused save advanced the generation")
	}
	// An allowlist that is off holds its lists dormant: they are probed when it is switched on.
	good := h.list("good", "DOMAIN-SUFFIX,news.example")
	v, err := h.put(g.ID, PutInput{State: domain.DestStateReport, ListIDs: []int64{good.ID}, BaseHosts: hosts()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.put(g.ID, h.editLists(v, domain.DestStateOff, wide.ID)); err != nil {
		t.Fatalf("an off group was probed: %v", err)
	}
}

// editLists is the PUT a page sends to change the state and the lists of what it read.
func (h *harness) editLists(v *View, to domain.DestState, lists ...int64) PutInput {
	in := h.edit(v, to)
	in.ListIDs = lists
	return in
}
