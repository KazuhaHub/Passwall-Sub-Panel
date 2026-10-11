package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

type destinationGroupMembersProbe struct {
	inner interface {
		TagMatchedMembers(context.Context, int64) (map[int64][]int64, error)
	}
	calls int
}

func (p *destinationGroupMembersProbe) TagMatchedMembers(ctx context.Context, id int64) (map[int64][]int64, error) {
	p.calls++
	return p.inner.TagMatchedMembers(ctx, id)
}

type destinationGroupStatsProbe struct {
	read func(context.Context, domain.DestGroupStatsQuery) (map[int64]domain.DestGroupStats, error)
}

func (p destinationGroupStatsProbe) ReadDestinationGroupStats(ctx context.Context, q domain.DestGroupStatsQuery) (map[int64]domain.DestGroupStats, error) {
	return p.read(ctx, q)
}

func TestBuildGroupOverviewUsesBatchMembershipStatsAndExactAppliedStage(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a, token := f.a, destinationRefreshAdminToken(t, f.a)
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy}
	f.report.CoreEngine = "xray"
	_ = syncNativeCacheFixture(t, a, f.credential, f.report)
	open := &domain.Group{Slug: "overview-open", Name: "Open zero members", TagFilter: domain.TagFilter{All: true}}
	if err := a.repos.Group.Create(t.Context(), open); err != nil {
		t.Fatal(err)
	}
	mode, err := a.destGroups.Save(t.Context(), f.group.ID, destpolicy.GroupModeInput{Mode: "allowlist", Stage: "trial", BaseHosts: []string{"dns.example.test"}}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if w := destinationListRequest(t, a, token, "POST", fmt.Sprintf("lists/%d/entries", mode.ExtraListID), map[string]any{"add": []string{"domain:covered.test"}}); w.Code != 200 {
		t.Fatal("could not seed current coverage")
	}
	hour := time.Now().UTC().Truncate(time.Hour)
	for _, item := range []struct {
		site  string
		count int
		at    time.Time
	}{{"covered.test", 10, hour}, {"unhandled.test", 5, hour}, {"(ip)", 2, hour}, {"old.test", 7, hour.Add(-48 * time.Hour)}} {
		if _, err := a.database.ExecContext(t.Context(), "INSERT INTO dest_hits (hour_ms,panel_id,user_id,source,action,dest,port,count,first_at,last_at) VALUES (?,?,?,?,?,?,?,?,?,?)", item.at.UnixMilli(), f.agent.PanelID, 0, fmt.Sprintf("g%d", f.group.ID), "observe", item.site, 0, item.count, item.at, item.at.Add(time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.database.ExecContext(t.Context(), "INSERT INTO dest_hits (hour_ms,panel_id,user_id,source,action,dest,port,count,first_at,last_at) VALUES (?,?,?,?,?,?,?,?,?,?)", hour.UnixMilli(), f.agent.PanelID, f.user.ID, fmt.Sprintf("g%d", f.group.ID), "block", "private-full-host.test", 443, 4, hour, hour.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	probe := &destinationGroupMembersProbe{inner: a.destTagMembers}
	a.destTagMembers = probe
	w := destinationListRequest(t, a, token, "GET", "groups", nil)
	var overview struct {
		Items []destpolicy.GroupAccessSummary `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &overview) != nil || probe.calls != 1 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("batch overview HTTP=%d membershipCalls=%d", w.Code, probe.calls)
	}
	find := func(id int64) destpolicy.GroupAccessSummary {
		t.Helper()
		for _, item := range overview.Items {
			if item.GroupID == id {
				return item
			}
		}
		t.Fatalf("group %d omitted", id)
		return destpolicy.GroupAccessSummary{}
	}
	trial, unrestricted := find(f.group.ID), find(open.ID)
	if trial.Mode != "allowlist" || trial.Members != 1 || trial.EligibleNodes != 1 || trial.AppliedNodes != 0 || trial.WouldDenyRecent != (destpolicy.GroupTrialCounters{Days: 7, Sites: 4, Count: 24}) || trial.Denied24h != 4 || unrestricted.Mode != "open" || unrestricted.Members != 0 || unrestricted.EligibleNodes != 1 {
		t.Fatalf("current group facts wrong: trial=%+v open=%+v", trial, unrestricted)
	}
	if strings.Contains(w.Body.String(), "private-full-host") || strings.Contains(w.Body.String(), "user_id") || strings.Contains(w.Body.String(), "source_text") {
		t.Fatal("overview exposed private observations/config")
	}
	path := fmt.Sprintf("groups/%d", f.group.ID)
	read := func() destpolicy.GroupAccessDetail {
		t.Helper()
		w := destinationListRequest(t, a, token, "GET", path, nil)
		var detail destpolicy.GroupAccessDetail
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil {
			t.Fatalf("group detail HTTP=%d body=%s", w.Code, w.Body.String())
		}
		return detail
	}
	detail := read()
	if !detail.Readiness.ListsReady || !detail.Readiness.BaseNonempty || len(detail.Readiness.UnappliedNodes) != 1 || detail.Readiness.RemainingSites24h != 2 || len(detail.Readiness.TopSites) != 2 || detail.Readiness.TopSites[0].Site != "unhandled.test" || detail.Readiness.TopSites[1].Site != "(ip)" || detail.Readiness.TrialLosses.Complete {
		t.Fatal("detail omitted current readiness blockers or anonymous uncovered-site warnings")
	}
	confirm := func() {
		t.Helper()
		if w := destinationListRequest(t, a, token, "POST", "publish", nil); w.Code != 200 {
			t.Fatal("actual group publication failed")
		}
		response := syncNativeCacheFixture(t, a, f.credential, f.report)
		if response.Config.Body == nil || response.Config.Body.Policy == nil {
			t.Fatal("actual group candidate missing")
		}
		if err := a.destCompiler.ObserveStatus(t.Context(), f.agent.AgentID, &protocol.PolicyStatus{State: "applied", Digest: protocol.PolicyDigest(response.Config.Body.Policy)}, f.report.Capabilities); err != nil {
			t.Fatal(err)
		}
	}
	confirm()
	detail = read()
	if detail.AppliedNodes != 1 || len(detail.Readiness.UnappliedNodes) != 0 {
		t.Fatal("current exact trial receipt was not counted")
	}
	before, err := a.destDefinitions.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	runtimeBefore, err := a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
	if err != nil {
		t.Fatal(err)
	}
	_ = read()
	after, stateErr := a.destDefinitions.State(t.Context())
	runtimeAfter, runtimeErr := a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
	if stateErr != nil || runtimeErr != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(runtimeBefore, runtimeAfter) {
		t.Fatal("group read published or minted state")
	}
	w = destinationListRequest(t, a, token, "PUT", path, map[string]any{"mode": "allowlist", "stage": "enforce", "list_ids": []int64{}, "updated_at": detail.UpdatedAt})
	if w.Code != 200 {
		t.Fatal("could not transition actual group to enforce")
	}
	if got := read(); got.Stage != "enforce" || got.AppliedNodes != 0 {
		t.Fatal("old trial receipt with same group ID proved enforce")
	}
	confirm()
	if got := read(); got.AppliedNodes != 1 {
		t.Fatal("new exact enforce receipt was not counted")
	}
	settings, err := a.settings.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	settings.DestTrialRetentionDays = 1
	if err := a.settings.Save(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.WouldDenyRecent != (destpolicy.GroupTrialCounters{Days: 1, Sites: 3, Count: 17}) || got.Denied24h != 4 {
		t.Fatal("live trial retention failed to bound counters independently of denials")
	}
}

func TestBuildGroupReadsWithholdDependencyFailuresAndAvoidOverviewReports(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a, token := f.a, destinationRefreshAdminToken(t, f.a)
	path := fmt.Sprintf("groups/%d", f.group.ID)
	stats := a.destGroupStatsRead
	a.destGroupStatsRead = destinationGroupStatsProbe{read: func(context.Context, domain.DestGroupStatsQuery) (map[int64]domain.DestGroupStats, error) {
		return map[int64]domain.DestGroupStats{f.group.ID: {TrialCount: 123}}, errors.New("private-stats-driver")
	}}
	for _, target := range []string{"groups", path} {
		w := destinationListRequest(t, a, token, "GET", target, nil)
		if w.Code != 503 || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "items") || strings.Contains(w.Body.String(), "readiness") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("failed stats returned partial overview/detail")
		}
	}
	a.destGroupStatsRead = stats
	listeners := a.destGroupListeners
	a.destGroupListeners = nil
	if w := destinationListRequest(t, a, token, "GET", "groups", nil); w.Code != 503 {
		t.Fatal("missing listener projection fabricated coverage")
	}
	a.destGroupListeners = listeners
	reports := 0
	a.destGroupTrialRead = destinationGroupTrialProbe{read: func(context.Context, domain.DestGroupTrialQuery) (domain.DestGroupTrialData, error) {
		reports++
		return domain.DestGroupTrialData{}, errors.New("private-trial-detail")
	}}
	if w := destinationListRequest(t, a, token, "GET", "groups", nil); w.Code != 200 || reports != 0 {
		t.Fatal("overview loaded per-group trial reports")
	}
	w := destinationListRequest(t, a, token, "GET", path, nil)
	if w.Code != 503 || reports != 1 || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "readiness") {
		t.Fatal("failed detail returned incomplete readiness")
	}
}

func TestBuildGroupReadRoutesAreAdministratorOnlyAndStrict(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a, token := f.a, destinationRefreshAdminToken(t, f.a)
	path := fmt.Sprintf("groups/%d", f.group.ID)
	for _, target := range []string{"groups?days=1", "groups/01", "groups/0", path + "?days=1"} {
		if w := destinationListRequest(t, a, token, "GET", target, nil); w.Code != 400 {
			t.Fatal("unsafe group read parameters")
		}
	}
	if w := destinationListRequest(t, a, token, "GET", "groups/999999", nil); w.Code != 404 {
		t.Fatal("missing group detail")
	}
	settings, err := a.settings.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []domain.Role{domain.RoleOperator, domain.RoleUser} {
		u := &domain.User{UPN: "group-overview-" + string(role) + "@example.test", Role: role, Enabled: true, UUID: "group-overview-" + string(role), SubToken: "group-overview-sub-" + string(role)}
		if err := a.repos.User.Create(t.Context(), u); err != nil {
			t.Fatal(err)
		}
		other, err := jwtutil.NewIssuer(a.cfg.JWTSecret, func() jwtutil.Params {
			return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: settings.JWTIssuer}
		}).IssueAccess(u.ID, u.UPN, u.Role, u.TokenVersion)
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"groups", path} {
			if w := destinationListRequest(t, a, other, "GET", target, nil); w.Code != 403 {
				t.Fatal("nonadmin group private read")
			}
		}
	}
	for _, target := range []string{"groups", path} {
		if w := destinationListRequest(t, a, "", "GET", target, nil); w.Code != 401 {
			t.Fatal("anonymous group private read")
		}
	}
}
