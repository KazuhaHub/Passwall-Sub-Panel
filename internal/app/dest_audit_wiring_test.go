package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// THE HIT INGEST IS WIRED IN THREE PLACES AND EACH FAILS SILENTLY WHEN FORGOTTEN:
// the sync handler's isolated decode of the audit subtree, nativeSync's sink
// (without SetAudit every batch is dropped on the floor), and the
// dest-audit-ingest worker Run starts (without it the queue is never drained).
// So this drives a batch from a node's report to the table through the
// application Build assembled and Run started: the handler decodes it, the sync
// answers 200 and the worker stores it. Shutdown drains the queue, so the row is
// read after it.
func TestBuildStoresAReportedHitBatch(t *testing.T) {
	ctx := t.Context()
	directory := t.TempDir()
	cfg := &config.Config{
		Listen: "127.0.0.1:0", JWTSecret: strings.Repeat("j", 48), EncryptionKey: strings.Repeat("e", 48),
		ConfigDir: filepath.Join(directory, "config"), DataDir: filepath.Join(directory, "data"),
	}
	a, err := Build(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlstore.ConfigureSecretKey("") })

	credential := strings.Repeat("fixture-dest-audit-credential-", 2)
	digest := sha256.Sum256([]byte(credential))
	agent := &domain.NodeAgent{AgentID: "agt_audit_wiring", Epoch: 7, CredentialSHA256: hex.EncodeToString(digest[:]),
		DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: "26.6.27"}
	panel := &domain.XUIPanel{Name: "audit-native", Kind: domain.PanelKindPSP, URL: "psp://" + agent.AgentID}
	if err := a.repos.NativeAgentProvisioning.Create(ctx, panel, agent); err != nil {
		t.Fatal(err)
	}
	if err := a.xuiPool.Add(panel); err != nil {
		t.Fatal(err)
	}
	user := &domain.User{
		UPN: "audit@example.test", Email: "audit@example.test", SSOProvider: domain.SSOProviderLocal, SSOSubject: "audit@example.test",
		Role: domain.RoleUser, Enabled: true, UUID: "55555555-5555-4555-8555-555555555555", SubToken: "fixture-audit-token", TrafficResetPeriod: domain.ResetMonthly,
	}
	if err := a.repos.User.Create(ctx, user); err != nil {
		t.Fatal(err)
	}

	runErr := make(chan error, 1)
	go func() { runErr <- a.Run() }()

	hour := time.Now().UTC().Truncate(time.Hour).UnixMilli()
	audit := map[string]any{
		"batch_id": strings.Repeat("c", 32),
		"hits": []map[string]any{{
			"hour": hour, "rule_id": "p7x1", "action": "observe", "subject": fmt.Sprintf("usr_%d", user.ID),
			"dest": "wired.example", "port": 443, "count": 4, "first_ms": hour + 10, "last_ms": hour + 20,
		}},
		"dropped_hits": 0, "dropped_trial": 0, "dropped_usage": 0, "unmatched": 0,
	}
	report := nodeprotocol.NodeReport{
		AgentID: agent.AgentID, ProtocolVersion: nodeprotocol.ProtocolVersion1, CoreEngine: "xray",
		Capabilities: []string{nodeprotocol.CapabilityAuditHits},
		Have: map[string]nodeprotocol.StreamState{
			nodeprotocol.StreamConfig: {}, nodeprotocol.StreamRoster: {}, nodeprotocol.StreamDirectives: {},
		},
	}
	raw, _ := json.Marshal(report)
	var generic map[string]any
	_ = json.Unmarshal(raw, &generic)
	generic["audit"] = audit
	payload, _ := json.Marshal(generic)
	req := httptest.NewRequest(http.MethodPost, "/v1/node/sync", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+credential)
	rec := httptest.NewRecorder()
	a.server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("the sync with an audit batch answered %d: %s", rec.Code, rec.Body.String())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	<-runErr

	rows, total, err := a.destRepos.Hits.List(ctx, domain.DestHitFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || rows[0].Dest != "wired.example" || rows[0].Source != "p7" || rows[0].Action != "observe" ||
		rows[0].UserID != user.ID || rows[0].PanelID != panel.ID || rows[0].Count != 4 {
		t.Fatalf("stored = %+v (%d): is nativeSync.SetAudit called, and is dest-audit-ingest started by Run?", rows, total)
	}
}
