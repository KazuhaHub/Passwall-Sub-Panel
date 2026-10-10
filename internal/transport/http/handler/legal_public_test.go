package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/gin-gonic/gin"
)

func TestLegalPublicHTTP_AnonymousFallbackAndCompleteETag(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repos := legalHTTPRepos(t)
	ctx := context.Background()
	s := ports.UISettings{LegalEnabled: true}
	if err := repos.Settings.Save(ctx, s); err != nil {
		t.Fatal(err)
	}
	publish := func(kind, locale, content string, major bool) {
		t.Helper()
		if _, err := repos.Legal.Publish(ctx, domain.LegalDraft{Kind: kind, Locale: locale, Content: content, ConsentBump: major, PublishedBy: 37}); err != nil {
			t.Fatal(err)
		}
	}
	publish("privacy", "zh-CN", "Chinese\n[[data-collection]]", false)
	r := gin.New()
	r.GET("/api/legal/:kind", NewLegalPublicHandler(repos.Legal).Get)
	request := func(path, etag string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("If-None-Match", etag)
		r.ServeHTTP(w, req)
		return w
	}
	decode := func(w *httptest.ResponseRecorder) domain.LegalPublicDocument {
		t.Helper()
		var doc domain.LegalPublicDocument
		if w.Code != 200 || w.Header().Get("ETag") == "" || w.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("response %d %v %s", w.Code, w.Header(), w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(w.Body.String(), "published_by") || strings.Contains(w.Body.String(), `"id"`) {
			t.Fatal("public response leaked administrator metadata")
		}
		return doc
	}
	w := request("/api/legal/privacy?lang=zh-TW", "")
	doc := decode(w)
	if doc.Locale != "zh-CN" || doc.FallbackFrom != "zh-TW" || doc.ConsentVersion != 1 || doc.DataCollection.ConnectionRetentionDays != 7 || doc.DataCollection.Access == nil {
		t.Fatalf("fallback/default disclosure %+v", doc)
	}
	etag := w.Header().Get("ETag")
	for _, validator := range []string{etag, `"other", W/` + etag, "*"} {
		w := request("/api/legal/privacy?lang=zh-TW", validator)
		if w.Code != 304 || w.Body.Len() != 0 || w.Header().Get("ETag") != etag || w.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("revalidation %q => %d %v %s", validator, w.Code, w.Header(), w.Body.String())
		}
	}
	// Another kind's major publication changes global consent, even though
	// this exact Markdown document and its local version have not changed.
	publish("terms", "en-US", "terms", true)
	w = request("/api/legal/privacy?lang=zh-TW", etag)
	doc = decode(w)
	if doc.ConsentVersion != 2 || doc.Version != 1 || w.Header().Get("ETag") == etag {
		t.Fatalf("global publication did not revalidate %+v", doc)
	}
	etag = w.Header().Get("ETag")
	s.SubLogRetentionDays = 14
	s.RiskHWIDCaptureOff = true
	s.RiskRefreshIntervalMinutes = 30
	if err := repos.Settings.Save(ctx, s); err != nil {
		t.Fatal(err)
	}
	w = request("/api/legal/privacy?lang=zh-TW", etag)
	doc = decode(w)
	if w.Header().Get("ETag") == etag || doc.DataCollection.SubLogRetentionDays != 14 || doc.DataCollection.HWIDCaptured || doc.DataCollection.RiskAssessmentRefreshMinutes != 30 {
		t.Fatalf("settings did not revalidate %+v", doc.DataCollection)
	}
	for _, path := range []string{"/api/legal/privacy?lang=../../bad", "/api/legal/unknown"} {
		if got := request(path, "*"); got.Code != 400 {
			t.Fatalf("invalid %s => %d", path, got.Code)
		}
	}
	s.LegalEnabled = false
	if err := repos.Settings.Save(ctx, s); err != nil {
		t.Fatal(err)
	}
	if got := request("/api/legal/privacy?lang=zh-TW", w.Header().Get("ETag")); got.Code != 404 || got.Header().Get("ETag") != "" {
		t.Fatalf("disabled conditional response %d %v", got.Code, got.Header())
	}
}

type failedLegalPublicRepo struct {
	ports.LegalRepo
	err error
}

func (r failedLegalPublicRepo) Public(context.Context, string, string) (domain.LegalPublicDocument, error) {
	return domain.LegalPublicDocument{}, r.err
}

func TestLegalPublicHTTP_AbsentAndUnavailableBeforeConditional(t *testing.T) {
	for _, tc := range []struct {
		repo   ports.LegalRepo
		status int
	}{
		{nil, 503},
		{failedLegalPublicRepo{err: domain.ErrNotFound}, 404},
		{failedLegalPublicRepo{err: errors.New("private database detail")}, 500},
	} {
		r := gin.New()
		r.GET("/api/legal/:kind", NewLegalPublicHandler(tc.repo).Get)
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/legal/terms", nil)
		req.Header.Set("If-None-Match", "*")
		r.ServeHTTP(w, req)
		if w.Code != tc.status || w.Header().Get("ETag") != "" || strings.Contains(w.Body.String(), "private database detail") {
			t.Fatalf("failure %d %v %s", w.Code, w.Header(), w.Body.String())
		}
	}
}
