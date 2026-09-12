package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"
	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type nodeTaskLifecycleSettingsRepo struct {
	settings ports.UISettings
	saves    int
}

func (r *nodeTaskLifecycleSettingsRepo) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	return r.settings, nil
}
func (r *nodeTaskLifecycleSettingsRepo) Save(_ context.Context, settings ports.UISettings) error {
	r.settings = settings
	r.saves++
	return nil
}

func nodeTaskLifecycleSettingsRouter(repo ports.SettingsRepo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewAdminSettingsHandler(repo, nil, nil, nil)
	router.GET("/api/admin/settings/ui", handler.Get)
	router.PUT("/api/admin/settings/ui", handler.Put)
	return router
}

func requestNodeTaskLifecycleSettings(t *testing.T, router http.Handler, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "/api/admin/settings/ui", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func assertNodeTaskLifecycleNumbers(t *testing.T, response *httptest.ResponseRecorder, want domain.NodeTaskLifecyclePolicy) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("settings status=%d body=%s", response.Code, response.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]int{
		"node_task_offline_reconcile_days": want.OfflineReconcileDays,
		"node_task_backup_restore_days":    want.BackupRestoreDays,
		"node_task_result_retention_days":  want.ResultRetentionDays,
	} {
		raw, exists := body[key]
		if !exists || bytes.Equal(raw, []byte("null")) {
			t.Fatalf("%s omitted/null, expected concrete number", key)
		}
		var got int
		if err := json.Unmarshal(raw, &got); err != nil || got != value {
			t.Fatalf("%s=%s want number %d", key, raw, value)
		}
	}
}

func settingsWithNodeTaskPolicy(policy domain.NodeTaskLifecyclePolicy) ports.UISettings {
	return ports.UISettings{LoginMode: "local_only", NodeTaskOfflineReconcileDays: policy.OfflineReconcileDays, NodeTaskBackupRestoreDays: policy.BackupRestoreDays, NodeTaskResultRetentionDays: policy.ResultRetentionDays}
}

func TestAdminSettingsNodeTaskLifecycleDefaultsAndGETMapping(t *testing.T) {
	defaults := domain.DefaultNodeTaskLifecyclePolicy()
	if defaults != (domain.NodeTaskLifecyclePolicy{OfflineReconcileDays: 30, BackupRestoreDays: 30, ResultRetentionDays: 90}) {
		t.Fatalf("approved defaults drifted: %+v", defaults)
	}
	handler := NewAdminSettingsHandler(nil, nil, nil, nil)
	if got := nodeTaskLifecyclePolicyFromSettings(handler.defaults()); got != defaults {
		t.Fatalf("HTTP defaults differ from canonical defaults: %+v", got)
	}
	for _, policy := range []domain.NodeTaskLifecyclePolicy{{}, {OfflineReconcileDays: 45, BackupRestoreDays: 75, ResultRetentionDays: 180}} {
		repo := &nodeTaskLifecycleSettingsRepo{settings: settingsWithNodeTaskPolicy(policy)}
		want := policy
		if want == (domain.NodeTaskLifecyclePolicy{}) {
			want = defaults
		}
		assertNodeTaskLifecycleNumbers(t, requestNodeTaskLifecycleSettings(t, nodeTaskLifecycleSettingsRouter(repo), http.MethodGet, ""), want)
		if repo.saves != 0 {
			t.Fatal("GET mutated settings")
		}
	}
}

