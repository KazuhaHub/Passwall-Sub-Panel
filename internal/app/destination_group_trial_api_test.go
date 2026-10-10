package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

type destinationGroupTrialProbe struct {
	read func(context.Context, domain.DestGroupTrialQuery) (domain.DestGroupTrialData, error)
}

func (p destinationGroupTrialProbe) ReadDestinationGroupTrial(ctx context.Context, q domain.DestGroupTrialQuery) (domain.DestGroupTrialData, error) {
	return p.read(ctx, q)
}

func TestBuildGroupTrialReportReadsRealAnonymousHistoryAndCurrentListCoverage(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a, token := f.a, destinationRefreshAdminToken(t, f.a)
	mode, err := a.destGroups.Save(t.Context(), f.group.ID, destpolicy.GroupModeInput{Mode: "allowlist", Stage: "trial"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	hour := time.Now().UTC().Truncate(time.Hour)
	for i, site := range []string{"covered.test", "unhandled.test", "(ip)"} {
		if _, err := a.database.ExecContext(t.Context(), "INSERT INTO dest_hits (hour_ms,panel_id,user_id,source,action,dest,port,count,first_at,last_at) VALUES (?,?,?,?,?,?,?,?,?,?)", hour.UnixMilli(), f.agent.PanelID, 0, fmt.Sprintf("g%d", f.group.ID), "observe", site, 0, i+1, hour, hour.Add(time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	if w := destinationListRequest(t, a, token, "POST", fmt.Sprintf("lists/%d/entries", mode.ExtraListID), map[string]any{"add": []string{"domain:covered.test"}}); w.Code != 200 {
		t.Fatalf("could not update owned list HTTP=%d", w.Code)
	}
	path := fmt.Sprintf("groups/%d/trial-report?days=31", f.group.ID)
	w := destinationListRequest(t, a, token, "GET", path, nil)
	var report destpolicy.GroupTrialReport
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &report) != nil || report.EffectiveDays != 7 || len(report.Items) != 3 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("assembled trial report HTTP=%d body=%s", w.Code, w.Body.String())
	}
	for _, item := range report.Items {
		if item.Added != (item.Site == "covered.test") {
			t.Fatal("report omitted current owned-list coverage")
		}
	}
	for _, private := range []string{"user_id", "upn", "sample", "panel_name", "group_name"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatalf("trial report exposed %s", private)
		}
	}
	oldHour := hour.Add(-48 * time.Hour)
	if _, err := a.database.ExecContext(t.Context(), "INSERT INTO dest_hits (hour_ms,panel_id,user_id,source,action,dest,port,count,first_at,last_at) VALUES (?,?,?,?,?,?,?,?,?,?)", oldHour.UnixMilli(), f.agent.PanelID, 0, fmt.Sprintf("g%d", f.group.ID), "observe", "old-trial.test", 0, 99, oldHour, oldHour.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	w = destinationListRequest(t, a, token, "GET", path, nil)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &report) != nil || len(report.Items) != 4 {
		t.Fatal("retained older trial record was not readable")
	}
	settings, err := a.settings.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	settings.DestTrialRetentionDays = 1
	if err := a.settings.Save(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	w = destinationListRequest(t, a, token, "GET", path, nil)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &report) != nil || report.EffectiveDays != 1 || len(report.Items) != 3 {
		t.Fatal("current retention change did not clamp stored history immediately")
	}
	for _, role := range []domain.Role{domain.RoleOperator, domain.RoleUser} {
		u := &domain.User{UPN: "trial-report-" + string(role) + "@example.test", Role: role, Enabled: true, UUID: "trial-report-" + string(role), SubToken: "trial-report-sub-" + string(role)}
		if err := a.repos.User.Create(t.Context(), u); err != nil {
			t.Fatal(err)
		}
		otherToken, err := jwtutil.NewIssuer(a.cfg.JWTSecret, func() jwtutil.Params {
			return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: settings.JWTIssuer}
		}).IssueAccess(u.ID, u.UPN, u.Role, u.TokenVersion)
		if err != nil {
			t.Fatal(err)
		}
		if w := destinationListRequest(t, a, otherToken, "GET", path, nil); w.Code != 403 {
			t.Fatalf("%s could read administrator trial report", role)
		}
	}
	for _, bad := range []string{"groups/0/trial-report", "groups/01/trial-report", fmt.Sprintf("groups/%d/trial-report?days=0", f.group.ID), path + "&days=1", path + "&user_id=1"} {
		if w := destinationListRequest(t, a, token, "GET", bad, nil); w.Code != 400 {
			t.Errorf("unsafe report path %s HTTP=%d", bad, w.Code)
		}
	}
	if w := destinationListRequest(t, a, token, "GET", "groups/999999/trial-report", nil); w.Code != 404 {
		t.Fatal("unknown group did not return 404")
	}
	if w := destinationListRequest(t, a, "", "GET", path, nil); w.Code != 401 {
		t.Fatal("anonymous group report read")
	}
}

func TestBuildGroupTrialReportWithholdsPrivatePartialReadsAndMissingDependencies(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a, token := f.a, destinationRefreshAdminToken(t, f.a)
	path := fmt.Sprintf("groups/%d/trial-report", f.group.ID)
	a.destGroupTrialRead = destinationGroupTrialProbe{read: func(context.Context, domain.DestGroupTrialQuery) (domain.DestGroupTrialData, error) {
		return domain.DestGroupTrialData{Items: []domain.DestGroupTrialSite{{Site: "private-partial.test", Count: 1}}}, errors.New("private-driver-detail")
	}}
	for _, missing := range []bool{false, true} {
		if missing {
			a.destGroupTrialRead = nil
		}
		w := destinationListRequest(t, a, token, "GET", path, nil)
		if w.Code != 503 || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "items") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("failed report exposed private partial data or became empty success")
		}
	}
}
