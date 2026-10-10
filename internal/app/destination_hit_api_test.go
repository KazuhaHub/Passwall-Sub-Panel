package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestBuildDestinationHitsReturnsStoredDetailsAndAllAggregations(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	f.report.Capabilities = []string{protocol.CapabilityAuditHits}
	f.report.CoreEngine = "xray"
	f.report.Audit = fixtureAudit(t, f, 61)
	_ = syncNativeCacheFixture(t, f.a, f.credential, f.report)
	f.a.startDestinationAudit()
	waitAuditRows(t, f.a, "dest_hits", 1)
	f.a.destAudit.StopOffers()
	f.a.bgWG.Wait()
	token := destinationRefreshAdminToken(t, f.a)
	w := destinationListRequest(t, f.a, token, "GET", "hits?since=24h", nil)
	var page struct {
		Items   []domain.DestHitRecord
		Total   int64
		GroupBy string `json:"group_by"`
		Summary domain.DestHitSummary
		Losses  domain.DestAuditLosses
		Dropped int64 `json:"dropped_in_range"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Total != 1 || len(page.Items) != 1 || page.Summary != (domain.DestHitSummary{Block: 4, Users: 1}) || page.Losses.Events != 2 || page.Losses.Unmatched != 3 || page.Losses.Rows != 0 || page.Losses.Complete || page.Dropped != 0 {
		t.Fatalf("assembled hits response HTTP=%d", w.Code)
	}
	if page.Items[0].UserUPN == nil || *page.Items[0].UserUPN != f.user.UPN || page.Items[0].SourceName != nil || page.Items[0].Source != "p12" || page.Items[0].Dest != "example.test" {
		t.Fatal("stored metadata or frozen deleted source changed")
	}
	for _, group := range []string{"site", "user", "policy"} {
		w := destinationListRequest(t, f.a, token, "GET", "hits?since=24h&group_by="+group, nil)
		var v struct {
			Items   []domain.DestHitGroup
			Total   int64
			GroupBy string `json:"group_by"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Total != 1 || len(v.Items) != 1 || v.Items[0].Count != 4 || v.GroupBy != group {
			t.Fatalf("assembled %s grouping HTTP=%d", group, w.Code)
		}
	}
	q := url.Values{"since": {fmt.Sprint(time.Now().Add(-time.Hour).UnixMilli())}, "until": {time.Now().UTC().Format(time.RFC3339Nano)}, "user_id": {fmt.Sprint(f.user.ID)}, "panel_id": {fmt.Sprint(f.agent.PanelID)}, "action": {"observe"}, "source_kind": {"policy"}, "q": {"private-destination.test"}}
	w = destinationListRequest(t, f.a, token, "GET", "hits?"+q.Encode(), nil)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Total != 0 || len(page.Items) != 0 || page.Summary.Block != 4 || page.Losses.Events != 2 {
		t.Fatal("empty list filters corrupted independent counters")
	}
}

type destinationHitReadProbe struct {
	read func(context.Context, domain.DestHitQuery) (domain.DestHitPage, error)
}

func (p destinationHitReadProbe) ReadDestinationHits(ctx context.Context, q domain.DestHitQuery) (domain.DestHitPage, error) {
	return p.read(ctx, q)
}

func TestBuildDestinationHitsFailureIsUnavailableWithoutPartialResults(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	for _, failure := range []error{domain.ErrUnavailable, errors.New("private destination SQL and account values")} {
		a.destHitsRead = destinationHitReadProbe{read: func(context.Context, domain.DestHitQuery) (domain.DestHitPage, error) {
			return domain.DestHitPage{Total: 12345, Records: []domain.DestHitRecord{{Dest: "private-host.test"}}}, failure
		}}
		w := destinationListRequest(t, a, token, "GET", "hits", nil)
		if w.Code != 503 || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "12345") {
			t.Fatalf("failed read became partial success or leaked details HTTP=%d", w.Code)
		}
	}
	a.destHitsRead = nil
	if w := destinationListRequest(t, a, token, "GET", "hits", nil); w.Code != 503 {
		t.Fatal("missing reader became an empty success")
	}
}

func TestBuildDestinationHitsRetainsBackendAdmissionForTheWholeRead(t *testing.T) {
	a := buildDestinationListsFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()
	a.destHitsRead = destinationHitReadProbe{read: func(ctx context.Context, _ domain.DestHitQuery) (domain.DestHitPage, error) {
		close(entered)
		select {
		case <-release:
			return domain.DestHitPage{}, domain.ErrUnavailable
		case <-ctx.Done():
			return domain.DestHitPage{}, ctx.Err()
		}
	}}
	done := make(chan error, 1)
	go func() { _, err := a.destinationHits(t.Context(), domain.DestHitQuery{}); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("records read did not enter its reader")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	err := a.operationGate.Exclusive(ctx, func(context.Context) error {
		t.Error("backend switch crossed an active records read")
		return nil
	})
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("records read released admission too early")
	}
	finish()
	if err := <-done; !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("failed reader returned successful empty records")
	}
	if err := a.operationGate.Exclusive(t.Context(), func(context.Context) error {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := a.destinationHits(ctx, domain.DestHitQuery{}); !errors.Is(err, context.Canceled) {
			t.Error("canceled records read crossed backend admission")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBuildDestinationHitsRejectsMalformedAndOverlongQueries(t *testing.T) {
	a := buildDestinationListsFixture(t)
	token := destinationRefreshAdminToken(t, a)
	for _, query := range []string{"since=bad", "since=32d", "since=-1", "user_id=0", "panel_id=-1", "source=p1x1", "source=p01", "source_kind=other", "action=allow", "group_by=dest", "page=-1", "page_size=0", "page_size=201", "include_trial=maybe", "page=1&page=2", "sort=dest", "q=%bad-escape", "q=" + strings.Repeat("x", 4097), "since=2026-01-01T00:00:00Z&until=2026-03-01T00:00:00Z", "since=2026-01-01T00:00:00Z&until=2026-01-01T00:00:00Z"} {
		w := destinationListRequest(t, a, token, "GET", "hits?"+query, nil)
		if w.Code != 400 || strings.Contains(w.Body.String(), "bad-escape") {
			t.Fatalf("invalid query accepted or echoed: HTTP=%d", w.Code)
		}
	}
	if w := destinationListRequest(t, a, token, "GET", "hits?include_trial=1&since=31d&page_size=200", nil); w.Code != 200 {
		t.Fatal("valid maximum range rejected")
	}
}

func TestBuildDestinationHitsIsAdministratorOnly(t *testing.T) {
	a := buildDestinationListsFixture(t)
	settings, err := a.settings.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []domain.Role{domain.RoleOperator, domain.RoleUser} {
		u := &domain.User{UPN: fmt.Sprintf("hit-reader-%s@example.test", role), Role: role, Enabled: true, UUID: "hit-reader-" + string(role), SubToken: "hit-reader-token-" + string(role)}
		if err := a.repos.User.Create(t.Context(), u); err != nil {
			t.Fatal(err)
		}
		token, err := jwtutil.NewIssuer(a.cfg.JWTSecret, func() jwtutil.Params {
			return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: settings.JWTIssuer}
		}).IssueAccess(u.ID, u.UPN, u.Role, u.TokenVersion)
		if err != nil {
			t.Fatal(err)
		}
		if w := destinationListRequest(t, a, token, "GET", "hits", nil); w.Code != 403 {
			t.Fatalf("%s could read destination records", role)
		}
	}
	if w := destinationListRequest(t, a, "", "GET", "hits", nil); w.Code != 401 {
		t.Fatal("anonymous destination records read")
	}
}
