//go:build destination_acceptance && linux

package app

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type nativeScopeControl struct {
	test        func() *testing.T
	request     func(string, string, any, int, any)
	publish     func()
	waitApplied func()
	traffic     func(string, string, bool)
	control     func(string, int, string)
	live        func() func()
	stop        func()
	output      func() string
}

type nativeScopeCases struct {
	groupScope, exemption, exemptionExpired, allowPriority func()
}

// Reuse the running production PSP and real Node/core. Membership, policies
// and exemptions change only through normal admin HTTP. Actual clock expiry
// invokes the production hourly maintenance callback, without editing SQL,
// reports, core artifacts or receipts; subsequent publication is automatic.
func nativeScopedDestinationCases(t *testing.T, f destinationPolicyFixture, peer *domain.User, policy *destinationPolicyAPIResult, input map[string]any, port int, cb nativeScopeControl) nativeScopeCases {
	t.Helper()
	var controlAddress, controlNonce string
	var controlPort int
	for range 8 {
		_, controlAddress, controlPort, controlNonce, _ = nativeTarget(t)
		if controlPort != port {
			break
		}
	}
	if controlPort == port {
		t.Fatal("scope control overlaps the blocked target port")
	}
	var initialPassword string
	t.Cleanup(func() {
		for _, private := range []string{controlNonce, initialPassword, peerEmail(peer)} {
			if private != "" && strings.Contains(cb.output(), private) {
				t.Error("private scoped traffic escaped into Node output; content withheld")
			}
		}
	})
	t.Cleanup(cb.stop)
	var expires time.Time
	var peerUUID string
	var engine domain.NodeCoreEngine
	count := func(user int64) int64 {
		var n int64
		nativeMust(cb.test(), f.a.database.QueryRowContext(cb.test().Context(), "SELECT COALESCE(SUM(count),0) FROM dest_hits WHERE panel_id=? AND user_id=? AND source=? AND action='block'", f.agent.PanelID, user, fmt.Sprintf("p%d", policy.ID)).Scan(&n), "read scoped hit count")
		return n
	}
	// Shape checks accompany an exact real applied receipt. A previous policy
	// or a successful listener alone cannot satisfy the new scope/exemption.
	waitPolicy := func(after time.Time, check func(*protocol.DestinationPolicy) bool) {
		nativeWait(cb.test(), 120*time.Second, "native scoped policy application", func() bool {
			state, err := f.a.destDefinitions.State(cb.test().Context())
			if err != nil || state.Paused {
				return false
			}
			agent, err := f.a.repos.NodeAgent.GetByAgentID(cb.test().Context(), f.agent.AgentID)
			if err != nil || agent.LastSeen == nil || !agent.LastSeen.After(after) {
				return false
			}
			engine = agent.ObservedCoreEngine
			runtime, err := f.a.repos.DestAgentPolicy.Get(cb.test().Context(), f.agent.AgentID, true)
			if err != nil || runtime == nil || runtime.MintedKind != domain.DestCandidateDesired || runtime.MintedGeneration != state.PublishedGeneration || runtime.ReportedState != "applied" || runtime.MintedSHA256 == "" || runtime.MintedSHA256 != runtime.ReportedSHA256 || runtime.MintedSHA256 != runtime.AppliedSHA256 || runtime.FallbackReason != "" || runtime.FallbackExhausted {
				return false
			}
			var body *protocol.DestinationPolicy
			if json.Unmarshal(runtime.MintedBody, &body) != nil || body == nil || protocol.ValidateDestinationPolicy(body) != nil || protocol.PolicyDigest(body) != runtime.MintedSHA256 || !check(body) {
				return false
			}
			stream, err := f.a.repos.NodeAgent.GetStream(cb.test().Context(), f.agent.AgentID, domain.NodeAgentStreamRoster)
			if err != nil || stream == nil || !stream.Converged() || stream.AppliedEpoch != agent.Epoch {
				return false
			}
			for _, user := range []*domain.User{f.user, peer} {
				if user == nil {
					return false
				}
				clients, err := f.a.repos.PSPClient.ListByUser(cb.test().Context(), user.ID)
				if err != nil {
					return false
				}
				matched := 0
				for _, client := range clients {
					if client.PanelID != f.agent.PanelID {
						continue
					}
					attachments, err := f.a.repos.PSPClient.ListInbounds(cb.test().Context(), client.ID)
					if err != nil || !client.DesiredEnable || client.UUID != user.UUID || len(attachments) != 1 || attachments[0].NodeID != f.node.ID || attachments[0].State != domain.ClientApplyApplied {
						return false
					}
					matched++
				}
				if matched != 1 {
					return false
				}
			}
			return true
		})
	}
	blockBody := func(body *protocol.DestinationPolicy, scoped, exempt bool) bool {
		if len(body.Rules) != 1 || body.Rules[0].ID != fmt.Sprintf("p%d", policy.ID) || body.Rules[0].Action != protocol.RuleBlock || body.Rules[0].Ports != strconv.Itoa(port) || len(body.Rules[0].Domains) != 0 || len(body.Rules[0].CIDRs) != 0 || len(body.Rules[0].Protocols) != 0 {
			return false
		}
		rule := body.Rules[0]
		if scoped {
			if len(rule.Subjects) != 1 || rule.Subjects[0] != protocol.NewSubjectKey(f.user.ID) {
				return false
			}
		} else if len(rule.Subjects) != 0 {
			return false
		}
		if exempt {
			return len(body.Exempt) == 1 && body.Exempt[0] == protocol.NewSubjectKey(f.user.ID)
		}
		return len(body.Exempt) == 0
	}
	setScope := func(scope string, groups []int64) {
		input["scope"], input["group_ids"], input["updated_at"] = scope, groups, policy.UpdatedAt
		cb.request("PUT", fmt.Sprintf("dest/policies/%d", policy.ID), input, 200, policy)
	}
	traffic := func(primary, other bool, host string) {
		live := cb.live()
		cb.control(controlAddress, controlPort, controlNonce)
		cb.traffic(f.user.UUID, host, primary)
		cb.traffic(peer.UUID, host, other)
		cb.control(controlAddress, controlPort, controlNonce)
		live()
	}
	waitNewHit := func(user int64, before int64) {
		if engine == domain.NodeCoreXray {
			nativeWait(cb.test(), 30*time.Second, "native scoped hit ingestion", func() bool { return count(user) > before })
		}
	}
	drainHits := func() {
		if engine == domain.NodeCoreXray {
			time.Sleep(7 * time.Second)
		}
	}
	return nativeScopeCases{
		groupScope: func() {
			var group struct {
				ID int64 `json:"id"`
			}
			cb.request("POST", "groups", map[string]any{"slug": "native-scope-other", "name": "Native scope other", "tag_filter": map[string]any{"all": true, "tags": []string{}}}, 201, &group)
			if group.ID <= 0 || group.ID == f.group.ID {
				cb.test().Fatal("scoped fixture group identity invalid")
			}
			if peer == nil {
				var created struct {
					User struct {
						ID int64 `json:"id"`
					} `json:"user"`
					InitialPassword string `json:"initial_password"`
				}
				cb.request("POST", "users", map[string]any{"upn": "native-scope-peer@example.test", "group_id": group.ID}, 201, &created)
				var err error
				peer, err = f.a.repos.User.GetByID(cb.test().Context(), created.User.ID)
				nativeMust(cb.test(), err, "read normally provisioned scoped peer")
				initialPassword = created.InitialPassword
			} else {
				cb.request("PUT", fmt.Sprintf("users/%d", peer.ID), map[string]any{"group_id": group.ID}, 200, nil)
			}
			peerUUID = peer.UUID
			after := time.Now().UTC()
			setScope("groups", []int64{f.group.ID})
			cb.publish()
			cb.waitApplied()
			waitPolicy(after, func(p *protocol.DestinationPolicy) bool { return blockBody(p, true, false) })
			before, peerBefore := count(f.user.ID), count(peer.ID)
			traffic(false, true, "")
			waitNewHit(f.user.ID, before)
			drainHits()
			if count(peer.ID) != peerBefore {
				cb.test().Fatal("unscoped peer traffic produced a block hit")
			}
		},
		exemption: func() {
			after := time.Now().UTC()
			setScope("all", []int64{})
			expires = time.Now().UTC().Add(time.Minute)
			cb.request("POST", "dest/exemptions", map[string]any{"user_id": f.user.ID, "reason": "Native scoped exemption", "expires_at": expires.UnixMilli()}, 201, nil)
			cb.publish()
			cb.waitApplied()
			waitPolicy(after, func(p *protocol.DestinationPolicy) bool { return blockBody(p, false, true) })
			drainHits()
			primaryBefore, before := count(f.user.ID), count(peer.ID)
			traffic(true, false, "")
			waitNewHit(peer.ID, before)
			drainHits()
			if count(f.user.ID) != primaryBefore {
				cb.test().Fatal("exempt traffic produced a block hit")
			}
		},
		exemptionExpired: func() {
			nativeWait(cb.test(), 90*time.Second, "native exemption clock expiry", func() bool { return !time.Now().UTC().Before(expires) })
			after := time.Now().UTC()
			f.a.pruneDestExemptions(cb.test().Context())
			waitPolicy(after, func(p *protocol.DestinationPolicy) bool { return blockBody(p, false, false) })
			before := count(f.user.ID)
			traffic(false, false, "")
			waitNewHit(f.user.ID, before)
		},
		allowPriority: func() {
			const host = "scope.example.test"
			const otherHost = "other-scope.example.test"
			var allowList, blockList struct {
				ID int64 `json:"id"`
			}
			cb.request("POST", "dest/lists", map[string]any{"name": "Native allowed domain", "kind": "custom", "text": "full:" + host + "\n"}, 201, &allowList)
			cb.request("POST", "dest/lists", map[string]any{"name": "Native blocked domains", "kind": "custom", "text": "full:" + host + "\nfull:" + otherHost + "\n"}, 201, &blockList)
			input["list_ids"] = []int64{blockList.ID}
			setScope("all", []int64{})
			allowInput := destinationPolicyInput("Native allow priority", "allow")
			allowInput["list_ids"], allowInput["inline"] = []int64{allowList.ID}, map[string]any{"ports": strconv.Itoa(port)}
			var allowed destinationPolicyAPIResult
			after := time.Now().UTC()
			cb.request("POST", "dest/policies", allowInput, 201, &allowed)
			cb.publish()
			cb.waitApplied()
			waitPolicy(after, func(p *protocol.DestinationPolicy) bool {
				return len(p.Exempt) == 0 && len(p.Rules) == 2 && p.Rules[0].ID == fmt.Sprintf("p%d", allowed.ID) && p.Rules[0].Action == protocol.RuleAllow && len(p.Rules[0].Domains) == 1 && p.Rules[0].Domains[0] == "full:"+host && p.Rules[0].Ports == strconv.Itoa(port) && p.Rules[1].ID == fmt.Sprintf("p%d", policy.ID) && p.Rules[1].Action == protocol.RuleBlock && len(p.Rules[1].Domains) == 2 && slices.Contains(p.Rules[1].Domains, "full:"+host) && slices.Contains(p.Rules[1].Domains, "full:"+otherHost)
			})
			drainHits()
			primaryBefore, peerBefore := count(f.user.ID), count(peer.ID)
			traffic(true, true, host)
			drainHits()
			if count(f.user.ID) != primaryBefore || count(peer.ID) != peerBefore {
				cb.test().Fatal("allow priority produced a block hit")
			}
			traffic(false, false, otherHost)
			waitNewHit(f.user.ID, primaryBefore)
			cb.request("DELETE", fmt.Sprintf("dest/policies/%d", allowed.ID), nil, 204, nil)
			input["list_ids"] = []int64{}
			setScope("all", []int64{})
			cb.request("DELETE", fmt.Sprintf("dest/lists/%d", allowList.ID), nil, 204, nil)
			cb.request("DELETE", fmt.Sprintf("dest/lists/%d", blockList.ID), nil, 204, nil)
			cb.request("PUT", fmt.Sprintf("users/%d", peer.ID), map[string]any{"group_id": f.group.ID}, 200, nil)
			cb.publish()
			cb.waitApplied()
			waitPolicy(time.Now().UTC().Add(-time.Second), func(p *protocol.DestinationPolicy) bool { return blockBody(p, false, false) })
			current, err := f.a.repos.User.GetByID(cb.test().Context(), peer.ID)
			nativeMust(cb.test(), err, "read restored scoped membership")
			if current.GroupID != f.group.ID || current.UUID != peerUUID {
				cb.test().Fatal("scoped membership changed original credentials")
			}
		},
	}
}

func peerEmail(peer *domain.User) string {
	if peer == nil {
		return ""
	}
	return peer.Email
}
