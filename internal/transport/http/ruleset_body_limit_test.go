package http

import (
	"context"
	"encoding/json"
	"fmt"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	yamladapter "github.com/KazuhaHub/passwall-sub-panel/internal/adapters/yaml"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/audit"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/auth"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

type bodyLimitNodes struct{ ports.NodeRepo }

func (bodyLimitNodes) List(context.Context) ([]*domain.Node, error) { return nil, nil }

type bodyLimitAudit struct {
	ports.AuditRepo
	mu      sync.Mutex
	entries []*domain.AuditEntry
}

func (a *bodyLimitAudit) Insert(_ context.Context, e *domain.AuditEntry) error {
	a.mu.Lock()
	a.entries = append(a.entries, e)
	a.mu.Unlock()
	return nil
}

// bodyLimitRouter is the assembled router with the real audit middleware, the
// real rule-set handler over a YAML repo, and an optional panel prefix.
func bodyLimitRouter(t *testing.T, panelPath string) (router stdhttp.Handler, token string, auditRepo *bodyLimitAudit) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	admin := &domain.User{ID: 1, UPN: "admin@example.test", Enabled: true, Role: domain.RoleAdmin}
	users := routerReleaseUsers{users: map[int64]*domain.User{1: admin}}
	issuer := jwtutil.NewIssuer(strings.Repeat("test-only-key", 3), func() jwtutil.Params {
		return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: "body-limit-test"}
	})
	authSvc := auth.New(issuer)
	token, _, err := authSvc.IssueTokens(admin)
	if err != nil {
		t.Fatal(err)
	}
	configDir := t.TempDir()
	ruleRepo, err := yamladapter.NewRuleSetRepo(configDir)
	if err != nil {
		t.Fatal(err)
	}
	auditRepo = &bodyLimitAudit{}
	router = NewRouter(Deps{
		Cfg: &config.Config{ConfigDir: configDir},
		Repos: ports.Repos{
			User: users, Node: bodyLimitNodes{}, RuleSet: ruleRepo,
			Settings: &dispatchSettingsRepo{settings: ports.UISettings{PanelPath: panelPath}},
		},
		Auth: authSvc, User: user.New(users, nil, nil, nil, nil, nil, nil, nil),
		Audit: audit.New(auditRepo),
	})
	return router, token, auditRepo
}

// bigRuleContent is n short rule lines (~46 bytes each).
func bigRuleContent(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "- DOMAIN-SUFFIX,h%07d.example.com,DIRECT\n", i)
	}
	b.WriteString("- MATCH,DIRECT\n")
	return b.String()
}

func doBodyLimit(router stdhttp.Handler, method, target, token string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "https://panel.example"+target, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestRuleSetEndpointsAcceptLargeBodies(t *testing.T) {
	for _, panelPath := range []string{"", "/mypanel"} {
		t.Run("panel_path="+panelPath, func(t *testing.T) {
			router, token, auditRepo := bodyLimitRouter(t, panelPath)
			content := bigRuleContent(70000)
			body, _ := json.Marshal(map[string]any{"slug": "huge", "name": "Huge", "enabled": true, "content": content})
			if len(body) < 3<<20 || len(body) > 4<<20 {
				t.Fatalf("fixture body is %d bytes, want ~3 MiB", len(body))
			}

			w := doBodyLimit(router, "PUT", panelPath+"/api/admin/rules/huge", token, body)
			if w.Code != stdhttp.StatusOK {
				t.Fatalf("PUT 3 MiB status=%d body=%.300s", w.Code, w.Body.String())
			}

			inspect, _ := json.Marshal(map[string]any{"content": content})
			w = doBodyLimit(router, "POST", panelPath+"/api/admin/rules/inspect-proxy-groups", token, inspect)
			if w.Code != stdhttp.StatusOK {
				t.Fatalf("inspect 3 MiB status=%d body=%.300s", w.Code, w.Body.String())
			}

			// The audit row stays bounded: the 3 MiB string is cut to 8,192 chars.
			auditRepo.mu.Lock()
			defer auditRepo.mu.Unlock()
			if len(auditRepo.entries) == 0 {
				t.Fatal("no audit rows were written")
			}
			for _, e := range auditRepo.entries {
				if len(e.BeforeJSON) > 16<<10 {
					t.Fatalf("audit row is %d bytes, want it bounded by per-string truncation", len(e.BeforeJSON))
				}
			}
		})
	}
}

func TestRuleSetOversizeBodyIs413WithStableCode(t *testing.T) {
	for _, panelPath := range []string{"", "/mypanel"} {
		t.Run("panel_path="+panelPath, func(t *testing.T) {
			router, token, _ := bodyLimitRouter(t, panelPath)
			body, _ := json.Marshal(map[string]any{"slug": "huge", "content": strings.Repeat("x", 9<<20)})
			for _, target := range []string{"/api/admin/rules/huge", "/api/admin/rules/inspect-proxy-groups"} {
				method := "PUT"
				if strings.HasSuffix(target, "inspect-proxy-groups") {
					method = "POST"
				}
				w := doBodyLimit(router, method, panelPath+target, token, body)
				if w.Code != stdhttp.StatusRequestEntityTooLarge {
					t.Fatalf("%s %s status=%d body=%.300s", method, target, w.Code, w.Body.String())
				}
				var out map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatalf("%s: %v (%.200s)", target, err, w.Body.String())
				}
				if out["code"] != "body_too_large" || out["limit_bytes"] != float64(8<<20) || out["error"] == "" {
					t.Fatalf("%s body = %s", target, w.Body.String())
				}
			}
		})
	}
}

// The widening is the rule-set family only: any other admin write keeps 1 MiB.
func TestOtherAdminWritesKeepOneMiBLimit(t *testing.T) {
	router, token, _ := bodyLimitRouter(t, "")
	body, _ := json.Marshal(map[string]any{"name": strings.Repeat("x", 2<<20)})
	w := doBodyLimit(router, "PUT", "/api/admin/users/1", token, body)
	if w.Code != stdhttp.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%.200s", w.Code, w.Body.String())
	}
}
