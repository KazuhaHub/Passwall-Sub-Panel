package http

import (
	"context"
	"encoding/json"
	stdhttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
)

// The allowlist group API (/api/admin/dest/groups...) through the assembled
// router. The group routes are the administrator's like the rest of the
// access-control API; the one thing staff can read about an allowlist is a
// group's state, on the groups list they already read.

func (f *destFixture) group(name string) *domain.Group {
	f.t.Helper()
	g := &domain.Group{Slug: strings.ToLower(name), Name: name, TagFilter: domain.TagFilter{All: true}}
	if err := f.repos.Group.Create(context.Background(), g); err != nil {
		f.t.Fatal(err)
	}
	return g
}

func (f *destFixture) customList(name, text string) int64 {
	f.t.Helper()
	l := f.expect("POST", "/lists", `{"name":"`+name+`","kind":"custom","text":"`+text+`"}`, 201, "")["list"].(map[string]any)
	return num(l["id"])
}

func gpath(g *domain.Group, tail string) string { return "/groups/" + itoa(g.ID) + tail }

func TestDestGroupsAPI(t *testing.T) {
	f := newDestFixture(t, true)
	g := f.group("Guests")
	lid := f.customList("news", "news.example.com")

	// Every group is listed, the one with no allowlist as off.
	list := f.expect("GET", "/groups", "", 200, "")["items"].([]any)
	if len(list) != 1 {
		t.Fatalf("groups = %v", list)
	}
	item := list[0].(map[string]any)
	if item["state"] != "off" || num(item["group_id"]) != g.ID || item["updated_at"] != nil || len(item["list_ids"].([]any)) != 0 || len(item["ineligible"].([]any)) != 1 && num(item["total_nodes"]) != 2 {
		t.Fatalf("item = %v", item)
	}

	f.expect("GET", "/groups/999", "", 404, "dest_group_not_found")
	f.expect("GET", "/groups/999/preview?state=on", "", 404, "dest_group_not_found")
	f.expect("GET", "/groups/999/report", "", 404, "dest_group_not_found")
	f.expect("PUT", "/groups/999", `{"state":"on","list_ids":[`+itoa(lid)+`],"base_hosts":[]}`, 404, "dest_group_not_found")
	f.expect("GET", "/groups/abc", "", 400, "dest_bad_id")

	gen0, _ := f.dest.State.Get(context.Background())
	// The refusals, each with its code and field.
	if out := f.expect("PUT", gpath(g, ""), `{"state":"maybe","list_ids":[],"base_hosts":[]}`, 400, "dest_policy_invalid"); out["field"] != "state" {
		t.Fatalf("body = %v", out)
	}
	if out := f.expect("PUT", gpath(g, ""), `{"state":"on","list_ids":[99999],"base_hosts":[]}`, 400, "dest_policy_invalid"); out["field"] != "list_ids" || out["bad"].([]any)[0] != "99999" {
		t.Fatalf("body = %v", out)
	}
	if out := f.expect("PUT", gpath(g, ""), `{"state":"on","list_ids":[`+itoa(lid)+`]}`, 400, "dest_policy_invalid"); out["field"] != "base_hosts" {
		t.Fatalf("body = %v", out)
	}
	f.expect("PUT", gpath(g, ""), `{"state":"on","list_ids":[`+itoa(lid)+`],"base_hosts":["https://x.example/dns"]}`, 400, "dest_policy_invalid")
	// Nothing was written by any of them.
	if st, _ := f.dest.State.Get(context.Background()); st.Generation != gen0.Generation {
		t.Fatalf("generation %d -> %d after refused saves", gen0.Generation, st.Generation)
	}

	// A list with an entry that would allow a whole public suffix is refused.
	wide := &domain.DestList{Name: "wide", Kind: domain.DestListCustom, Side: domain.DestSideDest, Entries: []byte("domain:com"), EntryCount: 1, ContentSHA256: strings.Repeat("c", 64)}
	if err := f.dest.Lists.Create(context.Background(), wide); err != nil {
		t.Fatal(err)
	}
	if out := f.expect("PUT", gpath(g, ""), `{"state":"report","list_ids":[`+itoa(wide.ID)+`],"base_hosts":[]}`, 400, "dest_list_too_broad"); out["entry"] != "domain:com" {
		t.Fatalf("body = %v", out)
	}

	// A list that has not arrived stops "on", and only on.
	feed := &domain.DestList{Name: "feed", Kind: domain.DestListRemote, Side: domain.DestSideDest, SourceURL: "https://lists.example.test/feed"}
	if err := f.dest.Lists.Create(context.Background(), feed); err != nil {
		t.Fatal(err)
	}
	nr := f.expect("PUT", gpath(g, ""), `{"state":"on","list_ids":[`+itoa(feed.ID)+`],"base_hosts":[]}`, 409, "dest_list_not_ready")
	if lists := nr["not_ready_lists"].([]any); len(lists) != 1 || num(lists[0].(map[string]any)["id"]) != feed.ID || lists[0].(map[string]any)["name"] != "feed" {
		t.Fatalf("not_ready_lists = %v", nr["not_ready_lists"])
	}

	// Off to on, directly: no report first.
	on := f.expect("PUT", gpath(g, ""), `{"state":"on","list_ids":[`+itoa(lid)+`],"base_hosts":["DoH.example.com"]}`, 200, "")
	if on["state"] != "on" || num(on["base_count"]) != 1 || num(on["base_list_id"]) == 0 || num(on["extra_list_id"]) == 0 || on["updated_at"] == nil {
		t.Fatalf("put = %v", on)
	}
	if got := f.resyncs.got(); len(got) != 1 || got[0] != g.ID {
		t.Fatalf("resyncs = %v: the write must tell the member sync", got)
	}
	if ineligible := on["ineligible"].([]any); len(ineligible) != 1 || ineligible[0].(map[string]any)["reason"] != "xui" || num(on["eligible_nodes"]) != 1 || num(on["total_nodes"]) != 2 {
		t.Fatalf("nodes = %v / %v", on["eligible_nodes"], on["ineligible"])
	}

	// The precondition.
	at := num(on["updated_at"])
	f.expect("PUT", gpath(g, ""), `{"state":"report","list_ids":[`+itoa(lid)+`]}`, 400, "dest_policy_invalid")
	f.expect("PUT", gpath(g, ""), `{"state":"report","list_ids":[`+itoa(lid)+`],"updated_at":`+itoa(at-1000)+`}`, 409, "dest_group_stale")
	back := f.expect("PUT", gpath(g, ""), `{"state":"report","list_ids":[`+itoa(lid)+`],"updated_at":`+itoa(at)+`}`, 200, "")
	if back["state"] != "report" {
		t.Fatalf("put = %v", back)
	}

	// The detail carries the readiness; the owned lists show up as such.
	detail := f.expect("GET", gpath(g, ""), "", 200, "")
	rd := detail["readiness"].(map[string]any)
	if rd["lists_ready"] != true || rd["base_nonempty"] != true || num(detail["group_id"]) != g.ID || rd["top_sites"] == nil || rd["not_ready_lists"] == nil {
		t.Fatalf("detail = %v", detail)
	}
	owned := f.expect("GET", "/lists/"+itoa(num(detail["base_list_id"])), "", 200, "")["list"].(map[string]any)
	if num(owned["owner_group_id"]) != g.ID {
		t.Fatalf("base list = %v", owned)
	}
	ub := owned["used_by"].([]any)
	if len(ub) != 1 || ub[0].(map[string]any)["kind"] != "group" || ub[0].(map[string]any)["name"] != "Guests" {
		t.Fatalf("used_by = %v", owned["used_by"])
	}

	// The preview changes nothing.
	gen, _ := f.dest.State.Get(context.Background())
	pv := f.expect("GET", gpath(g, "/preview?state=on&list_ids="+itoa(lid)), "", 200, "")
	if num(pv["total_nodes"]) != 2 || pv["base_hosts_source"] != "template" || pv["base_hosts"].([]any)[0] != "dns.alidns.com" || pv["nodes_lost"] == nil || pv["ineligible"] == nil {
		t.Fatalf("preview = %v", pv)
	}
	f.expect("GET", gpath(g, "/preview?state=maybe"), "", 400, "dest_policy_invalid")
	f.expect("GET", gpath(g, "/preview?state=on&list_ids=x"), "", 400, "dest_policy_invalid")
	if after, _ := f.dest.State.Get(context.Background()); after.Generation != gen.Generation {
		t.Fatal("a preview advanced the generation")
	}
}

