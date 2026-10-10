package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestBuildLegalCollectionTracksAppliedPolicyAndInvalidatesPublicETag(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	settings, err := f.a.repos.Settings.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	settings.LegalEnabled = true
	settings.NodePollSeconds = 60
	settings.DestHitRetentionDays = 43
	settings.DestTrialRetentionDays = 12
	settings.DestUsageRetentionDays = 9
	if err := f.a.repos.Settings.Save(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.repos.Legal.Publish(t.Context(), domain.LegalDraft{Kind: "privacy", Locale: "en-US", Content: "[[data-collection]]", PublishedBy: f.user.ID}); err != nil {
		t.Fatal(err)
	}
	token := destinationRefreshAdminToken(t, f.a)
	read := func(etag string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/legal/privacy?lang=en-US", nil)
		req.Header.Set("If-None-Match", etag)
		w := httptest.NewRecorder()
		f.a.server.Handler.ServeHTTP(w, req)
		return w
	}
	check := func(previous string, want []domain.LegalAccessCollection) string {
		t.Helper()
		w := read(previous)
		var doc domain.LegalPublicDocument
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &doc) != nil || !reflect.DeepEqual(doc.DataCollection.Access, want) {
			t.Fatalf("public disclosure HTTP=%d body=%s", w.Code, w.Body.String())
		}
		etag := w.Header().Get("ETag")
		if etag == "" || etag == previous || w.Header().Get("Cache-Control") != "no-cache" || doc.Version != 1 || doc.ConsentVersion != 1 {
			t.Fatal("collection changed document versions or kept stale ETag")
		}
		for _, private := range []string{f.user.UPN, f.agent.AgentID, f.node.ServerAddress, "panel_id", "agent_id", "policy_id"} {
			if strings.Contains(w.Body.String(), private) {
				t.Fatalf("public disclosure leaked %s", private)
			}
		}
		cached := read(etag)
		if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 || cached.Header().Get("ETag") != etag {
			t.Fatal("unchanged public representation lost conditional response")
		}
		return etag
	}
	etag := check("", []domain.LegalAccessCollection{})
	policy := saveWiringPolicy(t, f)
	f.report.CoreEngine = "xray"
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy, protocol.CapabilityAuditHits, "audit.usage.v1"}
	first := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if first.Config.Body == nil || first.Config.Body.Policy == nil {
		t.Fatal("missing collection candidate")
	}
	if w := read(etag); w.Code != http.StatusNotModified {
		t.Fatal("unacknowledged candidate became public collection")
	}
	if err := f.a.destCompiler.ObserveStatus(t.Context(), f.agent.AgentID, &protocol.PolicyStatus{State: "applied", Digest: protocol.PolicyDigest(first.Config.Body.Policy)}, f.report.Capabilities); err != nil {
		t.Fatal(err)
	}
	etag = check(etag, []domain.LegalAccessCollection{{Kind: "hits", Nodes: 1, RetentionDays: 43}})
	if w := destinationListRequest(t, f.a, token, "PUT", "settings", map[string]any{"settings": map[string]any{"dest_hit_retention_days": 44}}); w.Code != http.StatusOK {
		t.Fatalf("retention update HTTP=%d body=%s", w.Code, w.Body.String())
	}
	etag = check(etag, []domain.LegalAccessCollection{{Kind: "hits", Nodes: 1, RetentionDays: 44}})
	path := fmt.Sprintf("/%d", f.agent.PanelID)
	if w := serverAuditRequest(t, f.a, token, "PUT", path, map[string]any{"audit_collect": "off"}); w.Code != http.StatusOK {
		t.Fatal("off update failed")
	}
	etag = check(etag, []domain.LegalAccessCollection{})
	policy.Enabled = false
	if err := f.a.destDefinitions.SavePolicy(t.Context(), policy, policy.UpdatedAt, time.Now()); err != nil {
		t.Fatal(err)
	}
	if w := destinationListRequest(t, f.a, token, "POST", "publish", nil); w.Code != http.StatusOK {
		t.Fatal("rule-free publication failed")
	}
	if w := serverAuditRequest(t, f.a, token, "PUT", path, map[string]any{"audit_collect": "hits_and_usage"}); w.Code != http.StatusOK {
		t.Fatal("usage update failed")
	}
	empty := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if empty.Config.Body == nil || empty.Config.Body.Policy == nil || len(empty.Config.Body.Policy.Rules) != 0 {
		t.Fatal("missing rule-free usage candidate")
	}
	if w := read(etag); w.Code != http.StatusNotModified {
		t.Fatal("old applied revision became public usage collection")
	}
	if err := f.a.destCompiler.ObserveStatus(t.Context(), f.agent.AgentID, &protocol.PolicyStatus{State: "applied", Digest: protocol.PolicyDigest(empty.Config.Body.Policy)}, f.report.Capabilities); err != nil {
		t.Fatal(err)
	}
	check(etag, []domain.LegalAccessCollection{{Kind: "usage", Nodes: 1, RetentionDays: 9}})
}
