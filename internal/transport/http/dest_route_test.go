package http

import (
	"context"
	"encoding/json"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
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
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destcatalog"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destlist"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/handler"
)

// The access-control API through the assembled router: the real services over
// a real SQLite store, an administrator and an operator. staffGroup shares the
// /api/admin prefix, so only a request through the router shows which group a
// route landed in.

type destFixture struct {
	t             *testing.T
	do            func(method, target, token, body string) *httptest.ResponseRecorder
	adminToken    string
	operatorToken string
	repos         ports.Repos
	dest          *sqlstore.DestRepos
	nativeNode    int64
	foreignNode   int64
}

func newDestFixture(t *testing.T, wired bool) *destFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	sqlstore.ConfigureSecretKey("test-only-dest-route-key")
	t.Cleanup(func() { sqlstore.ConfigureSecretKey("") })
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "dest-route.db"))
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
	dest := sqlstore.NewDestRepos(db)
	f := &destFixture{t: t, repos: repos, dest: dest}

	admin := &domain.User{ID: 1, UPN: "admin@example.test", Enabled: true, Role: domain.RoleAdmin}
	operator := &domain.User{ID: 2, UPN: "operator@example.test", Enabled: true, Role: domain.RoleOperator}
	users := routerReleaseUsers{users: map[int64]*domain.User{1: admin, 2: operator}}
	repos.User = users
	issuer := jwtutil.NewIssuer(strings.Repeat("test-only-key", 3), func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: "dest-route-test"}
	})
	authSvc := auth.New(issuer)
	if f.adminToken, _, err = authSvc.IssueTokens(admin); err != nil {
		t.Fatal(err)
	}
	if f.operatorToken, _, err = authSvc.IssueTokens(operator); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	native := &domain.XUIPanel{Kind: domain.PanelKindPSP, Name: "native", URL: "https://native.example.test"}
	foreign := &domain.XUIPanel{Kind: domain.PanelKind3XUI, Name: "xui", URL: "https://xui.example.test"}
	for _, p := range []*domain.XUIPanel{native, foreign} {
		if err := repos.XUIPanel.Save(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		panel *domain.XUIPanel
		into  *int64
	}{{native, &f.nativeNode}, {foreign, &f.foreignNode}} {
		n := &domain.Node{PanelID: c.panel.ID, InboundID: 1, DisplayName: "in-" + c.panel.Name, ServerAddress: "n.example.test", DesiredProtocol: "vless", DesiredPort: 443, Enabled: true}
		if err := repos.Node.Create(ctx, n); err != nil {
			t.Fatal(err)
		}
		*c.into = n.ID
	}

	deps := Deps{
		Cfg:   &config.Config{ConfigDir: t.TempDir()},
		Repos: repos,
		Auth:  authSvc, User: user.New(users, nil, nil, nil, nil, nil, nil, nil),
		Audit: audit.New(repos.Audit),
	}
	if wired {
		lists := destlist.New(destlist.Deps{
			Lists: dest.Lists, Policies: dest.Policies, SourceRules: dest.SourceRules, State: dest.State,
			Settings: repos.Settings, DataDir: t.TempDir(),
		})
		catalog, err := destcatalog.New(lists, dest, dest.Policies)
		if err != nil {
			t.Fatal(err)
		}
		policy, err := destpolicy.New(destpolicy.Deps{
			Definitions: dest, State: dest.State, Agents: dest.Agents, Lists: lists, Countries: lists.Countries, Settings: repos.Settings,
		})
		if err != nil {
			t.Fatal(err)
		}
		adminSvc, err := destpolicy.NewAdmin(destpolicy.AdminDeps{
			Service: policy, Policies: dest.Policies, Lists: dest.Lists, Counter: dest, Groups: repos.Group, Panels: repos.XUIPanel,
			Nodes: repos.Node, Clients: repos.PSPClient, Agents: repos.NodeAgent, Desired: repos.NativeDesired, Settings: repos.Settings,
			Instantiate: dest.InstantiateTemplate, ExceptionEditor: lists, Hits: dest.Hits,
		})
		if err != nil {
			t.Fatal(err)
		}
		deps.Dest = handler.DestWiring{
			Lists: lists, Catalog: catalog, Policy: policy, Admin: adminSvc, Policies: dest.Policies, Exemptions: dest.Exemptions,
			SourceRules: dest.SourceRules, Users: users, Groups: repos.Group, Nodes: repos.Node, Panels: repos.XUIPanel,
			Hits: dest.Hits,
		}
	}
	router := NewRouter(deps)
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

type destRoute struct{ method, path, body string }

// destRoutes is every route of the access-control API, with a body that would
// do something if the request got through.
var destRoutes = []destRoute{
	{"GET", "/api/admin/dest/lists", ""},
	{"POST", "/api/admin/dest/lists/preview", `{"kind":"custom","text":"example.com"}`},
	{"POST", "/api/admin/dest/lists", `{"name":"x","kind":"custom","text":"example.com"}`},
	{"GET", "/api/admin/dest/lists/1", ""},
	{"PUT", "/api/admin/dest/lists/1", `{"name":"y","text":"example.org"}`},
	{"DELETE", "/api/admin/dest/lists/1", ""},
	{"POST", "/api/admin/dest/lists/1/entries", `{"add":["example.net"]}`},
	{"POST", "/api/admin/dest/lists/1/refresh", ""},
	{"GET", "/api/admin/dest/geosite/status", ""},
	{"GET", "/api/admin/dest/geosite/categories", ""},
	{"POST", "/api/admin/dest/geosite/update?dry_run=1", ""},
	{"POST", "/api/admin/dest/geosite/rollback?dry_run=1", ""},
	{"GET", "/api/admin/dest/catalog", ""},
	{"POST", "/api/admin/dest/catalog/smtp/instantiate", `{"state":"on"}`},
	{"GET", "/api/admin/dest/policies", ""},
	{"POST", "/api/admin/dest/policies/preview", `{"name":"p","action":"block","state":"on","inline":{"ports":"25"}}`},
	{"POST", "/api/admin/dest/policies", `{"name":"p","action":"block","state":"on","inline":{"ports":"25"}}`},
	{"PUT", "/api/admin/dest/policies/order", `{"action":"block","ids":[]}`},
	{"PUT", "/api/admin/dest/policies/1", `{"name":"p","action":"block","state":"on","inline":{"ports":"25"}}`},
	{"DELETE", "/api/admin/dest/policies/1", ""},
	{"GET", "/api/admin/dest/exemptions", ""},
	{"POST", "/api/admin/dest/exemptions", `{"user_id":2,"reason":"x"}`},
	{"GET", "/api/admin/dest/exemptions/2", ""},
	{"PUT", "/api/admin/dest/exemptions/2", `{"reason":"y"}`},
	{"DELETE", "/api/admin/dest/exemptions/2", ""},
	{"GET", "/api/admin/dest/source-rules", ""},
	{"GET", "/api/admin/dest/source-rules/1", ""},
	{"PUT", "/api/admin/dest/source-rules/1", `{"countries":["CN"],"state":"on"}`},
	{"POST", "/api/admin/dest/exceptions", `{"target":"example.com","match":"site","scope":"global"}`},
	{"POST", "/api/admin/dest/test", `{"target":"example.com"}`},
	{"GET", "/api/admin/dest/users/2", ""},
	{"GET", "/api/admin/dest/hits", ""},
	{"GET", "/api/admin/dest/status", ""},
	{"POST", "/api/admin/dest/publish", ""},
	{"PUT", "/api/admin/dest/pause", `{"paused":true}`},
	{"POST", "/api/admin/dest/agents/agt_1/retry", ""},
	{"GET", "/api/admin/dest/settings", ""},
	{"PUT", "/api/admin/dest/settings", `{"settings":{"dest_list_refresh_hours":12}}`},
}

// EVERY access-control route is the administrator's: an operator is refused and
// nothing is touched, and an anonymous caller is refused before that. The
// router is assembled with the real services, so a route that slipped into
// staffGroup would reach them and change the store.
func TestDestRoutesAreAdminOnly(t *testing.T) {
	f := newDestFixture(t, true)
	for _, r := range destRoutes {
		if w := f.do(r.method, r.path, f.operatorToken, r.body); w.Code != stdhttp.StatusForbidden {
			t.Errorf("operator %s %s = %d, want 403: %s", r.method, r.path, w.Code, w.Body.String())
		}
		if w := f.do(r.method, r.path, "", r.body); w.Code != stdhttp.StatusUnauthorized {
			t.Errorf("anonymous %s %s = %d, want 401", r.method, r.path, w.Code)
		}
	}
	ctx := context.Background()
	if lists, _ := f.dest.Lists.List(ctx, false); len(lists) != 0 {
		t.Fatal("a refused request created a list")
	}
	if st, _ := f.dest.State.Get(ctx); st.Generation != 0 {
		t.Fatalf("a refused request wrote a definition (generation %d)", st.Generation)
	}
}

// With nothing wired the routes still exist (so the gate above is provable) and
// answer 503 to an administrator: "this build cannot" is not "nothing there".
func TestDestRoutesWithoutWiringAnswer503(t *testing.T) {
	f := newDestFixture(t, false)
	for _, r := range destRoutes {
		if strings.HasPrefix(r.path, "/api/admin/dest/settings") {
			continue
		}
		w := f.do(r.method, r.path, f.adminToken, r.body)
		if w.Code != stdhttp.StatusServiceUnavailable {
			t.Errorf("admin %s %s = %d, want 503: %s", r.method, r.path, w.Code, w.Body.String())
		}
	}
}

func (f *destFixture) call(method, path, body string) (int, map[string]any) {
	f.t.Helper()
	w := f.do(method, "/api/admin/dest"+path, f.adminToken, body)
	var out map[string]any
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			f.t.Fatalf("%s %s: not JSON: %v: %s", method, path, err, w.Body.String())
		}
	}
	return w.Code, out
}