// 256 rules is the limit of a policy: a group's rules count, and a save that
// would pass it is refused whole.
func TestDestGroupsAPIOverTheLimit(t *testing.T) {
	f := newDestFixture(t, true)
	g := f.group("Guests")
	lid := f.customList("news", "news.example.com")
	for i := 0; i < 255; i++ {
		f.expect("POST", "/policies", `{"name":"p`+itoa(int64(i))+`","action":"block","state":"on","inline":{"ports":"26"}}`, 201, "")
	}
	out := f.expect("PUT", gpath(g, ""), `{"state":"on","list_ids":[`+itoa(lid)+`],"base_hosts":[]}`, 400, "dest_policy_over_limit")
	if out["kind"] != "rules" || num(out["used"]) != 257 || num(out["limit"]) != 256 {
		t.Fatalf("body = %v", out)
	}
	if lists, _ := f.dest.Lists.List(context.Background(), false); len(lists) != 1 {
		t.Fatalf("%d lists: a refused first save left its own lists behind", len(lists))
	}
	if got := f.expect("GET", "/groups", "", 200, "")["items"].([]any)[0].(map[string]any); got["state"] != "off" {
		t.Fatalf("group = %v", got)
	}
}

// The report folds the group's report-only rows (user 0) by site.
func TestReportFoldsTrialRowsBySite(t *testing.T) {
	f := newDestFixture(t, true)
	g := f.group("Guests")
	lid := f.customList("news", "news.example.com")
	f.expect("PUT", gpath(g, ""), `{"state":"report","list_ids":[`+itoa(lid)+`],"base_hosts":[]}`, 200, "")
	h := time.Now().UTC().Truncate(time.Hour).UnixMilli()
	src := "g" + itoa(g.ID)
	rows := []domain.DestHit{
		{HourMS: h, PanelID: f.nativePanel(), Source: src, Action: "observe", Dest: "cdn.example", Count: 10, FirstMS: h, LastMS: h + 5},
		{HourMS: h - 3600000, PanelID: f.nativePanel(), Source: src, Action: "observe", Dest: "cdn.example", Count: 5, FirstMS: h - 3600000, LastMS: h - 3600000 + 9},
		{HourMS: h, PanelID: f.nativePanel(), Source: src, Action: "observe", Dest: "api.example", Count: 40, FirstMS: h, LastMS: h + 7},
		{HourMS: h, PanelID: f.nativePanel(), UserID: 7, Source: src, Action: "observe", Dest: "not-a-trial-row.example", Port: 443, Count: 99, FirstMS: h, LastMS: h},
	}
	if _, err := f.dest.Hits.StoreBatch(context.Background(), "agt_x", "b1", time.Now(), rows); err != nil {
		t.Fatal(err)
	}
	if err := f.dest.Hits.AddDrops(context.Background(), h, f.nativePanel(), domain.DestDropNodeTrial, 12); err != nil {
		t.Fatal(err)
	}
	f.expect("POST", "/exceptions", `{"target":"cdn.example","match":"site","scope":"group","group_id":`+itoa(g.ID)+`}`, 200, "")

	r := f.expect("GET", gpath(g, "/report?days=3"), "", 200, "")
	items := r["items"].([]any)
	if r["state"] != "report" || num(r["effective_days"]) != 3 || num(r["dropped_in_range"]) != 12 || len(items) != 2 || r["since_at"] == nil {
		t.Fatalf("report = %v", r)
	}
	first, second := items[0].(map[string]any), items[1].(map[string]any)
	if first["site"] != "api.example" || num(first["count"]) != 40 || first["added"] != false ||
		second["site"] != "cdn.example" || num(second["count"]) != 15 || second["added"] != true || num(second["last_at"]) != h+5 {
		t.Fatalf("items = %v", items)
	}
	if got := f.expect("GET", gpath(g, "/report?days=900"), "", 200, ""); num(got["effective_days"]) != 7 {
		t.Fatalf("days are clamped to what is kept: %v", got["effective_days"])
	}
	// The group's list row says what it would have denied, and the status lists
	// the group among those in force.
	item := f.expect("GET", "/groups", "", 200, "")["items"].([]any)[0].(map[string]any)
	if w := item["would_deny_recent"].(map[string]any); num(w["sites"]) != 2 || num(w["count"]) != 55 || num(w["days"]) != 7 {
		t.Fatalf("would_deny_recent = %v", item["would_deny_recent"])
	}
	pol := f.expect("GET", "/policies", "", 200, "")["allowlist_groups"].([]any)
	if len(pol) != 1 || num(pol[0].(map[string]any)["group_id"]) != g.ID || pol[0].(map[string]any)["state"] != "report" || pol[0].(map[string]any)["name"] != "Guests" {
		t.Fatalf("policies.allowlist_groups = %v", pol)
	}
}

