package handler

import (
	"encoding/json"
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
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/corefixtures"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/front"
)

type frontFixture struct {
	svc    *front.Service
	repos  ports.Repos
	router *gin.Engine
	audit  *upgradeGateAudit
	panel  int64
	node   int64
}

func newFrontFixture(t *testing.T, engine domain.NodeCoreEngine) *frontFixture {
	t.Helper()
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "front.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := sqlstore.NewRepos(db)
	panel := &domain.Panel{Kind: domain.PanelKindPSP, Name: "hk", URL: "psp://agt_front"}
	if err := repos.XUIPanel.Save(t.Context(), panel); err != nil {
		t.Fatal(err)
	}
	agent := &domain.NodeAgent{
		AgentID: "agt_front", PanelID: panel.ID, Epoch: 1, CredentialSHA256: strings.Repeat("c", 64),
		DesiredCoreEngine: engine, DesiredCoreVersion: "26.6.27", ObservedAddress: "198.51.100.10",
	}
	if err := repos.NodeAgent.Create(t.Context(), agent); err != nil {
		t.Fatal(err)
	}
	if err := repos.NodeAgent.UpdateProtocolObservation(t.Context(), agent.AgentID, 1,
		[]string{nodeprotocol.CapabilityTransitSNI}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := repos.NodeAgent.UpdateCoreObservation(t.Context(), agent.AgentID, domain.NodeCoreXray); err != nil {
		t.Fatal(err)
	}
	node := &domain.Node{
		PanelID: panel.ID, InboundID: 1, DisplayName: "reality", ServerAddress: "198.51.100.10", DesiredProtocol: "vless",
		DesiredPort: 443, Enabled: true, Region: "HK",
		StreamSettings: `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["a.example.com"]}}`,
	}
	if err := repos.Node.Create(t.Context(), node); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	f := &frontFixture{audit: &upgradeGateAudit{}, panel: panel.ID, node: node.ID}
	f.svc = front.New(repos.TransitFronts, repos.Node, repos.XUIPanel, repos.NodeAgent, repos.Transit, repos.TransitLinks, nil)
	f.repos = repos
	h := NewAdminFrontHandler(f.svc, f.audit)
	f.router = gin.New()
	base := "/api/admin/servers/:id"
	f.router.GET(base+"/fronts", h.List)
	f.router.GET(base+"/fronts/options", h.Options)
	f.router.GET(base+"/fronts/suggest", h.Suggest)
	f.router.POST(base+"/fronts", h.Create)
	f.router.PUT(base+"/fronts/:fid", h.Update)
	f.router.DELETE(base+"/fronts/:fid", h.Delete)
	f.router.GET(base+"/ports", h.Ports)
	return f
}

func (f *frontFixture) do(method, path, body string) (int, map[string]any) {
	req := httptest.NewRequest(method, "/api/admin/servers/"+strconv.FormatInt(f.panel, 10)+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (f *frontFixture) body(confirm bool) string {
	return `{"port":443,"listeners":[{"node_id":` + strconv.FormatInt(f.node, 10) + `,"loopback_port":30001}],
"routes":[{"server_names":["a.example.com"],"target":{"kind":"listener","node_id":` + strconv.FormatInt(f.node, 10) + `}}],
"default":{"host":"www.example.com","port":443},"confirm_move_listeners":` + strconv.FormatBool(confirm) + `}`
}

func TestFrontHandlerRefusesWithACodeAndSavesAfterTheConfirmation(t *testing.T) {
	f := newFrontFixture(t, domain.NodeCoreXray)
	status, out := f.do(http.MethodPost, "/fronts", f.body(false))
	if status != http.StatusConflict || out["code"] != front.CodeConfirmRequired {
		t.Fatalf("unconfirmed: %d %v", status, out)
	}
	if len(f.audit.actions) != 0 {
		t.Fatal("a refused save was audited as a change")
	}
	status, out = f.do(http.MethodPost, "/fronts", f.body(true))
	if status != http.StatusCreated {
		t.Fatalf("confirmed: %d %v", status, out)
	}
	if !f.audit.saw("transit_front_created") {
		t.Fatalf("audit = %v", f.audit.actions)
	}
	id := int(out["front"].(map[string]any)["id"].(float64))

	status, out = f.do(http.MethodGet, "/fronts", "")
	if status != http.StatusOK || len(out["fronts"].([]any)) != 1 || out["support"].(map[string]any)["supported"] != true {
		t.Fatalf("list: %d %v", status, out)
	}
	status, out = f.do(http.MethodGet, "/ports", "")
	kinds := map[string]bool{}
	for _, p := range out["ports"].([]any) {
		kinds[p.(map[string]any)["kind"].(string)] = true
	}
	if status != http.StatusOK || !kinds["front"] || !kinds["front_loopback"] || !kinds["probe_range"] || kinds["listener"] {
		t.Fatalf("ports: %d %v", status, kinds)
	}
	status, out = f.do(http.MethodGet, "/fronts/suggest?what=loopback", "")
	if status != http.StatusOK || out["port"] == nil {
		t.Fatalf("suggest: %d %v", status, out)
	}
	if status, _ = f.do(http.MethodGet, "/fronts/suggest?what=nothing", ""); status != http.StatusBadRequest {
		t.Fatalf("suggest of nothing: %d", status)
	}

	status, out = f.do(http.MethodPut, "/fronts/"+strconv.Itoa(id), strings.Replace(f.body(false), `"port":443`, `"port":8443`, 1))
	if status != http.StatusBadRequest || out["code"] != front.CodeListenerPortMismatch {
		t.Fatalf("moving the shared port away from the listener: %d %v", status, out)
	}
	if status, out = f.do(http.MethodDelete, "/fronts/"+strconv.Itoa(id), ""); status != http.StatusConflict || out["code"] != front.CodeConfirmRequired {
		t.Fatalf("an unconfirmed delete of a front with a listener behind it: %d %v", status, out)
	}
	if status, _ = f.do(http.MethodDelete, "/fronts/"+strconv.Itoa(id)+"?confirm=true", ""); status != http.StatusOK || !f.audit.saw("transit_front_deleted") {
		t.Fatalf("delete: %d %v", status, f.audit.actions)
	}
}

func TestFrontHandlerRefusesASingBoxServer(t *testing.T) {
	f := newFrontFixture(t, domain.NodeCoreSingBox)
	status, out := f.do(http.MethodPost, "/fronts", f.body(true))
	if status != http.StatusBadRequest || out["code"] != front.CodeNotXray {
		t.Fatalf("sing-box: %d %v", status, out)
	}
	status, out = f.do(http.MethodGet, "/fronts", "")
	if status != http.StatusOK || out["support"].(map[string]any)["supported"] != false || out["support"].(map[string]any)["reason"] != front.CodeNotXray {
		t.Fatalf("support: %d %v", status, out)
	}
}

func TestFrontOptionsExplainWhyAListenerCannotShare(t *testing.T) {
	f := newFrontFixture(t, domain.NodeCoreXray)
	status, out := f.do(http.MethodGet, "/fronts/options", "")
	if status != http.StatusOK {
		t.Fatalf("options: %d", status)
	}
	listeners := out["listeners"].([]any)
	first := listeners[0].(map[string]any)
	if len(listeners) != 1 || first["frontable"] != true || first["server_names"].([]any)[0] != "a.example.com" {
		t.Fatalf("listeners = %v", listeners)
	}
}

func TestFrontsListSaysWhyASavedFrontIsNotInEffect(t *testing.T) {
	f := newFrontFixture(t, domain.NodeCoreXray)
	if status, out := f.do(http.MethodPost, "/fronts", f.body(true)); status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, out)
	}
	// A stray enabled listener on the shared port: Node would refuse the config.
	stray := &domain.Node{PanelID: f.panel, InboundID: 2, DisplayName: "stray", ServerAddress: "x.example", DesiredProtocol: "vless",
		DesiredPort: 443, Enabled: true, Region: "HK"}
	if err := f.repos.Node.Create(t.Context(), stray); err != nil {
		t.Fatal(err)
	}
	status, out := f.do(http.MethodGet, "/fronts", "")
	withheld := out["withheld"].([]any)
	if status != http.StatusOK || len(withheld) != 1 || withheld[0].(map[string]any)["reason"] != front.WithheldListenerOnPort {
		t.Fatalf("withheld: %d %v", status, out)
	}
	// Engine moved off Xray: the whole server's fronts are inactive, with the reason.
	if err := f.repos.NodeAgent.UpdateCoreSelection(t.Context(), "agt_front", domain.NodeCoreSingBox, "1.14.0", false); err != nil {
		t.Fatal(err)
	}
	status, out = f.do(http.MethodGet, "/ports", "")
	inactive := ""
	for _, p := range out["ports"].([]any) {
		if p.(map[string]any)["kind"] == "front" {
			inactive, _ = p.(map[string]any)["inactive"].(string)
		}
	}
	if status != http.StatusOK || inactive != front.CodeNotXray {
		t.Fatalf("ports: %d inactive=%q", status, inactive)
	}
}

func TestSelectingAnotherEngineAsksWhileSharedPortsAreSaved(t *testing.T) {
	f := newFrontFixture(t, domain.NodeCoreXray)
	if status, out := f.do(http.MethodPost, "/fronts", f.body(true)); status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, out)
	}
	client := &nativeCoreClientStub{}
	h := &AdminServersHandler{
		repo:        nativeCorePanelRepo{panel: &domain.XUIPanel{ID: f.panel, Kind: domain.PanelKindPSP}},
		pool:        fakeWebCertPool{client: client},
		coreCatalog: corefixtures.Static{},
		frontSvc:    f.svc,
	}
	request := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(f.panel, 10)}}
		ctx.Request = httptest.NewRequest(http.MethodPost, "/select-core", strings.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")
		h.SelectCore(ctx)
		return rec
	}
	asked := request(`{"engine":"sing-box","version":"1.14.0"}`)
	if asked.Code != http.StatusConflict || client.installed != "" || !strings.Contains(asked.Body.String(), `"code":"confirm_required"`) ||
		!strings.Contains(asked.Body.String(), "reality") {
		t.Fatalf("unconfirmed: %d %s selected=%q", asked.Code, asked.Body.String(), client.installed)
	}
	if ok := request(`{"engine":"sing-box","version":"1.14.0","confirm_fronts":true}`); ok.Code != http.StatusAccepted || client.installed != "1.14.0" {
		t.Fatalf("confirmed: %d %s", ok.Code, ok.Body.String())
	}
	// Staying on Xray never asks.
	client.installed = ""
	if ok := request(`{"engine":"xray","version":"26.6.27"}`); ok.Code != http.StatusAccepted {
		t.Fatalf("xray: %d %s", ok.Code, ok.Body.String())
	}
}

