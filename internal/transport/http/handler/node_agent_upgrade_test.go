package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/nodeagentupgrade"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

type nativeUpgradeHTTPStub struct {
	status      *nodeagentupgrade.Status
	created     bool
	err         error
	calls       int
	panelID     int64
	request     nodeagentupgrade.Request
	key, taskID string
}

func (s *nativeUpgradeHTTPStub) Request(_ context.Context, panelID int64, request nodeagentupgrade.Request, key string) (*nodeagentupgrade.Status, bool, error) {
	s.calls++
	s.panelID = panelID
	s.request = request
	s.key = key
	if key == "" {
		return nil, false, domain.ErrValidation
	}
	return s.status, s.created, s.err
}
func (s *nativeUpgradeHTTPStub) Get(_ context.Context, panelID int64, taskID string) (*nodeagentupgrade.Status, error) {
	s.calls++
	s.panelID = panelID
	s.taskID = taskID
	return s.status, s.err
}

func upgradeHTTPRequest(service NativeAgentUpgradeService, role domain.Role, method, path, body, key string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	h := NewAdminServersHandler(nil, nil, nil, nil, nil, nil).WithNativeAgentUpgrade(service)
	router := gin.New()
	group := router.Group("/api/admin", func(c *gin.Context) {
		if role != "" {
			c.Set(middleware.CtxClaims, &jwtutil.Claims{Role: role, UPN: "admin@example.test"})
		}
		c.Next()
	}, middleware.RequireRole(domain.RoleAdmin))
	group.POST("/servers/:id/upgrade-node-agent", h.UpgradeNativeAgent)
	group.GET("/servers/:id/node-agent-upgrades/:task_id", h.GetNativeAgentUpgrade)
	request := httptest.NewRequest(method, "/api/admin/servers/"+path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

const upgradeHTTPBody = `{"version":"v0.0.1-beta3","expected_version":"v0.0.1-beta2"}`
const upgradeHTTPKey = "request-upgrade-http-0001"

func TestNativeUpgradeHTTPAdministratorBoundaryAndPrivateStatus(t *testing.T) {
	status := &nodeagentupgrade.Status{TaskID: "tsk1_test", AgentID: "agt_test", Version: "v0.0.1-beta3", ExpectedVersion: "v0.0.1-beta2", Status: domain.NodeAgentTaskQueued, UpgradeState: "queued", NotAfterMS: 123456789}
	service := &nativeUpgradeHTTPStub{status: status, created: true}
	w := upgradeHTTPRequest(service, domain.RoleAdmin, http.MethodPost, "41/upgrade-node-agent", upgradeHTTPBody, upgradeHTTPKey)
	if w.Code != http.StatusAccepted || service.calls != 1 || service.panelID != 41 || service.key != upgradeHTTPKey || service.request.Version != status.Version || service.request.ExpectedVersion != status.ExpectedVersion {
		t.Fatalf("create code=%d call=%+v body=%s", w.Code, service, w.Body)
	}
	if w.Header().Get("Cache-Control") != "no-store, private" || w.Header().Get("Pragma") != "no-cache" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("upgrade responses must not be cached")
	}
	var got nodeagentupgrade.Status
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.TaskID != status.TaskID {
		t.Fatalf("response=%+v err=%v", got, err)
	}
	service.created = false
	w = upgradeHTTPRequest(service, domain.RoleAdmin, http.MethodPost, "41/upgrade-node-agent", upgradeHTTPBody, upgradeHTTPKey)
	if w.Code != http.StatusOK {
		t.Fatalf("replay code=%d", w.Code)
	}
	w = upgradeHTTPRequest(service, domain.RoleAdmin, http.MethodGet, "41/node-agent-upgrades/tsk1_test", "", "")
	if w.Code != http.StatusOK || service.taskID != "tsk1_test" || service.panelID != 41 {
		t.Fatalf("get code=%d service=%+v", w.Code, service)
	}
	for _, role := range []domain.Role{"", domain.RoleUser, domain.RoleOperator} {
		for _, route := range []struct{ method, path, body string }{{http.MethodPost, "41/upgrade-node-agent", upgradeHTTPBody}, {http.MethodGet, "41/node-agent-upgrades/tsk1_test", ""}} {
			before := service.calls
			w := upgradeHTTPRequest(service, role, route.method, route.path, route.body, upgradeHTTPKey)
			if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
				t.Fatalf("role=%s method=%s code=%d", role, route.method, w.Code)
			}
			if service.calls != before {
				t.Fatal("unauthorized request reached upgrade service")
			}
		}
	}
}

func TestNativeUpgradeHTTPStrictBoundedMetadataAndIdempotency(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"version":"latest","expected_version":"v0.0.1-beta2"}`, `{"version":"v0.0.1-beta2","expected_version":"v0.0.1-beta2"}`, `{"version":"v0.0.1-beta1","expected_version":"v0.0.1-beta2"}`, `{"version":"v0.0.1-beta3","expected_version":"v0.0.1-beta2","command":"private-secret"}`, `{"version":"v0.0.1-beta3","version":"v0.0.1-beta4","expected_version":"v0.0.1-beta2"}`, `{"VERSION":"v0.0.1-beta3","expected_version":"v0.0.1-beta2"}`, `{"version":1,"expected_version":"v0.0.1-beta2"}`, upgradeHTTPBody + ` {}`, strings.Repeat(" ", 4097) + upgradeHTTPBody} {
		service := &nativeUpgradeHTTPStub{}
		w := upgradeHTTPRequest(service, domain.RoleAdmin, http.MethodPost, "41/upgrade-node-agent", body, upgradeHTTPKey)
		if w.Code != http.StatusBadRequest || service.calls != 0 {
			t.Fatalf("invalid metadata accepted: code=%d calls=%d", w.Code, service.calls)
		}
		if strings.Contains(w.Body.String(), "private-secret") {
			t.Fatal("rejected metadata was echoed")
		}
	}
	service := &nativeUpgradeHTTPStub{}
	w := upgradeHTTPRequest(service, domain.RoleAdmin, http.MethodPost, "41/upgrade-node-agent", upgradeHTTPBody, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing idempotency key code=%d", w.Code)
	}
	for _, id := range []string{"0", "-1", "invalid"} {
		w := upgradeHTTPRequest(service, domain.RoleAdmin, http.MethodPost, id+"/upgrade-node-agent", upgradeHTTPBody, upgradeHTTPKey)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid server ID %s code=%d", id, w.Code)
		}
	}
}

func TestNativeUpgradeHTTPMapsSafeErrorsAndDegradedService(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{{domain.ErrValidation, 400}, {domain.ErrNotFound, 404}, {domain.ErrConflict, 409}, {domain.ErrResourceExhausted, 429}, {errors.New("database down"), 503}} {
		service := &nativeUpgradeHTTPStub{err: fmt.Errorf("private-secret payload: %w", tc.err)}
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			path := "41/upgrade-node-agent"
			body := upgradeHTTPBody
			if method == http.MethodGet {
				path = "41/node-agent-upgrades/tsk1_test"
				body = ""
			}
			w := upgradeHTTPRequest(service, domain.RoleAdmin, method, path, body, upgradeHTTPKey)
			if w.Code != tc.want || strings.Contains(w.Body.String(), "private-secret") {
				t.Fatalf("unsafe error code=%d want=%d body=%s", w.Code, tc.want, w.Body)
			}
		}
	}
	w := upgradeHTTPRequest(nil, domain.RoleAdmin, http.MethodPost, "41/upgrade-node-agent", upgradeHTTPBody, upgradeHTTPKey)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil service code=%d", w.Code)
	}
}