func TestDestGroupExceptionsAndTheirRefusals(t *testing.T) {
	f := newDestFixture(t, true)
	g, plain := f.group("Guests"), f.group("Plain")
	lid := f.customList("news", "news.example.com")
	f.expect("PUT", gpath(g, ""), `{"state":"report","list_ids":[`+itoa(lid)+`],"base_hosts":[]}`, 200, "")
	al, _ := f.dest.GroupAllowlists.Get(context.Background(), g.ID)

	res := f.expect("POST", "/exceptions", `{"target":"https://CDN.example/x","match":"site","scope":"group","group_id":`+itoa(g.ID)+`}`, 200, "")
	if res["entry"] != "DOMAIN-SUFFIX,cdn.example" || num(res["group_id"]) != g.ID || num(res["list_id"]) != al.ExtraListID || res["created"] != nil {
		t.Fatalf("result = %v", res)
	}
	extra := f.expect("GET", "/lists/"+itoa(al.ExtraListID), "", 200, "")["list"].(map[string]any)
	if num(extra["entry_count"]) != 1 {
		t.Fatalf("extra list = %v", extra)
	}
	f.expect("POST", "/exceptions", `{"target":"x.example","match":"site","scope":"group","group_id":`+itoa(plain.ID)+`}`, 400, "dest_policy_invalid")
	f.expect("POST", "/exceptions", `{"target":"x.example","match":"site","scope":"group","group_id":999}`, 404, "dest_group_not_found")
	f.expect("POST", "/exceptions", `{"target":"x.example","match":"site","scope":"group"}`, 400, "dest_policy_invalid")
	f.expect("POST", "/exceptions", `{"target":"x.example","match":"site","scope":"everyone"}`, 400, "dest_exception_invalid")
	f.expect("POST", "/exceptions", `{"target":"not a host","match":"site","scope":"group","group_id":`+itoa(g.ID)+`}`, 400, "dest_exception_invalid")
}

