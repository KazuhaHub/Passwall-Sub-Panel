package http

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The hit-record API, through the assembled router: GET /dest/hits, and the
// hit fields the policy list, the account view and the status gained.

func hitHour(ago time.Duration) int64 { return time.Now().Add(-ago).Truncate(time.Hour).UnixMilli() }

func (f *destFixture) seedHits(rows ...domain.DestHit) {
	f.t.Helper()
	if _, err := f.dest.Hits.StoreBatch(context.Background(), "agt_x", fmt.Sprintf("%032x", time.Now().UnixNano()), time.Now(), rows); err != nil {
		f.t.Fatal(err)
	}
}

func hitOf(ago time.Duration, panel, user int64, source, action, dest string, count int64) domain.DestHit {
	h := hitHour(ago)
	return domain.DestHit{HourMS: h, PanelID: panel, UserID: user, Source: source, Action: action, Dest: dest, Port: 443, Count: count, FirstMS: h + 1, LastMS: h + 2}
}

func (f *destFixture) policyID(name, action, state string) int64 {
	f.t.Helper()
	out := f.expect("POST", "/policies", fmt.Sprintf(`{"name":%q,"action":%q,"state":%q,"inline":{"ports":"%d"}}`, name, action, state, 20+len(name)), 201, "")
	return num(out["policy"].(map[string]any)["id"])
}

func TestDestHitsAPI(t *testing.T) {
	f := newDestFixture(t, true)
	block := f.policyID("blocker", "block", "on")
	watch := f.policyID("watcher", "block", "report")
	pb, pw := "p"+itoa(block), "p"+itoa(watch)
	native := f.nativeNode
	f.seedHits(
		hitOf(time.Hour, 1, 2, pb, "block", "bad.example", 30),
		hitOf(time.Hour, 1, 2, pw, "observe", "wb.example", 5),
		hitOf(2*time.Hour, 1, 1, pb, "block", "other.example", 7),
		hitOf(time.Hour, 1, 0, "g3", "observe", "trial.example", 100),
		hitOf(3*time.Hour, 1, 2, "p9999", "block", "gone.example", 2), // a deleted policy
		hitOf(time.Hour, 1, 2, "s"+itoa(native), "observe", "src.example", 1),
	)
	_ = f.dest.Hits.AddDrops(context.Background(), hitHour(time.Hour), 1, domain.DestDropOverBudget, 6)

	all := f.expect("GET", "/hits", "", 200, "")
	items := all["items"].([]any)
	if num(all["total"]) != 5 || len(items) != 5 {
		t.Fatalf("total %v items %d: the allowlist trial row is left out unless asked for", all["total"], len(items))
	}
	sum := all["summary"].(map[string]any)
	if num(sum["block"]) != 39 || num(sum["observe"]) != 6 || num(sum["deny"]) != 0 || num(sum["users"]) != 2 {
		t.Fatalf("summary = %v", sum)
	}
	if num(all["dropped_in_range"]) != 6 {
		t.Fatalf("dropped_in_range = %v", all["dropped_in_range"])
	}
	names := map[string]any{}
	for _, s := range all["sources"].([]any) {
		m := s.(map[string]any)
		names[m["source"].(string)] = m["name"]
	}
	if names[pb] != "blocker" || names[pw] != "watcher" || names["p9999"] != nil {
		t.Fatalf("source names = %v: a deleted policy has a null name", names)
	}
	first := items[0].(map[string]any)
	if first["source_name"] == nil || first["action"] == nil || first["dest"] == nil || first["upn"] == nil {
		t.Fatalf("item = %v", first)
	}
	// There is no source address anywhere in a row.
	for k := range first {
		switch k {
		case "ip", "source_ip", "client_ip", "addr":
			t.Fatalf("a hit row carries %s", k)
		}
	}

	// Report-only rows are the would-have-blocked ones.
	obs := f.expect("GET", "/hits?action=observe", "", 200, "")
	if num(obs["total"]) != 2 {
		t.Fatalf("observe total = %v", obs["total"])
	}
	if one := f.expect("GET", "/hits?user_id=1", "", 200, ""); num(one["total"]) != 1 {
		t.Fatalf("user filter total = %v", one["total"])
	}
	if one := f.expect("GET", "/hits?source="+pw, "", 200, ""); num(one["total"]) != 1 {
		t.Fatalf("source filter total = %v", one["total"])
	}
	if one := f.expect("GET", "/hits?source_kind=source", "", 200, ""); num(one["total"]) != 1 {
		t.Fatalf("source_kind=source total = %v", one["total"])
	}
	if tr := f.expect("GET", "/hits?include_trial=1", "", 200, ""); num(tr["total"]) != 6 {
		t.Fatalf("include_trial total = %v", tr["total"])
	}
	if q := f.expect("GET", "/hits?q=bad.exa", "", 200, ""); num(q["total"]) != 1 {
		t.Fatalf("q total = %v", q["total"])
	}
	by := f.expect("GET", "/hits?group_by=policy", "", 200, "")
	if rows := by["items"].([]any); len(rows) == 0 || rows[0].(map[string]any)["source"] != pb {
		t.Fatalf("group_by policy = %v", by["items"])
	}
	paged := f.expect("GET", "/hits?page_size=2&page=2", "", 200, "")
	if len(paged["items"].([]any)) != 2 || num(paged["total"]) != 5 {
		t.Fatalf("page 2 = %v", paged)
	}

	// Refusals.
	f.expect("GET", "/hits?group_by=dest", "", 400, "dest_hits_invalid")
	f.expect("GET", "/hits?action=allow", "", 400, "dest_hits_invalid")
	f.expect("GET", "/hits?since=abc", "", 400, "dest_hits_invalid")
	f.expect("GET", "/hits?page=9223372036854775807", "", 400, "dest_hits_invalid")
	long := fmt.Sprintf("/hits?since=%d&until=%d", time.Now().Add(-40*24*time.Hour).UnixMilli(), time.Now().UnixMilli())
	f.expect("GET", long, "", 400, "dest_hits_range")
}

