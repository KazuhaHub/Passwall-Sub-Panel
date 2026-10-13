//go:build destination_acceptance && linux

package app

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destaudit"
)

// Delays only entry into the real Begin transaction. It preserves the original
// admitted store, live permission watcher, clock, quota and eventual SQL write.
// A released warmup can finish before a later pressure phase arms a new hold.
type nativeGlobalAuditStorage struct {
	ports.DestAuditStore
	mu      sync.Mutex
	paused  *nativeGlobalStoragePause
	begins  atomic.Uint64
	blocked atomic.Uint64
}

type nativeGlobalStoragePause struct {
	entered chan struct{}
	ready   chan struct{}
	once    sync.Once
}

func (g *nativeGlobalAuditStorage) pause() (*nativeGlobalStoragePause, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.paused != nil {
		return nil, errors.New("global storage phase already held")
	}
	p := &nativeGlobalStoragePause{entered: make(chan struct{}), ready: make(chan struct{})}
	g.paused = p
	return p, nil
}

func (g *nativeGlobalAuditStorage) resume(p *nativeGlobalStoragePause) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.paused == p && p != nil {
		g.paused = nil
		close(p.ready)
	}
}

func (g *nativeGlobalAuditStorage) BeginDestinationAudit(ctx context.Context, batch domain.DestAuditBatch) (domain.DestAuditBegin, error) {
	g.mu.Lock()
	p := g.paused
	g.mu.Unlock()
	if p != nil {
		g.blocked.Add(1)
		p.once.Do(func() { close(p.entered) })
		select {
		case <-p.ready:
		case <-ctx.Done():
			return domain.DestAuditBegin{}, ctx.Err()
		}
	}
	g.begins.Add(1)
	return g.DestAuditStore.BeginDestinationAudit(ctx, batch)
}

func TestNativeGlobalStoragePauseKeepsControlAndOriginalSQLTransactions(t *testing.T) {
	directory := t.TempDir()
	var storage *nativeGlobalAuditStorage
	a, err := buildWithDestinationAuditStore(t.Context(), &config.Config{
		Listen: "127.0.0.1:0", JWTSecret: strings.Repeat("j", 48), EncryptionKey: strings.Repeat("e", 48),
		ConfigDir: filepath.Join(directory, "config"), DataDir: filepath.Join(directory, "data"),
	}, func(original ports.DestAuditStore) ports.DestAuditStore {
		storage = &nativeGlobalAuditStorage{DestAuditStore: original}
		return storage
	})
	nativeMust(t, err, "build production App with admitted SQL transaction hold")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		nativeMust(t, a.Shutdown(ctx), "close global SQL integration fixture")
		sqlstore.ConfigureSecretKey("")
	})
	if _, ok := storage.DestAuditStore.(admittedDestinationAuditStore); !ok {
		t.Fatal("global transaction hold bypassed production backend admission")
	}
	f := seedDestinationPolicyFixture(t, a)
	f.report.Capabilities = []string{protocol.CapabilityAuditHits}
	f.report.CoreEngine = "xray"
	a.startDestinationAudit()
	queue, ok := a.destAudit.(interface {
		Pressure() [4]destaudit.LanePressure
	})
	if !ok {
		t.Fatal("global transaction fixture lost its actual production queue")
	}
	for phase := 1; phase <= 2; phase++ {
		pause, err := storage.pause()
		nativeMust(t, err, "arm a fresh production SQL wait")
		t.Cleanup(func() { storage.resume(pause) })
		if _, err := storage.pause(); err == nil {
			t.Fatal("global SQL fixture replaced an unreleased phase")
		}
		f.report.Audit = fixtureAudit(t, f, phase)
		f.report.Audit.Hits[0].Dest = "phase-" + strconv.Itoa(phase) + ".example.test"
		before := storage.begins.Load()
		response := syncNativeCacheFixture(t, a, f.credential, f.report)
		if response.Config.Body == nil || response.Roster.Body == nil {
			t.Fatal("held storage suppressed production control streams")
		}
		select {
		case <-pause.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("real worker never reached the held Begin boundary")
		}
		lane := queue.Pressure()[0]
		if storage.begins.Load() != before || lane.Active != 1 || lane.Batches != 1 || lane.Bytes <= 0 || auditTableCount(t, a, "dest_hits") != phase-1 {
			t.Fatal("held SQL changed retained reservations or persisted early")
		}
		admission, cancel := context.WithTimeout(t.Context(), time.Second)
		err = a.operationGate.Exclusive(admission, func(context.Context) error { return nil })
		cancel()
		nativeMust(t, err, "transaction fixture retained a backend admission permit before delegation")
		_ = syncNativeCacheFixture(t, a, f.credential, f.report)
		if queue.Pressure()[0].Batches != 1 || storage.begins.Load() != before {
			t.Fatal("real retransmission escaped dedup while storage waited")
		}
		storage.resume(pause)
		storage.resume(pause)
		waitAuditRows(t, a, "dest_hits", phase)
		nativeWait(t, 5*time.Second, "original SQL worker completed released reservation", func() bool { return queue.Pressure()[0].Batches == 0 })
		if storage.begins.Load() != before+1 {
			t.Fatal("released hold duplicated the real SQL Begin transaction")
		}
	}
	if storage.blocked.Load() != 2 {
		t.Fatal("global fixture did not exercise separate warmup and pressure phases")
	}
	// A real committed collection change still reaches the SAME service while
	// its active worker waits. SQL must re-check the obsolete revision on release.
	pause, err := storage.pause()
	nativeMust(t, err, "hold real SQL before collection revocation")
	t.Cleanup(func() { storage.resume(pause) })
	f.report.Audit = fixtureAudit(t, f, 3)
	_ = syncNativeCacheFixture(t, a, f.credential, f.report)
	select {
	case <-pause.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("collection revocation did not bind a waiting transaction")
	}
	token := destinationRefreshAdminToken(t, a)
	for _, mode := range []string{"off", "hits"} {
		result := serverAuditRequest(t, a, token, "PUT", "/"+strconv.FormatInt(f.agent.PanelID, 10), map[string]any{"audit_collect": mode})
		if result.Code != 200 {
			t.Fatal("held SQL prevented production permission changes")
		}
	}
	storage.resume(pause)
	nativeWait(t, 5*time.Second, "obsolete SQL transaction rejected after actual revocation", func() bool { return queue.Pressure()[0].Batches == 0 })
	if auditTableCount(t, a, "dest_hits") != 2 {
		t.Fatal("held transaction resurrected a revoked collection revision")
	}
	// Cancellation must leave the real store untouched and cleanup must still
	// release the phase; no canceled request can borrow a fabricated SQL result.
	pause, err = storage.pause()
	nativeMust(t, err, "arm cancellation boundary")
	t.Cleanup(func() { storage.resume(pause) })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	before := storage.begins.Load()
	if _, err := storage.BeginDestinationAudit(ctx, domain.DestAuditBatch{}); !errors.Is(err, context.Canceled) || storage.begins.Load() != before {
		t.Fatal("canceled storage wait entered a real transaction or invented success")
	}
	storage.resume(pause)
}
