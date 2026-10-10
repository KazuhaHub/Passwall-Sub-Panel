package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type destinationUsageProbe struct{ calls int }

func (p *destinationUsageProbe) ReadDestinationUsage(context.Context, domain.DestUsageQuery) (domain.DestUsagePage, error) {
	p.calls++
	return domain.DestUsagePage{Items: []domain.DestUsageSite{}, Losses: domain.DestAuditLosses{Scope: "panel"}}, nil
}

func TestBuildDestinationUsageReadsAreBoundedAuditedAndPrivateFromOperators(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	token := destinationRefreshAdminToken(t, a)
	hour := time.Now().UTC().Truncate(time.Hour).UnixMilli()
	if _, err := a.database.ExecContext(t.Context(), "INSERT INTO dest_usage_hourly (hour_ms,panel_id,user_id,site,count) VALUES (?,?,?,?,?)", hour, f.agent.PanelID, f.user.ID, "private-usage.test", 9); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("usage?user_id=%d", f.user.ID)
	for _, suffix := range []string{"", "&since=7d"} {
		w := destinationListRequest(t, a, token, "GET", path+suffix, nil)
		var page domain.DestUsagePage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].Site != "private-usage.test" || page.Items[0].Count != 9 || page.TotalSites != 1 || page.TotalCount != 9 || page.Losses.Scope != "panel" || page.Losses.Complete || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("usage repository wiring HTTP=%d", w.Code)
		}
	}
	w := destinationListRequest(t, a, token, "GET", fmt.Sprintf("users/%d?usage=24h", f.user.ID), nil)
	var view struct {
		Usage *domain.DestUsagePage `json:"usage_top"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Usage == nil || view.Usage.TotalCount != 9 {
		t.Fatal("drawer usage is not wired")
	}
	entries, total, err := a.repos.Audit.List(t.Context(), ports.AuditFilter{Action: "dest.usage.read", Pagination: ports.Pagination{Page: 1, PageSize: 20}})
	if err != nil || total != 1 || len(entries) != 1 || entries[0].Target != "/api/admin/dest/usage" || strings.Contains(entries[0].BeforeJSON, "private") || entries[0].IP != "" {
		t.Fatal("audited reads did not share a private dedup boundary")
	}
	for _, bad := range []string{"usage", path + "&since=8d", path + "&since=31d", path + "&until=" + fmt.Sprint(time.Now().Add(time.Hour).UnixMilli()), path + "&since=" + fmt.Sprint(time.Now().Add(-60*24*time.Hour).UnixMilli()) + "&until=" + fmt.Sprint(time.Now().Add(-59*24*time.Hour).UnixMilli())} {
		if w := destinationListRequest(t, a, token, "GET", bad, nil); w.Code != 400 {
			t.Errorf("unbounded usage query HTTP=%d", w.Code)
		}
	}
	settings, err := a.settings.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	u := &domain.User{UPN: "usage-operator@example.test", Role: domain.RoleOperator, Enabled: true, UUID: "usage-operator", SubToken: "usage-operator-sub"}
	if err := a.repos.User.Create(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	operator, err := jwtutil.NewIssuer(a.cfg.JWTSecret, func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: settings.JWTIssuer}
	}).IssueAccess(u.ID, u.UPN, u.Role, u.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	if w := destinationListRequest(t, a, operator, "GET", path, nil); w.Code != 403 {
		t.Fatal("operator read usage")
	}
	request := httptest.NewRequest("GET", "/api/admin/audit?action=dest.usage.read", nil)
	request.Header.Set("Authorization", "Bearer "+operator)
	recorder := httptest.NewRecorder()
	a.server.Handler.ServeHTTP(recorder, request)
	var auditPage struct {
		Total int64               `json:"total"`
		Items []domain.AuditEntry `json:"items"`
	}
	if recorder.Code != 200 || json.Unmarshal(recorder.Body.Bytes(), &auditPage) != nil || auditPage.Total != 0 || len(auditPage.Items) != 0 || strings.Contains(recorder.Body.String(), "dest.usage.read") {
		t.Fatal("operator could discover usage audits")
	}
}

func TestBuildBaseDestinationAccessNeverCallsUsageRepository(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	probe := &destinationUsageProbe{}
	f.a.destUsageRead = probe
	token := destinationRefreshAdminToken(t, f.a)
	if w := destinationListRequest(t, f.a, token, "GET", fmt.Sprintf("users/%d", f.user.ID), nil); w.Code != 200 || probe.calls != 0 || strings.Contains(w.Body.String(), "usage_top") {
		t.Fatal("ordinary access request read usage")
	}
	if w := destinationListRequest(t, f.a, token, "GET", fmt.Sprintf("users/%d?usage=24h", f.user.ID), nil); w.Code != 200 || probe.calls != 1 {
		t.Fatal("explicit account usage request bypassed repository")
	}
	if w := destinationListRequest(t, f.a, token, "GET", fmt.Sprintf("users/%d?usage=8d", f.user.ID), nil); w.Code != 400 || probe.calls != 1 {
		t.Fatal("out-of-retention read reached repository")
	}
}