func TestDeletingAServerDeletesItsFrontsWithAnAuditEntryEach(t *testing.T) {
	f := newFrontFixture(t, domain.NodeCoreXray)
	if status, out := f.do(http.MethodPost, "/fronts", f.body(true)); status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, out)
	}
	h := &AdminServersHandler{fronts: f.repos.TransitFronts, audit: f.audit}
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodDelete, "/servers", nil)
	h.deleteFrontsOf(ctx, f.panel)
	left, err := f.repos.TransitFronts.ListByPanel(t.Context(), f.panel)
	if err != nil || len(left) != 0 {
		t.Fatalf("fronts left: %v %v", left, err)
	}
	if !f.audit.saw("transit_front_deleted") {
		t.Fatalf("audit = %v", f.audit.actions)
	}
}

func TestAListenerJoiningASharedPortIsAuditedByName(t *testing.T) {
	f := newFrontFixture(t, domain.NodeCoreXray)
	h := &AdminNodeHandler{audit: f.audit}
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/nodes/1/inbound", nil)
	c, conf := frontContext(ctx, &frontConfirmDTO{LoopbackPort: 30044}, false)
	if !front.ConfirmationFrom(c).Confirmed || conf.LoopbackPort != 30044 {
		t.Fatalf("confirmation = %+v", conf)
	}
	conf.Joined = &front.Joined{PanelID: f.panel, FrontID: 1, Port: 443, NodeID: 7, ServerNames: []string{"b.example.com"}, LoopbackPort: 30044}
	h.auditFrontJoin(ctx, conf)
	if !f.audit.saw("transit_front_listener_joined") {
		t.Fatalf("audit = %v", f.audit.actions)
	}
	// Nothing joined, nothing audited.
	f.audit.actions = nil
	h.auditFrontJoin(ctx, &front.Confirmation{Confirmed: true})
	if len(f.audit.actions) != 0 {
		t.Fatal("an audit entry without a join")
	}
}
