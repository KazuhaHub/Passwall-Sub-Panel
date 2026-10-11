package app

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

// EligiblePanelIDs is internal (json:"-"). The public group detail exposes
// counts and individual exclusions; require this panel's capability exclusion
// to appear/disappear while the other panels' coverage stays unchanged.
func nativeAllowlistCoverageMatches(original, current destpolicy.GroupAccessDetail, panelID int64, capable bool) bool {
	if panelID <= 0 || original.Mode != "allowlist" || original.Stage != "enforce" || original.EligibleNodes < 1 || original.TotalNodes != original.EligibleNodes+len(original.Ineligible) || current.Mode != original.Mode || current.Stage != original.Stage || current.TotalNodes != original.TotalNodes {
		return false
	}
	expected := make(map[int64]string, len(original.Ineligible)+1)
	for _, node := range original.Ineligible {
		if node.PanelID <= 0 || node.PanelID == panelID || expected[node.PanelID] != "" || node.Reason == "" {
			return false
		}
		expected[node.PanelID] = node.Reason
	}
	eligible := original.EligibleNodes
	if !capable {
		expected[panelID] = "capability"
		eligible--
	}
	if current.EligibleNodes != eligible || len(current.Ineligible) != len(expected) {
		return false
	}
	for _, node := range current.Ineligible {
		if node.Reason == "" || expected[node.PanelID] != node.Reason {
			return false
		}
		delete(expected, node.PanelID)
	}
	return len(expected) == 0
}

func TestNativeAllowlistCoverageUsesPublicHTTPFields(t *testing.T) {
	original := destpolicy.GroupAccessDetail{GroupAccessSummary: destpolicy.GroupAccessSummary{Mode: "allowlist", Stage: "enforce", GroupNodeCoverage: destpolicy.GroupNodeCoverage{TotalNodes: 3, EligibleNodes: 2, Ineligible: []destpolicy.GroupIneligibleNode{{PanelID: 15, Reason: "xui"}}, EligiblePanelIDs: []int64{11, 13}}}}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal("encode public group detail")
	}
	var baseline destpolicy.GroupAccessDetail
	if json.Unmarshal(data, &baseline) != nil || len(baseline.EligiblePanelIDs) != 0 {
		t.Fatal("group detail did not retain its public JSON contract")
	}
	for _, capable := range []bool{false, true} {
		scenarios := []string{"valid", "wrong-count", "wrong-panel", "wrong-reason", "lost-peer", "wrong-stage", "changed-total", "duplicate-panel"}
		if !capable {
			scenarios = append(scenarios, "peer-excluded-instead")
		}
		for _, scenario := range scenarios {
			name := "removed/"
			if capable {
				name = "restored/"
			}
			t.Run(name+scenario, func(t *testing.T) {
				current := baseline
				current.Ineligible = append([]destpolicy.GroupIneligibleNode{}, baseline.Ineligible...)
				if !capable {
					current.EligibleNodes--
					current.Ineligible = append(current.Ineligible, destpolicy.GroupIneligibleNode{PanelID: 11, Reason: "capability"})
				}
				switch scenario {
				case "wrong-count":
					current.EligibleNodes++
				case "wrong-panel":
					current.Ineligible[0].PanelID = 14
				case "wrong-reason":
					current.Ineligible[0].Reason = "capability"
				case "lost-peer":
					current.Ineligible = current.Ineligible[1:]
				case "wrong-stage":
					current.Stage = "trial"
				case "changed-total":
					current.TotalNodes++
				case "duplicate-panel":
					current.Ineligible = append(current.Ineligible, current.Ineligible[0])
				case "peer-excluded-instead":
					current.Ineligible[1].PanelID = 13
				}
				if got := nativeAllowlistCoverageMatches(baseline, current, 11, capable); got != (scenario == "valid") {
					t.Fatalf("public coverage readiness=%t", got)
				}
			})
		}
	}
}

