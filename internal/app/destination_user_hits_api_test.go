package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestBuildDestinationUserHitsIsAdministratorOnly(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	settings, err := f.a.settings.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("users/%d", f.user.ID)
	for _, role := range []domain.Role{domain.RoleOperator, domain.RoleUser} {
		u := &domain.User{UPN: fmt.Sprintf("user-hit-reader-%s@example.test", role), Role: role, Enabled: true, UUID: "user-hit-reader-" + string(role), SubToken: "user-hit-reader-token-" + string(role)}
		if err := f.a.repos.User.Create(t.Context(), u); err != nil {
			t.Fatal(err)
		}
		token, err := jwtutil.NewIssuer(f.a.cfg.JWTSecret, func() jwtutil.Params {
			return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: settings.JWTIssuer}
		}).IssueAccess(u.ID, u.UPN, u.Role, u.TokenVersion)
		if err != nil {
			t.Fatal(err)
		}
		if w := destinationListRequest(t, f.a, token, "GET", path, nil); w.Code != 403 {
			t.Fatalf("%s could read another account's destination summary", role)
		}
	}
	if w := destinationListRequest(t, f.a, "", "GET", path, nil); w.Code != 401 {
		t.Fatal("anonymous account hit read")
	}
}

type destinationUserHitsProbe struct {
	read func(context.Context, int64, time.Time, time.Time) (domain.DestRecentHits, error)
}

func (p destinationUserHitsProbe) ReadDestinationUserHits(ctx context.Context, id int64, since, until time.Time) (domain.DestRecentHits, error) {
	return p.read(ctx, id, since, until)
}

func TestBuildDestinationUserHitsFailureAndMissingReaderAreUnavailable(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	token := destinationRefreshAdminToken(t, f.a)
	path := fmt.Sprintf("users/%d", f.user.ID)
	f.a.destUserHitsRead = destinationUserHitsProbe{read: func(context.Context, int64, time.Time, time.Time) (domain.DestRecentHits, error) {
		return domain.DestRecentHits{Days: 12345, Items: []domain.DestUserHit{{Source: "private-host.test"}}}, errors.New("private destination SQL and account")
	}}
	if w := destinationListRequest(t, f.a, token, "GET", path, nil); w.Code != 503 || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "12345") {
		t.Fatal("partial failed user hits leaked or became success")
	}
	f.a.destUserHitsRead = nil
	if w := destinationListRequest(t, f.a, token, "GET", path, nil); w.Code != 503 {
		t.Fatal("missing user hit reader became empty success")
	}
}

