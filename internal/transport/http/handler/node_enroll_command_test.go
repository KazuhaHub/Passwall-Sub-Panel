package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/nodebootstrap"
	"github.com/KazuhaHub/passwall-sub-panel/internal/transport/http/middleware"
)

func mintEnrollFixture(t *testing.T, tokens *fakeTokens, origin string, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, origin+"/api/admin/servers/enroll-token", nil)
	if authenticated {
		c.Set(middleware.CtxClaims, &jwtutil.Claims{UserID: 12})
	}
	h := NewNodeEnrollHandler(tokens, &fakePanels{}, &fakePool{}, probeOnly(""))
	h.Mint(c)
	return w
}

func TestEnrollMintUsesSharedShortPrivateLauncher(t *testing.T) {
	tokens := &fakeTokens{}
	before := time.Now()
	w := mintEnrollFixture(t, tokens, "https://psp.example.com:8788", true)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	var response enrollTokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	endpoint := regexp.MustCompile(`https://psp\.example\.com:8788/enroll/[A-Za-z0-9_-]+`).FindString(response.Command)
	if endpoint == "" {
		t.Fatal("command does not target the intended enrollment origin")
	}
	expected, err := nodebootstrap.InstallCommand(endpoint)
	if err != nil || response.Command != expected || response.Cautious != expected {
		t.Fatal("enrollment did not reuse the complete-download launcher")
	}
	if len(response.Command) > 320 || strings.ContainsAny(response.Command, "\r\n\x00") {
		t.Fatal("enrollment command is not a short single line")
	}
	if len(tokens.created) != 1 {
		t.Fatal("enrollment did not persist exactly one token")
	}
	raw := strings.TrimPrefix(endpoint, "https://psp.example.com:8788/enroll/")
	stored := tokens.created[0]
	if stored.TokenHash != hashEnrollToken(raw) || stored.UserID != 12 || !stored.ExpiresAt.Equal(response.ExpiresAt) || response.ExpiresAt.Before(before.Add(nodeEnrollTTL)) || response.ExpiresAt.After(time.Now().Add(nodeEnrollTTL)) {
		t.Fatal("enrollment token identity or existing expiration changed")
	}
	assertPrivateEnrollResponse(t, w)
}

func TestEnrollMintRefusesNonHTTPSOrUnsafeOriginBeforeTokenCreation(t *testing.T) {
	for _, origin := range []string{"http://psp.example.com:8788", "https://psp.example.com:0", "https://psp.example.com:65536"} {
		t.Run(origin, func(t *testing.T) {
			tokens := &fakeTokens{}
			w := mintEnrollFixture(t, tokens, origin, true)
			if w.Code != http.StatusBadRequest || len(tokens.created) != 0 {
				t.Fatal("noncanonical origin persisted a usable enrollment token")
			}
			if !strings.Contains(w.Body.String(), "canonical HTTPS enrollment URL") {
				t.Fatal("refusal does not explain the HTTPS requirement")
			}
			assertPrivateEnrollResponse(t, w)
		})
	}

	tokens := &fakeTokens{}
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "https://psp.example.com/api/admin/servers/enroll-token", nil)
	c.Request.Host = "evil.example.com'; id; :'"
	c.Set(middleware.CtxClaims, &jwtutil.Claims{UserID: 12})
	NewNodeEnrollHandler(tokens, &fakePanels{}, &fakePool{}, probeOnly("")).Mint(c)
	if w.Code != http.StatusBadRequest || len(tokens.created) != 0 || strings.Contains(w.Body.String(), "id;") {
		t.Fatal("an unsafe host entered the enrollment command or token store")
	}
}

func TestEnrollMintStillRequiresAuthentication(t *testing.T) {
	tokens := &fakeTokens{}
	w := mintEnrollFixture(t, tokens, "https://psp.example.com", false)
	if w.Code != http.StatusUnauthorized || len(tokens.created) != 0 {
		t.Fatal("unauthenticated enrollment persisted a token")
	}
	assertPrivateEnrollResponse(t, w)
}

func TestEnrollScriptIsPrivatelyFramedForLegacyCurl(t *testing.T) {
	h := NewNodeEnrollHandler(&fakeTokens{}, &fakePanels{}, &fakePool{}, probeOnly(""))
	for _, token := range []string{goodToken, "short"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "https://psp.example.com/enroll/"+token, nil)
		enrollRouter(h).ServeHTTP(w, req)
		assertPrivateEnrollResponse(t, w)
		if token == goodToken {
			if w.Code != http.StatusOK || len(w.Body.Bytes()) == 0 || len(w.Body.Bytes()) > 1048576 || w.Header().Get("Content-Length") != strconv.Itoa(w.Body.Len()) {
				t.Fatal("successful enrollment script is not explicitly bounded and framed")
			}
			if w.Header().Get("Content-Type") != "text/x-shellscript; charset=utf-8" {
				t.Fatal("enrollment script content type changed")
			}
		}
	}
}

func assertPrivateEnrollResponse(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Header().Get("Cache-Control") != "no-store, private" || w.Header().Get("Pragma") != "no-cache" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("enrollment response is not private")
	}
}
