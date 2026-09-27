package app

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
)

// THE CONNECTION RECORDER IS LATE-BOUND, WHICH MAKES FORGETTING IT SILENT.
//
// SetConnectionRecorder is nil-tolerant, the traffic tests wire a fake, and
// with the line gone from Build the poll simply records nothing: the
// connection history stays empty, every other test stays green, and the
// hourly prune goes on pruning an empty table.
//
// The field is read by reflection rather than by driving a poll, because a
// poll records only connections a panel reported live, and the only panel
// this test could stand up is a loopback HTTP server — which the 3X-UI
// adapter's safehttp dialer refuses by design (SSRF). So this pins the
// wiring itself: the poll's recorder is the very store the hourly cleanup
// ages out (a.connHistory), not merely something non-nil — a second store
// would be one whose rows nothing prunes.
func TestBuildWiresTheConnectionRecorder(t *testing.T) {
	ctx := t.Context()
	directory := t.TempDir()
	cfg := &config.Config{
		Listen: "127.0.0.1:0", JWTSecret: strings.Repeat("j", 48), EncryptionKey: strings.Repeat("e", 48),
		ConfigDir: filepath.Join(directory, "config"), DataDir: filepath.Join(directory, "data"),
	}
	a, err := Build(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.Shutdown(shutdownCtx); err != nil {
			t.Error(err)
		}
		sqlstore.ConfigureSecretKey("")
	})

	field := reflect.ValueOf(a.traffic).Elem().FieldByName("connRec")
	if !field.IsValid() {
		t.Fatal("traffic.Service has no connRec field; update this test with the recorder's new home")
	}
	if field.IsNil() {
		t.Fatal("the traffic poll has no connection recorder: Build does not call SetConnectionRecorder")
	}
	store, ok := a.connHistory.(*sqlstore.ConnectionHistoryRepo)
	if !ok || store == nil {
		t.Fatalf("a.connHistory = %T, want the *sqlstore.ConnectionHistoryRepo the cleanup prunes", a.connHistory)
	}
	if got := field.Elem(); got.Type() != reflect.TypeOf(store) || got.Pointer() != reflect.ValueOf(store).Pointer() {
		t.Fatalf("the poll records into a %v other than the store the hourly cleanup prunes", got.Type())
	}
}
