package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destaudit"
)

func fixtureAudit(t *testing.T, f destinationPolicyFixture, id int) *protocol.AuditObservation {
	t.Helper()
	now := time.Now().UTC()
	b := &protocol.AuditObservation{BatchID: fmt.Sprintf("%032x", id), Hour: now.Truncate(time.Hour).UnixMilli(), Kind: "block", CollectRevision: 1, Dropped: 2, Unmatched: 3, Hits: []protocol.AuditHit{{Hour: now.Truncate(time.Hour).UnixMilli(), Subject: protocol.NewSubjectKey(f.user.ID), RuleID: "p12", Action: "block", Dest: "example.test", Port: 443, Count: 4, FirstMS: now.UnixMilli(), LastMS: now.UnixMilli()}}}
	if err := protocol.ValidateAuditObservation(*b); err != nil {
		t.Fatal(err)
	}
	return b
}

func auditTableCount(t *testing.T, a *App, table string) int {
	t.Helper()
	var n int
	if err := a.database.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func waitAuditRows(t *testing.T, a *App, table string, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for {
		if auditTableCount(t, a, table) == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s did not reach %d rows", table, want)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestBuildDestinationAuditSyncActuallyQueuesAndPersistsOnce(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	if f.a.destAudit == nil {
		t.Fatal("app did not wire the destination audit collector")
	}
	f.report.Capabilities = []string{protocol.CapabilityAuditHits}
	f.report.CoreEngine = "xray"
	f.report.Audit = fixtureAudit(t, f, 1)
	first := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if first.Config.Body == nil || first.Roster.Body == nil || auditTableCount(t, f.a, "dest_hits") != 0 {
		t.Fatal("Sync waited for ingest or changed control streams")
	}
	f.a.startDestinationAudit()
	waitAuditRows(t, f.a, "dest_hits", 1)
	_ = syncNativeCacheFixture(t, f.a, f.credential, f.report)
	f.a.destAudit.StopOffers()
	f.a.bgWG.Wait()
	var count int64
	if err := f.a.database.QueryRowContext(t.Context(), "SELECT count FROM dest_hits").Scan(&count); err != nil || count != 4 {
		t.Fatal("retransmission double-counted committed hits")
	}
}

func TestBuildDestinationAuditOffOnDropsQueuedAndOldRevisionReports(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	if f.a.destAudit == nil {
		t.Fatal("app did not wire current collection notifications")
	}
	f.report.Capabilities = []string{protocol.CapabilityAuditHits}
	f.report.CoreEngine = "xray"
	f.report.Audit = fixtureAudit(t, f, 1)
	_ = syncNativeCacheFixture(t, f.a, f.credential, f.report)
	token := destinationRefreshAdminToken(t, f.a)
	path := fmt.Sprintf("/%d", f.agent.PanelID)
	for _, mode := range []string{"off", "hits"} {
		w := serverAuditRequest(t, f.a, token, "PUT", path, map[string]any{"audit_collect": mode})
		if w.Code != 200 {
			t.Fatal("collection setting did not commit")
		}
	}
	f.report.Audit.BatchID = fmt.Sprintf("%032x", 2)
	_ = syncNativeCacheFixture(t, f.a, f.credential, f.report)
	f.report.Audit.BatchID = fmt.Sprintf("%032x", 3)
	f.report.Audit.CollectRevision = 3
	_ = syncNativeCacheFixture(t, f.a, f.credential, f.report)
	f.a.startDestinationAudit()
	f.a.destAudit.StopOffers()
	f.a.bgWG.Wait()
	if auditTableCount(t, f.a, "dest_hits") != 1 {
		t.Fatal("off/on resurrected old queued data")
	}
	var rows int64
	if err := f.a.database.QueryRowContext(t.Context(), "SELECT SUM(rows) FROM dest_audit_loss_hourly").Scan(&rows); err != nil || rows != 2 {
		t.Fatal("queue/revision losses not persisted")
	}
}

type auditLifecycleProbe struct {
	started, stopped, finished chan struct{}
	stop                       sync.Once
	afterStop                  func(context.Context)
}

func (p *auditLifecycleProbe) Offer(string, int64, time.Time, protocol.AuditObservation) {}
func (p *auditLifecycleProbe) StopOffers()                                               { p.stop.Do(func() { close(p.stopped) }) }
func (p *auditLifecycleProbe) Run(ctx context.Context) destaudit.Summary {
	close(p.started)
	<-p.stopped
	p.afterStop(ctx)
	close(p.finished)
	return destaudit.Summary{}
}

func TestDestinationAuditShutdownDrainsAfterHTTPAndBeforeDatabaseClose(t *testing.T) {
	a := buildDestinationListsFixture(t)
	p := &auditLifecycleProbe{started: make(chan struct{}), stopped: make(chan struct{}), finished: make(chan struct{})}
	a.destAudit = p
	a.destAuditCtx, a.destAuditCancel = context.WithCancel(context.Background())
	p.afterStop = func(ctx context.Context) {
		<-a.bgRootCtx.Done()
		if ctx.Err() != nil {
			t.Error("ordinary background cancellation aborted audit drain")
		}
		if _, err := a.repos.User.GetByID(ctx, 999999); !errors.Is(err, domain.ErrNotFound) {
			t.Error("database closed before audit worker exited")
		}
	}
	a.startDestinationAudit()
	a.startDestinationAudit()
	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("tracked audit worker was not started")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.finished:
	default:
		t.Fatal("shutdown returned before audit worker")
	}
	if err := a.database.PingContext(t.Context()); err == nil {
		t.Fatal("drained shutdown left database open")
	}
}

