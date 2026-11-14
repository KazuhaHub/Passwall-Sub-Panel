package destlist

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// dlcYAML renders categories the way the v2fly release does: a lists: array,
// one entry per category with its name and quoted rules.
func dlcYAML(cats map[string][]string) []byte {
	names := make([]string, 0, len(cats))
	for n := range cats {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("lists:\n")
	for _, n := range names {
		fmt.Fprintf(&b, "  - name: %q\n    length: %d\n    rules:\n", n, len(cats[n]))
		for _, r := range cats[n] {
			fmt.Fprintf(&b, "      - %q\n", r)
		}
	}
	return []byte(b.String())
}

// fakeUpstream stands in for GitHub: the releases/latest probe and the
// release assets. releases maps a tag to the YAML it serves; badSum serves a
// wrong checksum for the tags in it.
type fakeUpstream struct {
	mu       sync.Mutex
	latest   string
	releases map[string][]byte
	badSum   map[string]bool
	probes   int
	downs    int
	srv      *httptest.Server
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	f := &fakeUpstream{releases: map[string][]byte{}, badSum: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/latest", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.probes++
		fmt.Fprintf(w, `{"tag_name": %q}`, f.latest)
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/dl/"), "/")
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		body, ok := f.releases[parts[0]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		switch parts[1] {
		case "dlc.dat_plain.yml":
			f.downs++
			_, _ = w.Write(body)
		case "dlc.dat_plain.yml.sha256sum":
			sum := sha256.Sum256(body)
			hexsum := hex.EncodeToString(sum[:])
			if f.badSum[parts[0]] {
				hexsum = strings.Repeat("0", 64)
			}
			fmt.Fprintf(w, "%s  dlc.dat_plain.yml\n", hexsum)
		default:
			http.NotFound(w, r)
		}
	})
	f.srv = httptest.NewTLSServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeUpstream) release(tag string, cats map[string][]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases[tag] = dlcYAML(cats)
	f.latest = tag
}

// fakeSettings is the settings store the loop reads.
type fakeSettings struct {
	mu  sync.Mutex
	set ports.UISettings
}

func (f *fakeSettings) Load(context.Context, ports.UISettings) (ports.UISettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.set, nil
}

// harness is a Service over a real SQLite store and a fake upstream.
type harness struct {
	t        *testing.T
	repos    *sqlstore.DestRepos
	groups   ports.GroupRepo
	svc      *Service
	up       *fakeUpstream
	settings *fakeSettings
	dir      string
	clock    *fakeClock
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db, err := sqlstore.OpenQuiet("sqlite", filepath.Join(t.TempDir(), "dest.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	h := &harness{
		t: t, repos: sqlstore.NewDestRepos(db), groups: sqlstore.NewRepos(db).Group, up: newFakeUpstream(t), settings: &fakeSettings{},
		dir: t.TempDir(), clock: &fakeClock{t: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)},
	}
	h.svc = h.newService(nil)
	return h
}

// newService builds a Service over the harness's stores and data dir; a second
// call is "the process restarted".
func (h *harness) newService(loc MMDBLocator) *Service {
	s := New(Deps{
		Lists: h.repos.Lists, Policies: h.repos.Policies, SourceRules: h.repos.SourceRules, State: h.repos.State,
		Groups: h.repos.GroupAllowlists, Settings: h.settings, Locator: loc, DataDir: h.dir, HTTP: h.up.srv.Client(), Now: h.clock.Now,
	})
	s.geo.apiURL = h.up.srv.URL + "/api/latest"
	s.geo.downloadBase = h.up.srv.URL + "/dl"
	return s
}

func (h *harness) generation() int64 {
	h.t.Helper()
	st, err := h.repos.State.Get(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	return st.Generation
}

// policy creates a policy using the lists, in the given state.
func (h *harness) policy(name string, state domain.DestState, action domain.DestAction, lists ...int64) *domain.DestPolicy {
	h.t.Helper()
	p := &domain.DestPolicy{Name: name, Action: action, State: state, ListIDs: lists, Scope: domain.DestScopeAll}
	if err := h.repos.Policies.Create(context.Background(), p); err != nil {
		h.t.Fatal(err)
	}
	return p
}

// newListServer is a TLS server for list bodies, separate from the fake
// geosite upstream.
func newListServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return srv
}
