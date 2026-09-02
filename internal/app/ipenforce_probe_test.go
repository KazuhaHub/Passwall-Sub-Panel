package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The probe exists so a concurrent-IP cap that enforces nothing stops being
// invisible. Each case names the wrong answer it prevents an operator from
// being shown.

// f2bStub implements only the one optional method. The embedded interface is
// nil on purpose: any other call panics loudly instead of silently returning a
// zero value that a test could mistake for real behaviour.
type f2bStub struct {
	ports.PanelClient
	st  *domain.Fail2banStatus
	err error
}

func (s *f2bStub) GetFail2banStatus(context.Context) (*domain.Fail2banStatus, error) {
	return s.st, s.err
}

// panelRepoStub records the last enforcement write, and only that.
type panelRepoStub struct {
	ports.XUIPanelRepo
	writes int
	state  domain.IPLimitEnforcement
	at     time.Time
	err    error
}

func (r *panelRepoStub) UpdateIPLimitEnforcement(_ context.Context, _ int64, state domain.IPLimitEnforcement, at time.Time) error {
	r.writes++
	r.state = state
	r.at = at
	return r.err
}

func newProbeApp(repo ports.XUIPanelRepo) *App {
	return &App{repos: ports.Repos{XUIPanel: repo}}
}

func TestProbeIPLimitEnforcement_RecordsTheClassifiedState(t *testing.T) {
	repo := &panelRepoStub{}
	a := newProbeApp(repo)
	cli := &f2bStub{st: &domain.Fail2banStatus{Enabled: true}}

	now := time.Now()
	a.probeIPLimitEnforcement(context.Background(), &domain.XUIPanel{ID: 1, Name: "tokyo"}, cli, now)

	if repo.writes != 1 {
		t.Fatalf("writes = %d, want 1", repo.writes)
	}
	if repo.state != domain.IPLimitEnforcementNotInstalled {
		t.Fatalf("state = %q, want not_installed", repo.state)
	}
	if !repo.at.Equal(now) {
		t.Fatalf("probed_at = %v, want %v", repo.at, now)
	}
}

// A panel older than 3.7.0 cannot answer. That is a state of its own, not a
// fault: recording it as "not installed" would put a permanent, wrong warning
// on a node that may well be enforcing fine.
func TestProbeIPLimitEnforcement_OldPanelIsUnsupported(t *testing.T) {
	repo := &panelRepoStub{}
	a := newProbeApp(repo)
	cli := &f2bStub{err: fmt.Errorf("%w: HTTP 404", ports.ErrXUIEndpointUnsupported)}

	a.probeIPLimitEnforcement(context.Background(), &domain.XUIPanel{ID: 1}, cli, time.Now())

	if repo.writes != 1 || repo.state != domain.IPLimitEnforcementUnsupported {
		t.Fatalf("writes = %d, state = %q; want one write of unsupported", repo.writes, repo.state)
	}
}

// The invariant that makes the stored state trustworthy. A network blip must
// not overwrite "enforced" with "unknown": unknown is what an operator sees
// when the probe itself is broken, and manufacturing it here would send them
// after a fault that does not exist.
func TestProbeIPLimitEnforcement_KeepsTheLastStateOnAFailedProbe(t *testing.T) {
	repo := &panelRepoStub{}
	a := newProbeApp(repo)
	cli := &f2bStub{err: errors.New("dial tcp: i/o timeout")}

	a.probeIPLimitEnforcement(context.Background(),
		&domain.XUIPanel{ID: 1, IPLimitEnforcement: domain.IPLimitEnforcementEnforced}, cli, time.Now())

	if repo.writes != 0 {
		t.Fatalf("writes = %d, want 0 — a blip must not overwrite a known-good state", repo.writes)
	}
}

// S-UI has no such route and its adapter does not implement the optional
// interface. Writing "unknown" for it would add a permanent badge to a panel
// where the whole question is meaningless.
func TestProbeIPLimitEnforcement_SkipsAnAdapterThatCannotAnswer(t *testing.T) {
	repo := &panelRepoStub{}
	a := newProbeApp(repo)

	a.probeIPLimitEnforcement(context.Background(), &domain.XUIPanel{ID: 1}, struct{}{}, time.Now())

	if repo.writes != 0 {
		t.Fatalf("writes = %d, want 0", repo.writes)
	}
}

// A repo failure must not stop the probe from having run — the metric and the
// log line are the part that still works when storage does not.
func TestProbeIPLimitEnforcement_SurvivesAWriteFailure(t *testing.T) {
	repo := &panelRepoStub{err: errors.New("db is gone")}
	a := newProbeApp(repo)
	cli := &f2bStub{st: &domain.Fail2banStatus{Enabled: true, Installed: true, Usable: true}}

	a.probeIPLimitEnforcement(context.Background(), &domain.XUIPanel{ID: 1}, cli, time.Now())

	if repo.writes != 1 {
		t.Fatalf("writes = %d, want 1", repo.writes)
	}
}

func TestIPLimitEnforcementFix_NamesTheRightRemedy(t *testing.T) {
	// The two faults have different fixes and the disabled one is a trap:
	// XUI_ENABLE_FAIL2BAN=1 reads as "on" and turns enforcement off. A message
	// telling that operator to install fail2ban sends them to the wrong box.
	if got := ipLimitEnforcementFix(domain.IPLimitEnforcementDisabled); !strings.Contains(got, "XUI_ENABLE_FAIL2BAN") {
		t.Fatalf("disabled fix = %q, want it to name the environment variable", got)
	}
	if got := ipLimitEnforcementFix(domain.IPLimitEnforcementNotInstalled); !strings.Contains(got, "install fail2ban") {
		t.Fatalf("not_installed fix = %q, want it to say to install fail2ban", got)
	}
	for _, state := range []domain.IPLimitEnforcement{
		domain.IPLimitEnforcementEnforced,
		domain.IPLimitEnforcementDisconnectOnly,
		domain.IPLimitEnforcementUnknown,
		domain.IPLimitEnforcementUnsupported,
	} {
		if got := ipLimitEnforcementFix(state); got != "" {
			t.Fatalf("%q offered a fix (%q); nothing is wrong on that node", state, got)
		}
	}
}

// The probe is correct and covered above, and would stay so with nothing
// calling it. That failure — a tested function whose only caller is optional —
// has already happened four times in this area, and it is silent: every test
// stays green while the whole fleet stops being probed.
//
// A source-level guard because the alternative, driving probePanelVersionsOnce
// end to end, needs a full PanelClient double and makes live GitHub calls for
// the latest-version snapshot. If the call is moved somewhere that still runs
// per panel per tick, update the anchor below rather than deleting the test.
func TestIPLimitProbeIsActuallyCalled(t *testing.T) {
	src, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatalf("read app.go: %v", err)
	}
	body := string(src)
	const anchor = "func (a *App) probePanelVersionsOnce("
	i := strings.Index(body, anchor)
	if i < 0 {
		t.Fatalf("probePanelVersionsOnce is gone; this guard needs rewriting")
	}
	end := strings.Index(body[i:], "\n// probeIPLimitEnforcement")
	if end < 0 {
		t.Fatal("probeIPLimitEnforcement no longer follows probePanelVersionsOnce; this guard needs rewriting")
	}
	if !strings.Contains(body[i:i+end], "a.probeIPLimitEnforcement(") {
		t.Fatal("probePanelVersionsOnce no longer probes ip-cap enforcement — " +
			"every panel would silently stop being checked while these tests stayed green")
	}
}