// A policy is shown with how many hits it has had in the window; a report-only
// one with how many connections it WOULD have blocked. Null where no node is
// recording, so a policy nobody watches does not read "0".
func TestDestPoliciesCarryTheirHits(t *testing.T) {
	f := newDestFixture(t, true)
	block := f.policyID("blocker", "block", "on")
	watch := f.policyID("watcher", "block", "report")
	allow := f.policyID("ok-allow", "allow", "on")
	f.seedHits(
		hitOf(time.Hour, 1, 2, "p"+itoa(block), "block", "bad.example", 30),
		hitOf(time.Hour, 1, 2, "p"+itoa(watch), "observe", "wb.example", 5),
	)
	find := func(out map[string]any, id int64) map[string]any {
		for _, p := range out["block"].([]any) {
			if m := p.(map[string]any); num(m["id"]) == id {
				return m
			}
		}
		t.Fatalf("policy %d not listed", id)
		return nil
	}
	out := f.expect("GET", "/policies", "", 200, "")
	if b := find(out, block); b["hits_recent"] != nil || b["would_block_recent"] != nil {
		t.Fatalf("no node is recording, so the counts are null: %v", b)
	}
	if num(out["hit_window_days"]) != 7 {
		t.Fatalf("hit_window_days = %v", out["hit_window_days"])
	}

	// A node that records.
	ctx := context.Background()
	agent := &domain.NodeAgent{AgentID: "agt_rec", PanelID: f.nativePanel(), Epoch: 1, CredentialSHA256: fmt.Sprintf("%064x", 7), DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: "26.6.27"}
	if err := f.repos.NodeAgent.Create(ctx, agent); err != nil {
		t.Fatal(err)
	}
	_ = f.repos.NodeAgent.UpdateCoreObservation(ctx, "agt_rec", domain.NodeCoreXray)
	_ = f.repos.NodeAgent.UpdateProtocolObservation(ctx, "agt_rec", 1, []string{"policy.destination.v1", "audit.hits.v1"}, time.Now())
	d1 := "d1"
	state, hits := "applied", "hits"
	if err := f.dest.Agents.Patch(ctx, "agt_rec", domain.DestAgentPolicyPatch{MintedSHA256: &d1, ReportedSHA256: &d1, ReportedState: &state, CollectEffective: &hits}); err != nil {
		t.Fatal(err)
	}
	out = f.expect("GET", "/policies", "", 200, "")
	b, w := find(out, block), find(out, watch)
	if num(b["hits_recent"]) != 30 || b["would_block_recent"] != nil || b["last_hit_at"] == nil {
		t.Fatalf("block policy = %v", b)
	}
	if num(w["would_block_recent"]) != 5 || w["hits_recent"] == nil {
		t.Fatalf("report policy = %v: would_block_recent counts its observe rows", w)
	}
	// An allow policy records nothing, so its counts are null, never a "0" that
	// reads as "nobody is affected".
	for _, p := range out["allow"].([]any) {
		if m := p.(map[string]any); num(m["id"]) == allow {
			if m["hits_recent"] != nil || m["would_block_recent"] != nil || m["last_hit_at"] != nil {
				t.Fatalf("allow policy = %v: hits_recent must be null", m)
			}
			return
		}
	}
	t.Fatalf("allow policy %d not listed", allow)
}