func (f *destFixture) expect(method, path, body string, status int, code string) map[string]any {
	f.t.Helper()
	got, out := f.call(method, path, body)
	if got != status {
		f.t.Fatalf("%s %s = %d, want %d: %v", method, path, got, status, out)
	}
	if code != "" && out["code"] != code {
		f.t.Fatalf("%s %s code = %v, want %s: %v", method, path, out["code"], code, out)
	}
	return out
}

func num(v any) int64 { n, _ := v.(float64); return int64(n) }

func TestDestListsAPI(t *testing.T) {
	f := newDestFixture(t, true)

	// A preview parses and writes nothing.
	pv := f.expect("POST", "/lists/preview", `{"kind":"custom","text":"# c\nDOMAIN-SUFFIX,a.example.com\nregexp:x"}`, 200, "")
	if num(pv["entry_count"]) != 2 || len(pv["entries"].([]any)) != 2 {
		t.Fatalf("preview = %v", pv)
	}
	if lists, _ := f.dest.Lists.List(context.Background(), false); len(lists) != 0 {
		t.Fatal("a preview stored a list")
	}
	// A regexp that does not compile is a 400 with the line.
	bad := f.expect("POST", "/lists", `{"name":"bad","kind":"custom","text":"ok.example.com\nregexp:(oops"}`, 400, "dest_list_parse_failed")
	if lines := bad["lines"].([]any); len(lines) != 1 || num(lines[0].(map[string]any)["line"]) != 2 {
		t.Fatalf("parse error lines = %v", bad["lines"])
	}
	f.expect("POST", "/lists", `{"name":"x","kind":"remote","source_url":"http://insecure.example.com/l"}`, 400, "dest_list_insecure_url")
	f.expect("POST", "/lists/preview", `{"kind":"remote","source_url":"http://insecure.example.com/l"}`, 400, "dest_list_insecure_url")
	f.expect("POST", "/lists", `{"name":"x","kind":"weird"}`, 400, "dest_bad_request")
	f.expect("POST", "/lists", `{"name":"x","kind":"geosite","geosite_category":"category-ads-all"}`, 503, "dest_geosite_unavailable")

	created := f.expect("POST", "/lists", `{"name":"mine","kind":"custom","text":"a.example.com\nb.example.com"}`, 201, "")
	list := created["list"].(map[string]any)
	id := num(list["id"])
	if list["state"] != "ready" || num(list["entry_count"]) != 2 || list["kind"] != "custom" {
		t.Fatalf("list = %v", list)
	}
	one := f.expect("GET", "/lists/"+itoa(id)+"?text=1", "", 200, "")
	if one["source_text"] != "a.example.com\nb.example.com" || len(one["samples"].([]any)) != 2 {
		t.Fatalf("get = %v", one)
	}
	if got := f.expect("GET", "/lists/"+itoa(id), "", 200, ""); got["source_text"] != nil {
		t.Fatal("the text is only returned when asked for")
	}
	all := f.expect("GET", "/lists", "", 200, "")
	if len(all["items"].([]any)) != 1 || all["budget"] == nil || num(all["refresh_hours"]) != 24 {
		t.Fatalf("lists = %v", all)
	}

	// The editor's stale precondition.
	updatedAt := num(list["updated_at"])
	f.expect("PUT", "/lists/"+itoa(id), `{"name":"mine","text":"c.example.com","updated_at":`+itoa(updatedAt-1000)+`}`, 409, "dest_list_stale")
	ok := f.expect("PUT", "/lists/"+itoa(id), `{"name":"mine","text":"c.example.com","updated_at":`+itoa(updatedAt)+`}`, 200, "")
	if num(ok["list"].(map[string]any)["entry_count"]) != 1 {
		t.Fatalf("after the edit: %v", ok)
	}
	add := f.expect("POST", "/lists/"+itoa(id)+"/entries", `{"add":["d.example.com"],"remove":["c.example.com"]}`, 200, "")
	if num(add["list"].(map[string]any)["entry_count"]) != 1 {
		t.Fatalf("entries: %v", add)
	}
	f.expect("POST", "/lists/"+itoa(id)+"/refresh", "", 400, "dest_bad_request") // custom: nothing to refresh
	f.expect("GET", "/lists/999", "", 404, "")
	f.expect("GET", "/lists/abc", "", 400, "dest_bad_id")

	// A list a policy uses cannot be deleted, and the 409 says who.
	pol := f.expect("POST", "/policies", `{"name":"uses","action":"block","state":"on","list_ids":[`+itoa(id)+`]}`, 201, "")
	in := f.expect("DELETE", "/lists/"+itoa(id), "", 409, "dest_list_in_use")
	ub := in["used_by"].([]any)
	if len(ub) != 1 || ub[0].(map[string]any)["name"] != "uses" {
		t.Fatalf("used_by = %v", in["used_by"])
	}
	_ = pol
	f.expect("DELETE", "/policies/"+itoa(num(pol["policy"].(map[string]any)["id"])), "", 200, "")
	f.expect("DELETE", "/lists/"+itoa(id), "", 200, "")
}

