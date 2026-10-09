package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func requestDestinationRetry(a *App, token, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/admin/dest/agents/"+id+"/retry", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	a.server.Handler.ServeHTTP(response, req)
	return response
}

func TestBuildDestinationRetryIgnoresOldReceiptAndRestoresWarmAllowlist(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	if _, err := f.a.database.ExecContext(t.Context(), "INSERT INTO dest_group_modes (group_id, mode, stage, list_ids, updated_at) VALUES (?, ?, ?, ?, ?)", f.group.ID, "allowlist", "trial", "[]", time.Now()); err != nil {
		t.Fatal(err)
	}
	saveWiringPolicy(t, f)
	queued := make(chan func(context.Context), 8)
	f.a.user.SetBackgroundRunner(func(name string, work func(context.Context)) {
		if name == "user.resync-group-members" {
			queued <- work
		}
	})
	runQueued := func() {
		t.Helper()
		select {
		case work := <-queued:
			work(t.Context())
		case <-time.After(3 * time.Second):
			t.Fatal("retry did not enqueue allowlist members after commit")
		}
	}
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy}
	first := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	runQueued()
	if first.Config.Body == nil || first.Config.Body.Policy == nil {
		t.Fatal("fixture minted no active policy")
	}
	rejected := &protocol.PolicyStatus{State: "rejected", Digest: protocol.PolicyDigest(first.Config.Body.Policy), IssueCode: protocol.IssueDestinationPolicyRejected}
	// Commit a matching rejection before a fallback config is minted. This is
	// the boundary where clearing locks alone would be undone by the old receipt.
	if err := f.a.destCompiler.ObserveStatus(t.Context(), f.agent.AgentID, rejected, f.report.Capabilities); err != nil {
		t.Fatal(err)
	}
	runQueued()
	if wiringAttachmentCount(t, f) != 0 {
		t.Fatal("fixture did not warm ineligible removal")
	}
	token := destinationRefreshAdminToken(t, f.a)
	response := requestDestinationRetry(f.a, token, f.agent.AgentID)
	if response.Code != http.StatusOK {
		t.Fatalf("retry HTTP=%d, want 200", response.Code)
	}
	var view struct {
		RetryRequested bool `json:"retry_requested"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil || !view.RetryRequested {
		t.Fatal("retry did not report its committed reset")
	}
	state, err := f.a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, false)
	if err != nil || state.RejectedGeneration != 0 || state.FallbackReason != "" || state.MintedAt != nil {
		t.Fatal("route did not atomically reset and retire the old receipt")
	}
	runQueued()
	if wiringAttachmentCount(t, f) != 1 {
		t.Fatal("explicit retry did not invalidate the warm ineligible selector")
	}
	f.report.PolicyStatus = rejected
	again := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if again.Config.Body == nil || again.Config.Body.Policy == nil || protocol.PolicyDigest(again.Config.Body.Policy) != rejected.Digest {
		t.Fatal("same-generation manual retry did not resend the desired policy")
	}
	state, err = f.a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, false)
	if err != nil || state.FallbackReason != "" || state.RejectedGeneration != 0 || state.MintedAt == nil {
		t.Fatal("stale rejected receipt relocked retry or the mint did not rearm confirmation")
	}
	if response = requestDestinationRetry(f.a, token, f.agent.AgentID); response.Code != http.StatusOK {
		t.Fatal("idempotent retry failed")
	}
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil || view.RetryRequested {
		t.Fatal("idempotent retry scheduled another reset")
	}
	select {
	case <-queued:
		t.Fatal("idempotent retry repeated allowlist resync")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestBuildDestinationRetryIsAdminOnlyAndKeepsUnknownAgentMissing(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	admin := destinationRefreshAdminToken(t, f.a)
	operator := &domain.User{UPN: "retry-operator@example.test", Email: "retry-operator@example.test", SSOProvider: domain.SSOProviderLocal, SSOSubject: "retry-operator@example.test", Role: domain.RoleOperator, Enabled: true, UUID: "77777777-7777-4777-8777-777777777777", SubToken: "retry-operator-token"}
	if err := f.a.repos.User.Create(t.Context(), operator); err != nil {
		t.Fatal(err)
	}
	settings, err := f.a.settings.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	operatorToken, err := jwtutil.NewIssuer(f.a.cfg.JWTSecret, func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: settings.JWTIssuer}
	}).IssueAccess(operator.ID, operator.UPN, operator.Role, operator.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	regular := &domain.User{UPN: "retry-user@example.test", Email: "retry-user@example.test", SSOProvider: domain.SSOProviderLocal, SSOSubject: "retry-user@example.test", Role: domain.RoleUser, Enabled: true, UUID: "66666666-6666-4666-8666-666666666666", SubToken: "retry-regular-user-token"}
	if err := f.a.repos.User.Create(t.Context(), regular); err != nil {
		t.Fatal(err)
	}
	regularToken, err := jwtutil.NewIssuer(f.a.cfg.JWTSecret, func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: settings.JWTIssuer}
	}).IssueAccess(regular.ID, regular.UPN, regular.Role, regular.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		token, id string
		want      int
	}{{"", f.agent.AgentID, 401}, {operatorToken, f.agent.AgentID, 403}, {regularToken, f.agent.AgentID, 403}, {admin, "agt_missing_retry", 404}, {admin, f.agent.AgentID, 200}} {
		if response := requestDestinationRetry(f.a, sample.token, sample.id); response.Code != sample.want {
			t.Fatalf("retry HTTP=%d want=%d", response.Code, sample.want)
		}
	}
}
