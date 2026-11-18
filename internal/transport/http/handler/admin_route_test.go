package handler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/route"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/transit"
)

type routeFixture struct {
	repos  ports.Repos
	router *gin.Engine
	audit  *upgradeGateAudit
	entry  int64
	exit   int64
	node   int64
}

func newRouteFixture(t *testing.T, caps ...string) *routeFixture {
	t.Helper()
	sqlstore.ConfigureSecretKey("test-only-route-handler-key")
	t.Cleanup(func() { sqlstore.ConfigureSecretKey("") })
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "route.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := sqlstore.NewRepos(db)
	f := &routeFixture{repos: repos, audit: &upgradeGateAudit{}}
	server := func(name, agent, address string, capabilities []string) int64 {
		panel := &domain.Panel{Kind: domain.PanelKindPSP, Name: name, URL: "psp://" + agent}
		if err := repos.XUIPanel.Save(t.Context(), panel); err != nil {
			t.Fatal(err)
		}
		a := &domain.NodeAgent{AgentID: agent, PanelID: panel.ID, Epoch: 1, CredentialSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(agent))),
			DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: "26.6.27", ObservedAddress: address}
		if err := repos.NodeAgent.Create(t.Context(), a); err != nil {
			t.Fatal(err)
		}
		if err := repos.NodeAgent.UpdateProtocolObservation(t.Context(), agent, 1, capabilities, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		recorder, ok := repos.NodeAgent.(interface {
			RecordObservedAddress(ctx context.Context, agentID, address string) error
		})
		if !ok {
			t.Fatal("the agent store cannot record an address")
		}
		if err := recorder.RecordObservedAddress(t.Context(), agent, address); err != nil {
			t.Fatal(err)
		}
		return panel.ID
	}
	f.entry = server("entry", "agt_entry", "198.51.100.10", caps)
	f.exit = server("tokyo", "agt_exit", "198.51.100.20", []string{nodeprotocol.CapabilityTransitLink})
	node := &domain.Node{PanelID: f.entry, InboundID: 1, DisplayName: "main", ServerAddress: "198.51.100.10", DesiredProtocol: "vless", DesiredPort: 443, Enabled: true, Region: "HK"}
	if err := repos.Node.Create(t.Context(), node); err != nil {
		t.Fatal(err)
	}
	f.node = node.ID
	svc := route.New(route.Deps{Routes: repos.TransitRoutes, Nodes: repos.Node, Panels: repos.XUIPanel, Agents: repos.NodeAgent,
		Links: transit.New(repos.Node, repos.Transit).WithLinks(repos.TransitLinks, repos.NodeAgent), Probes: repos.TransitProbe})
	gin.SetMode(gin.TestMode)
	h := NewAdminRouteHandler(svc, f.audit)
	f.router = gin.New()
	f.router.GET("/api/admin/nodes/:id/routes", h.Get)
	f.router.PUT("/api/admin/nodes/:id/routes", h.Put)
	f.router.POST("/api/admin/nodes/:id/routes/check", h.Check)
	return f
}

