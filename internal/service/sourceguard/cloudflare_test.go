package sourceguard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type memStore struct {
	ranges domain.CloudflareRanges
	ok     bool
	saves  int
}

func (m *memStore) LoadCloudflare(context.Context) (domain.CloudflareRanges, bool, error) {
	return m.ranges, m.ok, nil
}
func (m *memStore) SaveCloudflare(_ context.Context, r domain.CloudflareRanges) error {
	m.ranges, m.ok = r, true
	m.saves++
	return nil
}

const goodV4 = "173.245.48.0/20\n103.21.244.0/22\n103.22.200.0/22\n103.31.4.0/22\n141.101.64.0/18\n"
const onlyV6 = "2400:cb00::/32\n2606:4700::/32\n2803:f800::/32\n2405:b500::/32\n2405:8100::/32\n"
const goodV6 = "2400:cb00::/32\n2606:4700::/32\n"

func serve(t *testing.T, v4, v6 *atomic.Value, userAgents *[]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	handle := func(v *atomic.Value) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if userAgents != nil {
				*userAgents = append(*userAgents, r.Header.Get("User-Agent"))
			}
			body := v.Load().(string)
			if body == "fail" {
				http.Error(w, "nope", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(body))
		}
	}
	mux.HandleFunc("/v4", handle(v4))
	mux.HandleFunc("/v6", handle(v6))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newValue(s string) *atomic.Value {
	v := &atomic.Value{}
	v.Store(s)
	return v
}

func TestCloudflareUsesBuiltinUntilAFetchSucceeds(t *testing.T) {
	var agents []string
	v4, v6 := newValue(goodV4), newValue(goodV6)
	srv := serve(t, v4, v6, &agents)
	store := &memStore{}
	c := NewCloudflare(store, nil).WithSource(srv.Client(), srv.URL+"/v4", srv.URL+"/v6")
	at := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return at }

	cur := c.Current(t.Context())
	if cur.Source != SourceBuiltin || len(cur.CIDRs) != len(BuiltinCloudflare) || cur.BuiltinDate != BuiltinCloudflareDate {
		t.Fatalf("before fetch: %+v", cur)
	}
	if err := c.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	cur = c.Current(t.Context())
	if cur.Source != SourceFetched || !cur.FetchedAt.Equal(at) || len(cur.CIDRs) != 7 {
		t.Fatalf("after fetch: %+v", cur)
	}
	if store.saves != 1 || len(store.ranges.CIDRs) != 7 {
		t.Fatalf("store = %+v saves=%d", store.ranges, store.saves)
	}
	for _, ua := range agents {
		if ua != "" {
			t.Fatalf("a User-Agent was sent: %q", ua)
		}
	}

	// A new process reads the stored list.
	again := NewCloudflare(store, nil).Current(t.Context())
	if again.Source != SourceFetched || !reflect.DeepEqual(again.CIDRs, cur.CIDRs) {
		t.Fatalf("restart: %+v", again)
	}
}

func TestCloudflareKeepsThePreviousRangesOnAnyFailure(t *testing.T) {
	v4, v6 := newValue(goodV4), newValue(goodV6)
	srv := serve(t, v4, v6, nil)
	store := &memStore{}
	c := NewCloudflare(store, nil).WithSource(srv.Client(), srv.URL+"/v4", srv.URL+"/v6")
	if err := c.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	good := c.Current(t.Context())
	for name, body := range map[string][2]string{
		"http error":    {"fail", goodV6},
		"too few":       {"173.245.48.0/20\n", goodV6},
		"one family":    {onlyV6, onlyV6},
		"not canonical": {"173.245.48.1/20\n103.21.244.0/22\n103.22.200.0/22\n103.31.4.0/22\n141.101.64.0/18\n", goodV6},
		"garbage":       {"<html>blocked</html>\n", goodV6},
		"match all":     {"0.0.0.0/0\n103.21.244.0/22\n103.22.200.0/22\n103.31.4.0/22\n141.101.64.0/18\n", goodV6},
		"too large":     {string(make([]byte, maxBody+10)), goodV6},
		"v6 http error": {goodV4, "fail"},
	} {
		v4.Store(body[0])
		v6.Store(body[1])
		if err := c.Refresh(t.Context()); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if got := c.Current(t.Context()); !reflect.DeepEqual(got, good) {
			t.Errorf("%s: ranges changed to %+v", name, got)
		}
	}
	if store.saves != 1 {
		t.Fatalf("a failed fetch was stored (%d saves)", store.saves)
	}
}

func TestCloudflareRunHonoursTheSetting(t *testing.T) {
	v4, v6 := newValue(goodV4), newValue(goodV6)
	srv := serve(t, v4, v6, nil)
	var on atomic.Bool
	c := NewCloudflare(&memStore{}, func(context.Context) bool { return on.Load() }).
		WithSource(srv.Client(), srv.URL+"/v4", srv.URL+"/v6")

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { c.Run(ctx, 10*time.Millisecond); close(done) }()
	time.Sleep(100 * time.Millisecond)
	if c.Current(ctx).Source != SourceBuiltin {
		t.Fatal("fetched while auto-fetch was off")
	}
	cancel()
	<-done

	on.Store(true)
	ctx2, cancel2 := context.WithCancel(t.Context())
	defer cancel2()
	go c.Run(ctx2, 10*time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for c.Current(ctx2).Source != SourceFetched {
		if time.Now().After(deadline) {
			t.Fatal("never fetched with auto-fetch on")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