func TestDestPoliciesAPI(t *testing.T) {
	f := newDestFixture(t, true)
	list := f.expect("POST", "/lists", `{"name":"l","kind":"custom","text":"a.example.com"}`, 201, "")["list"].(map[string]any)
	lid := num(list["id"])

	// Report-only is a state like any other for a block policy; an allow policy
	// has none.
	f.expect("POST", "/policies", `{"name":"p","action":"allow","state":"report","inline":{"ports":"25"}}`, 400, "dest_policy_state_unavailable")
	rep := f.expect("POST", "/policies", `{"name":"w","action":"block","state":"report","inline":{"ports":"25"}}`, 201, "")["policy"].(map[string]any)
	if rep["state"] != "report" {
		t.Fatalf("a report-only policy came back as %v", rep["state"])
	}
	f.expect("DELETE", "/policies/"+itoa(num(rep["id"])), "", 200, "")
	f.expect("POST", "/policies", `{"name":"p","action":"block","state":"on"}`, 400, "dest_policy_no_match")
	f.expect("POST", "/policies", `{"name":"p","action":"block","state":"on","inline":{"ports":"99999"}}`, 400, "dest_policy_invalid")
	f.expect("POST", "/policies", `{"name":"p","action":"block","state":"on","list_ids":[999]}`, 400, "dest_policy_invalid")

	a := f.expect("POST", "/policies", `{"name":"a","action":"block","state":"on","list_ids":[`+itoa(lid)+`],"inline":{"ports":"25,465"}}`, 201, "")["policy"].(map[string]any)
	b := f.expect("POST", "/policies", `{"name":"b","action":"block","state":"off","inline":{"cidrs":["10.0.0.0/8"]}}`, 201, "")["policy"].(map[string]any)
	if num(a["priority"]) != 1 || num(b["priority"]) != 2 {
		t.Fatalf("priorities = %v %v: the server assigns them, last of the action", a["priority"], b["priority"])
	}
	f.expect("POST", "/policies", `{"name":"a","action":"block","state":"on","inline":{"ports":"22"}}`, 409, "dest_name_taken")

	got := f.expect("GET", "/policies", "", 200, "")
	if len(got["block"].([]any)) != 2 || len(got["allow"].([]any)) != 0 || len(got["observe"].([]any)) != 0 || got["budget"] == nil {
		t.Fatalf("policies = %v", got)
	}
	budget := got["budget"].(map[string]any)
	if num(budget["rules"].(map[string]any)["limit"]) != 256 || num(budget["rules"].(map[string]any)["used"]) != 1 {
		t.Fatalf("budget = %v", budget)
	}

	// Preview: the budget as it would be, nothing written.
	pv := f.expect("POST", "/policies/preview", `{"name":"c","action":"block","state":"on","inline":{"ports":"26"}}`, 200, "")
	if num(pv["budget"].(map[string]any)["rules"].(map[string]any)["used"]) != 2 {
		t.Fatalf("preview = %v", pv)
	}
	if all, _ := f.dest.Policies.List(context.Background()); len(all) != 2 {
		t.Fatal("a preview saved a policy")
	}

	// Edit: the precondition; the priority stays; an action change moves it.
	id := itoa(num(a["id"]))
	at := num(a["updated_at"])
	f.expect("PUT", "/policies/"+id, `{"name":"a2","action":"block","state":"on","list_ids":[`+itoa(lid)+`],"updated_at":`+itoa(at-5000)+`}`, 409, "dest_policy_stale")
	up := f.expect("PUT", "/policies/"+id, `{"name":"a2","action":"block","state":"on","list_ids":[`+itoa(lid)+`],"updated_at":`+itoa(at)+`}`, 200, "")["policy"].(map[string]any)
	if up["name"] != "a2" || num(up["priority"]) != 1 {
		t.Fatalf("updated = %v", up)
	}
	moved := f.expect("PUT", "/policies/"+id, `{"name":"a2","action":"allow","state":"on","list_ids":[`+itoa(lid)+`]}`, 200, "")["policy"].(map[string]any)
	if moved["action"] != "allow" || num(moved["priority"]) != 1 {
		t.Fatalf("moved = %v", moved)
	}

	// Order: exactly the action's policies.
	f.expect("PUT", "/policies/order", `{"action":"block","ids":[`+itoa(num(b["id"]))+`,999]}`, 409, "dest_policy_order_stale")
	f.expect("PUT", "/policies/order", `{"action":"block","ids":[`+itoa(num(b["id"]))+`]}`, 200, "")
	f.expect("PUT", "/policies/order", `{"action":"other","ids":[]}`, 400, "dest_policy_invalid")
	f.expect("DELETE", "/policies/"+itoa(num(b["id"])), "", 200, "")
	f.expect("DELETE", "/policies/"+itoa(num(b["id"])), "", 404, "")
	f.expect("PUT", "/policies/999", `{"name":"z","action":"block","state":"on","inline":{"ports":"22"}}`, 404, "")
}

