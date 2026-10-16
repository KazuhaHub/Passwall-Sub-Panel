//go:build destination_acceptance && linux

package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func nativeLogGlobalReadiness(t *testing.T, readiness string, peer *nativeGlobalPeer) {
	t.Helper()
	t.Log("global batch readiness: " + readiness)
	if peer == nil {
		return
	}
	for name, value := range peer.sync.facts() {
		t.Logf("native global sync: %s=%d", name, value)
	}
	alive := 0
	if peer.process != nil && peer.process.Process != nil && peer.process.Process.Signal(syscall.Signal(0)) == nil {
		alive = 1
	}
	t.Logf("native global sync: parent_alive=%d", alive)
}

// Both real global pressure components use the same causal publication,
// capability, full membership and exact receipt gates. The returned predicate
// observes original production state; it cannot mint or apply any receipt.
func nativeGlobalCollectionPublication(t *testing.T, cohort *nativeGlobalCohort, token string) func([]*domain.User, int64) (string, *nativeGlobalPeer) {
	t.Helper()
	f, a := cohort.fixture, cohort.fixture.a
	// Bind the causal candidate before collection changes. A later no-op
	// publication can already see the new candidate while its ACK is pending;
	// using that snapshot would reject its eventual valid exact receipt.
	var prior []*domain.DestAgentPolicy
	for _, peer := range cohort.peers {
		p, err := a.repos.DestAgentPolicy.Get(t.Context(), peer.agent.AgentID, true)
		nativeMust(t, err, "bind global policies before actual collection edits")
		prior = append(prior, p)
	}
	for _, peer := range cohort.peers[1:] {
		response := serverAuditRequest(t, a, token, "PUT", "/"+strconv.FormatInt(peer.agent.PanelID, 10), map[string]any{"audit_collect": "hits_and_usage"})
		if response.Code != http.StatusOK {
			t.Fatal("global owner collection could not be enabled through production administration")
		}
	}
	afterMutation := time.UnixMilli(time.Now().UnixMilli())
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, cohort.server.URL+"/api/admin/dest/publish", nil)
	nativeMust(t, err, "create actual global publication request")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := cohort.server.Client().Do(request)
	nativeMust(t, err, "publish through the actual TLS administration route")
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("global policy publication failed")
	}

	return func(users []*domain.User, groupID int64) (string, *nativeGlobalPeer) {
		readiness := "state"
		var waitingPeer *nativeGlobalPeer
		state, err := a.destDefinitions.State(t.Context())
		if err != nil || state.Paused {
			return readiness, waitingPeer
		}
		for i, peer := range cohort.peers {
			waitingPeer = peer
			p, e1 := a.repos.DestAgentPolicy.Get(t.Context(), peer.agent.AgentID, true)
			agent, e2 := a.repos.NodeAgent.GetByAgentID(t.Context(), peer.agent.AgentID)
			panel, e3 := a.repos.XUIPanel.GetByID(t.Context(), peer.agent.PanelID)
			roster, e4 := a.repos.NodeAgent.GetStream(t.Context(), peer.agent.AgentID, domain.NodeAgentStreamRoster)
			readiness = "store"
			if e1 != nil || e2 != nil || e3 != nil || e4 != nil || p == nil || agent == nil || panel == nil || roster == nil {
				return readiness, waitingPeer
			}
			readiness = "capability"
			if !slicesContainsNative(agent.ObservedCapabilities, protocol.CapabilityAuditUsage) {
				return readiness, waitingPeer
			}
			readiness = "streams"
			if reason := cohort.readyReason(peer); reason != "" {
				readiness = "streams_" + reason
				return readiness, waitingPeer
			}
			readiness = "receipt"
			if !nativeMutationReceiptMatches(prior[i], *p, state.PublishedGeneration, agent.LastSeen, afterMutation) {
				return readiness, waitingPeer
			}
			readiness = "collection"
			if !nativeCollectionReceiptMatches(*p, protocol.CollectLevel("hits_and_usage"), uint64(panel.AuditCollectRevision)) {
				return readiness, waitingPeer
			}
			var body protocol.DestinationPolicy
			var members protocol.RosterBody
			readiness = "shape"
			if json.Unmarshal(p.MintedBody, &body) != nil || json.Unmarshal(roster.DesiredBody, &members) != nil || len(body.Rules) != 3 {
				return readiness, waitingPeer
			}
			trial := false
			for _, rule := range body.Rules {
				if rule.ID == fmt.Sprintf("g%d", groupID) && rule.CatchAll && rule.Action == protocol.RuleObserve && len(rule.Subjects) == 200 {
					seen := make(map[protocol.SubjectKey]bool)
					for _, subject := range rule.Subjects {
						seen[subject] = true
					}
					trial = true
					for _, user := range users {
						trial = trial && seen[protocol.NewSubjectKey(user.ID)]
					}
				}
			}
			seen := make(map[protocol.SubjectKey]bool)
			for _, member := range members.Clients {
				if member.Enabled {
					for _, listener := range member.Listeners {
						if listener == protocol.NewListenerKey(peer.node.ID) {
							seen[member.Subject] = true
						}
					}
				}
			}
			readiness = "subjects"
			if !trial {
				return readiness, waitingPeer
			}
			readiness = "roster"
			if !seen[protocol.NewSubjectKey(f.user.ID)] {
				return readiness, waitingPeer
			}
			for _, user := range users {
				if !seen[protocol.NewSubjectKey(user.ID)] {
					return readiness, waitingPeer
				}
			}
		}
		readiness = "complete"
		return readiness, waitingPeer
	}
}