// Empty attachment sets retire access while retaining the stable SQL and
// native roster identity that owns cumulative counters. Require the same
// membership in the transactional desired snapshot and the acknowledged body.
func nativeAllowlistMembershipMatches(original *domain.PSPClient, desired []ports.NativeDesiredClient, roster protocol.RosterBody, capable bool) bool {
	if original == nil || original.ID == 0 || original.UUID == "" {
		return false
	}
	var current *ports.NativeDesiredClient
	for i := range desired {
		client := desired[i].Client
		if client == nil {
			return false
		}
		if client.UserID != original.UserID {
			continue
		}
		if current != nil {
			return false
		}
		current = &desired[i]
	}
	if current == nil {
		return false
	}
	c := current.Client
	if c.ID != original.ID || c.PanelID != original.PanelID || c.UUID != original.UUID || !c.CreatedAt.Equal(original.CreatedAt) || c.LifetimeUpBytes < original.LifetimeUpBytes || c.LifetimeDownBytes < original.LifetimeDownBytes || c.LifetimeTotalBytes < original.LifetimeTotalBytes {
		return false
	}
	wantAttachments := 0
	if capable {
		wantAttachments = 1
	}
	if len(current.Inbounds) != wantAttachments {
		return false
	}
	if capable && (!c.DesiredEnable || current.Inbounds[0].ClientID != c.ID || current.Inbounds[0].NodeID == 0 || current.Inbounds[0].State != domain.ClientApplyApplied) {
		return false
	}
	matches := 0
	for _, client := range roster.Clients {
		if client.Key != protocol.NewClientKey(original.ID) && client.Subject != protocol.NewSubjectKey(original.UserID) && client.Credentials.UUID != original.UUID {
			continue
		}
		if client.Key != protocol.NewClientKey(c.ID) || client.Subject != protocol.NewSubjectKey(c.UserID) || client.Credentials.UUID != c.UUID || len(client.Listeners) != wantAttachments {
			return false
		}
		if capable && (!client.Enabled || client.Listeners[0] != protocol.NewListenerKey(current.Inbounds[0].NodeID)) {
			return false
		}
		matches++
	}
	return matches == 1
}

func TestNativeAllowlistMembershipPreservesRetiredIdentity(t *testing.T) {
	original := domain.PSPClient{ID: 7, UserID: 9, PanelID: 11, UUID: "original", DesiredEnable: true, CreatedAt: time.Unix(100, 0), LifetimeUpBytes: 30, LifetimeDownBytes: 40, LifetimeTotalBytes: 70}
	for _, capable := range []bool{false, true} {
		name := "retired"
		if capable {
			name = "restored"
		}
		t.Run(name, func(t *testing.T) {
			scenarios := []string{"valid", "deleted-row", "replaced-row", "lost-counters", "extra-local-client", "wrong-local-membership", "wrong-wire-membership", "missing-wire-client", "extra-wire-client", "wrong-wire-identity", "wrong-wire-subject", "wrong-wire-credentials"}
			if capable {
				scenarios = append(scenarios, "pending", "wrong-listener", "disabled")
			}
			for _, scenario := range scenarios {
				t.Run(scenario, func(t *testing.T) {
					c := original
					attachments := []domain.PSPClientInbound{}
					listeners := []protocol.ListenerKey{}
					if capable {
						attachments = append(attachments, domain.PSPClientInbound{ClientID: c.ID, NodeID: 13, State: domain.ClientApplyApplied})
						listeners = append(listeners, protocol.NewListenerKey(13))
					}
					desired := []ports.NativeDesiredClient{{Client: &c, Inbounds: attachments}}
					roster := protocol.RosterBody{Clients: []protocol.Client{{Key: protocol.NewClientKey(c.ID), Subject: protocol.NewSubjectKey(c.UserID), Enabled: true, Credentials: protocol.Credential{UUID: c.UUID}, Listeners: listeners}}}
					want := scenario == "valid"
					switch scenario {
					case "deleted-row":
						desired = nil
					case "replaced-row":
						c.ID++
					case "lost-counters":
						c.LifetimeTotalBytes = 0
					case "extra-local-client":
						desired = append(desired, desired[0])
					case "wrong-local-membership":
						if capable {
							desired[0].Inbounds = nil
						} else {
							desired[0].Inbounds = []domain.PSPClientInbound{{ClientID: c.ID, NodeID: 13, State: domain.ClientApplyApplied}}
						}
					case "wrong-wire-membership":
						if capable {
							roster.Clients[0].Listeners = nil
						} else {
							roster.Clients[0].Listeners = []protocol.ListenerKey{protocol.NewListenerKey(13)}
						}
					case "missing-wire-client":
						roster.Clients = nil
					case "extra-wire-client":
						roster.Clients = append(roster.Clients, roster.Clients[0])
					case "wrong-wire-identity":
						roster.Clients[0].Key = protocol.NewClientKey(8)
					case "wrong-wire-subject":
						roster.Clients[0].Subject = protocol.NewSubjectKey(10)
					case "wrong-wire-credentials":
						roster.Clients[0].Credentials.UUID = "replacement"
					case "pending":
						if capable {
							desired[0].Inbounds[0].State = domain.ClientApplyPending
						}
					case "wrong-listener":
						if capable {
							roster.Clients[0].Listeners[0] = protocol.NewListenerKey(14)
						}
					case "disabled":
						c.DesiredEnable = false
						roster.Clients[0].Enabled = false
					}
					if got := nativeAllowlistMembershipMatches(&original, desired, roster, capable); got != want {
						t.Fatalf("membership readiness=%t, want %t", got, want)
					}
				})
			}
		})
	}
}