// Over a limit the save is refused before anything is written, with which
// limit and by how much.
func TestDestPolicyOverLimitIsRefusedBeforeTheWrite(t *testing.T) {
	f := newDestFixture(t, true)
	for i := 0; i < 256; i++ {
		f.expect("POST", "/policies", `{"name":"p`+itoa(int64(i))+`","action":"block","state":"on","inline":{"ports":"26"}}`, 201, "")
	}
	out := f.expect("POST", "/policies", `{"name":"one-too-many","action":"block","state":"on","inline":{"ports":"27"}}`, 400, "dest_policy_over_limit")
	if out["kind"] != "rules" || num(out["used"]) != 257 || num(out["limit"]) != 256 {
		t.Fatalf("over-limit body = %v", out)
	}
	if all, _ := f.dest.Policies.List(context.Background()); len(all) != 256 {
		t.Fatalf("%d policies, want 256: the refused one must not be written", len(all))
	}
}

func TestDestExemptionsAPI(t *testing.T) {
	f := newDestFixture(t, true)
	f.expect("POST", "/exemptions", `{"user_id":0}`, 400, "dest_exemption_invalid")
	f.expect("POST", "/exemptions", `{"user_id":999,"reason":"x"}`, 400, "dest_exemption_invalid")
	f.expect("POST", "/exemptions", `{"user_id":2,"reason":"x","expires_at":1000}`, 400, "dest_exemption_invalid")

	exp := time.Now().Add(48 * time.Hour).UnixMilli()
	got := f.expect("POST", "/exemptions", `{"user_id":2,"reason":"ops account","expires_at":`+itoa(exp)+`}`, 201, "")["exemption"].(map[string]any)
	if got["upn"] != "operator@example.test" || got["created_by_upn"] != "admin@example.test" || got["expired"] != false || num(got["expires_at"]) != exp {
		t.Fatalf("exemption = %v", got)
	}
	f.expect("POST", "/exemptions", `{"user_id":2,"reason":"again"}`, 409, "dest_exemption_exists")
	up := f.expect("PUT", "/exemptions/2", `{"reason":"changed"}`, 200, "")["exemption"].(map[string]any)
	if up["reason"] != "changed" || up["created_by_upn"] != "admin@example.test" || up["expires_at"] != nil {
		t.Fatalf("updated = %v", up)
	}
	f.expect("PUT", "/exemptions/1", `{"reason":"never existed"}`, 404, "")
	list := f.expect("GET", "/exemptions", "", 200, "")
	if len(list["items"].([]any)) != 1 {
		t.Fatalf("list = %v", list)
	}
	f.expect("GET", "/exemptions/2", "", 200, "")
	user := f.expect("GET", "/users/2", "", 200, "")
	if user["exemption"] == nil || user["upn"] != "operator@example.test" || user["usage_available"] != nil {
		t.Fatalf("user = %v", user)
	}
	f.expect("GET", "/users/999", "", 404, "")
	f.expect("DELETE", "/exemptions/2", "", 200, "")
	f.expect("DELETE", "/exemptions/2", "", 404, "")
}