// A list an allowlist group holds is in use, in any state of the group.
func TestDestListsUsedByGroupsAreNotDeleted(t *testing.T) {
	f := newDestFixture(t, true)
	g := f.group("Guests")
	lid := f.customList("news", "news.example.com")
	f.expect("PUT", gpath(g, ""), `{"state":"report","list_ids":[`+itoa(lid)+`],"base_hosts":[]}`, 200, "")
	in := f.expect("DELETE", "/lists/"+itoa(lid), "", 409, "dest_list_in_use")
	ub := in["used_by"].([]any)
	if len(ub) != 1 || ub[0].(map[string]any)["kind"] != "group" || num(ub[0].(map[string]any)["id"]) != g.ID || ub[0].(map[string]any)["name"] != "Guests" {
		t.Fatalf("used_by = %v", in["used_by"])
	}
	got, _ := f.dest.GroupAllowlists.Get(context.Background(), g.ID)
	f.expect("GET", "/groups/"+itoa(g.ID), "", 200, "")
	if in := f.expect("DELETE", "/lists/"+itoa(got.BaseListID), "", 409, "dest_list_in_use"); in["used_by"] == nil {
		t.Fatalf("the group's own base list is in use too: %v", in)
	}
	// The listing says so as well.
	for _, it := range f.expect("GET", "/lists", "", 200, "")["items"].([]any) {
		m := it.(map[string]any)
		if num(m["id"]) == lid && len(m["used_by"].([]any)) != 1 {
			t.Fatalf("list = %v", m)
		}
	}
}

// What an account's view says of its group.
func TestDestUserViewCarriesTheGroupState(t *testing.T) {
	f := newDestFixture(t, true)
	g := f.group("Guests")
	lid := f.customList("news", "news.example.com")
	f.expect("PUT", gpath(g, ""), `{"state":"on","list_ids":[`+itoa(lid)+`],"base_hosts":[]}`, 200, "")
	f.users.users[2].GroupID = g.ID
	got := f.expect("GET", "/users/2", "", 200, "")["group"].(map[string]any)
	if num(got["id"]) != g.ID || got["name"] != "Guests" || got["state"] != "on" {
		t.Fatalf("group = %v", got)
	}
}

