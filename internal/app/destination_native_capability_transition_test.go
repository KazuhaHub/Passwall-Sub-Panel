//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

// Withdraw and regain capabilities through real immutable Node binaries. Only
// production observation and asynchronous membership work may change clients;
// the driver never edits capabilities, clients, rosters or applied receipts.
func nativeLegacyAllowlistCapabilityTransitions(t *testing.T, f nativeLegacyTransitionFixture, groupID int64) {
	t.Helper()
	binary := os.Getenv("PSP_NATIVE_MIXED_NODE_BIN")
	info, err := os.Stat(binary)
	if err != nil || !filepath.IsAbs(binary) || filepath.Base(binary) != "destination-mixed-node" || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		t.Fatal("missing immutable allowlist capability candidate")
	}
	stateFile, err := os.Stat(filepath.Join(f.dataDir, "state.db"))
	nativeMust(t, err, "bind allowlist capability node database")
	agent, err := f.app.repos.NodeAgent.GetByAgentID(t.Context(), f.agentID)
	nativeMust(t, err, "read allowlist capability agent identity")
	user, err := f.app.repos.User.GetByID(t.Context(), f.userID)
	nativeMust(t, err, "read allowlist capability original principal")
	originalClients, err := f.app.repos.PSPClient.ListByUser(t.Context(), user.ID)
	nativeMust(t, err, "bind allowlist capability stable client")
	var originalClient *domain.PSPClient
	for _, client := range originalClients {
		if client.PanelID == f.panelID {
			if originalClient != nil {
				t.Fatal("allowlist capability fixture has multiple original panel clients")
			}
			originalClient = client
		}
	}
	if originalClient == nil || originalClient.UUID != user.UUID {
		t.Fatal("allowlist capability fixture has no original stable client")
	}
	unblockedTarget := func() (string, int, string) {
		for range 8 {
			_, address, port, nonce, _ := nativeTarget(t)
			if port != f.targetPort {
				return address, port, nonce
			}
		}
		t.Fatal("independent capability target overlaps original port rule")
		return "", 0, ""
	}
	controlAddress, controlPort, controlNonce := unblockedTarget()
	unlistedAddress, unlistedPort, unlistedNonce := unblockedTarget()
	t.Cleanup(f.stop)
	request := func(test *testing.T, method, path string, body any, want int, result any) {
		test.Helper()
		data, err := json.Marshal(body)
		nativeMust(test, err, "encode capability admin input")
		r, err := http.NewRequestWithContext(test.Context(), method, f.adminURL+"/api/admin/"+path, bytes.NewReader(data))
		nativeMust(test, err, "create capability admin request")
		r.Header.Set("Authorization", "Bearer "+f.adminToken)
		r.Header.Set("Content-Type", "application/json")
		response, err := f.adminClient.Do(r)
		nativeMust(test, err, "call capability admin HTTP")
		defer response.Body.Close()
		if response.StatusCode != want {
			test.Fatalf("allowlist capability admin returned HTTP %d; body withheld", response.StatusCode)
		}
		if result != nil {
			nativeMust(test, json.NewDecoder(response.Body).Decode(result), "decode capability admin response")
		}
	}
	traffic := func(test *testing.T, controlAllowed, unlistedAllowed bool) {
		test.Helper()
		for _, target := range []struct {
			address string
			port    int
			nonce   string
			allow   bool
		}{
			{controlAddress, controlPort, controlNonce, controlAllowed}, {f.targetAddress, f.targetPort, f.nonce, false},
			{unlistedAddress, unlistedPort, unlistedNonce, unlistedAllowed}, {controlAddress, controlPort, controlNonce, controlAllowed},
		} {
			ctx, cancel := context.WithTimeout(test.Context(), 5*time.Second)
			err := nativeVLESSHost(ctx, f.inbound, target.address, target.port, user.UUID, target.nonce, "capability.example.test")
			cancel()
			if (err == nil) != target.allow {
				test.Fatal("allowlist capability traffic did not match actual membership")
			}
		}
	}
	wait := func(test *testing.T, sequence uint64, after time.Time, capable, enforced bool) {
		test.Helper()
		facts := map[string]bool{}
		defer func() {
			if !test.Failed() {
				return
			}
			for _, name := range []string{"streams", "capability", "agent_fresh", "membership", "policy_body", "policy_receipt"} {
				test.Logf("native diagnostic: capability_%s=%t", name, facts[name])
			}
		}()
		nativeWait(test, 120*time.Second, "real allowlist capability membership convergence", func() bool {
			s := f.probe.snapshot()
			facts["streams"] = nativePanelStreamsApplied(s, sequence)
			facts["capability"] = s.PolicyCapable == capable && (capable || s.AgentVersion == "v4.0.1.8" && !s.PolicyPresent)
			if !facts["streams"] || !facts["capability"] {
				return false
			}
			currentAgent, err := f.app.repos.NodeAgent.GetByAgentID(test.Context(), f.agentID)
			facts["agent_fresh"] = err == nil && currentAgent.LastSeen != nil && currentAgent.LastSeen.After(after) && slicesContainsNative(currentAgent.ObservedCapabilities, protocol.CapabilityDestinationPolicy) == capable
			if !facts["agent_fresh"] {
				return false
			}
			snapshot, err := f.app.repos.NativeDesired.Load(test.Context(), f.panelID)
			if err != nil || snapshot == nil {
				return false
			}
			stream, err := f.app.repos.NodeAgent.GetStream(test.Context(), f.agentID, domain.NodeAgentStreamRoster)
			if err != nil || stream == nil || !stream.Converged() || stream.AppliedEpoch != agent.Epoch || stream.DesiredETag == "" || stream.DesiredETag != string(s.HaveRoster.ETag) || stream.DesiredVersion != s.Roster.Version.Version {
				return false
			}
			var roster protocol.RosterBody
			if json.Unmarshal(stream.DesiredBody, &roster) != nil || roster.MinConfigVersion != s.Config.Version {
				return false
			}
			facts["membership"] = nativeAllowlistMembershipMatches(originalClient, snapshot.Clients, roster, capable)
			if !capable {
				return facts["membership"]
			}
			facts["policy_body"] = s.ConfigBodyKnown && s.ConfigPolicy && s.PolicyReportPresent && s.PolicyReportState == "applied"
			if !facts["membership"] || !facts["policy_body"] {
				return false
			}
			state, err := f.app.destDefinitions.State(test.Context())
			if err != nil || state.Paused {
				return false
			}
			policy, err := f.app.repos.DestAgentPolicy.Get(test.Context(), f.agentID, true)
			facts["policy_receipt"] = err == nil && policy != nil && policy.MintedKind == domain.DestCandidateDesired && policy.MintedGeneration == state.PublishedGeneration && policy.MintedSHA256 != "" && policy.MintedSHA256 == s.PolicyReportDigest && policy.ReportedState == "applied" && policy.ReportedSHA256 == policy.MintedSHA256 && policy.AppliedSHA256 == policy.MintedSHA256 && !policy.FallbackExhausted
			if !facts["policy_receipt"] {
				return false
			}
			var body *protocol.DestinationPolicy
			if json.Unmarshal(policy.MintedBody, &body) != nil || body == nil {
				return false
			}
			action := protocol.RuleObserve
			if enforced {
				action = protocol.RuleBlock
			}
			// A surviving global port rule cannot stand in for the group's
			// actual trial or deny-all rule scoped to this original account.
			groupMatches := 0
			for _, rule := range body.Rules {
				if rule.ID == fmt.Sprintf("g%d", groupID) && rule.Action == action && rule.CatchAll && len(rule.Subjects) == 1 && rule.Subjects[0] == protocol.NewSubjectKey(user.ID) {
					groupMatches++
				}
			}
			facts["policy_body"] = facts["policy_body"] && groupMatches == 1
			if !facts["policy_body"] {
				return false
			}
			return facts["policy_receipt"]
		})
	}
	var mode destinationGroupModeAPIResult
	if !t.Run("allowlist_capability_setup", func(test *testing.T) {
		sequence, after := f.probe.snapshot().Sequence, time.Now().UTC()
		path := fmt.Sprintf("dest/groups/%d", groupID)
		request(test, "PUT", path, map[string]any{"mode": "allowlist", "stage": "trial", "list_ids": []int64{}, "updated_at": int64(0), "base_hosts": []string{}}, 200, &mode)
		request(test, "POST", fmt.Sprintf("dest/lists/%d/entries", mode.ExtraListID), map[string]any{"add": []string{controlAddress + "/128"}}, 200, nil)
		request(test, "POST", "dest/publish", nil, 200, nil)
		wait(test, sequence, after, true, false)
		traffic(test, true, true)
		sequence, after = f.probe.snapshot().Sequence, time.Now().UTC()
		request(test, "PUT", path, map[string]any{"mode": "allowlist", "stage": "enforce", "list_ids": []int64{}, "updated_at": mode.UpdatedAt}, 200, &mode)
		request(test, "POST", "dest/publish", nil, 200, nil)
		wait(test, sequence, after, true, true)
		traffic(test, true, false)
	}) {
		return
	}
	var originalCoverage destpolicy.GroupAccessDetail
	request(t, "GET", fmt.Sprintf("dest/groups/%d", groupID), nil, 200, &originalCoverage)
	for _, step := range []struct {
		name, binary string
		capable      bool
	}{
		{"allowlist_capability_removed", f.oldBinary, false}, {"allowlist_capability_restored", binary, true},
	} {
		if !t.Run(step.name, func(test *testing.T) {
			sequence, after := f.probe.snapshot().Sequence, time.Now().UTC()
			cmd := f.replace(step.binary)
			wait(test, sequence, after, step.capable, true)
			// A live core plus complete streams distinguishes account removal
			// from an unavailable daemon when the old binary loses capability.
			live := nativeBindLiveCore(test, cmd.Process.Pid, f.dataDir, "xray")
			traffic(test, step.capable, false)
			live()
			currentAgent, err := f.app.repos.NodeAgent.GetByAgentID(test.Context(), f.agentID)
			nativeMust(test, err, "read capability transition identity")
			currentUser, err := f.app.repos.User.GetByID(test.Context(), user.ID)
			nativeMust(test, err, "read capability transition principal")
			currentFile, err := os.Stat(filepath.Join(f.dataDir, "state.db"))
			nativeMust(test, err, "read capability transition database")
			if !os.SameFile(stateFile, currentFile) || currentAgent.Epoch != agent.Epoch || currentAgent.CredentialSHA256 != agent.CredentialSHA256 || currentUser.UUID != user.UUID {
				test.Fatal("allowlist capability transition changed original identity")
			}
			var detail destpolicy.GroupAccessDetail
			request(test, "GET", fmt.Sprintf("dest/groups/%d", groupID), nil, 200, &detail)
			if !nativeAllowlistCoverageMatches(originalCoverage, detail, f.panelID, step.capable) {
				test.Fatal("allowlist capability coverage borrowed a peer eligibility")
			}
		}) {
			return
		}
	}
}