func TestAdminSettingsNodeTaskLifecyclePUTCustomValuesAndBounds(t *testing.T) {
	for _, policy := range []domain.NodeTaskLifecyclePolicy{
		{OfflineReconcileDays: 45, BackupRestoreDays: 60, ResultRetentionDays: 180},
		{OfflineReconcileDays: 1, BackupRestoreDays: 1, ResultRetentionDays: 1},
		{OfflineReconcileDays: 3650, BackupRestoreDays: 3650, ResultRetentionDays: 3650},
	} {
		repo := &nodeTaskLifecycleSettingsRepo{settings: settingsWithNodeTaskPolicy(domain.DefaultNodeTaskLifecyclePolicy())}
		router := nodeTaskLifecycleSettingsRouter(repo)
		payload, _ := json.Marshal(map[string]any{"login_mode": "local_only", "site_title": "updated", "node_task_offline_reconcile_days": policy.OfflineReconcileDays, "node_task_backup_restore_days": policy.BackupRestoreDays, "node_task_result_retention_days": policy.ResultRetentionDays})
		assertNodeTaskLifecycleNumbers(t, requestNodeTaskLifecycleSettings(t, router, http.MethodPut, string(payload)), policy)
		if got := nodeTaskLifecyclePolicyFromSettings(repo.settings); got != policy || repo.saves != 1 || repo.settings.SiteTitle != "updated" {
			t.Fatalf("PUT mapping lost settings: %+v saves=%d", got, repo.saves)
		}
		assertNodeTaskLifecycleNumbers(t, requestNodeTaskLifecycleSettings(t, router, http.MethodGet, ""), policy)
	}
}

func TestAdminSettingsNodeTaskLifecycleOmittedAndNullKeepExisting(t *testing.T) {
	custom := domain.NodeTaskLifecyclePolicy{OfflineReconcileDays: 75, BackupRestoreDays: 120, ResultRetentionDays: 365}
	for _, test := range []struct {
		name, body     string
		previous, want domain.NodeTaskLifecyclePolicy
	}{
		{"old-client-omitted", `{"login_mode":"local_only","site_title":"old client edit"}`, custom, custom},
		{"null-is-omitted", `{"login_mode":"local_only","node_task_offline_reconcile_days":null,"node_task_backup_restore_days":null,"node_task_result_retention_days":null}`, custom, custom},
		{"partial-edit", `{"login_mode":"local_only","node_task_offline_reconcile_days":60}`, custom, domain.NodeTaskLifecyclePolicy{OfflineReconcileDays: 60, BackupRestoreDays: 120, ResultRetentionDays: 365}},
		{"legacy-all-zero", `{"login_mode":"local_only"}`, domain.NodeTaskLifecyclePolicy{}, domain.DefaultNodeTaskLifecyclePolicy()},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &nodeTaskLifecycleSettingsRepo{settings: settingsWithNodeTaskPolicy(test.previous)}
			assertNodeTaskLifecycleNumbers(t, requestNodeTaskLifecycleSettings(t, nodeTaskLifecycleSettingsRouter(repo), http.MethodPut, test.body), test.want)
			if got := nodeTaskLifecyclePolicyFromSettings(repo.settings); got != test.want {
				t.Fatalf("omitted/null fields reset custom policy: %+v", got)
			}
		})
	}
}

func TestAdminSettingsNodeTaskLifecycleRejectsInvalidWholePolicyWithoutSave(t *testing.T) {
	for _, field := range []string{"node_task_offline_reconcile_days", "node_task_backup_restore_days", "node_task_result_retention_days"} {
		for _, value := range []string{"0", "-1", "3651", "0.5", `"30"`, "true", "{}", "[]", "9999999999999999999999999999999999999999"} {
			t.Run(field+"="+value, func(t *testing.T) {
				previous := settingsWithNodeTaskPolicy(domain.DefaultNodeTaskLifecyclePolicy())
				repo := &nodeTaskLifecycleSettingsRepo{settings: previous}
				body := `{"login_mode":"local_only","` + field + `":` + value + `}`
				response := requestNodeTaskLifecycleSettings(t, nodeTaskLifecycleSettingsRouter(repo), http.MethodPut, body)
				if response.Code != http.StatusBadRequest || repo.saves != 0 || !reflect.DeepEqual(repo.settings, previous) {
					t.Fatalf("invalid policy status=%d saves=%d body=%s", response.Code, repo.saves, response.Body.String())
				}
			})
		}
	}
	for _, test := range []struct {
		name, body string
		previous   domain.NodeTaskLifecyclePolicy
	}{
		{"retention-below-offline", `{"login_mode":"local_only","node_task_offline_reconcile_days":91}`, domain.DefaultNodeTaskLifecyclePolicy()},
		{"retention-below-backup", `{"login_mode":"local_only","node_task_backup_restore_days":91}`, domain.DefaultNodeTaskLifecyclePolicy()},
		{"retention-below-both", `{"login_mode":"local_only","node_task_result_retention_days":29}`, domain.DefaultNodeTaskLifecyclePolicy()},
		{"partial-edit-compares-existing", `{"login_mode":"local_only","node_task_result_retention_days":90}`, domain.NodeTaskLifecyclePolicy{OfflineReconcileDays: 100, BackupRestoreDays: 120, ResultRetentionDays: 365}},
		{"partially-corrupt-previous-not-repaired", `{"login_mode":"local_only"}`, domain.NodeTaskLifecyclePolicy{OfflineReconcileDays: 0, BackupRestoreDays: 30, ResultRetentionDays: 90}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &nodeTaskLifecycleSettingsRepo{settings: settingsWithNodeTaskPolicy(test.previous)}
			response := requestNodeTaskLifecycleSettings(t, nodeTaskLifecycleSettingsRouter(repo), http.MethodPut, test.body)
			if response.Code != http.StatusBadRequest || repo.saves != 0 {
				t.Fatalf("unsafe whole policy status=%d saves=%d body=%s", response.Code, repo.saves, response.Body.String())
			}
		})
	}
}

