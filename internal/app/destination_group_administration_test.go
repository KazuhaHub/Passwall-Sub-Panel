package app

import (
	"context"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

func TestBuildGroupModeAdministrationPreviewsAndInvalidatesEligibilityAfterCommit(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a := f.a
	destinationRefreshAdminToken(t, a)
	if a.destGroups == nil {
		t.Fatal("group mode administrator omitted from production wiring")
	}
	selector, ok := a.destTagMembers.(interface {
		NodesFor(context.Context, *domain.Group) ([]*domain.Node, error)
	})
	if !ok {
		t.Fatal("production membership reader omitted group node selection")
	}
	selected, err := selector.NodesFor(t.Context(), f.group)
	if err != nil || len(selected) != 1 {
		t.Fatal("could not warm the open group's selection cache")
	}
	input := destpolicy.GroupModeInput{Mode: "allowlist", Stage: "trial", BaseHosts: []string{"dns.example.test"}}
	before, err := a.destDefinitions.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	preview, err := a.destGroups.Preview(t.Context(), f.group.ID, input)
	if err != nil || preview.Mode.Mode != "allowlist" || len(preview.Definitions.Lists) != 2 {
		t.Fatal("prospective mode omitted its two owned lists")
	}
	after, err := a.destDefinitions.State(t.Context())
	if err != nil || after.Generation != before.Generation {
		t.Fatal("group preview wrote definitions")
	}
	mode, err := a.destGroups.Save(t.Context(), f.group.ID, input, time.Time{})
	if err != nil || mode.BaseListID <= 0 || mode.ExtraListID <= 0 || mode.BaseListID == mode.ExtraListID || mode.UpdatedAt.IsZero() {
		t.Fatalf("atomic group-mode commit failed: %v", err)
	}
	after, err = a.destDefinitions.State(t.Context())
	if err != nil || after.Generation != before.Generation+1 {
		t.Fatal("group mode did not commit exactly one generation")
	}
	selected, err = selector.NodesFor(t.Context(), f.group)
	if err != nil || len(selected) != 0 {
		t.Fatal("committed allowlist kept the warmed open-mode eligibility cache")
	}
	base, err := a.destDefinitions.GetList(t.Context(), mode.BaseListID)
	if err != nil || base.OwnerGroupID != f.group.ID || string(base.Entries) != "full:dns.example.test\n" {
		t.Fatal("committed base contents or ownership are wrong")
	}
	if _, err := a.destGroups.Save(t.Context(), f.group.ID, destpolicy.GroupModeInput{Mode: "open"}, time.Time{}); err == nil {
		t.Fatal("stale group edit was accepted")
	}
	mode, err = a.destGroups.Save(t.Context(), f.group.ID, destpolicy.GroupModeInput{Mode: "open"}, mode.UpdatedAt)
	if err != nil || mode.Mode != "open" || mode.BaseListID != base.ID {
		t.Fatal("closing group destroyed its owned lists")
	}
	// Restoring open mode must invalidate the restrictive cache too.
	selected, err = selector.NodesFor(t.Context(), f.group)
	if err != nil || len(selected) != 1 {
		t.Fatal("closing group kept restrictive eligibility cache")
	}
	stored, err := a.destDefinitions.GetList(t.Context(), base.ID)
	if err != nil || stored.Kind != domain.DestListCustom || stored.ContentSHA256 != base.ContentSHA256 {
		t.Fatal("mode changes replaced retained base source")
	}
}
