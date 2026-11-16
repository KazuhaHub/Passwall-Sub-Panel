package http

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// PER-SITE USAGE, through the assembled router: POST /dest/users/:id/usage reads
// one account's usage. It is a POST so that the audit middleware writes a row for
// every read by itself, and the row is an administrator's (adminOnlyAuditTargets).

func (f *destFixture) seedUsage(rows ...domain.DestUsage) {
	f.t.Helper()
	if _, err := f.dest.Hits.StoreBatchWithUsage(context.Background(), "agt_x", fmt.Sprintf("%032x", time.Now().UnixNano()), time.Now(), nil, rows); err != nil {
		f.t.Fatal(err)
	}
}

func usageOf(ago time.Duration, panel, user int64, site string, count int64) domain.DestUsage {
	return domain.DestUsage{HourMS: hitHour(ago), PanelID: panel, UserID: user, Site: site, Count: count}
}

func TestDestUsageAPI(t *testing.T) {
	f := newDestFixture(t, true)
	var rows []domain.DestUsage
	for i := 0; i < 25; i++ {
		rows = append(rows, usageOf(time.Hour, f.nativePanelID, 2, fmt.Sprintf("s%02d.example", i), int64(100-i)))
	}
	rows = append(rows,
		usageOf(2*time.Hour, f.nativePanelID, 2, "s00.example", 10),
		usageOf(3*24*time.Hour, f.nativePanelID, 2, "older.example", 7),   // outside 24h, inside 7d
		usageOf(time.Hour, f.nativePanelID+50, 2, "elsewhere.example", 3), // another panel
		usageOf(time.Hour, f.nativePanelID, 1, "someone-else.example", 999),
	)
	f.seedUsage(rows...)
	_ = f.dest.Hits.AddDrops(context.Background(), hitHour(time.Hour), f.nativePanelID, domain.DestDropUsageOverBudget, 4)

	day := f.expect("POST", "/users/2/usage", `{"range":"24h"}`, 200, "")
	if num(day["effective_days"]) != 1 {
		t.Fatalf("effective_days = %v", day["effective_days"])
	}
	items := day["items"].([]any)
	if len(items) != 20 {
		t.Fatalf("items = %d, want the top 20", len(items))
	}
	first := items[0].(map[string]any)
	if first["site"] != "s00.example" || num(first["count"]) != 110 {
		t.Fatalf("first = %v: the two hours of the same site add", first)
	}
	others := day["others"].(map[string]any)
	// 26 sites in range (25 + elsewhere), 20 shown.
	if num(others["sites"]) != 6 {
		t.Fatalf("others = %v", others)
	}
	var shown int64
	for _, it := range items {
		shown += num(it.(map[string]any)["count"])
	}
	if num(day["total"]) != shown+num(others["count"]) || num(day["total"]) == 0 {
		t.Fatalf("total %v is not the shown %d plus the rest %v", day["total"], shown, others["count"])
	}
	if num(day["dropped_in_range"]) != 4 {
		t.Fatalf("dropped_in_range = %v", day["dropped_in_range"])
	}
	if _, ok := day["nodes"].([]any); !ok {
		t.Fatalf("nodes = %v", day["nodes"])
	}

	// A range is clamped to the retention (7 days by default); a panel narrows it.
	week := f.expect("POST", "/users/2/usage", `{"range":"365d"}`, 200, "")
	if num(week["effective_days"]) != 7 {
		t.Fatalf("effective_days = %v, want the retention", week["effective_days"])
	}
	if num(week["total"]) <= num(day["total"]) {
		t.Fatalf("the longer range did not widen the total: %v vs %v", week["total"], day["total"])
	}
	one := f.expect("POST", "/users/2/usage", fmt.Sprintf(`{"range":"24h","panel_id":%d}`, f.nativePanelID+50), 200, "")
	if its := one["items"].([]any); len(its) != 1 || its[0].(map[string]any)["site"] != "elsewhere.example" {
		t.Fatalf("panel filter = %v", one["items"])
	}
	empty := f.expect("POST", "/users/1/usage", `{"range":"24h","panel_id":99999}`, 200, "")
	if num(empty["total"]) != 0 || len(empty["items"].([]any)) != 0 {
		t.Fatalf("empty = %v", empty)
	}

	for _, body := range []string{`{}`, `{"range":"soon"}`, `{"range":"0d"}`, `{"range":"-3d"}`, `{"range":"7d","panel_id":-1}`} {
		f.expect("POST", "/users/2/usage", body, 400, "dest_usage_invalid")
	}
	f.expect("POST", "/users/999/usage", `{"range":"24h"}`, 404, "")

	// The answer is never stored by anything between the panel and the browser.
	if w := f.do("POST", "/api/admin/dest/users/2/usage", f.adminToken, `{"range":"24h"}`); w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", w.Header().Get("Cache-Control"))
	}

	// The account view says whether any node is recording usage for the account.
	view := f.expect("GET", "/users/2", "", 200, "")
	if view["usage_available"] != false {
		t.Fatalf("usage_available = %v with no node recording", view["usage_available"])
	}
	if nodes, ok := view["usage_nodes"].([]any); !ok || len(nodes) != 0 {
		t.Fatalf("usage_nodes = %v", view["usage_nodes"])
	}
}

// Every read is audited by the middleware, and only an administrator reads the
// row (which names the account and the range).
func TestDestUsageReadsAreAuditedForAdministratorsOnly(t *testing.T) {
	f := newDestFixture(t, true)
	f.expect("POST", "/users/2/usage", `{"range":"3d"}`, 200, "")
	read := func(token string) string {
		return f.do("GET", "/api/admin/audit?page_size=100", token, "").Body.String()
	}
	body := read(f.adminToken)
	if !strings.Contains(body, "/api/admin/dest/users/:id/usage") || !strings.Contains(body, "3d") {
		t.Fatalf("the read left no audit row: %s", body)
	}
	if body := read(f.operatorToken); strings.Contains(body, "/usage") {
		t.Fatalf("an operator reads the usage audit row: %s", body)
	}
	if w := f.do("POST", "/api/admin/dest/users/2/usage", f.operatorToken, `{"range":"24h"}`); w.Code != stdhttp.StatusForbidden {
		t.Fatalf("operator = %d, want 403", w.Code)
	}
}