func TestBuildDestinationUserHitsHoldsBackendAdmissionThroughRead(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()
	f.a.destUserHitsRead = destinationUserHitsProbe{read: func(ctx context.Context, _ int64, _, _ time.Time) (domain.DestRecentHits, error) {
		close(entered)
		select {
		case <-release:
			return domain.DestRecentHits{}, domain.ErrUnavailable
		case <-ctx.Done():
			return domain.DestRecentHits{}, ctx.Err()
		}
	}}
	done := make(chan error, 1)
	go func() { _, err := f.a.destinationUserAccess(t.Context(), f.user.ID); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("user access omitted hit read")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	err := f.a.operationGate.Exclusive(ctx, func(context.Context) error { t.Error("backend switch crossed user read"); return nil })
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("user read released backend admission before its dependency")
	}
	finish()
	if !errors.Is(<-done, domain.ErrUnavailable) {
		t.Fatal("failed hit dependency became empty success")
	}
	if err := f.a.operationGate.Exclusive(t.Context(), func(context.Context) error {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := f.a.destinationUserAccess(ctx, f.user.ID)
		if !errors.Is(err, context.Canceled) {
			t.Error("canceled user read crossed backend admission")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBuildDestinationUserHitsAvailabilityNeedsCurrentClientAndAppliedProof(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	saveWiringPolicy(t, f)
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy, protocol.CapabilityAuditHits}
	f.report.CoreEngine = "xray"
	f.report.Audit = fixtureAudit(t, f, 71)
	first := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	f.a.startDestinationAudit()
	waitAuditRows(t, f.a, "dest_hits", 1)
	f.a.destAudit.StopOffers()
	f.a.bgWG.Wait()
	token := destinationRefreshAdminToken(t, f.a)
	path := fmt.Sprintf("users/%d", f.user.ID)
	check := func(available bool, days int) {
		t.Helper()
		w := destinationListRequest(t, f.a, token, "GET", path, nil)
		var view struct {
			Available      *bool                  `json:"hits_available"`
			Recent         *domain.DestRecentHits `json:"recent_hits"`
			UsageAvailable *bool                  `json:"usage_available"`
			UsageNodes     []domain.DestUsageNode `json:"usage_nodes"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Available == nil || *view.Available != available || view.Recent == nil || view.Recent.Days != days || len(view.Recent.Items) != 1 || view.Recent.Items[0].Count != 4 || view.Recent.Items[0].SourceName != nil || view.Recent.Items[0].TopDests[0].Dest != "example.test" || view.Recent.Losses.Events != 2 || view.Recent.Losses.Unmatched != 3 || view.Recent.Losses.Scope != "panel" || view.Recent.Losses.Complete || view.UsageAvailable == nil || *view.UsageAvailable || view.UsageNodes == nil || len(view.UsageNodes) != 0 {
			t.Fatalf("account hits availability/history HTTP=%d available=%v", w.Code, view.Available)
		}
	}
	check(false, 7)
	if err := f.a.destCompiler.ObserveStatus(t.Context(), f.agent.AgentID, &protocol.PolicyStatus{State: "applied", Digest: protocol.PolicyDigest(first.Config.Body.Policy)}, f.report.Capabilities); err != nil {
		t.Fatal(err)
	}
	check(true, 7)
	if w := destinationListRequest(t, f.a, token, "PUT", "settings", map[string]any{"settings": map[string]any{"dest_hit_retention_days": 1, "dest_trial_retention_days": 1}}); w.Code != 200 {
		t.Fatal("retention update failed")
	}
	check(true, 1)
	if _, err := f.a.database.ExecContext(t.Context(), "DELETE FROM psp_clients WHERE user_id = ?", f.user.ID); err != nil {
		t.Fatal(err)
	}
	check(false, 1) // The node still collects, but is no longer this account's node.
	if w := serverAuditRequest(t, f.a, token, "PUT", fmt.Sprintf("/%d", f.agent.PanelID), map[string]any{"audit_collect": "off"}); w.Code != 200 {
		t.Fatal("collection off failed")
	}
	check(false, 1)
}

func TestBuildDestinationUserHitsRejectsUnknownOrMalformedReadParameters(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	token := destinationRefreshAdminToken(t, f.a)
	for _, suffix := range []string{"?usage=25h", "?usage=0d", "?usage=8d", "?usage=%zz", "?q=private-host.test", "?usage=24h&usage=7d", "?usage=24h&panel_id=1"} {
		if w := destinationListRequest(t, f.a, token, "GET", fmt.Sprintf("users/%d%s", f.user.ID, suffix), nil); w.Code != 400 {
			t.Fatalf("invalid user read parameters HTTP=%d", w.Code)
		}
	}
}

func TestBuildDestinationUserHitsRetainedWindowExcludesOlderBuckets(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	now := time.Now().UTC()
	if _, err := f.a.database.ExecContext(t.Context(), "INSERT INTO dest_hits (hour_ms,panel_id,user_id,source,action,dest,port,count,first_at,last_at) VALUES (?,?,?,?,?,?,?,?,?,?)", now.Add(-8*24*time.Hour).Truncate(time.Hour).UnixMilli(), f.agent.PanelID, f.user.ID, "p12", "block", "old-private.test", 443, 900, now.Add(-8*24*time.Hour), now.Add(-8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	w := destinationListRequest(t, f.a, destinationRefreshAdminToken(t, f.a), "GET", fmt.Sprintf("users/%d", f.user.ID), nil)
	var v struct {
		Available *bool                  `json:"hits_available"`
		Recent    *domain.DestRecentHits `json:"recent_hits"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Available == nil || *v.Available || v.Recent == nil || v.Recent.Days != 7 || v.Recent.Items == nil || len(v.Recent.Items) != 0 {
		t.Fatal("old history escaped displayed account window or no-current-collector state was unknown")
	}
}
