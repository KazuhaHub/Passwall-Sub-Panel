package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/node"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/sourceguard"
)

type sourceGuardFixture struct {
	repos  ports.Repos
	router *gin.Engine
	nodeID int64
	panel  int64
}

func newSourceGuardFixture(t *testing.T) *sourceGuardFixture {
	t.Helper()
	sqlstore.ConfigureSecretKey("test-only-source-guard-handler-key")
	t.Cleanup(func() { sqlstore.ConfigureSecretKey("") })
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "guard.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := sqlstore.NewRepos(db)
	panel := &domain.Panel{Kind: domain.PanelKindPSP, Name: "land", URL: "psp://land"}
	if err := repos.XUIPanel.Save(t.Context(), panel); err != nil {
		t.Fatal(err)
	}
	agent := &domain.NodeAgent{AgentID: "land", PanelID: panel.ID, Epoch: 1, CredentialSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("land"))),
		DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: "26.6.27"}
	if err := repos.NodeAgent.Create(t.Context(), agent); err != nil {
		t.Fatal(err)
	}
	if err := repos.NodeAgent.UpdateProtocolObservation(t.Context(), "land", 1, []string{"transit.source_guard.v1"}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	two, three := 2000, 3000
	landing := &domain.Node{PanelID: panel.ID, InboundID: 1, DisplayName: "JP", ServerAddress: "land.example.com", StreamSettings: `{"network":"ws","security":"tls"}`,
		DesiredProtocol: "vless", DesiredPort: 443, Region: "JP", Enabled: true,
		Relays: []domain.RelayLine{
			{ID: "ext", Name: "ext", Address: "e.example", Port: 443, Enabled: true, Tags: []string{}, TrafficFactorMilli: &two, SourceCIDRs: []string{"192.0.2.0/24"}},
			{ID: "cdn", Name: "cdn", Mode: domain.RelayModeCDN, Domain: "c.example.com", Address: "104.16.1.1", Port: 8443, Enabled: true, Tags: []string{}, TrafficFactorMilli: &three},
		}}
	if err := repos.Node.Create(t.Context(), landing); err != nil {
		t.Fatal(err)
	}
	cf := sourceguard.NewCloudflare(repos.SourceGuard, nil)
	view := sourceguard.NewAdmin(repos.NodeAgent, cf, repos.SourceGuard)
	nodeSvc := node.New(repos.Node, nil, nil, nil, nil, nil, nil)
	nodeSvc.SetSourceGuardCheck(func(_ context.Context, _ int64) bool { return true })
	h := NewAdminSourceGuardHandler(view, nodeSvc, repos.Settings)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/source-guard/cloudflare", h.GetCloudflare)
	r.GET("/nodes/:id/source-guard", h.Get)
	r.POST("/nodes/:id/lines/:line_id/source-cidrs", h.AddSource)
	return &sourceGuardFixture{repos: repos, router: r, nodeID: landing.ID, panel: panel.ID}
}

