package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func TestNodeAgentIssueRepoDeduplicatesPreservesReviewAndFilters(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	repo := NewRepos(db).NodeAgentIssue
	ctx := context.Background()
	firstSeen := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	issue := domain.NodeAgentIssue{
		Code: "object_rejected_timeout", Key: "cli_7", Detail: "roster object remained rejected",
	}
	if err := repo.RecordBatch(ctx, "agt_1", []domain.NodeAgentIssue{issue}, firstSeen); err != nil {
		t.Fatal(err)
	}
	secondSeen := firstSeen.Add(time.Minute)
	if err := repo.RecordBatch(ctx, "agt_1", []domain.NodeAgentIssue{
		issue,
		{Code: issue.Code, Key: issue.Key, Detail: "a different rejected version"},
	}, secondSeen); err != nil {
		t.Fatal(err)
	}
	items, total, err := repo.List(ctx, ports.NodeAgentIssueFilter{})
	if err != nil || total != 2 || len(items) != 2 {
		t.Fatalf("issue list = (%+v, %d, %v)", items, total, err)
	}
	var repeated *domain.NodeAgentIssue
	for _, item := range items {
		if item.Detail == issue.Detail {
			repeated = item
		}
	}
	if repeated == nil || !repeated.FirstSeenAt.Equal(firstSeen) || !repeated.LastSeenAt.Equal(secondSeen) {
		t.Fatalf("replayed issue = %+v", repeated)
	}
	acknowledgedAt := secondSeen.Add(time.Minute)
	if err := repo.Acknowledge(ctx, repeated.ID, acknowledgedAt); err != nil {
		t.Fatal(err)
	}
	thirdSeen := acknowledgedAt.Add(time.Minute)
	if err := repo.RecordBatch(ctx, "agt_1", []domain.NodeAgentIssue{issue}, thirdSeen); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, repeated.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AcknowledgedAt == nil || !got.AcknowledgedAt.Equal(acknowledgedAt) || !got.LastSeenAt.Equal(thirdSeen) {
		t.Fatalf("acknowledged replay = %+v", got)
	}
	unacknowledged := false
	filtered, total, err := repo.List(ctx, ports.NodeAgentIssueFilter{
		AgentID: "agt_1", Code: issue.Code, Acknowledged: &unacknowledged,
		Pagination: ports.Pagination{Keyword: "different", Page: 1, PageSize: 10},
	})
	if err != nil || total != 1 || len(filtered) != 1 || filtered[0].AcknowledgedAt != nil {
		t.Fatalf("filtered issue list = (%+v, %d, %v)", filtered, total, err)
	}
	if err := repo.Acknowledge(ctx, 999_999, acknowledgedAt); err != domain.ErrNotFound {
		t.Fatalf("missing issue acknowledge error = %v", err)
	}
}

