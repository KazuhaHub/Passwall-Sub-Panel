package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type destinationPolicyFixture struct {
	a          *App
	credential string
	agent      *domain.NodeAgent
	node       *domain.Node
	group      *domain.Group
	user       *domain.User
	report     protocol.NodeReport
}

func buildDestinationPolicyFixture(t *testing.T) destinationPolicyFixture {
	t.Helper()
	a := buildDestinationListsFixture(t)
	credential := "fixture-destination-policy-credential-with-enough-entropy"
	digest := sha256.Sum256([]byte(credential))
	agent := &domain.NodeAgent{AgentID: "agt_destination_wiring", Epoch: 1, CredentialSHA256: hex.EncodeToString(digest[:]), DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: "26.6.27"}
	panel := &domain.Panel{Name: "destination wiring", Kind: domain.PanelKindPSP, URL: "psp://" + agent.AgentID}
	if err := a.repos.NativeAgentProvisioning.Create(t.Context(), panel, agent); err != nil {
		t.Fatal(err)
	}
	if err := a.xuiPool.Add(panel); err != nil {
		t.Fatal(err)
	}
	captured := time.Now().UTC()
	n := &domain.Node{PanelID: panel.ID, InboundID: 1, DisplayName: "policy node", ServerAddress: "node.example.test", DesiredProtocol: "vless", DesiredPort: 444, ObservedProtocol: "vless", ObservedPort: 444, InboundListen: "0.0.0.0", InboundSettings: `{"decryption":"none"}`, StreamSettings: `{"network":"tcp","security":"none"}`, Sniffing: `{}`, Allocate: `{}`, Enabled: true, ConfigSyncedAt: &captured, ConfigSyncState: domain.ConfigSyncSynced}
	if err := a.repos.Node.Create(t.Context(), n); err != nil {
		t.Fatal(err)
	}
	g := &domain.Group{Slug: "destination-wiring", Name: "Destination wiring", TagFilter: domain.TagFilter{All: true}}
	if err := a.repos.Group.Create(t.Context(), g); err != nil {
		t.Fatal(err)
	}
	u := &domain.User{UPN: "destination@example.test", Email: "destination@example.test", SSOProvider: domain.SSOProviderLocal, SSOSubject: "destination@example.test", Role: domain.RoleUser, Enabled: true, GroupID: g.ID, UUID: "11111111-1111-4111-8111-111111111111", SubToken: "fixture-destination-subscription-token"}
	if err := a.repos.User.Create(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	c := &domain.PSPClient{UserID: u.ID, PanelID: panel.ID, Email: "destination@psp.local", UUID: u.UUID, Password: "fixture-password"}
	c.SetDesiredLifecycle(domain.UserLifecycle{Enable: true})
	var err error
	c.ID, err = a.repos.PSPClient.Create(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.repos.PSPClient.SetInbounds(t.Context(), c.ID, []domain.PSPClientInbound{{ClientID: c.ID, NodeID: n.ID, State: domain.ClientApplyPending}}); err != nil {
		t.Fatal(err)
	}
	return destinationPolicyFixture{a: a, credential: credential, agent: agent, node: n, group: g, user: u, report: protocol.NodeReport{AgentID: agent.AgentID, ProtocolVersion: protocol.ProtocolVersion1, Have: map[string]protocol.StreamState{protocol.StreamConfig: {}, protocol.StreamRoster: {}, protocol.StreamDirectives: {}}}}
}

func saveWiringPolicy(t *testing.T, f destinationPolicyFixture) *domain.DestPolicy {
	t.Helper()
	p := &domain.DestPolicy{Name: "Wired scoped rule", Action: domain.DestBlock, Scope: domain.DestScopeGroups, GroupIDs: []int64{f.group.ID}, Enabled: true, Inline: domain.DestInline{Ports: "443"}}
	if err := f.a.destDefinitions.SavePolicy(t.Context(), p, time.Time{}, time.Now().Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBuildWiresTheDestinationRepos(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	saveWiringPolicy(t, f)
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy}
	response := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if response.Config.Body == nil || response.Config.Body.Policy == nil || len(response.Config.Body.Policy.Rules) != 1 {
		t.Fatal("assembled sync omitted the published destination policy")
	}
	rule := response.Config.Body.Policy.Rules[0]
	if rule.Ports != "443" || len(rule.Subjects) != 1 || rule.Subjects[0] != protocol.NewSubjectKey(f.user.ID) {
		t.Fatal("assembled compiler omitted production group membership")
	}
	state, err := f.a.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, true)
	if err != nil || state.DesiredSHA256 != protocol.PolicyDigest(response.Config.Body.Policy) || len(state.MintedBody) == 0 {
		t.Fatalf("assembled sync did not atomically mint its exact policy candidate: %v", err)
	}
}

func TestBuildDestinationEligibilityExcludesLegacyAllowlistNode(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	// Seed the persisted mode before any selector warms its cache. Mode CRUD
	// has separate acceptance; this check exercises the real selection reader.
	if _, err := f.a.database.ExecContext(context.Background(), "INSERT INTO dest_group_modes (group_id, mode, stage, list_ids, updated_at) VALUES (?, ?, ?, ?, ?)", f.group.ID, "allowlist", "trial", "[]", time.Now()); err != nil {
		t.Fatal(err)
	}
	// No live upstream report is needed for the committed local attachment
	// plan. A later provisioning failure must not hide selection's result.
	_ = f.a.user.ResyncMembership(t.Context(), f.user.ID)
	clients, err := f.a.repos.PSPClient.ListByUser(t.Context(), f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range clients {
		inbounds, err := f.a.repos.PSPClient.ListInbounds(t.Context(), client.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(inbounds) != 0 {
			t.Fatal("assembled selector admitted a native node without destination-policy capability into a trial allowlist")
		}
	}
}

func TestBuildLegacySyncKeepsConfigBytesAndETag(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	first := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	saveWiringPolicy(t, f)
	second := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if first.Config.Body == nil || first.Config.Body.Policy != nil || second.Config.Body == nil || second.Config.Body.Policy != nil || first.Config.Version != second.Config.Version || first.Config.ETag != second.Config.ETag {
		t.Fatal("publication changed a legacy node's policy-free config stream")
	}
	before, err := json.Marshal(first.Config.Body)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(second.Config.Body)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("publication changed legacy config bytes")
	}
}

func wiringAttachmentCount(t *testing.T, f destinationPolicyFixture) int {
	t.Helper()
	clients, err := f.a.repos.PSPClient.ListByUser(t.Context(), f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, client := range clients {
		inbounds, err := f.a.repos.PSPClient.ListInbounds(t.Context(), client.ID)
		if err != nil {
			t.Fatal(err)
		}
		count += len(inbounds)
	}
	return count
}

func TestBuildCapabilityAndFallbackChangesResyncWarmAllowlist(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	if _, err := f.a.database.ExecContext(t.Context(), "INSERT INTO dest_group_modes (group_id, mode, stage, list_ids, updated_at) VALUES (?, ?, ?, ?, ?)", f.group.ID, "allowlist", "trial", "[]", time.Now()); err != nil {
		t.Fatal(err)
	}
	p := saveWiringPolicy(t, f)
	queued := make(chan func(context.Context), 8)
	// Retain the real Build-owned outer dispatcher and its live eligibility
	// invalidation. Capture only the user work to test each committed boundary
	// deterministically, after the HTTP sync owner has released its lock.
	f.a.user.SetBackgroundRunner(func(name string, work func(context.Context)) {
		if name == "user.resync-group-members" {
			queued <- work
		}
	})
	runQueued := func() {
		t.Helper()
		select {
		case work := <-queued:
			work(t.Context())
		case <-time.After(3 * time.Second):
			t.Fatal("assembled post-commit hook did not enqueue allowlist members")
		}
	}
	_ = f.a.user.ResyncMembership(t.Context(), f.user.ID)
	if wiringAttachmentCount(t, f) != 0 {
		t.Fatal("legacy capability facts did not warm an ineligible selector")
	}
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy}
	syncNativeCacheFixture(t, f.a, f.credential, f.report)
	runQueued()
	if wiringAttachmentCount(t, f) != 1 {
		t.Fatal("capability gain reused the warm ineligible selector")
	}
	active := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if active.Config.Body == nil || active.Config.Body.Policy == nil {
		t.Fatal("restored allowlist member received no policy")
	}
	f.report.PolicyStatus = &protocol.PolicyStatus{State: "rejected", Digest: protocol.PolicyDigest(active.Config.Body.Policy), IssueCode: protocol.IssueDestinationPolicyRejected}
	syncNativeCacheFixture(t, f.a, f.credential, f.report)
	runQueued()
	if wiringAttachmentCount(t, f) != 0 {
		t.Fatal("rejection reused the warm eligible selector")
	}
	f.report.PolicyStatus = &protocol.PolicyStatus{State: "applied", Digest: ""}
	syncNativeCacheFixture(t, f.a, f.credential, f.report)
	select {
	case <-queued:
		t.Fatal("empty-policy success reopened the rejected allowlist")
	case <-time.After(30 * time.Millisecond):
	}
	p.Inline.Ports = "80"
	if err := f.a.destDefinitions.SavePolicy(t.Context(), p, p.UpdatedAt, time.Now().Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	f.report.PolicyStatus = nil
	retry := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if retry.Config.Body == nil || retry.Config.Body.Policy == nil {
		t.Fatal("new publication did not retry the scoped policy against retained client identities")
	}
	if retry.Roster.Body == nil || len(retry.Roster.Body.Clients) != 1 || len(retry.Roster.Body.Clients[0].Listeners) != 0 {
		t.Fatal("allowlist removal must preserve a zero-attachment roster identity for counters and policy confirmation")
	}
	rule := retry.Config.Body.Policy.Rules[0]
	if rule.Ports != "80" || len(rule.Subjects) != 1 || rule.Subjects[0] != protocol.NewSubjectKey(f.user.ID) {
		t.Fatal("scoped recovery candidate lost the retained subject identity")
	}
	select {
	case <-queued:
		t.Fatal("publication reopened the rejected allowlist before desired confirmation")
	case <-time.After(30 * time.Millisecond):
	}
	f.report.PolicyStatus = &protocol.PolicyStatus{State: "applied", Digest: protocol.PolicyDigest(retry.Config.Body.Policy)}
	syncNativeCacheFixture(t, f.a, f.credential, f.report)
	runQueued()
	if wiringAttachmentCount(t, f) != 1 {
		t.Fatal("desired confirmation did not restore the removed allowlist attachment")
	}
	f.report.Capabilities = nil
	f.report.PolicyStatus = nil
	syncNativeCacheFixture(t, f.a, f.credential, f.report)
	runQueued()
	if wiringAttachmentCount(t, f) != 0 {
		t.Fatal("capability loss reused the warm eligible selector")
	}
	// An unchanged legacy report must not schedule another membership pass.
	syncNativeCacheFixture(t, f.a, f.credential, f.report)
	select {
	case <-queued:
		t.Fatal("unchanged capability report repeated allowlist resync")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestBuildPolicyInputsInvalidateAfterUserGroupChange(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	saveWiringPolicy(t, f)
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy}
	first := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if first.Config.Body == nil || first.Config.Body.Policy == nil {
		t.Fatal("fixture did not warm the scoped-policy cache")
	}
	other := &domain.Group{Slug: "other-wiring", Name: "Other wiring", TagFilter: domain.TagFilter{All: true}}
	if err := f.a.repos.Group.Create(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	if err := f.a.user.ChangeGroupAndSync(t.Context(), f.user.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	second := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if second.Config.Body == nil || second.Config.Body.Policy != nil || second.Config.ETag == first.Config.ETag {
		t.Fatal("committed user-group change reused old policy membership")
	}
}

func TestDestinationPolicyDebounceUsesLiveBoundedSettingsAndPreservesErrors(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	for _, sample := range []struct{ raw, want int }{{0, 60}, {30, 30}, {9999, 3600}, {-1, 60}} {
		stored, err := f.a.settings.Load(t.Context(), ports.UISettings{})
		if err != nil {
			t.Fatal(err)
		}
		stored.DestPolicyApplyMinSeconds = sample.raw
		if err := f.a.settings.Save(t.Context(), stored); err != nil {
			t.Fatal(err)
		}
		got, err := destinationPolicyMinSeconds(t.Context(), f.a.settings)
		if err != nil || got != sample.want {
			t.Fatalf("raw=%d got=%d want=%d: %v", sample.raw, got, sample.want, err)
		}
	}
	problem := errors.New("settings read failed")
	if got, err := destinationPolicyMinSeconds(t.Context(), failedDestinationSettings{err: problem}); got != 0 || !errors.Is(err, problem) {
		t.Fatal("settings read failure became a default debounce")
	}
	if _, err := destinationPolicyMinSeconds(t.Context(), nil); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("missing settings became a default debounce")
	}
}

func TestBuildCompilerReadsChangedDebounceWithoutRestart(t *testing.T) {
	f := buildDestinationPolicyFixture(t)
	p := &domain.DestPolicy{Name: "debounced live setting", Action: domain.DestBlock, Scope: domain.DestScopeAll, Enabled: true, Inline: domain.DestInline{Ports: "443"}}
	if err := f.a.destDefinitions.SavePolicy(t.Context(), p, time.Time{}, time.Now().Add(-45*time.Second)); err != nil {
		t.Fatal(err)
	}
	f.report.Capabilities = []string{protocol.CapabilityDestinationPolicy}
	before := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if before.Config.Body == nil || before.Config.Body.Policy != nil {
		t.Fatal("default 60-second debounce published a 45-second-old write")
	}
	stored, err := f.a.settings.Load(t.Context(), ports.UISettings{})
	if err != nil {
		t.Fatal(err)
	}
	stored.DestPolicyApplyMinSeconds = 30
	if err := f.a.settings.Save(t.Context(), stored); err != nil {
		t.Fatal(err)
	}
	after := syncNativeCacheFixture(t, f.a, f.credential, f.report)
	if after.Config.Body == nil || after.Config.Body.Policy == nil || len(after.Config.Body.Policy.Rules) != 1 {
		t.Fatal("assembled compiler retained its boot-time debounce after a persisted setting change")
	}
}