func TestAdminSettingsLoweringNodeTaskPolicyDoesNotMutateExistingTasksOrEvidence(t *testing.T) {
	db, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "node-task-lifecycle-settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	repos := sqlstore.NewRepos(db)
	ctx := t.Context()
	const agentID = "agt_settings_lifecycle"
	if err := repos.NodeAgent.Create(ctx, &domain.NodeAgent{AgentID: agentID, PanelID: 961, CredentialSHA256: nodeprotocol.ComputeTaskInputSHA256(agentID, nil)}); err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	task := &domain.NodeAgentTask{TaskID: "task-lifecycle-untouched", AgentID: agentID, Kind: "reality_probe.v1", Args: []byte("original input")}
	task.InputSHA256 = nodeprotocol.ComputeTaskInputSHA256(task.Kind, task.Args)
	if _, _, err := repos.NodeAgentTask.CreateOrGet(ctx, task); err != nil {
		t.Fatal(err)
	}
	result := domain.NodeAgentTaskResult{TaskID: task.TaskID, Kind: task.Kind, InputSHA256: task.InputSHA256, OK: true, Result: []byte("original result")}
	if err := repos.NodeAgentTask.CompleteBatch(ctx, agentID, []domain.NodeAgentTaskResult{result}, first); err != nil {
		t.Fatal(err)
	}
	beforeTask, err := repos.NodeAgentTask.GetByTaskID(ctx, task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	beforeEvidence, err := repos.NodeAgentTask.GetQuarantinedResult(ctx, agentID, task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	beforeTaskJSON, _ := json.Marshal(beforeTask)
	beforeEvidenceJSON, _ := json.Marshal(beforeEvidence)
	previous := settingsWithNodeTaskPolicy(domain.NodeTaskLifecyclePolicy{OfflineReconcileDays: 90, BackupRestoreDays: 90, ResultRetentionDays: 180})
	if err := repos.Settings.Save(ctx, previous); err != nil {
		t.Fatal(err)
	}
	router := nodeTaskLifecycleSettingsRouter(repos.Settings)
	want := domain.NodeTaskLifecyclePolicy{OfflineReconcileDays: 1, BackupRestoreDays: 1, ResultRetentionDays: 1}
	assertNodeTaskLifecycleNumbers(t, requestNodeTaskLifecycleSettings(t, router, http.MethodPut, `{"login_mode":"local_only","node_task_offline_reconcile_days":1,"node_task_backup_restore_days":1,"node_task_result_retention_days":1}`), want)
	afterTask, err := repos.NodeAgentTask.GetByTaskID(ctx, task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	afterEvidence, err := repos.NodeAgentTask.GetQuarantinedResult(ctx, agentID, task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	afterTaskJSON, _ := json.Marshal(afterTask)
	afterEvidenceJSON, _ := json.Marshal(afterEvidence)
	if !bytes.Equal(beforeTaskJSON, afterTaskJSON) || !bytes.Equal(beforeEvidenceJSON, afterEvidenceJSON) {
		t.Fatal("settings-only policy edit mutated task state, dispatch closure, or durable evidence")
	}
}
