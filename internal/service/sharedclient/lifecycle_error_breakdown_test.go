package sharedclient

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	pkglog "github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// psp_lifecycle_sync_error_total said HOW MANY status writes failed and
// nothing else: the diagnostics page showed "17 failures" with no way to tell
// a dead native node from a panel that rejects writes, and the user-level log
// line carries only the first error per user. The two breakdowns below are the
// answer, and they are only worth having if they partition the total exactly —
// so every test here checks that each failure lands in exactly one child of
// each family, alongside the unchanged total.

var lifecycleStages = []string{"pool_get", "update", "confirm_read", "confirm_mismatch", "record_credentials"}

// stageXUI fails one chosen step of SyncLifecycle. Before the first write it
// reports the client absent, so the compare cannot skip and the write path —
// where every counted failure lives — is always taken.
type stageXUI struct {
	fakeXUI
	updateErr  error
	confirmErr error
	mismatch   bool

	mu      sync.Mutex
	updated bool
	spec    ports.ClientSpec
}

func (c *stageXUI) GetClient(_ context.Context, email string) (*ports.ClientDetail, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.updated {
		return nil, nil
	}
	if c.confirmErr != nil {
		return nil, c.confirmErr
	}
	if c.mismatch {
		return &ports.ClientDetail{Email: email, ID: "someone-else", Password: "x", Auth: "someone-else"}, nil
	}
	return &ports.ClientDetail{Email: email, ID: c.spec.ID, Password: c.spec.Password, Auth: c.spec.Auth}, nil
}

func (c *stageXUI) UpdateClient(_ context.Context, spec ports.ClientSpec) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.updateErr != nil {
		return c.updateErr
	}
	c.updated, c.spec = true, spec
	return nil
}

// kindPool is a pool that can say which adapter kind sits behind a panel id,
// and can fail Get the way the production pool does for an unregistered id.
type kindPool struct {
	ports.XUIPool
	c      ports.XUIClient
	kinds  map[int64]domain.PanelKind
	getErr error
}

func (p kindPool) Get(int64) (ports.XUIClient, error) {
	if p.getErr != nil {
		return nil, p.getErr
	}
	return p.c, nil
}

func (p kindPool) KindOf(panelID int64) (domain.PanelKind, bool) {
	k, ok := p.kinds[panelID]
	return k, ok
}

// failRecordClients makes the final step, saving what the panel confirmed,
// fail.
type failRecordClients struct{ *fakeClients }

func (failRecordClients) UpdateInboundState(context.Context, domain.PSPClientInbound) error {
	return errors.New("database is locked")
}

func appliedAttachment() *fakeClients {
	return &fakeClients{attachments: []domain.PSPClientInbound{
		{ClientID: 1, NodeID: 11, FlowOverride: "xtls-rprx-vision", State: domain.ClientApplyApplied},
	}}
}