func (f *sourceGuardFixture) do(method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func TestSourceGuardEditorViewAndAddRefusedSource(t *testing.T) {
	f := newSourceGuardFixture(t)
	ctx := t.Context()
	set := string(sourceguard.Key(f.nodeID, 2000))
	cdnSet := string(sourceguard.Key(f.nodeID, 3000))
	if err := f.repos.SourceGuard.RecordHits(ctx, f.panel, []domain.SourceGuardHit{
		{PanelID: f.panel, Set: set, ListenerNodeID: f.nodeID, IP: "198.51.100.77", Count: 5, LastSeen: time.Now()},
		// Inside the ext set: an identity used through the wrong line.
		{PanelID: f.panel, Set: set, ListenerNodeID: f.nodeID, IP: "192.0.2.9", Count: 2, LastSeen: time.Now()},
		// Inside Cloudflare's ranges, which the CDN default class owns: the ext
		// class's identity came through the CDN.
		{PanelID: f.panel, Set: set, ListenerNodeID: f.nodeID, IP: "173.245.48.5", Count: 1, LastSeen: time.Now()},
		// An unregistered source of the CDN class, whose line has no addresses of its own.
		{PanelID: f.panel, Set: cdnSet, ListenerNodeID: f.nodeID, IP: "203.0.113.5", Count: 3, LastSeen: time.Now()},
	}, time.Now().Add(-time.Hour), 100); err != nil {
		t.Fatal(err)
	}

	rec := f.do(http.MethodGet, fmt.Sprintf("/nodes/%d/source-guard", f.nodeID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var view sourceGuardNodeDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if !view.CanGuard || len(view.Lines) != 2 || view.Cloudflare.Source != "builtin" || view.Cloudflare.BuiltinDate == "" || !view.Cloudflare.AutoFetch {
		t.Fatalf("view = %+v", view)
	}
	ext, cdn := view.Lines[0], view.Lines[1]
	if len(ext.Effective) != 1 || ext.Effective[0] != "192.0.2.0/24" || len(ext.Refused) != 3 {
		t.Fatalf("ext = %+v", ext)
	}
	for _, r := range ext.Refused {
		switch r.IP {
		case "198.51.100.77":
			if r.Kind != "unregistered" || !r.Addable || r.Count != 5 {
				t.Fatalf("unregistered source: %+v", r)
			}
		default:
			if r.Kind != "wrong_line" || r.Addable {
				t.Fatalf("%s must be a wrong-line hit with no Add: %+v", r.IP, r)
			}
		}
	}
	if len(cdn.Refused) != 1 || cdn.Refused[0].Kind != "unregistered" || cdn.Refused[0].Addable {
		t.Fatalf("a CDN line on the Cloudflare default offers no Add: %+v", cdn.Refused)
	}
	if len(cdn.Effective) != len(sourceguard.BuiltinCloudflare) {
		t.Fatalf("a CDN line without addresses takes Cloudflare's: %+v", cdn)
	}

	// The one-click addition is refused for the CDN default line and for a
	// wrong-line hit, whatever the client sends.
	for name, c := range map[string]struct{ line, ip string }{
		"cdn default": {"cdn", "203.0.113.5"}, "wrong line": {"ext", "192.0.2.9"},
	} {
		if rec := f.do(http.MethodPost, fmt.Sprintf("/nodes/%d/lines/%s/source-cidrs", f.nodeID, c.line), map[string]string{"ip": c.ip}); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d: %s", name, rec.Code, rec.Body)
		}
	}

	// One click adds the address through the node save.
	rec = f.do(http.MethodPost, fmt.Sprintf("/nodes/%d/lines/ext/source-cidrs", f.nodeID), map[string]string{"ip": "198.51.100.77"})
	if rec.Code != http.StatusOK {
		t.Fatalf("add: status %d: %s", rec.Code, rec.Body)
	}
	stored, err := f.repos.Node.GetByID(ctx, f.nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.Relays[0].SourceCIDRs; len(got) != 2 || got[0] != "192.0.2.0/24" || got[1] != "198.51.100.77/32" {
		t.Fatalf("source cidrs = %v", got)
	}
	for _, hit := range func() []domain.SourceGuardHit { h, _ := f.repos.SourceGuard.ListHits(ctx, []int64{f.panel}); return h }() {
		if hit.IP == "198.51.100.77" {
			t.Fatalf("the warning stayed: %+v", hit)
		}
	}

	for name, c := range map[string]struct {
		path, ip string
		want     int
	}{
		"not an address": {fmt.Sprintf("/nodes/%d/lines/ext/source-cidrs", f.nodeID), "nope", http.StatusBadRequest},
		"unknown line":   {fmt.Sprintf("/nodes/%d/lines/zzz/source-cidrs", f.nodeID), "198.51.100.1", http.StatusNotFound},
	} {
		if rec := f.do(http.MethodPost, c.path, map[string]string{"ip": c.ip}); rec.Code != c.want {
			t.Errorf("%s: status %d: %s", name, rec.Code, rec.Body)
		}
	}
}

// The settings page's Cloudflare auto-fetch switch: on by default, set by the
// page, and kept by a client that does not send it.
func TestSettingsPutCloudflareAutoFetch(t *testing.T) {
	stored := storedEverywhere()
	stored.SourceGuardCloudflareFetchOff = false
	repo := &policySettingsRepo{settings: stored}
	put := func(extra map[string]any) ports.UISettings {
		t.Helper()
		body := sentDestOtherThan(stored.AccessControlSettings())
		body["login_mode"] = "local_only"
		for k, v := range extra {
			body[k] = v
		}
		payload, _ := json.Marshal(body)
		rec := requestRiskPolicy(destSettingsRouter(repo), http.MethodPut, "/api/admin/settings/ui", string(payload))
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT = %d: %s", rec.Code, rec.Body)
		}
		got, _, _ := repo.snapshot()
		return got
	}
	if got := put(nil); got.SourceGuardCloudflareFetchOff {
		t.Fatal("a client that does not know the switch turned auto-fetch off")
	}
	if got := put(map[string]any{"cloudflare_ips_auto_fetch": false}); !got.SourceGuardCloudflareFetchOff {
		t.Fatal("the switch did not turn auto-fetch off")
	}
	if got := put(nil); !got.SourceGuardCloudflareFetchOff {
		t.Fatal("a client that does not send the switch changed it")
	}
	if got := put(map[string]any{"cloudflare_ips_auto_fetch": true}); got.SourceGuardCloudflareFetchOff {
		t.Fatal("the switch did not turn auto-fetch on")
	}
}

func TestParseRelaysSourceCIDRs(t *testing.T) {
	empty := []string{}
	some := []string{"192.0.2.1"}
	lines, err := parseRelays([]relayLineDTO{
		{Address: "a.example", Enabled: true},
		{Address: "b.example", Enabled: true, SourceCIDRs: &empty},
		{Address: "c.example", Enabled: true, SourceCIDRs: &some},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !lines[0].SourceCIDRsUnresolved || lines[1].SourceCIDRsUnresolved || len(lines[1].SourceCIDRs) != 0 || lines[2].SourceCIDRs[0] != "192.0.2.1" {
		t.Fatalf("lines = %+v", lines)
	}
	out := relayDTOsOn(nil, []domain.RelayLine{{ID: "x", SourceCIDRs: []string{"10.0.0.0/8"}}, {ID: "t", Mode: domain.RelayModeTunnel}}, false)
	if out[0].SourceCIDRs == nil || (*out[0].SourceCIDRs)[0] != "10.0.0.0/8" || out[1].SourceCIDRs != nil {
		t.Fatalf("dtos = %+v", out)
	}
}
