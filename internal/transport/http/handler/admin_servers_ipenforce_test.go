package handler

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// The DTO is the only path from the stored verdict to the admin's screen, and
// a dropped field here fails silently: the API answers 200, the badge never
// renders, and a node whose IP cap does nothing looks exactly like one that
// works.

func TestServerDTO_CarriesTheIPCapVerdict(t *testing.T) {
	at := time.Now()
	dto := toServerDTO(&domain.XUIPanel{
		ID: 1, Kind: domain.PanelKind3XUI, Name: "tokyo",
		IPLimitEnforcement: domain.IPLimitEnforcementNotInstalled,
		IPLimitProbedAt:    &at,
	})
	if dto.IPLimitEnforcement != string(domain.IPLimitEnforcementNotInstalled) {
		t.Fatalf("ip_limit_enforcement = %q, want not_installed", dto.IPLimitEnforcement)
	}
	if dto.IPLimitProbedAt == nil || !dto.IPLimitProbedAt.Equal(at) {
		t.Fatalf("ip_limit_probed_at = %v, want %v", dto.IPLimitProbedAt, at)
	}
}

// "Never probed" must reach the client as an explicit unknown rather than as
// an absent field. Absent renders nothing, which is indistinguishable from
// "enforced" — the exact failure the probe exists to prevent, reintroduced at
// the transport layer.
func TestServerDTO_UnprobedPanelSaysUnknownRatherThanNothing(t *testing.T) {
	dto := toServerDTO(&domain.XUIPanel{ID: 1, Kind: domain.PanelKind3XUI, Name: "tokyo"})
	if dto.IPLimitEnforcement != string(domain.IPLimitEnforcementUnknown) {
		t.Fatalf("ip_limit_enforcement = %q, want unknown", dto.IPLimitEnforcement)
	}
}

// S-UI cannot express a concurrent-IP cap at all, which the capability list
// already reports. A second, permanently-unknown badge there would be noise an
// operator has to learn to ignore — and the ones they learn to ignore are the
// ones that matter elsewhere.
func TestServerDTO_StaysSilentForSUI(t *testing.T) {
	dto := toServerDTO(&domain.XUIPanel{ID: 1, Kind: domain.PanelKindSUI, Name: "osaka"})
	if dto.IPLimitEnforcement != "" {
		t.Fatalf("ip_limit_enforcement = %q, want empty for an S-UI panel", dto.IPLimitEnforcement)
	}
}

// A stored value nothing can interpret must not reach the UI as a verdict
// about somebody's node.
func TestServerDTO_JunkReadsAsUnknown(t *testing.T) {
	dto := toServerDTO(&domain.XUIPanel{
		ID: 1, Kind: domain.PanelKind3XUI, Name: "tokyo",
		IPLimitEnforcement: domain.IPLimitEnforcement("usable"),
	})
	if dto.IPLimitEnforcement != string(domain.IPLimitEnforcementUnknown) {
		t.Fatalf("ip_limit_enforcement = %q, want unknown", dto.IPLimitEnforcement)
	}
}

// The "test connection" button re-probes on demand, so an admin who just
// installed fail2ban does not wait out the ten-minute tick. It is a SECOND
// decision site beside the background probe and is free to drift from it, so
// the same three rules are asserted here rather than assumed.

// Embedded interfaces are nil on purpose: any call this path is not supposed
// to make panics instead of quietly returning a zero value.
type f2bClientStub struct {
	ports.XUIClient
	st  *domain.Fail2banStatus
	err error
}

func (c *f2bClientStub) GetFail2banStatus(context.Context) (*domain.Fail2banStatus, error) {
	return c.st, c.err
}

type ipEnforceRepoStub struct {
	ports.XUIPanelRepo
	writes int
	state  domain.IPLimitEnforcement
}

func (r *ipEnforceRepoStub) UpdateIPLimitEnforcement(_ context.Context, _ int64, state domain.IPLimitEnforcement, _ time.Time) error {
	r.writes++
	r.state = state
	return nil
}

func TestRefreshIPLimitEnforcement_StoresAndReturnsTheVerdict(t *testing.T) {
	repo := &ipEnforceRepoStub{}
	h := &AdminServersHandler{repo: repo}
	cli := &f2bClientStub{st: &domain.Fail2banStatus{Enabled: true, Installed: true, Usable: true}}

	got, ok := h.refreshIPLimitEnforcement(context.Background(), 1, cli, time.Now())
	if !ok || got != domain.IPLimitEnforcementEnforced {
		t.Fatalf("got (%q, %v), want (enforced, true)", got, ok)
	}
	if repo.writes != 1 || repo.state != domain.IPLimitEnforcementEnforced {
		t.Fatalf("writes = %d, state = %q", repo.writes, repo.state)
	}
}

func TestRefreshIPLimitEnforcement_OldPanelIsUnsupported(t *testing.T) {
	repo := &ipEnforceRepoStub{}
	h := &AdminServersHandler{repo: repo}
	cli := &f2bClientStub{err: fmt.Errorf("%w: HTTP 404", ports.ErrXUIEndpointUnsupported)}

	got, ok := h.refreshIPLimitEnforcement(context.Background(), 1, cli, time.Now())
	if !ok || got != domain.IPLimitEnforcementUnsupported {
		t.Fatalf("got (%q, %v), want (unsupported, true)", got, ok)
	}
	if repo.writes != 1 {
		t.Fatalf("writes = %d, want 1 — an old panel's inability to answer is itself the answer", repo.writes)
	}
}

// Same invariant as the background probe, asserted separately because this is
// a separate decision: a failed probe must leave the stored verdict alone
// rather than replacing a known-good "enforced" with "unknown".
func TestRefreshIPLimitEnforcement_KeepsTheLastStateOnAFailedProbe(t *testing.T) {
	repo := &ipEnforceRepoStub{}
	h := &AdminServersHandler{repo: repo}
	cli := &f2bClientStub{err: errors.New("dial tcp: i/o timeout")}

	_, ok := h.refreshIPLimitEnforcement(context.Background(), 1, cli, time.Now())
	if ok {
		t.Fatal("a failed probe must not report a verdict")
	}
	if repo.writes != 0 {
		t.Fatalf("writes = %d, want 0", repo.writes)
	}
}
