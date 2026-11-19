package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/audit"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/node"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

// The line-aware order API through the assembled router: real node service and
// SQLite store, an administrator and an operator. /nodes/order shares the
// /api/admin prefix between staffGroup and adminGroup, so only a request
// through the router shows which group the route landed in.

type countingNodeList struct {
	ports.NodeRepo
	lists atomic.Int32
}

func (r *countingNodeList) List(ctx context.Context) ([]*domain.Node, error) {
	r.lists.Add(1)
	return r.NodeRepo.List(ctx)
}

type countingSeparatorList struct {
	ports.SeparatorRepo
	lists atomic.Int32
}

func (r *countingSeparatorList) List(ctx context.Context) ([]*domain.SeparatorEntry, error) {
	r.lists.Add(1)
	return r.SeparatorRepo.List(ctx)
}

type countingPanelList struct {
	ports.XUIPanelRepo
	lists atomic.Int32
}

func (r *countingPanelList) List(ctx context.Context) ([]*domain.XUIPanel, error) {
	r.lists.Add(1)
	return r.XUIPanelRepo.List(ctx)
}

type orderFixture struct {
	t                         *testing.T
	repos                     ports.Repos
	nodes                     *countingNodeList
	seps                      *countingSeparatorList
	panels                    *countingPanelList
	invalidations             atomic.Int32
	adminToken, operatorToken string
	nodeA, nodeB              *domain.Node
	sep                       *domain.SeparatorEntry
	do                        func(method, target, token, body string) *httptest.ResponseRecorder
}