func TestDestSourceRulesAPI(t *testing.T) {
	f := newDestFixture(t, true)
	node := itoa(f.nativeNode)

	empty := f.expect("GET", "/source-rules/"+node, "", 200, "")
	rule := empty["rule"].(map[string]any)
	if rule["state"] != "off" || rule["updated_at"] != nil || empty["native"] != true || empty["geoip_available"] != false {
		t.Fatalf("default rule = %v", empty)
	}
	if foreign := f.expect("GET", "/source-rules/"+itoa(f.foreignNode), "", 200, ""); foreign["native"] != false {
		t.Fatalf("foreign = %v", foreign)
	}
	f.expect("GET", "/source-rules/9999", "", 404, "")

	put := f.expect("PUT", "/source-rules/"+node, `{"countries":["cn","CN"],"cidrs":["203.0.113.9/24"],"state":"on"}`, 200, "")
	saved := put["rule"].(map[string]any)
	if cs := saved["countries"].([]any); len(cs) != 1 || cs[0] != "CN" {
		t.Fatalf("countries = %v", saved["countries"])
	}
	if cs := saved["cidrs"].([]any); len(cs) != 1 || cs[0] != "203.0.113.0/24" {
		t.Fatalf("cidrs = %v", saved["cidrs"])
	}
	f.expect("PUT", "/source-rules/"+itoa(f.foreignNode), `{"countries":["CN"],"state":"on"}`, 409, "dest_source_rule_not_native")
	f.expect("PUT", "/source-rules/"+node, `{"countries":["CHN"],"state":"on"}`, 400, "dest_source_rule_invalid")
	f.expect("PUT", "/source-rules/"+node, `{"state":"maybe"}`, 400, "dest_policy_invalid")
	at := num(saved["updated_at"])
	f.expect("PUT", "/source-rules/"+node, `{"countries":["US"],"state":"on","updated_at":`+itoa(at-9000)+`}`, 409, "dest_source_rule_stale")
	f.expect("PUT", "/source-rules/"+node, `{"countries":["US"],"state":"on","updated_at":`+itoa(at)+`}`, 200, "")

	all := f.expect("GET", "/source-rules", "", 200, "")
	items := all["items"].([]any)
	if len(items) != 1 || all["geoip_available"] != false {
		t.Fatalf("source rules = %v", all)
	}
	// With no GeoIP database the rule is not enforced, and the page is told.
	f.expect("POST", "/publish", "", 200, "")
	st := f.expect("GET", "/status", "", 200, "")
	if st["geoip_unavailable"] != true {
		t.Fatalf("status = %v: a country that cannot be expanded must be surfaced", st)
	}
	listed := f.expect("GET", "/source-rules", "", 200, "")["items"].([]any)[0].(map[string]any)
	if listed["note"] != "geoip_unavailable" {
		t.Fatalf("rule note = %v", listed)
	}
}