func TestDestinationAuditShutdownDeadlineCancelsOwnWorkerContext(t *testing.T) {
	a := buildDestinationListsFixture(t)
	p := &auditLifecycleProbe{started: make(chan struct{}), stopped: make(chan struct{}), finished: make(chan struct{})}
	a.destAudit = p
	a.destAuditCtx, a.destAuditCancel = context.WithCancel(context.Background())
	p.afterStop = func(ctx context.Context) { <-ctx.Done() }
	a.startDestinationAudit()
	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("tracked audit worker was not started")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := a.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("forced drain claimed success")
	}
	select {
	case <-p.finished:
	case <-time.After(time.Second):
		t.Fatal("deadline did not cancel audit storage context")
	}
}

func TestDestinationAuditShutdownWaitsForHTTPProducerBeforeStoppingOffers(t *testing.T) {
	a := buildDestinationListsFixture(t)
	p := &auditLifecycleProbe{started: make(chan struct{}), stopped: make(chan struct{}), finished: make(chan struct{}), afterStop: func(context.Context) {}}
	a.destAudit = p
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	a.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		select {
		case <-p.stopped:
			t.Error("HTTP producer drained after offers had already stopped")
		default:
		}
		w.WriteHeader(204)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- a.server.Serve(listener) }()
	clientDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 3 * time.Second}
		response, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		clientDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("HTTP producer did not enter")
	}
	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		shutdownDone <- a.Shutdown(ctx)
	}()
	// Serve returning proves Shutdown has closed the listener and is draining
	// the still-blocked handler, rather than merely not having been scheduled.
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}
	select {
	case <-p.stopped:
		t.Fatal("audit offers stopped before HTTP drain")
	default:
	}
	finish()
	if err := <-clientDone; err != nil {
		t.Fatal(err)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.finished:
	default:
		t.Fatal("HTTP drain did not start and join the audit worker")
	}
}

type admittedAuditProbe struct {
	entered, release chan struct{}
}

func (p *admittedAuditProbe) wait(ctx context.Context) error {
	close(p.entered)
	select {
	case <-p.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (p *admittedAuditProbe) ResolveDestinationAuditUsers(ctx context.Context, _ []int64) (map[int64]bool, error) {
	return nil, p.wait(ctx)
}
func (p *admittedAuditProbe) BeginDestinationAudit(ctx context.Context, _ domain.DestAuditBatch) (domain.DestAuditBegin, error) {
	return domain.DestAuditBegin{}, p.wait(ctx)
}
func (p *admittedAuditProbe) WriteDestinationAuditChunk(ctx context.Context, _ domain.DestAuditChunk) (string, error) {
	return "", p.wait(ctx)
}
func (p *admittedAuditProbe) FlushDestinationAuditLoss(ctx context.Context, _ domain.DestAuditLossBatch) error {
	return p.wait(ctx)
}
func (p *admittedAuditProbe) WatchDestinationAuditControls(ctx context.Context, _ func([]domain.DestAuditControl)) error {
	return p.wait(ctx)
}

func TestDestinationAuditStorageParticipatesInOnlineBackendAdmissionPerCall(t *testing.T) {
	for _, method := range []string{"resolve", "begin", "chunk", "loss", "warm"} {
		t.Run(method, func(t *testing.T) {
			a := buildDestinationListsFixture(t)
			p := &admittedAuditProbe{entered: make(chan struct{}), release: make(chan struct{})}
			var releaseOnce sync.Once
			finish := func() { releaseOnce.Do(func() { close(p.release) }) }
			defer finish()
			r := admittedDestinationAuditStore{store: p, gate: a.operationGate}
			done := make(chan error, 1)
			go func() {
				var err error
				switch method {
				case "resolve":
					_, err = r.ResolveDestinationAuditUsers(t.Context(), nil)
				case "begin":
					_, err = r.BeginDestinationAudit(t.Context(), domain.DestAuditBatch{})
				case "chunk":
					_, err = r.WriteDestinationAuditChunk(t.Context(), domain.DestAuditChunk{})
				case "loss":
					err = r.FlushDestinationAuditLoss(t.Context(), domain.DestAuditLossBatch{})
				case "warm":
					err = r.WatchDestinationAuditControls(t.Context(), func([]domain.DestAuditControl) {})
				}
				done <- err
			}()
			select {
			case <-p.entered:
			case <-time.After(time.Second):
				t.Fatal("audit store did not enter")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			err := a.operationGate.Exclusive(ctx, func(context.Context) error {
				t.Error("backend switch crossed an active audit storage call")
				return nil
			})
			cancel()
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("audit storage call did not retain backend admission")
			}
			finish()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if err := a.operationGate.Exclusive(t.Context(), func(context.Context) error { return nil }); err != nil {
				t.Fatal("completed audit operation retained admission")
			}
		})
	}
}