func TestSyncLifecycle_CountsEachFailureByStageAndPanelKind(t *testing.T) {
	kinds := map[int64]domain.PanelKind{10: domain.PanelKindSUI}
	for _, tc := range []struct {
		stage   string
		kind    string
		clients ports.PSPClientRepo
		pool    ports.XUIPool
	}{
		// The production pool fails Get only for an id it does not hold, so
		// it cannot name a kind for it either.
		{"pool_get", "unknown", appliedAttachment(),
			kindPool{getErr: errors.New("panel id 10 not registered"), kinds: map[int64]domain.PanelKind{}}},
		{"update", "sui", appliedAttachment(),
			kindPool{c: &stageXUI{updateErr: errors.New("panel refused the write")}, kinds: kinds}},
		{"confirm_read", "sui", appliedAttachment(),
			kindPool{c: &stageXUI{confirmErr: errors.New("agent is offline")}, kinds: kinds}},
		{"confirm_mismatch", "sui", appliedAttachment(),
			kindPool{c: &stageXUI{mismatch: true}, kinds: kinds}},
		{"record_credentials", "sui", failRecordClients{appliedAttachment()},
			kindPool{c: &stageXUI{}, kinds: kinds}},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			metrics.Reset()
			svc := New(tc.clients, tc.pool, fakeNodes{})
			c := &domain.PSPClient{ID: 1, PanelID: 10, Email: "u1@psp.local", UUID: "uuid-x", Password: "pw-x"}
			if err := syncLifecycle(svc, context.Background(), c, domain.UserLifecycle{Enable: true}); err == nil {
				t.Fatalf("want the %s step to fail", tc.stage)
			}
			if got := counterByName(t, "psp_lifecycle_sync_error_total"); got != 1 {
				t.Fatalf("error total = %d, want 1 (the unlabelled total must not change meaning)", got)
			}
			for _, s := range lifecycleStages {
				want := int64(0)
				if s == tc.stage {
					want = 1
				}
				if got := counterByName(t, "psp_lifecycle_sync_error_stage_total{stage="+s+"}"); got != want {
					t.Errorf("stage %s = %d, want %d", s, got, want)
				}
			}
			for _, k := range []string{"3xui", "sui", "psp", "unknown"} {
				want := int64(0)
				if k == tc.kind {
					want = 1
				}
				if got := counterByName(t, "psp_lifecycle_sync_error_panel_kind_total{kind="+k+"}"); got != want {
					t.Errorf("kind %s = %d, want %d", k, got, want)
				}
			}
		})
	}
}

// A legacy (unset) kind is 3X-UI everywhere else in PSP, and must be here too,
// or every pre-adapter panel would be reported under an empty label.
func TestSyncLifecycle_LegacyEmptyPanelKindCountsAs3XUI(t *testing.T) {
	metrics.Reset()
	pool := kindPool{c: &stageXUI{updateErr: errors.New("refused")}, kinds: map[int64]domain.PanelKind{10: ""}}
	svc := New(appliedAttachment(), pool, fakeNodes{})
	c := &domain.PSPClient{ID: 1, PanelID: 10, Email: "u1@psp.local", UUID: "uuid-x"}
	_ = syncLifecycle(svc, context.Background(), c, domain.UserLifecycle{Enable: true})
	if got := counterByName(t, "psp_lifecycle_sync_error_panel_kind_total{kind=3xui}"); got != 1 {
		t.Fatalf("kind 3xui = %d, want 1", got)
	}
}

// A pool that cannot name kinds (the test doubles, or a future pool) must not
// make one up: the failure is still counted, under "unknown".
func TestSyncLifecycle_PanelKindIsUnknownWhenThePoolCannotSay(t *testing.T) {
	metrics.Reset()
	svc := New(appliedAttachment(), fakePool{c: &stageXUI{updateErr: errors.New("refused")}}, fakeNodes{})
	c := &domain.PSPClient{ID: 1, PanelID: 10, Email: "u1@psp.local", UUID: "uuid-x"}
	_ = syncLifecycle(svc, context.Background(), c, domain.UserLifecycle{Enable: true})
	if got := counterByName(t, "psp_lifecycle_sync_error_panel_kind_total{kind=unknown}"); got != 1 {
		t.Fatalf("kind unknown = %d, want 1", got)
	}
	if got := counterByName(t, "psp_lifecycle_sync_error_stage_total{stage=update}"); got != 1 {
		t.Fatalf("stage update = %d, want 1", got)
	}
}

// A success counts nothing in either breakdown.
func TestSyncLifecycle_SuccessCountsNoFailureBreakdown(t *testing.T) {
	metrics.Reset()
	pool := kindPool{c: &stageXUI{}, kinds: map[int64]domain.PanelKind{10: domain.PanelKindPSP}}
	svc := New(appliedAttachment(), pool, fakeNodes{})
	c := &domain.PSPClient{ID: 1, PanelID: 10, Email: "u1@psp.local", UUID: "uuid-x"}
	if err := syncLifecycle(svc, context.Background(), c, domain.UserLifecycle{Enable: true}); err != nil {
		t.Fatal(err)
	}
	for _, snap := range metrics.Take().Counters {
		if strings.HasPrefix(snap.Name, "psp_lifecycle_sync_error_") && snap.Value != 0 {
			t.Errorf("%s = %d after a successful sync, want 0", snap.Name, snap.Value)
		}
	}
}

