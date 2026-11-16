package xui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The panel's update lookup goes to api.github.com from the CI runner's shared
// address; when GitHub has throttled that address the panel answers with its
// failure envelope however healthy it is. The check asks GitHub directly so a
// throttled runner is told apart from a real regression.
func TestGitHubLookupUnavailable(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   bool
	}{
		"quota left":       {200, `{"rate":{"limit":60,"remaining":12}}`, false},
		"quota exhausted":  {200, `{"rate":{"limit":60,"remaining":0}}`, true},
		"throttled 403":    {403, `{"message":"API rate limit exceeded"}`, true},
		"throttled 429":    {429, ``, true},
		"server error":     {502, ``, true},
		"unparseable body": {200, `not json`, false},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			got, reason := githubLookupUnavailable(srv.URL)
			if got != tc.want {
				t.Fatalf("unavailable = %v (%s), want %v", got, reason, tc.want)
			}
		})
	}
	if got, _ := githubLookupUnavailable("http://127.0.0.1:1"); !got {
		t.Fatal("an unreachable GitHub must count as unavailable")
	}
}
