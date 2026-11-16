package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/group"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/user"
)

// groupRowFake stores one group row and hands out copies, like the database.
// Every method the Update path does not use stays nil and would panic.
type groupRowFake struct {
	ports.GroupRepo
	mu  sync.Mutex
	row domain.Group
}

func (f *groupRowFake) GetByID(_ context.Context, id int64) (*domain.Group, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.row.ID {
		return nil, domain.ErrNotFound
	}
	g := f.row
	g.TagFilter.Tags = append([]string(nil), f.row.TagFilter.Tags...)
	return &g, nil
}

func (f *groupRowFake) Update(_ context.Context, g *domain.Group) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.row = *g
	return nil
}

// groupUpdateHarness serves PUT /groups/:id with a recording background
// runner in place of the member resync, which never runs.
func groupUpdateHarness(row domain.Group) (*gin.Engine, *groupRowFake, func() []string) {
	gin.SetMode(gin.TestMode)
	repo := &groupRowFake{row: row}
	users := user.New(nil, nil, nil, nil, nil, nil, nil, nil)
	var mu sync.Mutex
	var dispatched []string
	users.SetBackgroundRunner(func(name string, _ func(context.Context)) {
		mu.Lock()
		defer mu.Unlock()
		dispatched = append(dispatched, name)
	})
	h := &AdminGroupHandler{group: group.New(repo, nil, nil), user: users}
	r := gin.New()
	r.PUT("/groups/:id", h.Update)
	return r, repo, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), dispatched...)
	}
}

func putGroup(t *testing.T, r *gin.Engine, body string) {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/groups/7", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /groups/7 = %d %s", w.Code, w.Body.String())
	}
}

// The group editor sends tag_filter on every save. A save that restates the
// stored filter (in any spelling the matcher treats as equal) must not push
// every member to the panels; one that really changes it, or the limits, must.
func TestGroupUpdateResyncsOnlyOnRealChange(t *testing.T) {
	three := 3
	stored := domain.Group{
		ID: 7, Slug: "g", Name: "G",
		TagFilter: domain.TagFilter{Tags: []string{"region:JP", "tag:reality"}},
		Limits:    domain.GroupLimits{IPLimit: &three},
	}
	for _, tc := range []struct {
		name   string
		stored domain.TagFilter
		body   string
		resync bool
	}{
		{"rename with the editor's unchanged filter", stored.TagFilter,
			`{"name":"G2","tag_filter":{"all":false,"tags":["region:JP","tag:reality"],"mode":"all"},"ip_limit":3}`, false},
		{"same filter reordered and respaced", stored.TagFilter,
			`{"tag_filter":{"all":false,"tags":[" tag: reality","region:JP"]}}`, false},
		{"no tag_filter in request", stored.TagFilter, `{"remark":"x"}`, false},
		{"all stays all", domain.TagFilter{All: true},
			`{"name":"G2","tag_filter":{"all":true,"tags":[],"mode":"all"}}`, false},

		{"condition added", stored.TagFilter,
			`{"tag_filter":{"all":false,"tags":["region:JP","tag:reality","tag:x"],"mode":"all"}}`, true},
		{"mode all to any", stored.TagFilter,
			`{"tag_filter":{"all":false,"tags":["region:JP","tag:reality"],"mode":"any"}}`, true},
		{"conditions to all", stored.TagFilter,
			`{"tag_filter":{"all":true,"tags":[],"mode":"all"}}`, true},
		{"all to conditions", domain.TagFilter{All: true},
			`{"tag_filter":{"all":false,"tags":["region:JP"],"mode":"all"}}`, true},
		{"limit changed, filter unchanged", stored.TagFilter,
			`{"tag_filter":{"all":false,"tags":["region:JP","tag:reality"],"mode":"all"},"ip_limit":5}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := stored
			row.TagFilter = tc.stored
			r, repo, dispatched := groupUpdateHarness(row)
			putGroup(t, r, tc.body)
			got := dispatched()
			if tc.resync && (len(got) != 1 || got[0] != "user.resync-group-members") {
				t.Fatalf("dispatched %v, want one member resync", got)
			}
			if !tc.resync && len(got) != 0 {
				t.Fatalf("dispatched %v, want no member resync", got)
			}
			// Whatever the decision, the request's filter is what gets stored.
			if strings.Contains(tc.body, `"tag_filter"`) && repo.row.TagFilter.All != strings.Contains(tc.body, `"all":true`) {
				t.Fatalf("stored filter %+v does not reflect the request", repo.row.TagFilter)
			}
		})
	}
}
