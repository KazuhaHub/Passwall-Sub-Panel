//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

type nativeLegacyTransitionFixture struct {
	app                             *App
	probe                           *nativeLegacyProbe
	agentID, dataDir                string
	panelID, userID                 int64
	inbound, targetPort             int
	targetAddress, nonce            string
	originalConfig                  []byte
	originalConfigETag              protocol.ETag
	adminToken, adminURL, oldBinary string
	adminClient                     *http.Client
	replace                         func(string) *exec.Cmd
	stop                            func()
}

// Replace only the agent executable. No state, report, receipt or core artifact
// is rewritten by the driver, and this does not exercise the remote updater.
func nativeLegacyNodeTransitions(t *testing.T, f nativeLegacyTransitionFixture) {
	t.Helper()
	binary := os.Getenv("PSP_NATIVE_MIXED_NODE_BIN")
	info, err := os.Stat(binary)
	if err != nil || !filepath.IsAbs(binary) || filepath.Base(binary) != "destination-mixed-node" || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		t.Fatal("missing immutable mixed-fleet candidate")
	}
	statePath := filepath.Join(f.dataDir, "state.db")
	originalState, err := os.Stat(statePath)
	nativeMust(t, err, "bind original node state database")
	originalAgent, err := f.app.repos.NodeAgent.GetByAgentID(t.Context(), f.agentID)
	nativeMust(t, err, "read original transition identity")
	originalUser, err := f.app.repos.User.GetByID(t.Context(), f.userID)
	nativeMust(t, err, "read original transition principal")
	_, controlAddress, controlPort, controlNonce, _ := nativeTarget(t)
	// Stop the last executable before either owned nonce target is removed.
	t.Cleanup(f.stop)

	for _, step := range []struct {
		name, executable string
		capable          bool
	}{
		{"node_upgrade", binary, true}, {"node_downgrade", f.oldBinary, false}, {"node_reupgrade", binary, true},
	} {
		if !t.Run(step.name, func(test *testing.T) {
			priorPolicy, err := f.app.repos.DestAgentPolicy.Get(test.Context(), f.agentID, true)
			nativeMust(test, err, "read policy history before node transition")
			after := time.Now().UTC()
			sequence := f.probe.snapshot().Sequence
			cmd := f.replace(step.executable)
			defer func() {
				if !test.Failed() {
					return
				}
				s := f.probe.snapshot()
				for _, fact := range []struct {
					name  string
					value bool
				}{
					{"sequence", s.Sequence >= sequence+3}, {"capability", s.PolicyCapable == step.capable}, {"policy", s.PolicyPresent == step.capable},
					{"core", s.CoreState == "running" && s.CoreEngine == "xray" && s.CoreVersion == "26.6.27"}, {"listener", s.ListenerApplied},
					{"config", s.HaveConfig.ETag == s.Config.ETag && s.HaveConfig.Applied == s.Config.Version},
					{"roster", s.HaveRoster.ETag == s.Roster.ETag && s.HaveRoster.Applied == s.Roster.Version},
					{"version", step.capable || s.AgentVersion == "v4.0.1.8"},
					{"original_etags", step.capable || s.Config.ETag == f.originalConfigETag},
				} {
					test.Logf("native diagnostic: node_transition_%s=%t", fact.name, fact.value)
				}
				agent, err := f.app.repos.NodeAgent.GetByAgentID(t.Context(), f.agentID)
				fresh := err == nil && agent.LastSeen != nil && agent.LastSeen.After(after) && slicesContainsNative(agent.ObservedCapabilities, protocol.CapabilityDestinationPolicy) == step.capable
				test.Logf("native diagnostic: node_transition_agent_fresh=%t", fresh)
				streams := err == nil
				if streams {
					for _, name := range []domain.NodeAgentStreamName{domain.NodeAgentStreamConfig, domain.NodeAgentStreamRoster} {
						stream, err := f.app.repos.NodeAgent.GetStream(t.Context(), f.agentID, name)
						streams = streams && err == nil && stream.Converged() && stream.AppliedEpoch == agent.Epoch
					}
				}
				test.Logf("native diagnostic: node_transition_streams=%t", streams)
				policy, err := f.app.repos.DestAgentPolicy.Get(t.Context(), f.agentID, false)
				test.Logf("native diagnostic: node_transition_minted_empty=%t", err == nil && policy != nil && policy.MintedKind == domain.DestCandidateEmpty && policy.MintedSHA256 == "")
			}()
			nativeWait(test, 120*time.Second, "original node binary transition", func() bool {
				s := f.probe.snapshot()
				if s.Sequence < sequence+3 || s.PolicyCapable != step.capable || s.PolicyPresent != step.capable || s.CoreState != "running" || s.CoreEngine != "xray" || s.CoreVersion != "26.6.27" || !s.ListenerApplied || s.HaveConfig.ETag != s.Config.ETag || s.HaveConfig.Applied != s.Config.Version || s.HaveRoster.ETag != s.Roster.ETag || s.HaveRoster.Applied != s.Roster.Version {
					return false
				}
				// Roster embeds the new config version, so A/B/A restores the
				// config ETag but must acknowledge the current roster identity.
				if !step.capable && (s.AgentVersion != "v4.0.1.8" || s.Config.ETag != f.originalConfigETag) {
					return false
				}
				agent, err := f.app.repos.NodeAgent.GetByAgentID(test.Context(), f.agentID)
				if err != nil || agent.LastSeen == nil || !agent.LastSeen.After(after) || slicesContainsNative(agent.ObservedCapabilities, protocol.CapabilityDestinationPolicy) != step.capable {
					return false
				}
				for _, name := range []domain.NodeAgentStreamName{domain.NodeAgentStreamConfig, domain.NodeAgentStreamRoster} {
					stream, err := f.app.repos.NodeAgent.GetStream(test.Context(), f.agentID, name)
					if err != nil || !stream.Converged() || stream.AppliedEpoch != agent.Epoch {
						return false
					}
				}
				policy, err := f.app.repos.DestAgentPolicy.Get(test.Context(), f.agentID, true)
				if err != nil || policy == nil {
					return false
				}
				if !step.capable {
					return policy.MintedKind == domain.DestCandidateEmpty && policy.MintedSHA256 == ""
				}
				state, err := f.app.destDefinitions.State(test.Context())
				return err == nil && !state.Paused && policy.MintedGeneration == state.PublishedGeneration && policy.MintedKind == domain.DestCandidateDesired && policy.MintedSHA256 != "" && policy.ReportedState == "applied" && policy.ReportedAt != nil && policy.ReportedAt.After(after) && policy.ReportedSHA256 == policy.MintedSHA256 && policy.AppliedSHA256 == policy.MintedSHA256 && policy.AppliedRuleCount == 1 && !policy.FallbackExhausted
			})
			live := nativeBindLiveCore(t, cmd.Process.Pid, f.dataDir, "xray")
			traffic := func(address string, port int, nonce string, allow bool) {
				ctx, cancel := context.WithTimeout(test.Context(), 5*time.Second)
				defer cancel()
				err := nativeVLESSHost(ctx, f.inbound, address, port, originalUser.UUID, nonce, "transition.example.test")
				if (err == nil) != allow {
					test.Fatal("original node transition traffic did not match execution")
				}
			}
			traffic(controlAddress, controlPort, controlNonce, true)
			traffic(f.targetAddress, f.targetPort, f.nonce, !step.capable)
			traffic(controlAddress, controlPort, controlNonce, true)
			live()
			currentState, err := os.Stat(statePath)
			nativeMust(test, err, "read preserved node state database")
			db, err := sql.Open("sqlite", "file:"+statePath+"?mode=ro")
			nativeMust(test, err, "open original node schema read-only")
			var schema int
			err = db.QueryRowContext(test.Context(), "PRAGMA user_version").Scan(&schema)
			closeErr := db.Close()
			nativeMust(test, err, "read preserved node schema")
			nativeMust(test, closeErr, "close original node schema")
			if !os.SameFile(originalState, currentState) || schema != 9 {
				test.Fatal("node binary transition replaced its state database or changed schema")
			}
			agent, err := f.app.repos.NodeAgent.GetByAgentID(test.Context(), f.agentID)
			nativeMust(test, err, "read preserved node identity")
			user, err := f.app.repos.User.GetByID(test.Context(), f.userID)
			nativeMust(test, err, "read preserved node principal")
			if agent.Epoch != originalAgent.Epoch || agent.CredentialSHA256 != originalAgent.CredentialSHA256 || user.UUID != originalUser.UUID {
				test.Fatal("node binary transition lost its original principal or identity")
			}
			if !step.capable {
				artifact, err := os.ReadFile(filepath.Join(f.dataDir, "runtime", "xray", "current.json"))
				nativeMust(test, err, "read downgraded core artifact")
				if !bytes.Equal(artifact, f.originalConfig) {
					test.Fatal("node downgrade did not restore the original core artifact")
				}
				policy, err := f.app.repos.DestAgentPolicy.Get(test.Context(), f.agentID, true)
				nativeMust(test, err, "read downgraded current candidate")
				var body *protocol.DestinationPolicy
				if len(policy.MintedBody) > 0 && (json.Unmarshal(policy.MintedBody, &body) != nil || body != nil) {
					test.Fatal("node downgrade retained executable policy in the current candidate")
				}
				if priorPolicy == nil || policy.AppliedSHA256 != priorPolicy.AppliedSHA256 || !bytes.Equal(policy.AppliedBody, priorPolicy.AppliedBody) || policy.AppliedRuleCount != priorPolicy.AppliedRuleCount {
					test.Fatal("node downgrade erased confirmed policy history")
				}
			}
			r, err := http.NewRequestWithContext(test.Context(), "GET", f.adminURL+"/api/admin/dest/status", nil)
			nativeMust(test, err, "create transition coverage request")
			r.Header.Set("Authorization", "Bearer "+f.adminToken)
			response, err := f.adminClient.Do(r)
			nativeMust(test, err, "read transition coverage HTTP")
			defer response.Body.Close()
			var status destpolicy.DestinationStatus
			if response.StatusCode != 200 {
				test.Fatal("node transition coverage did not match its own execution")
			}
			nativeMust(test, json.NewDecoder(response.Body).Decode(&status), "decode transition coverage")
			found := false
			for _, row := range status.Nodes {
				if row.PanelID != f.panelID {
					continue
				}
				found = true
				// Old Node cannot report an empty PolicyStatus. Its historical LKG
				// count survives; unsupported capability plus the empty pending
				// candidate must not be mistaken for current policy execution.
				if row.Supports.Policy != step.capable || step.capable && (row.State != "applied" || row.AppliedRules != 1 || row.PendingSince != nil) || !step.capable && (row.State != "unsupported_version" || row.MintedKind != domain.DestCandidateEmpty || row.PendingSince == nil || row.AppliedRules != priorPolicy.AppliedRuleCount || row.Collecting) {
					test.Fatal("node transition coverage did not match its own execution")
				}
			}
			if !found {
				test.Fatal("node transition coverage did not match its own execution")
			}
		}) {
			return
		}
	}
}