func TestDestCatalogAPI(t *testing.T) {
	f := newDestFixture(t, true)
	cat := f.expect("GET", "/catalog", "", 200, "")
	if len(cat["templates"].([]any)) == 0 || len(cat["unavailable"].([]any)) == 0 || num(cat["catalog_version"]) < 1 {
		t.Fatalf("catalog = %v", cat)
	}
	f.expect("POST", "/catalog/smtp/instantiate", `{"state":""}`, 400, "dest_policy_invalid")
	f.expect("POST", "/catalog/nope/instantiate", `{"state":"on"}`, 404, "")
	f.expect("POST", "/catalog/education/instantiate", `{"state":"on"}`, 400, "dest_template_unavailable")
	made := f.expect("POST", "/catalog/smtp/instantiate", `{"state":"report","locale":"en-US"}`, 201, "")["policy"].(map[string]any)
	if made["template_key"] != "smtp" || made["state"] != "report" || made["action"] != "block" {
		t.Fatalf("instantiated = %v", made)
	}
	f.expect("POST", "/catalog/smtp/instantiate", `{"state":"on"}`, 409, "dest_template_exists")
	again := f.expect("GET", "/catalog", "", 200, "")
	added := false
	for _, tpl := range again["templates"].([]any) {
		if m := tpl.(map[string]any); m["key"] == "smtp" {
			added = m["added"] == true
		}
	}
	if !added {
		t.Fatal("the catalog does not mark the group as added")
	}
}

func TestDestGeositeAPIWithoutData(t *testing.T) {
	f := newDestFixture(t, true)
	st := f.expect("GET", "/geosite/status", "", 200, "")
	if st["update_available"] == nil || st["mode"] != "manual" {
		t.Fatalf("status = %v", st)
	}
	f.expect("GET", "/geosite/categories", "", 503, "dest_geosite_unavailable")
	f.expect("POST", "/geosite/rollback?dry_run=1", "", 404, "")
}

func TestDestExceptionsTestStatusAndControl(t *testing.T) {
	f := newDestFixture(t, true)
	f.expect("POST", "/exceptions", `{"target":"example.com","match":"site","scope":"group","group_id":3}`, 400, "dest_exception_invalid")
	f.expect("POST", "/exceptions", `{"target":"","match":"site"}`, 400, "dest_exception_invalid")
	first := f.expect("POST", "/exceptions", `{"target":"ok.example.com","match":"site","scope":"global"}`, 200, "")
	created := first["created"].(map[string]any)
	if created["list_id"] == nil || created["policy_id"] == nil {
		t.Fatalf("first exception = %v", first)
	}
	if second := f.expect("POST", "/exceptions", `{"target":"x.example.net","match":"host"}`, 200, ""); second["created"] != nil {
		t.Fatalf("second exception = %v", second)
	}
	f.expect("POST", "/policies", `{"name":"block all of example.com","action":"block","state":"on","inline":{"cidrs":["198.51.100.0/24"]}}`, 201, "")

	// Before the publish the exception is not in force, and the test says so.
	pre := f.expect("POST", "/test", `{"target":"198.51.100.5"}`, 200, "")
	if pre["unpublished"] != true || pre["verdict"] != "default" {
		t.Fatalf("test before publish = %v", pre)
	}
	pub := f.expect("POST", "/publish", "", 200, "")
	if num(pub["published_generation"]) != num(pub["generation"]) || pub["unpublished"] != false {
		t.Fatalf("publish = %v", pub)
	}
	res := f.expect("POST", "/test", `{"target":"198.51.100.5","port":443,"network":"tcp"}`, 200, "")
	if res["verdict"] != "block" || res["unpublished"] != false || len(res["steps"].([]any)) == 0 || res["notes"] == nil {
		t.Fatalf("test = %v", res)
	}
	steps := res["steps"].([]any)
	var hit map[string]any
	for _, s := range steps {
		if m := s.(map[string]any); m["result"] == "hit" {
			hit = m
		}
	}
	if hit == nil || hit["kind"] != "block" || hit["name"] != "block all of example.com" {
		t.Fatalf("hit step = %v", hit)
	}
	allowed := f.expect("POST", "/test", `{"target":"www.ok.example.com"}`, 200, "")
	if allowed["verdict"] != "allow" {
		t.Fatalf("the exception did not allow: %v", allowed)
	}
	f.expect("POST", "/test", `{"target":"x","network":"icmp"}`, 400, "dest_test_invalid")
	f.expect("POST", "/test", `{"target":"x","port":0}`, 400, "dest_test_invalid")
	f.expect("POST", "/test", `{"target":"x","port":65536}`, 400, "dest_test_invalid")

	status := f.expect("GET", "/status", "", 200, "")
	if status["paused"] != false || status["publish_error"] != nil || status["nodes"] == nil || status["geosite_release"] == nil {
		t.Fatalf("status = %v", status)
	}
	var native map[string]any
	for _, n := range status["nodes"].([]any) {
		if m := n.(map[string]any); m["kind"] == "psp" {
			native = m
		}
	}
	if native == nil || native["state"] != "unsupported_version" {
		t.Fatalf("native node status = %v (no agent has reported)", native)
	}
	if v, present := native["report_only_not_deployed"]; !present || v != nil {
		t.Fatalf("report_only_not_deployed = %v (present %v): the field is always there, null when nothing was left out", v, present)
	}

	f.expect("PUT", "/pause", `{}`, 400, "dest_bad_request")
	if p := f.expect("PUT", "/pause", `{"paused":true}`, 200, ""); p["paused"] != true {
		t.Fatalf("pause = %v", p)
	}
	if st := f.expect("GET", "/status", "", 200, ""); st["paused"] != true {
		t.Fatalf("status = %v", st)
	}
	f.expect("PUT", "/pause", `{"paused":false}`, 200, "")
	if r := f.expect("POST", "/agents/agt_unknown/retry", "", 200, ""); r["retried"] != false {
		t.Fatalf("retry = %v", r)
	}
}