// captureLog runs fn with pkg/log writing into a pipe and returns what it
// wrote. pkg/log binds os.Stdout when its logger is built, so the logger is
// rebuilt around the swap and again after it. This package's tests do not run
// in parallel.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	out := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(rd)
		out <- string(b)
	}()
	orig := os.Stdout
	func() {
		defer func() {
			os.Stdout = orig
			pkglog.SetLevel(slog.LevelInfo)
		}()
		os.Stdout = wr
		pkglog.SetLevel(slog.LevelInfo)
		fn()
	}()
	_ = wr.Close()
	s := <-out
	_ = rd.Close()
	return s
}

func logLinesContaining(log, needle string) []string {
	var out []string
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, needle) {
			out = append(out, line)
		}
	}
	return out
}

// The user-level "shared-client lifecycle push failed" line (user service)
// carries only the user and the FIRST error, so a user whose clients failed on
// two panels named one of them and no step. One line per failing client, with
// the panel id and the step, is what lets an operator go from the counter to
// the panel. Log only: a panel id on the metric would grow its label space
// with the deployment.
func TestSyncUserLifecycle_LogsEveryFailingClientWithPanelAndStage(t *testing.T) {
	byUser := []*domain.PSPClient{
		{ID: 1, UserID: 7, PanelID: 10, Email: "a@psp.local", UUID: "u"},
		{ID: 2, UserID: 7, PanelID: 11, Email: "b@psp.local", UUID: "u"},
		{ID: 3, UserID: 7, PanelID: 12, Email: "c@psp.local", UUID: "u"},
	}
	clients := &fakeClients{byUser: byUser, attachments: []domain.PSPClientInbound{
		{ClientID: 1, NodeID: 101, State: domain.ClientApplyApplied},
	}}
	xui := &countingUpdateXUI{fail: map[string]bool{"a@psp.local": true, "c@psp.local": true}}
	svc := New(clients, fakePool{c: xui}, fakeNodes{})

	log := captureLog(t, func() {
		if err := svc.SyncUserLifecycle(context.Background(), 7, domain.UserLifecycle{Enable: true}); err == nil {
			t.Fatal("want an error")
		}
	})
	lines := logLinesContaining(log, "shared-client lifecycle push failed for a client")
	if len(lines) != 2 {
		t.Fatalf("got %d per-client lines, want one per failing client (2):\n%s", len(lines), log)
	}
	for _, want := range []string{"panel_id=10", "panel_id=12"} {
		found := false
		for _, line := range lines {
			if strings.Contains(line, want) && strings.Contains(line, "stage=update") &&
				strings.Contains(line, "user_id=7") {
				found = true
			}
		}
		if !found {
			t.Errorf("no per-client line with %s stage=update user_id=7:\n%s", want, log)
		}
	}
	if len(logLinesContaining(log, "panel_id=11")) != 0 {
		t.Errorf("the client that succeeded was logged as failing:\n%s", log)
	}
}

// Most users have one client, and that path skips the fan-out entirely; it
// must log the same line.
func TestSyncUserLifecycle_LogsTheSingleClientPathToo(t *testing.T) {
	byUser := []*domain.PSPClient{{ID: 1, UserID: 7, PanelID: 10, Email: "a@psp.local", UUID: "u"}}
	clients := &fakeClients{byUser: byUser, attachments: []domain.PSPClientInbound{
		{ClientID: 1, NodeID: 101, State: domain.ClientApplyApplied},
	}}
	svc := New(clients, kindPool{getErr: errors.New("panel id 10 not registered")}, fakeNodes{})

	log := captureLog(t, func() {
		_ = svc.SyncUserLifecycle(context.Background(), 7, domain.UserLifecycle{Enable: true})
	})
	lines := logLinesContaining(log, "shared-client lifecycle push failed for a client")
	if len(lines) != 1 || !strings.Contains(lines[0], "panel_id=10") || !strings.Contains(lines[0], "stage=pool_get") {
		t.Fatalf("want one line with panel_id=10 stage=pool_get, got:\n%s", log)
	}
}
