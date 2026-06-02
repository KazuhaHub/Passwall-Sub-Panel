package geo

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type fakeSettings struct{ s ports.UISettings }

func (f *fakeSettings) Load(_ context.Context, _ ports.UISettings) (ports.UISettings, error) {
	return f.s, nil
}
func (f *fakeSettings) Save(_ context.Context, s ports.UISettings) error { f.s = s; return nil }

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("garbage-not-a-real-mmdb"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Selection rules: chosen file wins when present; otherwise first by name;
// missing chosen falls back to first; only ONE file is ever active (no merge,
// so two databases can't conflict). Base() blocks path traversal.
func TestResolveActivePathSelection(t *testing.T) {
	cfg := t.TempDir()
	svc := New(&fakeSettings{}, cfg)

	if p := svc.resolveActivePath(""); p != "" {
		t.Fatalf("empty dir → %q, want empty", p)
	}
	write(t, filepath.Join(svc.Dir(), "a.mmdb"))
	write(t, filepath.Join(svc.Dir(), "b.mmdb"))

	if got := filepath.Base(svc.resolveActivePath("")); got != "a.mmdb" {
		t.Fatalf("default → %q, want a.mmdb (first by name)", got)
	}
	if got := filepath.Base(svc.resolveActivePath("b.mmdb")); got != "b.mmdb" {
		t.Fatalf("chosen → %q, want b.mmdb", got)
	}
	if got := filepath.Base(svc.resolveActivePath("nope.mmdb")); got != "a.mmdb" {
		t.Fatalf("missing chosen → %q, want a.mmdb fallback", got)
	}
	if got := filepath.Base(svc.resolveActivePath("../../../etc/x.mmdb")); got != "a.mmdb" {
		t.Fatalf("traversal → %q, want a.mmdb (no escape)", got)
	}
}

// TestLookupConcurrentReloadNoDeadlock exercises the H2 fix: Lookup now holds
// the read lock across its whole loop while the hot-reload Close()s the old
// reader under the write lock. This guards against (a) a deadlock regression
// from that lock-around-loop change, and (b) under `go test -race`, an unguarded
// concurrent read/write of s.reader. (The underlying munmap-during-Lookup crash
// needs a valid mmdb to reproduce and would abort the process, so it can't be a
// normal assertion — this pins the locking discipline instead.)
func TestLookupConcurrentReloadNoDeadlock(t *testing.T) {
	cfg := t.TempDir()
	svc := New(&fakeSettings{s: ports.UISettings{GeoIPEnabled: true}}, cfg)
	write(t, filepath.Join(svc.Dir(), "db.mmdb")) // bad mmdb → reader stays nil; lock paths still run

	stop := make(chan struct{})
	var reloader sync.WaitGroup
	reloader.Add(1)
	go func() {
		defer reloader.Done()
		for {
			select {
			case <-stop:
				return
			default:
				svc.reload()
				svc.ensureReader("")
			}
		}
	}()

	var readers sync.WaitGroup
	for i := 0; i < 8; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for j := 0; j < 300; j++ {
				svc.Lookup(context.Background(), []string{"8.8.8.8", "1.1.1.1", "9.9.9.9"})
			}
		}()
	}

	doneReaders := make(chan struct{})
	go func() { readers.Wait(); close(doneReaders) }()
	select {
	case <-doneReaders:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: concurrent Lookup + reload did not complete in 10s")
	}
	close(stop)
	reloader.Wait()
}

func TestLookupDisabledAndBadFile(t *testing.T) {
	cfg := t.TempDir()

	off := New(&fakeSettings{s: ports.UISettings{GeoIPEnabled: false}}, cfg)
	if m := off.Lookup(context.Background(), []string{"8.8.8.8"}); len(m) != 0 {
		t.Fatalf("disabled → %v, want empty", m)
	}

	on := New(&fakeSettings{s: ports.UISettings{GeoIPEnabled: true}}, cfg)
	write(t, filepath.Join(on.Dir(), "bad.mmdb")) // not a real mmdb
	if m := on.Lookup(context.Background(), []string{"8.8.8.8"}); len(m) != 0 {
		t.Fatalf("garbage db → %v, want empty (graceful, no crash)", m)
	}
}

func TestStatusListsAvailable(t *testing.T) {
	cfg := t.TempDir()
	svc := New(&fakeSettings{s: ports.UISettings{GeoIPEnabled: true, GeoIPDBFile: "b.mmdb"}}, cfg)
	write(t, filepath.Join(svc.Dir(), "a.mmdb"))
	write(t, filepath.Join(svc.Dir(), "b.mmdb"))

	st := svc.Status(context.Background())
	if !st.Enabled || st.Active != "b.mmdb" || len(st.Available) != 2 {
		t.Fatalf("status = %+v, want enabled active=b.mmdb 2 files", st)
	}
	for _, db := range st.Available {
		if db.Error == "" {
			t.Fatalf("garbage file should report an open Error: %+v", db)
		}
	}
}