func newOrderFixture(t *testing.T) *orderFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	sqlstore.ConfigureSecretKey("test-only-node-order-key")
	t.Cleanup(func() { sqlstore.ConfigureSecretKey("") })
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "order.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s, e := db.DB(); e == nil {
			_ = s.Close()
		}
	})
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := sqlstore.NewRepos(db)
	f := &orderFixture{t: t, repos: repos}

	admin := &domain.User{ID: 1, UPN: "admin@example.test", Enabled: true, Role: domain.RoleAdmin}
	operator := &domain.User{ID: 2, UPN: "operator@example.test", Enabled: true, Role: domain.RoleOperator}
	users := routerReleaseUsers{users: map[int64]*domain.User{1: admin, 2: operator}}
	repos.User = users
	issuer := jwtutil.NewIssuer(strings.Repeat("test-only-key", 3), func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: "node-order-test"}
	})
	authSvc := auth.New(issuer)
	if f.adminToken, _, err = authSvc.IssueTokens(admin); err != nil {
		t.Fatal(err)
	}
	if f.operatorToken, _, err = authSvc.IssueTokens(operator); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	entry := &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: "gz-relay", URL: "https://gz.example.test"}
	land := &domain.XUIPanel{Kind: domain.PanelKind3XUI, Name: "tk-1", URL: "https://tk.example.test"}
	for _, p := range []*domain.XUIPanel{entry, land} {
		if err := repos.XUIPanel.Save(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	mk := func(inbound int, name string, order int, relays ...domain.RelayLine) *domain.Node {
		n := &domain.Node{PanelID: land.ID, InboundID: inbound, DisplayName: name, Region: "JP", ServerAddress: name + ".example.test",
			DesiredProtocol: "vless", DesiredPort: 443, Enabled: true, SortOrder: order, Relays: relays}
		if err := repos.Node.Create(ctx, n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	f.nodeA = mk(1, "Tokyo A", 10,
		domain.RelayLine{ID: "a-fwd", Name: "GZ forward", Address: "gz.example", Port: 20443, Enabled: true, EntryPanelID: entry.ID},
		domain.RelayLine{ID: "a-ext", Name: "External", Address: "ext.example", Enabled: true})
	f.nodeB = mk(2, "Osaka B", 20)
	f.sep = &domain.SeparatorEntry{DisplayName: "---- JP ----", Enabled: true, Mode: domain.SeparatorModeGlobal, SortOrder: 5}
	if err := repos.Separator.Create(ctx, f.sep); err != nil {
		t.Fatal(err)
	}

	f.nodes = &countingNodeList{NodeRepo: repos.Node}
	f.seps = &countingSeparatorList{SeparatorRepo: repos.Separator}
	f.panels = &countingPanelList{XUIPanelRepo: repos.XUIPanel}
	nodeSvc := node.New(f.nodes, f.seps, nil, nil, repos.SyncTask, repos.Group, users)
	nodeSvc.SetSubscriptionInvalidator(func() { f.invalidations.Add(1) })
	routed := repos
	routed.XUIPanel = f.panels
	router := NewRouter(Deps{
		Cfg:   &config.Config{ConfigDir: t.TempDir()},
		Repos: routed,
		Auth:  authSvc, User: user.New(users, nil, nil, nil, nil, nil, nil, nil),
		Group: group.New(repos.Group, repos.Node, repos.ScopeSettings),
		Audit: audit.New(repos.Audit),
		Node:  nodeSvc,
	})
	f.do = func(method, target, token, body string) *httptest.ResponseRecorder {
		t.Helper()
		var reqBody io.Reader
		if body != "" {
			reqBody = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, "https://panel.example"+target, reqBody)
		if reqBody != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	return f
}

func (f *orderFixture) get(token string) (int, map[string]any) {
	f.t.Helper()
	w := f.do("GET", "/api/admin/nodes/order", token, "")
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (f *orderFixture) put(token, body string) (int, map[string]any) {
	f.t.Helper()
	w := f.do("PUT", "/api/admin/nodes/order", token, body)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// rowKeys renders the rows as "sep:3", "node:1/direct", "node:1/a-fwd".
func rowKeys(t *testing.T, out map[string]any) []string {
	t.Helper()
	var keys []string
	for _, r := range out["rows"].([]any) {
		row := r.(map[string]any)
		if row["kind"] == "separator" {
			keys = append(keys, fmt.Sprintf("sep:%d", int(row["id"].(float64))))
		} else {
			keys = append(keys, fmt.Sprintf("node:%d/%s", int(row["node_id"].(float64)), row["line_id"]))
		}
	}
	return keys
}

func orderPutBody(version string, refs ...string) string {
	var items []string
	for _, r := range refs {
		var id, node int
		var line string
		if _, err := fmt.Sscanf(r, "sep:%d", &id); err == nil {
			items = append(items, fmt.Sprintf(`{"kind":"separator","id":%d}`, id))
		} else if _, err := fmt.Sscanf(r, "node:%d/%s", &node, &line); err == nil {
			items = append(items, fmt.Sprintf(`{"kind":"line","node_id":%d,"line_id":%q}`, node, line))
		}
	}
	return fmt.Sprintf(`{"base_version":%q,"items":[%s]}`, version, strings.Join(items, ","))
}

// Reading is staff work, writing the administrator's: an operator may look at
// the order and may not change it, and nothing is written for a refused caller.
func TestNodesOrderRoleGates(t *testing.T) {
	f := newOrderFixture(t)
	if code, _ := f.get(f.operatorToken); code != stdhttp.StatusOK {
		t.Fatalf("operator GET = %d, want 200", code)
	}
	if code, _ := f.get(""); code != stdhttp.StatusUnauthorized {
		t.Fatalf("anonymous GET = %d, want 401", code)
	}
	_, cur := f.get(f.adminToken)
	body := orderPutBody(cur["order_version"].(string), rowKeys(t, cur)...)
	if code, out := f.put(f.operatorToken, body); code != stdhttp.StatusForbidden {
		t.Fatalf("operator PUT = %d, want 403: %v", code, out)
	}
	if code, _ := f.put("", body); code != stdhttp.StatusUnauthorized {
		t.Fatalf("anonymous PUT = %d, want 401", code)
	}
	if f.invalidations.Load() != 0 {
		t.Fatal("a refused request wrote")
	}
}

func TestNodesOrderGetShape(t *testing.T) {
	f := newOrderFixture(t)
	code, out := f.get(f.adminToken)
	if code != stdhttp.StatusOK {
		t.Fatalf("GET = %d: %v", code, out)
	}
	want := []string{
		fmt.Sprintf("sep:%d", f.sep.ID),
		fmt.Sprintf("node:%d/direct", f.nodeA.ID), fmt.Sprintf("node:%d/a-fwd", f.nodeA.ID), fmt.Sprintf("node:%d/a-ext", f.nodeA.ID),
		fmt.Sprintf("node:%d/direct", f.nodeB.ID),
	}
	if got := rowKeys(t, out); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	if v, _ := out["order_version"].(string); v == "" {
		t.Fatal("no order_version")
	}
	rows := out["rows"].([]any)
	sep := rows[0].(map[string]any)
	if sep["name"] != "---- JP ----" || sep["enabled"] != true || sep["mode"] != "global" || sep["position"].(float64) != 5 {
		t.Fatalf("separator row: %v", sep)
	}
	fwd := rows[2].(map[string]any)
	land := fwd["landing"].(map[string]any)
	entry := fwd["entry"].(map[string]any)
	if fwd["line_type"] != "forward" || fwd["name"] != "GZ forward" || fwd["published"] != false || fwd["withheld"] != "waiting_entry" ||
		fwd["positioned"] != false || fwd["position"].(float64) != 10 || fwd["address"] != "gz.example" || fwd["port"].(float64) != 20443 ||
		land["name"] != "Tokyo A" || land["panel_name"] != "tk-1" || land["region"] != "JP" ||
		entry["panel_name"] != "gz-relay" || entry["missing"] != false {
		t.Fatalf("forward row: %v", fwd)
	}
	ext := rows[3].(map[string]any)
	if ext["line_type"] != "external" || ext["entry"] != nil || ext["published"] != true {
		t.Fatalf("external row: %v", ext)
	}
	direct := rows[1].(map[string]any)
	if direct["line_type"] != "direct" || direct["line_id"] != "direct" || direct["name"] != "Tokyo A" || direct["hidden_direct"] != false ||
		direct["health"].(map[string]any)["state"] == nil {
		t.Fatalf("direct row: %v", direct)
	}
}

// A single read of each table, however many rows come back.
func TestNodesOrderGetSingleQueryPerKind(t *testing.T) {
	f := newOrderFixture(t)
	f.nodes.lists.Store(0)
	f.seps.lists.Store(0)
	f.panels.lists.Store(0)
	if code, _ := f.get(f.adminToken); code != stdhttp.StatusOK {
		t.Fatal(code)
	}
	if n, s, p := f.nodes.lists.Load(), f.seps.lists.Load(), f.panels.lists.Load(); n != 1 || s != 1 || p != 1 {
		t.Fatalf("list calls: nodes=%d separators=%d panels=%d, want 1 each", n, s, p)
	}
}

func TestNodesOrderPutRoundTrip(t *testing.T) {
	f := newOrderFixture(t)
	_, cur := f.get(f.adminToken)
	a, b := f.nodeA.ID, f.nodeB.ID
	// Osaka first, then the separator, then Tokyo with its external line moved
	// above its forward line, and the forward line dragged under Osaka.
	arranged := []string{
		fmt.Sprintf("node:%d/direct", b), fmt.Sprintf("node:%d/a-fwd", a),
		fmt.Sprintf("sep:%d", f.sep.ID),
		fmt.Sprintf("node:%d/direct", a), fmt.Sprintf("node:%d/a-ext", a),
	}
	code, out := f.put(f.adminToken, orderPutBody(cur["order_version"].(string), arranged...))
	if code != stdhttp.StatusOK {
		t.Fatalf("PUT = %d: %v", code, out)
	}
	newVersion, _ := out["order_version"].(string)
	if newVersion == "" || newVersion == cur["order_version"] {
		t.Fatalf("order_version = %q", newVersion)
	}
	if f.invalidations.Load() != 1 {
		t.Fatalf("invalidations = %d, want 1", f.invalidations.Load())
	}
	_, after := f.get(f.adminToken)
	if got := rowKeys(t, after); strings.Join(got, ",") != strings.Join(arranged, ",") {
		t.Fatalf("order after PUT = %v, want %v", got, arranged)
	}
	if after["order_version"] != newVersion {
		t.Fatalf("GET version %v differs from the PUT's %s", after["order_version"], newVersion)
	}
	// Stored as explicit ten-step positions: direct lines on nodes, lines in the table.
	ctx := context.Background()
	gotA, _ := f.repos.Node.GetByID(ctx, a)
	gotB, _ := f.repos.Node.GetByID(ctx, b)
	if gotB.SortOrder != 10 || gotA.SortOrder != 40 {
		t.Fatalf("node sort_order b=%d a=%d, want 10 and 40", gotB.SortOrder, gotA.SortOrder)
	}
	pos := map[string]int{}
	for _, r := range gotA.Relays {
		if r.Position != nil {
			pos[r.ID] = *r.Position
		}
	}
	if pos["a-fwd"] != 20 || pos["a-ext"] != 50 {
		t.Fatalf("line positions = %v", pos)
	}
}

// A stale or incomplete request is a 409 that hands over the current order.
func TestNodesOrderPut409CarriesCurrentOrder(t *testing.T) {
	f := newOrderFixture(t)
	_, cur := f.get(f.adminToken)
	keys := rowKeys(t, cur)
	for name, body := range map[string]string{
		"old version":  orderPutBody("deadbeef", keys...),
		"row missing":  orderPutBody(cur["order_version"].(string), keys[1:]...),
		"unknown line": orderPutBody(cur["order_version"].(string), append(append([]string(nil), keys...), "node:999/nope")...),
		"unknown node": orderPutBody(cur["order_version"].(string), append(append([]string(nil), keys...), "node:999/direct")...),
		"unknown sep":  orderPutBody(cur["order_version"].(string), append(append([]string(nil), keys...), "sep:999")...),
	} {
		code, out := f.put(f.adminToken, body)
		if code != stdhttp.StatusConflict || out["code"] != "order_stale" {
			t.Fatalf("%s: PUT = %d %v, want 409 order_stale", name, code, out)
		}
		current, _ := out["order"].(map[string]any)
		if current == nil || current["order_version"] != cur["order_version"] || len(current["rows"].([]any)) != len(keys) {
			t.Fatalf("%s: 409 must carry the current order, got %v", name, out["order"])
		}
	}
	if f.invalidations.Load() != 0 {
		t.Fatal("a refused request wrote")
	}
}

func TestNodesOrderPut400(t *testing.T) {
	f := newOrderFixture(t)
	_, cur := f.get(f.adminToken)
	v := cur["order_version"].(string)
	for name, body := range map[string]string{
		"not json":     `{`,
		"no items":     fmt.Sprintf(`{"base_version":%q,"items":[]}`, v),
		"no version":   `{"items":[{"kind":"separator","id":1}]}`,
		"unknown kind": fmt.Sprintf(`{"base_version":%q,"items":[{"kind":"node","id":1}]}`, v),
		"duplicate":    fmt.Sprintf(`{"base_version":%q,"items":[{"kind":"separator","id":1},{"kind":"separator","id":1}]}`, v),
		"line no id":   fmt.Sprintf(`{"base_version":%q,"items":[{"kind":"line","node_id":1}]}`, v),
	} {
		if code, out := f.put(f.adminToken, body); code != stdhttp.StatusBadRequest {
			t.Fatalf("%s: PUT = %d %v, want 400", name, code, out)
		}
	}
}

// The legacy reorder endpoints keep working next to the new one.
func TestLegacyReorderEndpointsStillWork(t *testing.T) {
	f := newOrderFixture(t)
	body := fmt.Sprintf(`{"items":[{"id":%d,"sort_order":70},{"id":%d,"sort_order":60}]}`, f.nodeA.ID, f.nodeB.ID)
	if w := f.do("PUT", "/api/admin/nodes/reorder", f.adminToken, body); w.Code != stdhttp.StatusNoContent {
		t.Fatalf("legacy node reorder = %d: %s", w.Code, w.Body.String())
	}
	sep := fmt.Sprintf(`{"items":[{"id":%d,"sort_order":1}]}`, f.sep.ID)
	if w := f.do("PUT", "/api/admin/nodes/separator/reorder", f.adminToken, sep); w.Code != stdhttp.StatusNoContent {
		t.Fatalf("legacy separator reorder = %d: %s", w.Code, w.Body.String())
	}
	got, _ := f.repos.Node.GetByID(context.Background(), f.nodeA.ID)
	if got.SortOrder != 70 {
		t.Fatalf("sort_order = %d", got.SortOrder)
	}
}

// The edit dialog used to send sort_order with every save, so one opened before
// a drag and saved after it put the node back where it was (and a client that
// omitted the field wrote 0). Omitting it now keeps the stored position; an
// explicit value (the legacy UI) is still honoured.
func TestUpdateMetadataWithoutSortOrderKeepsPosition(t *testing.T) {
	f := newOrderFixture(t)
	ctx := context.Background()
	_, cur := f.get(f.adminToken)
	// Drag Osaka to the top through the new endpoint: it gets sort_order 10.
	keys := rowKeys(t, cur)
	arranged := append([]string{keys[len(keys)-1]}, keys[:len(keys)-1]...)
	if code, out := f.put(f.adminToken, orderPutBody(cur["order_version"].(string), arranged...)); code != stdhttp.StatusOK {
		t.Fatalf("PUT = %d %v", code, out)
	}
	moved, _ := f.repos.Node.GetByID(ctx, f.nodeB.ID)

	w := f.do("PUT", fmt.Sprintf("/api/admin/nodes/%d/metadata", f.nodeB.ID), f.adminToken, `{"display_name":"Osaka B renamed"}`)
	if w.Code != stdhttp.StatusOK && w.Code != stdhttp.StatusNoContent {
		t.Fatalf("metadata save = %d: %s", w.Code, w.Body.String())
	}
	got, _ := f.repos.Node.GetByID(ctx, f.nodeB.ID)
	if got.DisplayName != "Osaka B renamed" {
		t.Fatalf("the save did not apply: %+v", got)
	}
	if got.SortOrder != moved.SortOrder || got.SortOrder == 0 {
		t.Fatalf("sort_order = %d after a save without it, want the dragged %d", got.SortOrder, moved.SortOrder)
	}

	w = f.do("PUT", fmt.Sprintf("/api/admin/nodes/%d/metadata", f.nodeB.ID), f.adminToken, `{"display_name":"Osaka B renamed","sort_order":777}`)
	if w.Code != stdhttp.StatusOK && w.Code != stdhttp.StatusNoContent {
		t.Fatalf("metadata save = %d: %s", w.Code, w.Body.String())
	}
	if got, _ = f.repos.Node.GetByID(ctx, f.nodeB.ID); got.SortOrder != 777 {
		t.Fatalf("an explicit sort_order must still be written, got %d", got.SortOrder)
	}
}

// A line dragged to the bottom must not be undercut by a node created later:
// the next node's default position is past every line position too.
func TestNewNodeLandsBelowDraggedLines(t *testing.T) {
	f := newOrderFixture(t)
	ctx := context.Background()
	_, cur := f.get(f.adminToken)
	keys := rowKeys(t, cur)
	// Move Tokyo's forward line to the very bottom.
	var arranged []string
	for _, k := range keys {
		if k != fmt.Sprintf("node:%d/a-fwd", f.nodeA.ID) {
			arranged = append(arranged, k)
		}
	}
	arranged = append(arranged, fmt.Sprintf("node:%d/a-fwd", f.nodeA.ID))
	if code, out := f.put(f.adminToken, orderPutBody(cur["order_version"].(string), arranged...)); code != stdhttp.StatusOK {
		t.Fatalf("PUT = %d %v", code, out)
	}
	maxLine := 0
	a, _ := f.repos.Node.GetByID(ctx, f.nodeA.ID)
	for _, r := range a.Relays {
		if r.Position != nil && *r.Position > maxLine {
			maxLine = *r.Position
		}
	}
	n := &domain.Node{PanelID: f.nodeA.PanelID, InboundID: 9, DisplayName: "New", Enabled: true}
	if err := f.repos.Node.Create(ctx, n); err != nil {
		t.Fatal(err)
	}
	if n.SortOrder <= maxLine {
		t.Fatalf("new node sort_order %d is not past the last line position %d", n.SortOrder, maxLine)
	}
}
