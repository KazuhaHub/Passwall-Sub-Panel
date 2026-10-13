//go:build destination_acceptance && linux

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

type nativeFallbackRejectionFixture struct {
	root                                    *testing.T
	app                                     **App
	agentID                                 string
	panelID, nodeID, userID, canaryClientID int64
	userUUID, canaryUUID, xrayVersion       string
	gateway                                 *nativePanelGateway
	probe                                   *nativeRestartProbe
	request                                 func(string, string, any, int, any)
	publish, waitApplied                    func()
	setSniffing                             func(string)
	fullSniffing                            string
	stopDaemon                              func(*testing.T)
	streamsReady                            func(*domain.NodeAgent) bool
	traffic                                 func(string, bool)
}

// Install and apply the original policy with the reviewed sing-box core, then
// change intent through production admin HTTP to a reviewed Xray core. No
// binary, report, on-disk artifact or contract validator is modified.
func (f nativeFallbackRejectionFixture) run(t *testing.T, pruned bool) {
	request := f.request
	selectCore := func(engine, version string) {
		request("POST", fmt.Sprintf("servers/%d/select-core", f.panelID), map[string]any{"engine": engine, "version": version, "confirm_restricted": version == "26.9.9"}, 202, nil)
	}
	observedEngine := func(agent *domain.NodeAgent, engine, version string) bool {
		panel, err := (*f.app).repos.XUIPanel.GetByID(t.Context(), f.panelID)
		return err == nil && string(agent.ObservedCoreEngine) == engine && string(agent.DesiredCoreEngine) == engine && agent.DesiredCoreVersion == version && panel.XrayVersion == version
	}
	waitEngine := func(engine, version string) {
		nativeWait(t, 120*time.Second, "reviewed core switch", func() bool {
			agent, err := (*f.app).repos.NodeAgent.GetByAgentID(t.Context(), f.agentID)
			return err == nil && observedEngine(agent, engine, version) && f.streamsReady(agent)
		})
	}
	request("PUT", fmt.Sprintf("servers/%d", f.panelID), map[string]any{"audit_collect": "off"}, 200, nil)
	f.waitApplied()
	// sing-box rejects legacy per-inbound Xray sniffing even without domains.
	// Prepare compatible listener intent through the same production HTTP route.
	f.setSniffing(`{}`)
	f.waitApplied()
	selectCore("sing-box", "1.14.0")
	f.waitApplied()
	waitEngine("sing-box", "1.14.0")
	input := destinationPolicyInput("Previous mapped prefix", "block")
	input["inline"] = map[string]any{"cidrs": []string{nativeRejectedMappedCIDR}}
	var previous destinationPolicyAPIResult
	request("POST", "dest/policies", input, 201, &previous)
	var removed destinationPolicyAPIResult
	if pruned {
		removedInput := destinationPolicyInput("Retired fallback source", "observe")
		removedInput["inline"] = map[string]any{"ports": "1"}
		request("POST", "dest/policies", removedInput, 201, &removed)
	}
	f.publish()
	f.waitApplied()
	prior, err := (*f.app).repos.DestAgentPolicy.Get(t.Context(), f.agentID, true)
	nativeMust(t, err, "read sing-box confirmed mapped-prefix policy")
	var fallback, desired protocol.DestinationPolicy
	nativeMust(t, json.Unmarshal(prior.AppliedBody, &fallback), "decode original LKG")
	nativeMust(t, json.Unmarshal(prior.AppliedBody, &desired), "decode next desired fixture")
	wantRules := 2
	if pruned {
		wantRules++
	}
	if prior.MintedKind != domain.DestCandidateDesired || prior.MintedSHA256 != prior.AppliedSHA256 || protocol.PolicyDigest(&fallback) != prior.AppliedSHA256 || len(fallback.Rules) != wantRules || fallback.Collect != "" {
		t.Fatal("mapped-prefix LKG was not actually confirmed by sing-box")
	}
	f.traffic(f.userUUID, true)
	f.traffic(f.canaryUUID, true)
	// Freeze only real sync traffic while the two admin mutations are published.
	// This prevents sing-box from confirming the new desired policy before the
	// core switch. The previous confirmed policy remains the only LKG.
	f.gateway.partitionSync(&nativeSyncPartition{})
	t.Cleanup(func() { f.gateway.partitionSync(nil) })
	input["inline"], input["updated_at"] = map[string]any{"cidrs": []string{"::ffff:198.51.100.0/120"}}, previous.UpdatedAt
	request("PUT", fmt.Sprintf("dest/policies/%d", previous.ID), input, 200, &previous)
	if pruned {
		request("DELETE", fmt.Sprintf("dest/policies/%d", removed.ID), nil, 204, nil)
	}
	prune := func(rules []protocol.DestinationRule, update bool) []protocol.DestinationRule {
		out := make([]protocol.DestinationRule, 0, len(rules))
		for _, rule := range rules {
			if pruned && rule.ID == fmt.Sprintf("p%d", removed.ID) {
				continue
			}
			if update && rule.ID == fmt.Sprintf("p%d", previous.ID) {
				rule.CIDRs = []string{"::ffff:198.51.100.0/120"}
			}
			out = append(out, rule)
		}
		return out
	}
	fallback.Rules, desired.Rules = prune(fallback.Rules, false), prune(desired.Rules, true)
	nativeMust(t, protocol.ValidateDestinationPolicy(&fallback), "validate exact fallback fixture")
	nativeMust(t, protocol.ValidateDestinationPolicy(&desired), "validate exact desired fixture")
	fallbackSHA, desiredSHA := protocol.PolicyDigest(&fallback), protocol.PolicyDigest(&desired)
	if (fallbackSHA != prior.AppliedSHA256) != pruned || desiredSHA == fallbackSHA {
		t.Fatal("fallback rejection fixture did not distinguish pruning from desired")
	}
	f.publish()
	state, err := (*f.app).destDefinitions.State(t.Context())
	nativeMust(t, err, "read fallback rejection publication")
	f.probe.armRejectedDigest(fallbackSHA)
	selectedAfter := time.UnixMilli(time.Now().UnixMilli())
	selectCore("xray", f.xrayVersion)
	f.gateway.partitionSync(nil)
	var exhausted *domain.DestAgentPolicy
	waitEmpty := func(after time.Time) {
		facts := map[string]bool{}
		defer func() {
			if t.Failed() {
				for _, category := range []string{"fallback_report", "fallback_accepted", "fallback_empty", "fallback_exhausted", "fallback_core", "fallback_streams", "fallback_issue"} {
					t.Logf("native diagnostic: %s=%t", category, facts[category])
				}
			}
		}()
		nativeWait(t, 120*time.Second, "rejected fallback empty deployment", func() bool {
			status, seen, accepted := f.probe.first()
			facts["fallback_report"], facts["fallback_accepted"] = seen, accepted
			if !seen {
				return false
			}
			if !accepted || status == nil || status.State != "rejected" || status.Digest != fallbackSHA || status.IssueCode != protocol.IssueDestinationPolicyRejected || len(status.Listeners) != 0 {
				t.Fatal("real rejected fallback report did not match the exact minted policy")
			}
			a := *f.app
			r, err := a.repos.DestAgentPolicy.Get(t.Context(), f.agentID, true)
			if err != nil {
				return false
			}
			agent, err := a.repos.NodeAgent.GetByAgentID(t.Context(), f.agentID)
			if err != nil {
				return false
			}
			facts["fallback_streams"], facts["fallback_core"] = f.streamsReady(agent), observedEngine(agent, "xray", f.xrayVersion)
			facts["fallback_exhausted"] = r.FallbackExhausted
			facts["fallback_empty"] = r.MintedKind == domain.DestCandidateEmpty && r.MintedSHA256 == "" && r.ReportedState == "applied" && r.ReportedSHA256 == ""
			if agent.LastSeen == nil || !agent.LastSeen.After(after) || !facts["fallback_streams"] || !facts["fallback_core"] {
				return false
			}
			if r.MintedKind != domain.DestCandidateEmpty || r.MintedGeneration != state.PublishedGeneration || r.MintedSHA256 != "" || r.DesiredSHA256 != desiredSHA || !r.FallbackExhausted || r.FallbackReason != "rejected" || r.RejectedGeneration != state.PublishedGeneration || r.RejectedContext != r.MintedContext || r.AppliedSHA256 != "" || len(r.AppliedBody) != 0 || r.AppliedAt != nil || r.ReportedState != "applied" || r.ReportedSHA256 != "" || r.ReportedIssue != "" || r.ReportedAt == nil || !r.ReportedAt.After(selectedAfter) {
				return false
			}
			issues, _, err := a.repos.NodeAgentIssue.List(t.Context(), ports.NodeAgentIssueFilter{AgentID: f.agentID, Code: "destination_policy_lkg_rejected", Pagination: ports.Pagination{Page: 1, PageSize: 50}})
			if err != nil {
				return false
			}
			for _, issue := range issues {
				if issue.Key == fallbackSHA && issue.LastSeenAt.After(selectedAfter) {
					facts["fallback_issue"] = true
					exhausted = r
					return true
				}
			}
			return false
		})
		var status destpolicy.DestinationStatus
		request("GET", "dest/status", nil, 200, &status)
		if len(status.Nodes) != 1 || !status.Nodes[0].FallbackExhausted || status.Nodes[0].MintedKind != domain.DestCandidateEmpty || status.Nodes[0].AppliedRules != 0 || status.Nodes[0].PendingSince != nil || status.Nodes[0].State != "rejected" {
			t.Fatal("status API did not confirm exhausted rejected fallback")
		}
	}
	waitEmpty(selectedAfter)
	f.traffic(f.userUUID, true)
	f.traffic(f.canaryUUID, true)
	policyContext, generation := exhausted.MintedContext, exhausted.MintedGeneration
	request("POST", fmt.Sprintf("users/%d/set-enabled", f.userID), map[string]any{"enabled": false}, 204, nil)
	revokedAfter := time.UnixMilli(time.Now().UnixMilli())
	nativeWait(t, 120*time.Second, "rejected fallback roster revocation", func() bool {
		a := *f.app
		agent, err := a.repos.NodeAgent.GetByAgentID(t.Context(), f.agentID)
		if err != nil || agent.LastSeen == nil || !agent.LastSeen.After(revokedAfter) || !f.streamsReady(agent) {
			return false
		}
		roster, err := a.repos.NodeAgent.GetStream(t.Context(), f.agentID, domain.NodeAgentStreamRoster)
		return err == nil && nativeOpenRosterReceiptMatches(*roster, agent.Epoch, protocol.NewSubjectKey(f.userID), protocol.NewClientKey(f.canaryClientID), protocol.NewListenerKey(f.nodeID))
	})
	waitEmpty(revokedAfter)
	if exhausted.MintedContext != policyContext || exhausted.MintedGeneration != generation {
		t.Fatal("roster mutation rearmed rejected fallback")
	}
	f.traffic(f.userUUID, false)
	f.traffic(f.canaryUUID, true)
	if pruned {
		// Reopen the actual production App, SQL pool and caches on the original
		// database while the same Node continues serving the empty deployment.
		old := *f.app
		cfg := *old.cfg
		f.gateway.replace(nil)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := old.Shutdown(ctx)
		cancel()
		nativeMust(t, err, "stop PSP after rejected pruned fallback")
		restored, err := Build(f.root.Context(), &cfg)
		nativeMust(t, err, "reopen PSP after rejected pruned fallback")
		*f.app = restored
		f.root.Cleanup(func() {
			f.stopDaemon(f.root)
			f.gateway.replace(nil)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			nativeMust(f.root, restored.Shutdown(ctx), "close rejected fallback PSP")
		})
		restored.startDestinationAudit()
		reopenedAfter := time.UnixMilli(time.Now().UnixMilli())
		f.gateway.replace(restored.server.Handler)
		waitEmpty(reopenedAfter)
		if exhausted.MintedContext != policyContext || exhausted.MintedGeneration != generation {
			t.Fatal("PSP restart rearmed rejected pruned fallback")
		}
		f.traffic(f.userUUID, false)
		f.traffic(f.canaryUUID, true)
	}
	// Manual retry must offer the desired version again, then reach a fresh
	// rejection without restoring the discarded LKG or the revoked account.
	f.probe.armRejectedDigest(desiredSHA)
	var retry struct {
		Requested bool `json:"retry_requested"`
	}
	request("POST", "dest/agents/"+f.agentID+"/retry", nil, 200, &retry)
	if !retry.Requested {
		t.Fatal("exhausted fallback retry was not admitted")
	}
	nativeWait(t, 120*time.Second, "exhausted fallback manual retry rejection", func() bool {
		status, seen, accepted := f.probe.first()
		return seen && accepted && status != nil && status.Digest == desiredSHA && status.IssueCode == protocol.IssueDestinationPolicyRejected
	})
	// Restore the shared watch fixture through normal admin intent for later
	// scenarios. Publishing a repaired definition is an allowed retry trigger.
	request("DELETE", fmt.Sprintf("dest/policies/%d", previous.ID), nil, 204, nil)
	request("POST", fmt.Sprintf("users/%d/set-enabled", f.userID), map[string]any{"enabled": true}, 204, nil)
	f.publish()
	f.waitApplied()
	f.setSniffing(f.fullSniffing)
	f.waitApplied()
	request("PUT", fmt.Sprintf("servers/%d", f.panelID), map[string]any{"audit_collect": "hits_and_usage"}, 200, nil)
	f.waitApplied()
	waitEngine("xray", f.xrayVersion)
	f.traffic(f.userUUID, true)
	f.traffic(f.canaryUUID, true)
}
