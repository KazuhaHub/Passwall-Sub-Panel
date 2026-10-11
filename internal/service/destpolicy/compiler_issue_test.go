package destpolicy

import (
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestCompilerPersistsPreflightAndQuotaIssuesWithoutIdleWrites(t *testing.T) {
	for _, reason := range []string{"sniffing", "over_limit"} {
		t.Run(reason, func(t *testing.T) {
			c, inputs, runtime, agent, snapshot, base := cachedCompilerFixture(t)
			defs := domain.DestDefinitions{State: domain.DestPolicyState{Generation: 1, PublishedGeneration: 1}, Policies: []domain.DestPolicy{{ID: 1, Enabled: true, Action: domain.DestBlock, Scope: domain.DestScopeGroups, GroupIDs: []int64{8}, Inline: domain.DestInline{Ports: "443"}}}}
			wantCode := "destination_policy_over_limit"
			if reason == "sniffing" {
				wantCode = "destination_policy_sniffing_insufficient"
				defs.Policies[0].Inline.Protocols = []string{"bittorrent"}
				base.Core.Engine = "xray"
				base.Listeners = []protocol.Listener{sniffListener(t, 1, true, `{"enabled":true,"metadataOnly":true,"destOverride":["http","tls"]}`)}
			} else {
				c.inputs = &metricsQuotaInputs{*inputs}
			}
			body, err := BuildDefinitionSnapshot(defs)
			if err != nil {
				t.Fatal(err)
			}
			c.definitions.(*cachedPublicationStore).snapshot.Body = body
			issues := &fallbackIssueStore{fail: true}
			c.observer.issues = issues
			caps := []string{protocol.CapabilityDestinationPolicy}
			// No diagnostic may escape an uncommitted candidate decision.
			runtime.fail = true
			if _, err := c.Compile(t.Context(), agent, snapshot, caps, base); !errors.Is(err, domain.ErrUnavailable) || issues.calls != 0 {
				t.Fatal("rolled-back decision emitted an issue")
			}
			runtime.fail = false
			if _, err := c.Compile(t.Context(), agent, snapshot, caps, base); !errors.Is(err, domain.ErrUnavailable) || issues.calls != 1 {
				t.Fatal("issue failure was not returned")
			}
			issues.fail = false
			candidate, err := c.Compile(t.Context(), agent, snapshot, caps, base)
			if err != nil || issues.calls != 2 || len(issues.issues) != 1 || issues.issues[0].Code != wantCode || issues.issues[0].Key != candidate.Mint.Context {
				t.Fatal("cached candidate did not retry its exact issue")
			}
			for range 3 {
				if _, err := c.Compile(t.Context(), agent, snapshot, caps, base); err != nil {
					t.Fatal(err)
				}
			}
			if issues.calls != 2 {
				t.Fatal("unchanged fault rewrote its issue on idle sync")
			}
			faultBase, faultInputs := base, c.inputs
			// Membership changes advance the production cache generation.
			inputs.generation++
			base.Listeners, c.inputs = nil, inputs
			if _, err := c.Compile(t.Context(), agent, snapshot, caps, base); err != nil {
				t.Fatal(err)
			}
			if issues.calls != 2 {
				t.Fatal("healthy policy emitted an issue")
			}
			base, c.inputs = faultBase, faultInputs
			if quota, ok := faultInputs.(*metricsQuotaInputs); ok {
				quota.generation = inputs.generation + 1
			}
			if _, err := c.Compile(t.Context(), agent, snapshot, caps, base); err != nil || issues.calls != 3 {
				t.Fatal("a recovered fault did not record a fresh occurrence")
			}
		})
	}
}