func TestNodeAgentIssueServerLabelsAreBatchProjectedNativeMetadata(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	// Deliberately unreadable ciphertext proves that issue labels never go through
	// the ordinary panel decoder or depend on the panel's decryption key.
	panels := []xuiPanelRow{
		{Name: "Canada Home", Kind: string(domain.PanelKindPSP), URL: "https://native.example.test", APIToken: "enc:v1:invalid"},
		{Name: "Legacy 3X-UI", Kind: string(domain.PanelKind3XUI), URL: "https://xui.example.test"},
	}
	if err := db.Create(&panels).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]nodeAgentRow{
		{AgentID: "agt_native", PanelID: panels[0].ID, CredentialSHA256: "native-secret-digest"},
		{AgentID: "agt_legacy", PanelID: panels[1].ID, CredentialSHA256: "legacy-secret-digest"},
		{AgentID: "agt_missing_server", PanelID: 999_999, CredentialSHA256: "missing-secret-digest"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	repo := NewRepos(db).NodeAgentIssue.(ports.NodeAgentIssueServerRepo)
	queryCount := 0
	if err := db.Callback().Row().After("gorm:row").Register("test:issue-label-query-count", func(*gorm.DB) { queryCount++ }); err != nil {
		t.Fatal(err)
	}
	labels, err := repo.ListIssueServers(context.Background(), []string{"agt_native", "agt_native", "agt_legacy", "agt_missing_server", "agt_deleted"})
	if err != nil || len(labels) != 1 || labels["agt_native"].ServerID != panels[0].ID || labels["agt_native"].ServerName != "Canada Home" {
		t.Fatalf("server labels = (%+v, %v)", labels, err)
	}
	if queryCount != 1 {
		t.Fatalf("label queries = %d, want one page-wide query", queryCount)
	}
	if labels, err := repo.ListIssueServers(context.Background(), nil); err != nil || len(labels) != 0 || queryCount != 1 {
		t.Fatalf("empty batch = (%+v, %v), queries=%d", labels, err, queryCount)
	}
}

func TestNodeAgentIssueKeywordMatchesNativeServerAndLiteralRawFields(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	panels := []xuiPanelRow{
		{Name: "Canada_50% !Home", Kind: string(domain.PanelKindPSP), URL: "https://native.example.test"},
		{Name: "CanadaX50percent XHome", Kind: string(domain.PanelKindPSP), URL: "https://near.example.test"},
		{Name: "Other server", Kind: string(domain.PanelKind3XUI), URL: "https://xui.example.test"},
	}
	if err := db.Create(&panels).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]nodeAgentRow{
		{AgentID: "agt_native", PanelID: panels[0].ID, CredentialSHA256: "native"},
		{AgentID: "agt_near", PanelID: panels[1].ID, CredentialSHA256: "near"},
		{AgentID: "agt_legacy", PanelID: panels[2].ID, CredentialSHA256: "legacy"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	repo := NewRepos(db).NodeAgentIssue
	ctx := context.Background()
	for _, report := range []struct{ agent, key, detail string }{
		{"agt_native", "cli_7", "100% ready"},
		{"agt_near", "cliX7", "100X ready"},
		{"agt_legacy", "cli_9", "legacy detail"},
		{"agt_deleted", "cli_10", "historical detail"},
	} {
		if err := repo.RecordBatch(ctx, report.agent, []domain.NodeAgentIssue{{Code: "pending", Key: report.key, Detail: report.detail}}, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		keyword string
		want    int64
		agent   string
	}{
		{"CANADA_", 1, "agt_native"},
		{"50%", 1, "agt_native"},
		{"!home", 1, "agt_native"},
		{"other server", 0, ""},
		{"AGT_NATIVE", 1, "agt_native"},
		{"cli_7", 1, "agt_native"},
		{"100%", 1, "agt_native"},
		{"historical", 1, "agt_deleted"},
		{"PENDING", 4, ""},
		{"' OR 1=1 --", 0, ""},
	} {
		t.Run(tc.keyword, func(t *testing.T) {
			items, total, err := repo.List(ctx, ports.NodeAgentIssueFilter{Pagination: ports.Pagination{Keyword: tc.keyword, Page: 1, PageSize: 10}})
			if err != nil || total != tc.want || int64(len(items)) != tc.want {
				t.Fatalf("search %q = (%+v, %d, %v), want %d", tc.keyword, items, total, err, tc.want)
			}
			if tc.agent != "" && items[0].AgentID != tc.agent {
				t.Fatalf("search %q agent = %s, want %s", tc.keyword, items[0].AgentID, tc.agent)
			}
		})
	}
	// Name matches and typed review/code filters must combine, not bypass one
	// another through an unparenthesized OR condition.
	if _, total, err := repo.List(ctx, ports.NodeAgentIssueFilter{Code: "other", Pagination: ports.Pagination{Keyword: "Canada_"}}); err != nil || total != 0 {
		t.Fatalf("name+code filter = (%d, %v), want no rows", total, err)
	}
}

func TestNodeAgentIssueViewsFilterBeforeCountAndPaginationWithoutChangingReports(t *testing.T) {
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	panels := []xuiPanelRow{
		{Name: "Canada Home", Kind: string(domain.PanelKindPSP), URL: "https://canada.example.test"},
		{Name: "Taiwan Home", Kind: string(domain.PanelKindPSP), URL: "https://taiwan.example.test"},
	}
	if err := db.Create(&panels).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]nodeAgentRow{
		{AgentID: "agt_canada", PanelID: panels[0].ID, CredentialSHA256: "canada"},
		{AgentID: "agt_taiwan", PanelID: panels[1].ID, CredentialSHA256: "taiwan"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	xrayStartup := "collect core telemetry: xray process is starting"
	singboxStartup := "collect core telemetry: sing-box process is starting"
	reports := []domain.NodeAgentIssue{
		{Code: "core_telemetry_failed", Detail: "collect core telemetry: xray process is degraded"},
		{Code: "core_telemetry_failed", Detail: "collect core telemetry: sing-box statistics snapshot is not ready"},
		{Code: "CORE_TELEMETRY_FAILED", Detail: xrayStartup},
		{Code: "unknown_future_code", Detail: xrayStartup},
		{Code: "core_telemetry_failed", Detail: strings.ToUpper(xrayStartup)},
		{Code: "core_telemetry_failed", Detail: xrayStartup + ": telemetry lost"},
		{Code: "core_telemetry_failed", Detail: singboxStartup + " "},
		{Code: "task_identity_replay", Detail: "old task identity rejected"},
		{Code: "object_pending_timeout", Detail: "roster object remained pending"},
		{Code: "core_telemetry_failed", Detail: "historical degraded telemetry"},
		{Code: "core_telemetry_failed", Detail: xrayStartup},
		{Code: "core_telemetry_failed", Detail: singboxStartup},
		{Code: "core_telemetry_failed", Detail: xrayStartup},
		{Code: "core_telemetry_failed", Detail: "collect core telemetry: xray process is degraded"},
		{Code: "core_telemetry_failed", Detail: xrayStartup},
	}
	repo := NewRepos(db).NodeAgentIssue
	ctx := context.Background()
	seenAt := time.Date(2026, 9, 9, 1, 0, 0, 0, time.UTC)
	for i := range reports {
		reports[i].Key = fmt.Sprintf("object_%d", i)
		agentID := "agt_canada"
		if i >= 13 {
			agentID = "agt_taiwan"
		}
		if err := repo.RecordBatch(ctx, agentID, reports[i:i+1], seenAt.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	all, _, err := repo.List(ctx, ports.NodeAgentIssueFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range all {
		if item.Key == "object_9" || item.Key == "object_12" {
			if err := repo.Acknowledge(ctx, item.ID, seenAt.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
		}
	}
	pending, reviewed := false, true
	for _, tc := range []struct {
		name   string
		filter ports.NodeAgentIssueFilter
		total  int64
		keys   []string
	}{
		{"legacy omitted includes diagnostics", ports.NodeAgentIssueFilter{}, 15, nil},
		{"explicit all includes diagnostics", ports.NodeAgentIssueFilter{View: domain.NodeAgentIssueViewAll}, 15, nil},
		{"attention excludes only exact startup", ports.NodeAgentIssueFilter{View: domain.NodeAgentIssueViewAttention}, 11, []string{"object_13", "object_9", "object_8", "object_7", "object_6", "object_5", "object_4", "object_3", "object_2", "object_1", "object_0"}},
		{"diagnostic exact startup only", ports.NodeAgentIssueFilter{View: domain.NodeAgentIssueViewDiagnostic}, 4, []string{"object_14", "object_12", "object_11", "object_10"}},
		{"attention second page", ports.NodeAgentIssueFilter{View: domain.NodeAgentIssueViewAttention, Pagination: ports.Pagination{Page: 2, PageSize: 3}}, 11, []string{"object_7", "object_6", "object_5"}},
		{"diagnostic second page", ports.NodeAgentIssueFilter{View: domain.NodeAgentIssueViewDiagnostic, Pagination: ports.Pagination{Page: 2, PageSize: 2}}, 4, []string{"object_11", "object_10"}},
		{"pending attention", ports.NodeAgentIssueFilter{View: domain.NodeAgentIssueViewAttention, Acknowledged: &pending}, 10, nil},
		{"reviewed diagnostic", ports.NodeAgentIssueFilter{View: domain.NodeAgentIssueViewDiagnostic, Acknowledged: &reviewed}, 1, []string{"object_12"}},
		{"agent and diagnostic", ports.NodeAgentIssueFilter{View: domain.NodeAgentIssueViewDiagnostic, AgentID: "agt_taiwan"}, 1, []string{"object_14"}},
		{"search name and reviewed attention", ports.NodeAgentIssueFilter{View: domain.NodeAgentIssueViewAttention, Acknowledged: &reviewed, Pagination: ports.Pagination{Keyword: "Canada"}}, 1, []string{"object_9"}},
		{"search name code pending attention", ports.NodeAgentIssueFilter{View: domain.NodeAgentIssueViewAttention, Code: "object_pending_timeout", Acknowledged: &pending, Pagination: ports.Pagination{Keyword: "Canada"}}, 1, []string{"object_8"}},
		{"search name and diagnostic", ports.NodeAgentIssueFilter{View: domain.NodeAgentIssueViewDiagnostic, Pagination: ports.Pagination{Keyword: "Canada"}}, 3, []string{"object_12", "object_11", "object_10"}},
		{"raw search does not reveal exact startup in attention", ports.NodeAgentIssueFilter{View: domain.NodeAgentIssueViewAttention, Pagination: ports.Pagination{Keyword: "starting"}}, 5, []string{"object_6", "object_5", "object_4", "object_3", "object_2"}},
		{"code filter cannot bypass diagnostic", ports.NodeAgentIssueFilter{View: domain.NodeAgentIssueViewDiagnostic, Code: "task_identity_replay", Pagination: ports.Pagination{Keyword: "Canada"}}, 0, []string{}},
		{"keyword parameter cannot bypass attention", ports.NodeAgentIssueFilter{View: domain.NodeAgentIssueViewAttention, Pagination: ports.Pagination{Keyword: "' OR 1=1 --"}}, 0, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items, total, err := repo.List(ctx, tc.filter)
			if err != nil || total != tc.total {
				t.Fatalf("list total/error = %d/%v, want %d", total, err, tc.total)
			}
			keys := make([]string, len(items))
			for i, item := range items {
				keys[i] = item.Key
				if tc.filter.View == domain.NodeAgentIssueViewAttention && domain.IsNodeAgentIssueDiagnostic(item.Code, item.Detail) {
					t.Fatalf("diagnostic leaked into attention: %+v", item)
				}
				if tc.filter.View == domain.NodeAgentIssueViewDiagnostic && !domain.IsNodeAgentIssueDiagnostic(item.Code, item.Detail) {
					t.Fatalf("failure hidden in diagnostics: %+v", item)
				}
			}
			if tc.keys != nil && !reflect.DeepEqual(keys, tc.keys) {
				t.Fatalf("keys = %v, want %v", keys, tc.keys)
			}
		})
	}
	if _, _, err := repo.List(ctx, ports.NodeAgentIssueFilter{View: "future"}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("invalid view error = %v", err)
	}
	// Listing does not auto-review/delete records, even the diagnostic startup
	// observations. Replaying one also preserves its original identity and review.
	before, total, err := repo.List(ctx, ports.NodeAgentIssueFilter{})
	if err != nil || total != 15 {
		t.Fatalf("durable reports after filtering = %d/%v", total, err)
	}
	for _, item := range before {
		wantReviewed := item.Key == "object_9" || item.Key == "object_12"
		if (item.AcknowledgedAt != nil) != wantReviewed {
			t.Fatalf("review state changed by view filtering: %+v", item)
		}
	}
}

func TestNodeAgentIssueDiagnosticPredicateUsesFixedDialectExpressionsAndParameters(t *testing.T) {
	wantArgs := make([]any, 0, 4)
	for _, signature := range domain.NodeAgentIssueDiagnosticSignatures() {
		wantArgs = append(wantArgs, signature.Code, signature.Detail)
	}
	for _, tc := range []struct {
		dialect string
		code    string
		detail  string
	}{
		{"sqlite", "code COLLATE BINARY", "detail COLLATE BINARY"},
		{"mysql", "CAST(code AS BINARY)", "CAST(detail AS BINARY)"},
		{"postgres", `code COLLATE "C"`, `detail COLLATE "C"`},
	} {
		t.Run(tc.dialect, func(t *testing.T) {
			predicate, args := nodeAgentIssueDiagnosticPredicate(tc.dialect)
			pair := "(" + tc.code + " = ? AND " + tc.detail + " = ?)"
			if want := "(" + pair + " OR " + pair + ")"; predicate != want {
				t.Fatalf("predicate = %q, want %q", predicate, want)
			}
			if !reflect.DeepEqual(args, wantArgs) {
				t.Fatalf("predicate parameters = %v, want %v", args, wantArgs)
			}
		})
	}
}
