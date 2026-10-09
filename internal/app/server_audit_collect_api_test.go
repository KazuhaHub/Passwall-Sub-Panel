package app

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func serverAuditRequest(t *testing.T, a *App, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, "/api/admin/servers"+path, strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	a.server.Handler.ServeHTTP(w, req)
	return w
}

func TestBuildServerAuditCollectPersistsAtomicRevisionAndOmissionPreserves(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	path := fmt.Sprintf("/%d", f.agent.PanelID)
	before, err := a.repos.XUIPanel.GetByID(t.Context(), f.agent.PanelID)
	if err != nil || before.AuditCollect != domain.AuditCollectHits || before.AuditCollectRevision != 1 {
		t.Fatal("native collection fixture missing")
	}
	w := serverAuditRequest(t, a, token, "GET", "", nil)
	var listed struct {
		Items []struct {
			ID      int64  `json:"id"`
			Collect string `json:"audit_collect"`
		} `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &listed) != nil || len(listed.Items) != 1 || listed.Items[0].ID != before.ID || listed.Items[0].Collect != "hits" {
		t.Fatal("native server list omitted saved collection mode")
	}
	for _, step := range []struct {
		input    map[string]any
		collect  domain.AuditCollect
		revision int64
	}{
		{map[string]any{"audit_collect": "off", "remark": "collection disabled"}, domain.AuditCollectOff, 2},
		{map[string]any{"audit_collect": "off"}, domain.AuditCollectOff, 2},
		{map[string]any{"remark": "display only"}, domain.AuditCollectOff, 2},
		{map[string]any{"audit_collect": nil}, domain.AuditCollectOff, 2},
		{map[string]any{"audit_collect": "hits", "audit_collect_revision": 999}, domain.AuditCollectHits, 3},
		{map[string]any{"audit_collect": "hits_and_usage"}, domain.AuditCollectHitsAndUsage, 4},
	} {
		w = serverAuditRequest(t, a, token, "PUT", path, step.input)
		var view struct {
			Collect string `json:"audit_collect"`
		}
		got, err := a.repos.XUIPanel.GetByID(t.Context(), before.ID)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Collect != string(step.collect) || err != nil || got.AuditCollect != step.collect || got.AuditCollectRevision != step.revision {
			t.Fatalf("collection save lost atomic revision or omitted input: HTTP=%d expected=%s/%d", w.Code, step.collect, step.revision)
		}
		if got.URL != before.URL || got.Kind != before.Kind || got.UpdateChannel != before.UpdateChannel || got.PanelVersion != before.PanelVersion || got.XrayVersion != before.XrayVersion {
			t.Fatal("collection edit rewrote native identity or runtime metadata")
		}
	}
	state, err := a.destDefinitions.State(t.Context())
	if err != nil || state.Generation != 0 {
		t.Fatal("collection settings advanced definition generation")
	}
}

func TestBuildServerAuditCollectRejectsInvalidAndThirdPartyWithoutPartialEdits(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	for _, kind := range []domain.PanelKind{domain.PanelKindPSP, domain.PanelKind3XUI, domain.PanelKindSUI} {
		id := f.agent.PanelID
		if kind != domain.PanelKindPSP {
			p := &domain.Panel{Name: "collection boundary " + string(kind), Kind: kind, URL: "https://panel.example.test", APIToken: "fixture-api-token"}
			if err := a.repos.XUIPanel.Save(t.Context(), p); err != nil {
				t.Fatal(err)
			}
			id = p.ID
		}
		before, err := a.repos.XUIPanel.GetByID(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		inputs := []any{"", "OFF", "invalid", 1, true}
		if kind != domain.PanelKindPSP {
			inputs = append(inputs, "off", "hits", "hits_and_usage")
		}
		for _, input := range inputs {
			w := serverAuditRequest(t, a, token, "PUT", fmt.Sprintf("/%d", id), map[string]any{"audit_collect": input, "remark": "must not commit"})
			after, err := a.repos.XUIPanel.GetByID(t.Context(), id)
			if w.Code != 400 || err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("invalid collection input partially edited %s: HTTP=%d", kind, w.Code)
			}
		}
	}
}

func TestBuildServerAuditCollectStorageFailureRollsBackMetadataAndRevision(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	before, err := a.repos.XUIPanel.GetByID(t.Context(), f.agent.PanelID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.ExecContext(t.Context(), "CREATE TRIGGER fail_server_collect BEFORE UPDATE OF audit_collect_revision ON xui_panels BEGIN SELECT RAISE(ABORT, 'private-collection-storage-marker'); END"); err != nil {
		t.Fatal(err)
	}
	w := serverAuditRequest(t, a, token, "PUT", fmt.Sprintf("/%d", before.ID), map[string]any{"audit_collect": "off", "remark": "must roll back"})
	after, err := a.repos.XUIPanel.GetByID(t.Context(), before.ID)
	if w.Code != 500 || err != nil || !reflect.DeepEqual(before, after) || strings.Contains(w.Body.String(), "private-collection-storage-marker") {
		t.Fatal("failed collection save committed partial metadata/revision or leaked a storage error")
	}
}

func TestBuildServerAuditCollectChangeRebuildsWarmNativeCandidate(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	saveWiringPolicy(t, f)
	token := destinationRefreshAdminToken(t, f.a)
	path := fmt.Sprintf("/%d", f.agent.PanelID)
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy, "audit.hits.v1", "audit.usage.v1"}
	first := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if first.Config.Body == nil || first.Config.Body.Policy == nil || first.Config.Body.Policy.Collect != protocol.CollectHits || first.Config.Body.Policy.CollectRevision != 1 {
		t.Fatal("initial native collection candidate missing")
	}
	before, err := f.a.destDefinitions.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		saved    string
		collect  protocol.CollectLevel
		revision uint64
	}{
		{"off", "", 0}, {"hits", protocol.CollectHits, 3}, {"hits_and_usage", protocol.CollectHitsAndUsage, 4},
	} {
		w := serverAuditRequest(t, f.a, token, "PUT", path, map[string]any{"audit_collect": step.saved})
		if w.Code != 200 {
			t.Fatalf("collection save HTTP=%d", w.Code)
		}
		response := syncNativeCacheFixture(t, f.a, f.credential, f.report)
		if response.Config.Body == nil || response.Config.Body.Policy == nil || response.Config.Body.Policy.Collect != step.collect || response.Config.Body.Policy.CollectRevision != step.revision || len(response.Config.Body.Policy.Rules) != 1 {
			t.Fatal("collection settings reused a stale warm candidate or removed policy enforcement")
		}
		stored, err := f.a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, false)
		if err != nil || stored.CollectEffective != string(step.collect) {
			t.Fatal("candidate did not persist effective collection")
		}
	}
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy, "audit.hits.v1"}
	limited := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if limited.Config.Body == nil || limited.Config.Body.Policy == nil || limited.Config.Body.Policy.Collect != protocol.CollectHits || limited.Config.Body.Policy.CollectRevision != 4 {
		t.Fatal("saved usage mode overrode the node's lower collection capability")
	}
	after, err := f.a.destDefinitions.State(t.Context())
	if err != nil || after.Generation != before.Generation || after.PublishedGeneration != before.PublishedGeneration {
		t.Fatal("collection revision altered definition/publication generations")
	}
}

func TestBuildServerAuditCollectAdministratorBoundary(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	s, err := f.a.settings.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	issuer := jwtutil.NewIssuer(f.a.cfg.JWTSecret, func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: s.JWTIssuer}
	})
	tokens := []string{""}
	for _, role := range []domain.Role{domain.RoleUser, domain.RoleOperator} {
		u := &domain.User{UPN: "collection-" + string(role) + "@example.test", Role: role, Enabled: true, UUID: "collection-" + string(role), SubToken: "collection-token-" + string(role)}
		if err := f.a.repos.User.Create(t.Context(), u); err != nil {
			t.Fatal(err)
		}
		token, err := issuer.IssueAccess(u.ID, u.UPN, u.Role, u.TokenVersion)
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, token)
	}
	for i, token := range tokens {
		status := 403
		if i == 0 {
			status = 401
		}
		for _, route := range []struct{ method, path string }{{"GET", ""}, {"PUT", fmt.Sprintf("/%d", f.agent.PanelID)}} {
			w := serverAuditRequest(t, f.a, token, route.method, route.path, map[string]any{"audit_collect": "off"})
			if w.Code != status {
				t.Fatalf("collection authorization HTTP=%d expected=%d", w.Code, status)
			}
		}
	}
	p, err := f.a.repos.XUIPanel.GetByID(t.Context(), f.agent.PanelID)
	if err != nil || p.AuditCollect != domain.AuditCollectHits || p.AuditCollectRevision != 1 {
		t.Fatal("denied collection request changed saved control")
	}
}
