package sqlstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// The stored state is what an operator reads to decide whether a concurrent-IP
// cap on this node does anything. Every case here guards against it saying the
// wrong thing.

func newPanelRepo(t *testing.T) *xuiPanelRepo {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return &xuiPanelRepo{db: db}
}

func seedPanel(t *testing.T, r *xuiPanelRepo) *domain.XUIPanel {
	t.Helper()
	p := &domain.XUIPanel{Kind: domain.PanelKind3XUI, Name: "tokyo", URL: "https://p.example", APIToken: "t"}
	if err := r.Save(context.Background(), p); err != nil {
		t.Fatalf("seed panel: %v", err)
	}
	return p
}

// A panel that has never been probed must read as unknown, not as a state that
// reassures. A legacy row predating these columns holds an empty string, and
// mapping that to anything else would let a fleet upgraded from an older build
// look like it had been checked and passed.
func TestIPLimitEnforcement_NeverProbedIsUnknown(t *testing.T) {
	r := newPanelRepo(t)
	p := seedPanel(t, r)

	got, err := r.GetByID(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.IPLimitEnforcement != domain.IPLimitEnforcementUnknown {
		t.Fatalf("state = %q, want unknown — an unprobed node must not read as checked", got.IPLimitEnforcement)
	}
	if got.IPLimitProbedAt != nil {
		t.Fatalf("probed_at = %v, want nil", got.IPLimitProbedAt)
	}
}

func TestIPLimitEnforcement_RoundTrip(t *testing.T) {
	r := newPanelRepo(t)
	p := seedPanel(t, r)
	ctx := context.Background()
	when := time.Now().UTC().Truncate(time.Second)

	if err := r.UpdateIPLimitEnforcement(ctx, p.ID, domain.IPLimitEnforcementNotInstalled, when); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := r.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.IPLimitEnforcement != domain.IPLimitEnforcementNotInstalled {
		t.Fatalf("state = %q, want not_installed", got.IPLimitEnforcement)
	}
	if got.IPLimitProbedAt == nil || !got.IPLimitProbedAt.UTC().Truncate(time.Second).Equal(when) {
		t.Fatalf("probed_at = %v, want %v", got.IPLimitProbedAt, when)
	}
}

// The probe runs on a timer against rows an admin may be editing in another
// tab. A full-row write here would roll back their credential change, so the
// update has to be column-scoped — the same rule UpdateVersion follows.
func TestIPLimitEnforcement_DoesNotClobberTheRestOfTheRow(t *testing.T) {
	r := newPanelRepo(t)
	p := seedPanel(t, r)
	ctx := context.Background()

	if err := r.UpdateIPLimitEnforcement(ctx, p.ID, domain.IPLimitEnforcementEnforced, time.Now()); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := r.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "tokyo" || got.URL != "https://p.example" || got.APIToken != "t" {
		t.Fatalf("row was clobbered: %+v", got)
	}
}

// An uninterpretable value reads back as unknown, which is indistinguishable
// from a probe that never ran — so it is rejected at the write instead, where
// there is still something to report.
func TestIPLimitEnforcement_RejectsAnUnknownState(t *testing.T) {
	r := newPanelRepo(t)
	p := seedPanel(t, r)

	err := r.UpdateIPLimitEnforcement(context.Background(), p.ID, "usable", time.Now())
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

// An admin editing the panel writes the whole row back. The probed state must
// survive that, or every credential edit would silently reset the verdict to
// unknown and the badge would clear itself.
func TestIPLimitEnforcement_SurvivesAFullRowSave(t *testing.T) {
	r := newPanelRepo(t)
	p := seedPanel(t, r)
	ctx := context.Background()

	if err := r.UpdateIPLimitEnforcement(ctx, p.ID, domain.IPLimitEnforcementDisabled, time.Now()); err != nil {
		t.Fatalf("update: %v", err)
	}
	edited, err := r.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	edited.Remark = "moved racks"
	if err := r.Save(ctx, edited); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := r.GetByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.IPLimitEnforcement != domain.IPLimitEnforcementDisabled {
		t.Fatalf("state = %q, want disabled to survive an admin edit", got.IPLimitEnforcement)
	}
}