func (f *routeFixture) do(method, suffix, body string) (int, map[string]any) {
	req := httptest.NewRequest(method, "/api/admin/nodes/"+strconv.FormatInt(f.node, 10)+"/routes"+suffix, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (f *routeFixture) body() string {
	return `{"routes":[{"name":"streaming","target":{"kind":"server","panel_id":` + strconv.FormatInt(f.exit, 10) + `},"domains":["domain:netflix.com"],"ports":"443"},` +
		`{"target":{"kind":"local"},"cidrs":["10.0.0.0/8"]}]}`
}

func TestRouteHandlerSavesChecksAndAuditsARoutedNode(t *testing.T) {
	f := newRouteFixture(t, nodeprotocol.CapabilityTransitLink, nodeprotocol.CapabilityTransitRoute)
	// Nothing saved yet: the section is supported and empty.
	status, out := f.do(http.MethodGet, "", "")
	if status != http.StatusOK || out["supported"] != true || len(out["routes"].([]any)) != 0 {
		t.Fatalf("get: %d %v", status, out)
	}
	// The dry run writes nothing and is not audited.
	status, out = f.do(http.MethodPost, "/check", f.body())
	if status != http.StatusOK || len(out["routes"].([]any)) != 2 || len(f.audit.actions) != 0 {
		t.Fatalf("check: %d %v %v", status, out, f.audit.actions)
	}
	if stored, _ := f.repos.TransitRoutes.Get(t.Context(), f.node); len(stored.Routes) != 0 {
		t.Fatal("a dry run stored routes")
	}

	status, out = f.do(http.MethodPut, "", f.body())
	if status != http.StatusOK {
		t.Fatalf("put: %d %v", status, out)
	}
	routes := out["routes"].([]any)
	first := routes[0].(map[string]any)
	if first["id"] != "r1" || routes[1].(map[string]any)["id"] != "r2" || first["name"] != "streaming" {
		t.Fatalf("routes = %v", routes)
	}
	link := first["link"].(map[string]any)
	if link["exists"] != true || link["ready"] != false || link["server_name"] != "tokyo" {
		t.Fatalf("link = %v", link)
	}
	if routes[1].(map[string]any)["link"] != nil {
		t.Fatal("a local route has no link")
	}
	if out["sent"] != false || out["running"] != false || out["default_exit"].(map[string]any)["kind"] != "local" {
		t.Fatalf("state = %v", out)
	}
	if !f.audit.saw("transit_routes_updated") {
		t.Fatalf("audit = %v", f.audit.actions)
	}
	if got, _ := f.repos.TransitLinks.GetLink(t.Context(), f.entry, f.exit); got.ID == 0 {
		t.Fatal("no tunnel to the exit")
	}

	// A refusal answers with its stable code and parameters, and writes nothing.
	f.audit.actions = nil
	status, out = f.do(http.MethodPut, "", `{"routes":[{"target":{"kind":"local"},"domains":["regexp:^a"]}]}`)
	if status != http.StatusBadRequest || out["code"] != route.CodeRegexp || out["params"].(map[string]any)["entry"] != "regexp:^a" {
		t.Fatalf("regexp: %d %v", status, out)
	}
	if len(f.audit.actions) != 0 {
		t.Fatal("a refused save was audited as a change")
	}
	if stored, _ := f.repos.TransitRoutes.Get(t.Context(), f.node); len(stored.Routes) != 2 {
		t.Fatal("a refused save changed the stored routes")
	}
	// Clearing leaves none.
	if status, out = f.do(http.MethodPut, "", `{"routes":[]}`); status != http.StatusOK || len(out["routes"].([]any)) != 0 {
		t.Fatalf("clear: %d %v", status, out)
	}
}

func TestRouteHandlerExplainsAServerThatCannotRunRoutes(t *testing.T) {
	f := newRouteFixture(t, nodeprotocol.CapabilityTransitLink)
	status, out := f.do(http.MethodGet, "", "")
	if status != http.StatusOK || out["supported"] != false || out["reason"] != route.CodeNoCapability {
		t.Fatalf("get: %d %v", status, out)
	}
	status, out = f.do(http.MethodPut, "", f.body())
	if status != http.StatusBadRequest || out["code"] != route.CodeNoCapability {
		t.Fatalf("put: %d %v", status, out)
	}
	if status, _ = f.do(http.MethodGet, "", ""); status != http.StatusOK {
		t.Fatal("the saved routes of an unsupported server are still shown")
	}
}

// A server that a node's routes leave through is not deleted from under them:
// the answer names the node and its routes.
func TestDeletingAnExitServerOfRoutesIsRefused(t *testing.T) {
	f := newRouteFixture(t, nodeprotocol.CapabilityTransitLink, nodeprotocol.CapabilityTransitRoute)
	if status, out := f.do(http.MethodPut, "", f.body()); status != http.StatusOK {
		t.Fatalf("put: %d %v", status, out)
	}
	h := NewAdminServersHandler(f.repos.XUIPanel, nil, f.repos.Node, f.audit, nil, nil)
	router := gin.New()
	router.DELETE("/api/admin/servers/:id", h.Delete)
	req := httptest.NewRequest(http.MethodDelete, "/api/admin/servers/"+strconv.FormatInt(f.exit, 10), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusConflict || out["code"] != "server_used_by_routes" {
		t.Fatalf("delete: %d %v", rec.Code, out)
	}
	used := out["used_by"].([]any)[0].(map[string]any)
	if used["node"] != "main" || used["routes"].([]any)[0] != "r1" {
		t.Fatalf("used by = %v", used)
	}
	if _, err := f.repos.XUIPanel.GetByID(t.Context(), f.exit); err != nil {
		t.Fatalf("the server was deleted: %v", err)
	}
}
