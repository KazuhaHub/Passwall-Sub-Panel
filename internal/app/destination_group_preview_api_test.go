package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/jwtutil"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/seed"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

func TestBuildGroupPreviewUsesProspectiveEligibilityAndAllowsManualDNSFallback(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a, token := f.a, destinationRefreshAdminToken(t, f.a)
	path := fmt.Sprintf("groups/%d/preview", f.group.ID)
	before, err := a.destDefinitions.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	w := destinationListRequest(t, a, token, "GET", path, nil)
	var preview destpolicy.GroupAccessPreview
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.Members != 1 || preview.TotalNodes != 1 || preview.EligibleNodes != 0 || len(preview.Ineligible) != 1 || preview.Ineligible[0].Reason != "capability" || preview.BaseHostsAvailable || preview.BaseHosts == nil {
		t.Fatalf("prospective preview HTTP=%d body=%s", w.Code, w.Body.String())
	}
	// The real group is still open and currently selects this node. The
	// preview must not reuse open-mode selection as allowlist eligibility.
	mode, err := a.destDefinitions.GetGroupMode(t.Context(), f.group.ID)
	if err != nil || mode.Mode != "open" {
		t.Fatal("preview changed the saved mode")
	}
	if err := seed.Ensure(a.cfg.ConfigDir); err != nil {
		t.Fatal(err)
	}
	caps, _ := json.Marshal([]string{protocol.CapabilityDestinationPolicy})
	if _, err := a.database.ExecContext(t.Context(), "UPDATE node_agents SET observed_capabilities=? WHERE agent_id=?", string(caps), f.agent.AgentID); err != nil {
		t.Fatal(err)
	}
	w = destinationListRequest(t, a, token, "GET", path+"?list_ids=", nil)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.EligibleNodes != 1 || !preview.BaseHostsAvailable || len(preview.BaseHosts) != 2 {
		t.Fatalf("current capability/template preview HTTP=%d body=%s", w.Code, w.Body.String())
	}
	after, err := a.destDefinitions.State(t.Context())
	defs, defsErr := a.destDefinitions.ReadDefinitions(t.Context())
	if err != nil || defsErr != nil || after.Generation != before.Generation || len(defs.Lists) != 0 || len(defs.Groups) != 0 {
		t.Fatal("preview persisted prospective owned lists or generation")
	}
	for _, suffix := range []string{"?list_ids=0", "?list_ids=1,", "?list_ids=1&list_ids=2", "?list_ids=01", "?list_ids=999999", "?user_id=1"} {
		if w := destinationListRequest(t, a, token, "GET", path+suffix, nil); w.Code != 400 {
			t.Errorf("unsafe preview query %s HTTP=%d", suffix, w.Code)
		}
	}
	if w := destinationListRequest(t, a, token, "GET", "groups/999999/preview", nil); w.Code != 404 {
		t.Fatal("missing preview group did not return 404")
	}
	if w := destinationListRequest(t, a, "", "GET", path, nil); w.Code != 401 {
		t.Fatal("anonymous preview read")
	}
	settings, err := a.settings.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []domain.Role{domain.RoleOperator, domain.RoleUser} {
		u := &domain.User{UPN: "group-preview-" + string(role) + "@example.test", Role: role, Enabled: true, UUID: "group-preview-" + string(role), SubToken: "group-preview-sub-" + string(role)}
		if err := a.repos.User.Create(t.Context(), u); err != nil {
			t.Fatal(err)
		}
		otherToken, err := jwtutil.NewIssuer(a.cfg.JWTSecret, func() jwtutil.Params {
			return jwtutil.Params{AccessTTL: time.Hour, RefreshTTL: time.Hour, Issuer: settings.JWTIssuer}
		}).IssueAccess(u.ID, u.UPN, u.Role, u.TokenVersion)
		if err != nil {
			t.Fatal(err)
		}
		if w := destinationListRequest(t, a, otherToken, "GET", path, nil); w.Code != 403 {
			t.Fatalf("%s could read administrator group preview", role)
		}
	}
}

type destinationGroupPreviewMembersFailure struct{}

func TestBuildGroupPreviewUsesDesiredCoreFromNarrowStatusProjection(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	if _, err := f.a.database.ExecContext(t.Context(), "UPDATE node_agents SET desired_core_engine=? WHERE agent_id=?", "sing-box", f.agent.AgentID); err != nil {
		t.Fatal(err)
	}
	inputs, err := f.a.destinationGroupNodeInputs(t.Context(), f.group.ID)
	if err != nil || len(inputs) != 1 || inputs[0].Engine != "sing-box" || inputs[0].Panel.Agent.CredentialSHA256 != "" {
		t.Fatal("prospective preview defaulted the desired engine or loaded credentials")
	}
	if err := seed.Ensure(f.a.cfg.ConfigDir); err != nil {
		t.Fatal(err)
	}
	caps, _ := json.Marshal([]string{protocol.CapabilityDestinationPolicy})
	if _, err := f.a.database.ExecContext(t.Context(), "UPDATE node_agents SET observed_capabilities=? WHERE agent_id=?", string(caps), f.agent.AgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.database.ExecContext(t.Context(), "UPDATE nodes SET sniffing=? WHERE id=?", `{"enabled":true,"metadataOnly":true}`, f.node.ID); err != nil {
		t.Fatal(err)
	}
	token := destinationRefreshAdminToken(t, f.a)
	path := fmt.Sprintf("groups/%d/preview", f.group.ID)
	var preview destpolicy.GroupAccessPreview
	w := destinationListRequest(t, f.a, token, "GET", path, nil)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.EligibleNodes != 1 {
		t.Fatal("Sing-box preview incorrectly applied Xray sniffing requirements")
	}
	if _, err := f.a.database.ExecContext(t.Context(), "UPDATE node_agents SET desired_core_engine=? WHERE agent_id=?", "xray", f.agent.AgentID); err != nil {
		t.Fatal(err)
	}
	w = destinationListRequest(t, f.a, token, "GET", path, nil)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.EligibleNodes != 0 || len(preview.Ineligible) != 1 || preview.Ineligible[0].Reason != "sniffing" {
		t.Fatal("Xray preview ignored its actual desired-engine preflight")
	}
}

func (destinationGroupPreviewMembersFailure) TagMatchedMembers(context.Context, int64) (map[int64][]int64, error) {
	return nil, errors.New("private-preview-membership-detail")
}

func TestBuildGroupPreviewFailureWithholdsCountsAndPrivateDetails(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	a, token := f.a, destinationRefreshAdminToken(t, f.a)
	a.destTagMembers = destinationGroupPreviewMembersFailure{}
	w := destinationListRequest(t, a, token, "GET", fmt.Sprintf("groups/%d/preview", f.group.ID), nil)
	if w.Code != 503 || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "eligible_nodes") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("failed preview exposed private details or partial coverage")
	}
}