// A publish the validation refuses is a 409 with the publish_error, and the
// status carries it.
func TestDestPublishRefusalIs409WithThePublishError(t *testing.T) {
	f := newDestFixture(t, true)
	ctx := context.Background()
	// 257 enabled rules written straight to the store (the API would refuse
	// the 257th before the write; this is what several individually legal
	// writes can add up to).
	for i := 0; i < 257; i++ {
		p := &domain.DestPolicy{Name: "p" + itoa(int64(i)), Action: domain.DestActionBlock, State: domain.DestStateOn, Scope: domain.DestScopeAll,
			Inline: domain.DestPolicyInline{Ports: "26"}}
		if err := f.dest.Policies.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	out := f.expect("POST", "/publish", "", 409, "dest_policy_over_limit")
	pe := out["publish_error"].(map[string]any)
	if pe["kind"] != "rules" || num(pe["used"]) != 257 || num(pe["limit"]) != 256 {
		t.Fatalf("publish_error = %v", pe)
	}
	st := f.expect("GET", "/status", "", 200, "")
	if st["publish_error"] == nil || num(st["published_generation"]) == num(st["generation"]) {
		t.Fatalf("status = %v", st)
	}
}

// What the audit log keeps of the access-control API: every write but the two
// previews, and none of it readable by an operator.
func TestDestAPIAuditRowsAreAdminOnly(t *testing.T) {
	f := newDestFixture(t, true)
	const secretText = "secret-customer.example.com"
	f.expect("POST", "/lists/preview", `{"kind":"custom","text":"`+secretText+`"}`, 200, "")
	f.expect("POST", "/policies/preview", `{"name":"p","action":"block","state":"on","inline":{"ports":"25"}}`, 200, "")
	f.expect("POST", "/lists", `{"name":"mine","kind":"custom","text":"`+secretText+`"}`, 201, "")
	f.expect("POST", "/test", `{"target":"`+secretText+`"}`, 200, "")
	f.expect("POST", "/exemptions", `{"user_id":2,"reason":"shares an office with 203.0.113.9"}`, 201, "")
	if err := f.repos.Audit.Insert(context.Background(), &domain.AuditEntry{
		Actor: "operator@example.test", Action: "update /api/admin/users/:id", Target: "/api/admin/users/:id", At: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	type page struct {
		Items []struct {
			Target string `json:"target"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	read := func(token, query string) (page, string) {
		t.Helper()
		w := f.do("GET", "/api/admin/audit"+query, token, "")
		if w.Code != 200 {
			t.Fatalf("GET audit%s = %d: %s", query, w.Code, w.Body.String())
		}
		var p page
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p, w.Body.String()
	}

	adminPage, adminRaw := read(f.adminToken, "?page_size=100")
	var targets []string
	for _, it := range adminPage.Items {
		targets = append(targets, it.Target)
	}
	joined := strings.Join(targets, " ")
	for _, want := range []string{"/api/admin/dest/lists", "/api/admin/dest/test", "/api/admin/dest/exemptions"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the administrator's audit lacks %s (have %v)", want, targets)
		}
	}
	if strings.Contains(joined, "preview") {
		t.Errorf("a preview was audited: %v", targets)
	}
	if !strings.Contains(adminRaw, secretText) {
		t.Error("the administrator cannot read what the writes carried")
	}

	for _, q := range []string{"", "?page_size=1", "?search=dest", "?search=" + url.QueryEscape("203.0.113.9"), "?search=secret-customer"} {
		p, raw := read(f.operatorToken, q)
		for _, leak := range []string{"/dest/", secretText, "203.0.113.9", "office"} {
			if strings.Contains(raw, leak) {
				t.Fatalf("the operator's audit page%s carries %q: %s", q, leak, raw)
			}
		}
		if q == "" && (p.Total != 1 || len(p.Items) != 1 || p.Items[0].Target != "/api/admin/users/:id") {
			t.Fatalf("the operator's page = %+v, want the one row outside access control", p)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// counts_as_risk is written and read back on every policy route; an allow
// policy cannot carry it; a PUT that omits it keeps it.
func TestDestPolicyCountsAsRiskRoundTrip(t *testing.T) {
	f := newDestFixture(t, true)
	policy := func(out map[string]any) map[string]any { return out["policy"].(map[string]any) }

	made := policy(f.expect("POST", "/policies", `{"name":"mail","action":"block","state":"on","counts_as_risk":true,"inline":{"ports":"25"}}`, 201, ""))
	if made["counts_as_risk"] != true {
		t.Fatalf("POST = %v", made)
	}
	plain := policy(f.expect("POST", "/policies", `{"name":"plain","action":"block","state":"on","inline":{"ports":"26"}}`, 201, ""))
	if plain["counts_as_risk"] != false {
		t.Fatalf("omitted on POST = %v, want false", plain["counts_as_risk"])
	}
	flags := map[string]any{}
	for _, p := range f.expect("GET", "/policies", "", 200, "")["block"].([]any) {
		flags[p.(map[string]any)["name"].(string)] = p.(map[string]any)["counts_as_risk"]
	}
	if flags["mail"] != true || flags["plain"] != false {
		t.Fatalf("GET = %v", flags)
	}

	id := itoa(num(made["id"]))
	at := num(made["updated_at"])
	kept := policy(f.expect("PUT", "/policies/"+id, `{"name":"mail","action":"block","state":"report","inline":{"ports":"25"},"updated_at":`+itoa(at)+`}`, 200, ""))
	if kept["counts_as_risk"] != true || kept["state"] != "report" {
		t.Fatalf("a PUT that omits the flag = %v: it keeps the stored value (a report-only block may store it)", kept)
	}
	cleared := policy(f.expect("PUT", "/policies/"+id, `{"name":"mail","action":"block","state":"on","counts_as_risk":false,"inline":{"ports":"25"},"updated_at":`+itoa(num(kept["updated_at"]))+`}`, 200, ""))
	if cleared["counts_as_risk"] != false {
		t.Fatalf("explicit false = %v", cleared)
	}

	// An allow policy cannot count.
	bad := f.expect("POST", "/policies", `{"name":"allow-me","action":"allow","state":"on","counts_as_risk":true,"inline":{"ports":"27"}}`, 400, "dest_policy_invalid")
	if bad["field"] != "counts_as_risk" {
		t.Fatalf("400 body = %v, want field counts_as_risk", bad)
	}
	// A block that becomes an allow in the same request drops the flag silently;
	// an allow that stays an allow with it is refused.
	cur := f.expect("GET", "/policies", "", 200, "")["block"].([]any)
	var curAt int64
	for _, p := range cur {
		if m := p.(map[string]any); m["name"] == "mail" {
			curAt = num(m["updated_at"])
		}
	}
	toAllow := policy(f.expect("PUT", "/policies/"+id, `{"name":"mail","action":"allow","state":"on","counts_as_risk":true,"inline":{"ports":"25"},"updated_at":`+itoa(curAt)+`}`, 200, ""))
	if toAllow["action"] != "allow" || toAllow["counts_as_risk"] != false {
		t.Fatalf("block -> allow echoing true = %v", toAllow)
	}
	f.expect("PUT", "/policies/"+id, `{"name":"mail","action":"allow","state":"on","counts_as_risk":true,"inline":{"ports":"25"},"updated_at":`+itoa(num(toAllow["updated_at"]))+`}`, 400, "dest_policy_invalid")

	// The preview judges it like the save and echoes it back.
	pv := f.expect("POST", "/policies/preview", `{"name":"pv","action":"block","state":"report","counts_as_risk":true,"inline":{"ports":"28"}}`, 200, "")
	if policy(pv)["counts_as_risk"] != true || pv["budget"] == nil {
		t.Fatalf("preview = %v", pv)
	}
	f.expect("POST", "/policies/preview", `{"name":"pv","action":"allow","state":"on","counts_as_risk":true,"inline":{"ports":"28"}}`, 400, "dest_policy_invalid")
	if all, _ := f.dest.Policies.List(context.Background()); len(all) != 2 {
		t.Fatalf("%d policies stored, want 2: a preview or a refusal wrote one", len(all))
	}
}

// The catalog offers the default, and a template added from it carries it.
func TestDestCatalogTemplateCarriesCountsAsRisk(t *testing.T) {
	f := newDestFixture(t, true)
	cat := f.expect("GET", "/catalog", "", 200, "")
	got := map[string]any{}
	for _, tpl := range cat["templates"].([]any) {
		m := tpl.(map[string]any)
		got[m["key"].(string)] = m["counts_as_risk"]
	}
	if got["smtp"] != true || got["bittorrent"] != true || got["porn"] == true || got["private"] == true {
		t.Fatalf("template defaults = %v", got)
	}
	made := f.expect("POST", "/catalog/smtp/instantiate", `{"state":"report"}`, 201, "")
	if made["policy"].(map[string]any)["counts_as_risk"] != true {
		t.Fatalf("instantiate = %v", made)
	}
}
