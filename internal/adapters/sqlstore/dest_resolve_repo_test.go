package sqlstore

import (
	"context"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// ---- dest_resolve on the panel row ----

func TestDestResolve_DefaultsToAsIsOnCreate(t *testing.T) {
	r := newPanelRepo(t)
	p := &domain.Panel{Kind: domain.PanelKindPSP, Name: "n1", URL: "psp://agt_1"}
	if err := r.Save(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetByID(context.Background(), p.ID)
	if err != nil || got.DestResolve != domain.DestResolveAsIs {
		t.Fatalf("a created panel's dest_resolve = %q, %v; want as_is", got.DestResolve, err)
	}
}

// An admin editing the server saves the whole row, which carries the value read
// earlier. That must never reset a choice made meanwhile through the narrow
// writer.
func TestDestResolve_AWholeRowSaveNeverWritesIt(t *testing.T) {
	r := newPanelRepo(t)
	ctx := context.Background()
	p := &domain.Panel{Kind: domain.PanelKindPSP, Name: "n1", URL: "psp://agt_1"}
	if err := r.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	on := domain.DestResolveOnDemand
	if err := r.UpdateNativeMetadata(ctx, p.ID, nil, nil, nil, nil, &on); err != nil {
		t.Fatal(err)
	}
	stale, _ := r.GetByID(ctx, p.ID)
	stale.DestResolve = domain.DestResolveAsIs
	stale.Remark = "edited"
	if err := r.Save(ctx, stale); err != nil {
		t.Fatal(err)
	}
	stale.DestResolve = ""
	if err := r.Save(ctx, stale); err != nil {
		t.Fatal(err)
	}
	got, _ := r.GetByID(ctx, p.ID)
	if got.DestResolve != domain.DestResolveOnDemand || got.Remark != "edited" {
		t.Fatalf("after whole-row saves dest_resolve = %q remark = %q; want on_demand / edited", got.DestResolve, got.Remark)
	}
}

func TestDestResolve_NativeMetadataWriteIsNarrowAndPspOnly(t *testing.T) {
	r := newPanelRepo(t)
	ctx := context.Background()
	p := &domain.Panel{Kind: domain.PanelKindPSP, Name: "n1", URL: "psp://agt_1", Remark: "keep"}
	if err := r.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	on := domain.DestResolveOnDemand
	if err := r.UpdateNativeMetadata(ctx, p.ID, nil, nil, nil, nil, &on); err != nil {
		t.Fatal(err)
	}
	got, _ := r.GetByID(ctx, p.ID)
	if got.DestResolve != domain.DestResolveOnDemand || got.Remark != "keep" || got.AuditCollect != domain.AuditCollectHits {
		t.Fatalf("after the write = %q %q %q", got.DestResolve, got.Remark, got.AuditCollect)
	}
	// Omitted leaves it alone.
	remark := "x"
	if err := r.UpdateNativeMetadata(ctx, p.ID, nil, &remark, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ = r.GetByID(ctx, p.ID); got.DestResolve != domain.DestResolveOnDemand {
		t.Fatalf("an omitted dest_resolve changed to %q", got.DestResolve)
	}
	// It can go back.
	asIs := domain.DestResolveAsIs
	if err := r.UpdateNativeMetadata(ctx, p.ID, nil, nil, nil, nil, &asIs); err != nil {
		t.Fatal(err)
	}
	if got, _ = r.GetByID(ctx, p.ID); got.DestResolve != domain.DestResolveAsIs {
		t.Fatalf("dest_resolve = %q after setting as_is", got.DestResolve)
	}
	for _, bad := range []domain.DestResolve{"everything", "if_non_match", ""} {
		bad := bad
		if err := r.UpdateNativeMetadata(ctx, p.ID, nil, nil, nil, nil, &bad); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("mode %q = %v, want ErrValidation", bad, err)
		}
	}
	third := seedPanel(t, r)
	if err := r.UpdateNativeMetadata(ctx, third.ID, nil, nil, nil, nil, &on); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("a non-native server = %v, want ErrValidation", err)
	}
}

// ---- resolve_effective / resolve_unavailable on the delivery state ----

func TestDestAgentPolicy_ResolveColumnsRoundTripAndDefaultEmpty(t *testing.T) {
	repos, _, ctx := newDestRepos(t)
	if err := repos.Agents.Patch(ctx, "agt_r", domain.DestAgentPolicyPatch{}); err != nil {
		t.Fatal(err)
	}
	got, err := repos.Agents.Get(ctx, "agt_r")
	if err != nil {
		t.Fatal(err)
	}
	if got.ResolveEffective != "" || got.ResolveUnavailable != "" {
		t.Fatalf("defaults = %q %q, want empty", got.ResolveEffective, got.ResolveUnavailable)
	}
	eff, why := "on_demand", "route_only"
	if err := repos.Agents.Patch(ctx, "agt_r", domain.DestAgentPolicyPatch{ResolveEffective: &eff, ResolveUnavailable: &why}); err != nil {
		t.Fatal(err)
	}
	got, _ = repos.Agents.Get(ctx, "agt_r")
	if got.ResolveEffective != "on_demand" || got.ResolveUnavailable != "route_only" {
		t.Fatalf("round trip = %q %q", got.ResolveEffective, got.ResolveUnavailable)
	}
	empty := ""
	if err := repos.Agents.Patch(ctx, "agt_r", domain.DestAgentPolicyPatch{ResolveUnavailable: &empty}); err != nil {
		t.Fatal(err)
	}
	got, _ = repos.Agents.Get(ctx, "agt_r")
	if got.ResolveEffective != "on_demand" || got.ResolveUnavailable != "" {
		t.Fatalf("a patch must be column-level: %q %q", got.ResolveEffective, got.ResolveUnavailable)
	}
	long := "xxxxxxxxxxxxxxxxx"
	if err := repos.Agents.Patch(ctx, "agt_r", domain.DestAgentPolicyPatch{ResolveEffective: &long}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("a 17-byte value = %v, want ErrValidation", err)
	}
}