// The groups list is staff's (an operator picks a group for a new account), so
// it carries the one thing staff may know of an allowlist: its state. The lists,
// the nodes and the report are the administrator's.
func TestGroupsListStaffSeesStateOnly(t *testing.T) {
	f := newDestFixture(t, true)
	g := f.group("Guests")
	lid := f.customList("news", "secret-list-entry.example.com")
	f.expect("PUT", gpath(g, ""), `{"state":"report","list_ids":[`+itoa(lid)+`],"base_hosts":["secret-doh.example.com"]}`, 200, "")

	for _, tok := range []string{f.operatorToken, f.adminToken} {
		w := f.do("GET", "/api/admin/groups", tok, "")
		if w.Code != stdhttp.StatusOK {
			t.Fatalf("GET /api/admin/groups = %d: %s", w.Code, w.Body.String())
		}
		var out struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Items) != 1 {
			t.Fatalf("body = %s, %v", w.Body.String(), err)
		}
		if out.Items[0]["dest_state"] != "report" {
			t.Fatalf("dest_state = %v", out.Items[0]["dest_state"])
		}
		body := w.Body.String()
		for _, leak := range []string{"list_ids", "base_list_id", "secret-", "eligible", "would_deny"} {
			if strings.Contains(body, leak) {
				t.Fatalf("the groups list leaks %q: %s", leak, body)
			}
		}
	}
	// A group with no allowlist is off.
	f.group("Plain")
	var out struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(f.do("GET", "/api/admin/groups", f.operatorToken, "").Body.Bytes(), &out)
	states := map[string]any{}
	for _, it := range out.Items {
		states[it["name"].(string)] = it["dest_state"]
	}
	if states["Guests"] != "report" || states["Plain"] != "off" {
		t.Fatalf("states = %v", states)
	}
	for _, r := range []destRoute{{"GET", "/api/admin/dest/groups", ""}, {"GET", "/api/admin/dest/groups/" + itoa(g.ID), ""}, {"GET", "/api/admin/dest/groups/" + itoa(g.ID) + "/report", ""}} {
		if w := f.do(r.method, r.path, f.operatorToken, r.body); w.Code != stdhttp.StatusForbidden {
			t.Errorf("operator %s %s = %d, want 403", r.method, r.path, w.Code)
		}
	}
}

// Writes under /dest/groups are audited, and only an administrator reads the
// rows (the base hosts and the lists are in the body).
func TestDestGroupsAuditRowsAreAdminOnly(t *testing.T) {
	f := newDestFixture(t, true)
	g := f.group("Guests")
	lid := f.customList("news", "news.example.com")
	f.expect("PUT", gpath(g, ""), `{"state":"report","list_ids":[`+itoa(lid)+`],"base_hosts":["private-doh.example.com"]}`, 200, "")
	f.expect("GET", gpath(g, "/preview?state=on"), "", 200, "")

	read := func(token string) string {
		w := f.do("GET", "/api/admin/audit?page_size=100", token, "")
		return w.Body.String()
	}
	if body := read(f.adminToken); !strings.Contains(body, "/api/admin/dest/groups/") {
		t.Fatalf("the administrator does not see the group write in the audit log: %s", body)
	}
	if body := read(f.operatorToken); strings.Contains(body, "/api/admin/dest/") || strings.Contains(body, "private-doh") {
		t.Fatalf("an operator reads the allowlist writes: %s", body)
	}
}

// Deleting a group takes its allowlist, its own lists and its report rows' owner
// with it, and moves the destination generation.
func TestGroupDeleteCleansDestRows(t *testing.T) {
	f := newDestFixture(t, true)
	g := f.group("Guests")
	lid := f.customList("news", "news.example.com")
	f.expect("PUT", gpath(g, ""), `{"state":"on","list_ids":[`+itoa(lid)+`],"base_hosts":["doh.example.com"]}`, 200, "")
	al, _ := f.dest.GroupAllowlists.Get(context.Background(), g.ID)
	before, _ := f.dest.State.Get(context.Background())

	groups := group.New(f.repos.Group, f.repos.Node, f.repos.ScopeSettings)
	if err := groups.Delete(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{al.BaseListID, al.ExtraListID} {
		if _, err := f.dest.Lists.GetByID(context.Background(), id); err == nil {
			t.Fatalf("list %d the group owned is still there", id)
		}
	}
	if _, err := f.dest.Lists.GetByID(context.Background(), lid); err != nil {
		t.Fatalf("a shared list went with the group: %v", err)
	}
	if after, _ := f.dest.State.Get(context.Background()); after.Generation != before.Generation+1 {
		t.Fatalf("generation %d -> %d: one advance for the delete", before.Generation, after.Generation)
	}
	f.expect("GET", gpath(g, ""), "", 404, "dest_group_not_found")
	if items := f.expect("GET", "/groups", "", 200, "")["items"].([]any); len(items) != 0 {
		t.Fatalf("groups = %v", items)
	}
	// The list is free again.
	f.expect("DELETE", "/lists/"+itoa(lid), "", 200, "")
}