func (f *destFixture) nativePanel() int64 {
	f.t.Helper()
	n, err := f.repos.Node.GetByID(context.Background(), f.nativeNode)
	if err != nil {
		f.t.Fatal(err)
	}
	return n.PanelID
}

func TestDestUserCarriesRecentHits(t *testing.T) {
	f := newDestFixture(t, true)
	block := f.policyID("blocker", "block", "on")
	watch := f.policyID("watcher", "block", "report")
	f.seedHits(
		hitOf(time.Hour, 1, 2, "p"+itoa(block), "block", "bad.example", 30),
		hitOf(2*time.Hour, 1, 2, "p"+itoa(block), "block", "worse.example", 40),
		hitOf(time.Hour, 1, 2, "p"+itoa(watch), "observe", "wb.example", 5),
		hitOf(time.Hour, 1, 1, "p"+itoa(block), "block", "someone-else.example", 99),
	)
	out := f.expect("GET", "/users/2", "", 200, "")
	if out["hits_available"] != false {
		t.Fatalf("hits_available = %v: no node of the account records", out["hits_available"])
	}
	rh := out["recent_hits"].(map[string]any)
	if num(rh["days"]) != 7 {
		t.Fatalf("days = %v", rh["days"])
	}
	items := rh["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v", items)
	}
	top := items[0].(map[string]any)
	if top["source"] != "p"+itoa(block) || top["action"] != "block" || num(top["count"]) != 70 || top["source_name"] != "blocker" {
		t.Fatalf("top = %v", top)
	}
	if d := top["top_dests"].([]any); len(d) != 2 || d[0] != "worse.example" {
		t.Fatalf("top_dests = %v: most-hit first", d)
	}
	if items[1].(map[string]any)["action"] != "observe" {
		t.Fatalf("second = %v", items[1])
	}
}

// summary honours the exact source and still ignores action and source_kind:
// the convert dialog reads the accounts one policy hit from summary.users.
func TestDestHitsSummaryHonoursTheSourceFilter(t *testing.T) {
	f := newDestFixture(t, true)
	a := f.policyID("alpha", "block", "report")
	b := f.policyID("bravo", "block", "on")
	pa, pb := "p"+itoa(a), "p"+itoa(b)
	f.seedHits(
		hitOf(time.Hour, 1, 11, pa, "observe", "x.example", 5),
		hitOf(time.Hour, 1, 12, pa, "observe", "y.example", 3),
		hitOf(time.Hour, 1, 13, pb, "block", "z.example", 40),
	)
	sum := func(q string) map[string]any {
		return f.expect("GET", "/hits?"+q, "", 200, "")["summary"].(map[string]any)
	}
	if s := sum(""); num(s["users"]) != 3 || num(s["observe"]) != 8 || num(s["block"]) != 40 {
		t.Fatalf("whole selection = %v", s)
	}
	if s := sum("source=" + pa + "&group_by=site"); num(s["users"]) != 2 || num(s["observe"]) != 8 || num(s["block"]) != 0 {
		t.Fatalf("summary for %s = %v, want its 2 accounts and 8 would-blocks", pa, s)
	}
	if s := sum("source=" + pb); num(s["users"]) != 1 || num(s["block"]) != 40 {
		t.Fatalf("summary for %s = %v", pb, s)
	}
	// Action and source_kind still do not narrow it.
	if s := sum("action=block&source_kind=policy"); num(s["users"]) != 3 || num(s["observe"]) != 8 {
		t.Fatalf("summary with action/source_kind = %v, want the whole selection", s)
	}
}
