//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
	"golang.org/x/sys/unix"
)

type nativeLegacyPanelFixture struct {
	app                         *App
	probe                       *nativeLegacyProbe
	gateway                     *nativePanelGateway
	dataDir, panelDB, agentID   string
	panelID, userID             int64
	parent, inbound, targetPort int
	targetAddress, nonce        string
	originalConfig              []byte
	adminURL                    string
	adminClient                 *http.Client
	stopCandidate, stopLegacy   func(*testing.T)
	startLegacy                 func(*testing.T) http.Handler
	startCandidate              func(*testing.T) *App
	stopNode                    func()
	resumePeer                  func(http.Handler)
}

// Only the panel changes. The original candidate Node remains alive with its
// own identity, database and TLS endpoint. The other mixed node stays isolated
// throughout these assertions; this is not an all-node rollback claim.
func nativeLegacyPanelTransitions(t *testing.T, f nativeLegacyPanelFixture) {
	t.Helper()
	originalAgent, err := f.app.repos.NodeAgent.GetByAgentID(t.Context(), f.agentID)
	nativeMust(t, err, "read original PSP rollback agent")
	originalUser, err := f.app.repos.User.GetByID(t.Context(), f.userID)
	nativeMust(t, err, "read original PSP rollback principal")
	state, err := f.app.destDefinitions.State(t.Context())
	nativeMust(t, err, "read original published PSP rollback state")
	if state.Paused || state.PublishedGeneration == 0 {
		t.Fatal("panel rollback requires a published executing policy")
	}
	policy, err := f.app.repos.DestAgentPolicy.Get(t.Context(), f.agentID, true)
	nativeMust(t, err, "read original applied PSP rollback policy")
	if policy == nil || policy.MintedKind != domain.DestCandidateDesired || policy.MintedSHA256 == "" || policy.AppliedSHA256 != policy.MintedSHA256 || policy.AppliedRuleCount != 1 {
		t.Fatal("panel rollback requires a published executing policy")
	}
	panelFile, err := os.Stat(f.panelDB)
	nativeMust(t, err, "bind original panel database")
	nodeFile, err := os.Stat(filepath.Join(f.dataDir, "state.db"))
	nativeMust(t, err, "bind panel rollback node database")
	configPath := filepath.Join(f.dataDir, "runtime", "xray", "current.json")
	confirmed, err := os.ReadFile(configPath)
	nativeMust(t, err, "read pre-downgrade core artifact")
	live := nativeBindLiveCore(t, f.parent, f.dataDir, "xray")
	originalExited := nativePanelHoldCoreExit(t, f.parent, f.dataDir)
	_, controlAddress, controlPort, controlNonce, _ := nativeTarget(t)
	t.Cleanup(f.stopNode)
	traffic := func(test *testing.T, allow bool) {
		test.Helper()
		for _, target := range []struct {
			address string
			port    int
			nonce   string
			allow   bool
		}{
			{controlAddress, controlPort, controlNonce, true}, {f.targetAddress, f.targetPort, f.nonce, allow}, {controlAddress, controlPort, controlNonce, true},
		} {
			ctx, cancel := context.WithTimeout(test.Context(), 5*time.Second)
			err := nativeVLESSHost(ctx, f.inbound, target.address, target.port, originalUser.UUID, target.nonce, "panel-transition.example.test")
			cancel()
			if (err == nil) != target.allow {
				test.Fatal("panel transition traffic did not match actual execution")
			}
		}
	}
	partition := &nativeSyncPartition{}
	var preserved map[string][32]byte
	if !t.Run("panel_downgrade_offline", func(test *testing.T) {
		f.gateway.partitionSync(partition)
		f.stopCandidate(test)
		preserved = nativePanelDestinationSnapshot(test, f.panelDB)
		if len(preserved) != 12 {
			test.Fatal("panel rollback did not bind the complete destination schema")
		}
		f.gateway.replace(f.startLegacy(test))
		nativeWait(test, 45*time.Second, "panel downgrade offline attempts", func() bool { attempts, _ := partition.snapshot(); return attempts >= 3 })
		live()
		artifact, err := os.ReadFile(configPath)
		nativeMust(test, err, "read offline panel rollback artifact")
		if !bytes.Equal(artifact, confirmed) || !reflect.DeepEqual(preserved, nativePanelDestinationSnapshot(test, f.panelDB)) {
			test.Fatal("offline panel downgrade changed its live artifact or destination data")
		}
		traffic(test, false)
		live()
	}) {
		return
	}
	if !t.Run("panel_downgrade", func(test *testing.T) {
		startedAt := time.Now().UTC()
		sequence := f.probe.snapshot().Sequence
		defer nativePanelTransitionDiagnostics(test, f.probe, sequence, policy.MintedSHA256, originalExited)
		f.gateway.partitionSync(nil)
		nativeWait(test, 120*time.Second, "online PSP downgrade application", func() bool {
			s := f.probe.snapshot()
			return nativePanelStreamsApplied(s, sequence) && s.PolicyCapable && s.ConfigBodyKnown && !s.ConfigPolicy && s.PolicyReportPresent && s.PolicyReportState == "applied" && s.PolicyReportDigest == "" && originalExited()
		})
		appliedAt := time.Now().UTC()
		artifact, err := os.ReadFile(configPath)
		nativeMust(test, err, "read online panel rollback artifact")
		if !nativePanelPolicyFreeArtifactMatches(f.originalConfig, artifact) {
			test.Fatal("online panel downgrade did not deploy the original policy-free artifact")
		}
		traffic(test, true)
		newLive := nativeBindLiveCore(t, f.parent, f.dataDir, "xray")
		newLive()
		if !reflect.DeepEqual(preserved, nativePanelDestinationSnapshot(test, f.panelDB)) {
			test.Fatal("released panel modified destination tables or data")
		}
		// Retain duration/count evidence only, not a private node identifier.
		test.Logf("native panel application: downgrade=%d", appliedAt.Sub(startedAt).Milliseconds())
	}) {
		return
	}
	t.Run("panel_reupgrade", func(test *testing.T) {
		oldExited := nativePanelHoldCoreExit(test, f.parent, f.dataDir)
		f.stopLegacy(test)
		if !reflect.DeepEqual(preserved, nativePanelDestinationSnapshot(test, f.panelDB)) {
			test.Fatal("released panel modified destination tables or data")
		}
		after := time.Now().UTC()
		sequence := f.probe.snapshot().Sequence
		defer nativePanelTransitionDiagnostics(test, f.probe, sequence, policy.MintedSHA256, oldExited)
		a := f.startCandidate(test)
		restored := nativePanelDestinationSnapshot(test, f.panelDB)
		for _, table := range []string{"dest_lists", "dest_policies", "dest_exemptions", "dest_group_modes", "dest_policy_snapshots"} {
			if restored[table] != preserved[table] {
				test.Fatal("PSP reupgrade lost the published policy state")
			}
		}
		f.gateway.replace(a.server.Handler)
		currentState, err := a.destDefinitions.State(test.Context())
		nativeMust(test, err, "read restored publication")
		if currentState.Paused || currentState.PublishedGeneration != state.PublishedGeneration {
			test.Fatal("PSP reupgrade lost the published policy state")
		}
		nativeWait(test, 120*time.Second, "PSP reupgrade current policy execution", func() bool {
			s := f.probe.snapshot()
			if !nativePanelStreamsApplied(s, sequence) || !s.PolicyCapable || !s.ConfigBodyKnown || !s.ConfigPolicy || !s.PolicyReportPresent || s.PolicyReportState != "applied" || s.PolicyReportDigest != policy.MintedSHA256 || !oldExited() {
				return false
			}
			agent, err := a.repos.NodeAgent.GetByAgentID(test.Context(), f.agentID)
			if err != nil || agent.LastSeen == nil || !agent.LastSeen.After(after) {
				return false
			}
			current, err := a.repos.DestAgentPolicy.Get(test.Context(), f.agentID, false)
			return err == nil && current != nil && current.MintedGeneration == state.PublishedGeneration && current.MintedKind == domain.DestCandidateDesired && current.MintedSHA256 == policy.MintedSHA256 && current.ReportedState == "applied" && current.ReportedSHA256 == current.MintedSHA256 && current.AppliedSHA256 == current.MintedSHA256 && current.AppliedRuleCount == 1 && !current.FallbackExhausted
		})
		traffic(test, false)
		test.Logf("native panel application: reupgrade=%d", time.Since(after).Milliseconds())
		nativeBindLiveCore(test, f.parent, f.dataDir, "xray")()
		agent, err := a.repos.NodeAgent.GetByAgentID(test.Context(), f.agentID)
		nativeMust(test, err, "read reupgraded agent identity")
		user, err := a.repos.User.GetByID(test.Context(), f.userID)
		nativeMust(test, err, "read reupgraded principal")
		panelNow, err := os.Stat(f.panelDB)
		nativeMust(test, err, "read reupgraded panel database")
		nodeNow, err := os.Stat(filepath.Join(f.dataDir, "state.db"))
		nativeMust(test, err, "read reupgraded node database")
		if !os.SameFile(panelFile, panelNow) || !os.SameFile(nodeFile, nodeNow) || agent.Epoch != originalAgent.Epoch || agent.CredentialSHA256 != originalAgent.CredentialSHA256 || user.UUID != originalUser.UUID {
			test.Fatal("panel transition replaced an original database or identity")
		}
		token := destinationRefreshAdminToken(test, a)
		r, err := http.NewRequestWithContext(test.Context(), "GET", f.adminURL+"/api/admin/dest/status", nil)
		nativeMust(test, err, "create PSP reupgrade coverage request")
		r.Header.Set("Authorization", "Bearer "+token)
		response, err := f.adminClient.Do(r)
		nativeMust(test, err, "read PSP reupgrade coverage HTTP")
		defer response.Body.Close()
		var status destpolicy.DestinationStatus
		if response.StatusCode != 200 {
			test.Fatal("PSP reupgrade coverage did not confirm current execution")
		}
		nativeMust(test, json.NewDecoder(response.Body).Decode(&status), "decode PSP reupgrade coverage")
		found := false
		for _, row := range status.Nodes {
			if row.PanelID == f.panelID {
				found = row.Supports.Policy && row.State == "applied" && row.AppliedRules == 1 && row.PendingSince == nil
			}
		}
		if !found || status.PublishedGeneration != state.PublishedGeneration {
			test.Fatal("PSP reupgrade coverage did not confirm current execution")
		}
		f.resumePeer(a.server.Handler)
	})
}

func nativePanelTransitionDiagnostics(t *testing.T, probe *nativeLegacyProbe, sequence uint64, digest string, exited func() bool) {
	if !t.Failed() {
		return
	}
	s := probe.snapshot()
	for _, fact := range []struct {
		name  string
		value bool
	}{
		{"streams", nativePanelStreamsApplied(s, sequence)}, {"capability", s.PolicyCapable}, {"config_known", s.ConfigBodyKnown}, {"config_policy", s.ConfigPolicy},
		{"report_applied", s.PolicyReportPresent && s.PolicyReportState == "applied"}, {"report_empty", s.PolicyReportDigest == ""}, {"report_current", s.PolicyReportDigest == digest}, {"original_core_exited", exited()},
	} {
		t.Logf("native diagnostic: panel_%s=%t", fact.name, fact.value)
	}
}

func nativePanelStreamsApplied(s nativeLegacySnapshot, sequence uint64) bool {
	return s.Sequence >= sequence+3 && s.CoreState == "running" && s.CoreEngine == "xray" && s.CoreVersion == "26.6.27" && s.ListenerApplied && s.Config.ETag != "" && s.HaveConfig.ETag == s.Config.ETag && s.HaveConfig.Applied == s.Config.Version && s.Roster.ETag != "" && s.HaveRoster.ETag == s.Roster.ETag && s.HaveRoster.Applied == s.Roster.Version
}

func nativePanelHoldCoreExit(t *testing.T, parent int, dataDir string) func() bool {
	t.Helper()
	nativeBindLiveCore(t, parent, dataDir, "xray")()
	for _, child := range nativeCoreChildren(parent) {
		if !nativeCoreHasParent(child, parent) || !nativeOwnedLiveCoreCommand(nativeCoreArgs(child), "xray", filepath.Join(dataDir, "runtime", "xray")) {
			continue
		}
		fd, err := unix.PidfdOpen(child, 0)
		nativeMust(t, err, "hold original core for panel transition")
		t.Cleanup(func() { _ = unix.Close(fd) })
		return func() bool {
			poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			n, err := unix.Poll(poll, 0)
			return err == nil && n == 1 && poll[0].Revents&unix.POLLIN != 0
		}
	}
	t.Fatal("panel transition cannot bind its original core")
	return nil
}

// Hash every column and row in a read-only, consistent SQLite snapshot. Sort
// encoded rows rather than assuming row order or a particular primary key.
func nativePanelDestinationSnapshot(t *testing.T, path string) map[string][32]byte {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	nativeMust(t, err, "open destination tables read-only")
	defer db.Close()
	tx, err := db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	nativeMust(t, err, "bind destination snapshot")
	defer tx.Rollback()
	rows, err := tx.QueryContext(t.Context(), "SELECT name, sql FROM sqlite_master WHERE type='table' AND name GLOB 'dest_*' ORDER BY name")
	nativeMust(t, err, "read complete destination table schema")
	tables := make(map[string]string)
	for rows.Next() {
		var name, schema string
		nativeMust(t, rows.Scan(&name, &schema), "read destination table name")
		if !regexp.MustCompile(`^dest_[a-z_]+$`).MatchString(name) || len(tables) >= 64 {
			t.Fatal("invalid owned destination snapshot table")
		}
		tables[name] = schema
	}
	nativeMust(t, rows.Err(), "complete destination table schema")
	nativeMust(t, rows.Close(), "close destination table schema")
	result := make(map[string][32]byte, len(tables))
	for name, schema := range tables {
		rows, err := tx.QueryContext(t.Context(), `SELECT * FROM "`+name+`"`)
		nativeMust(t, err, "read unchanged destination table")
		columns, err := rows.Columns()
		nativeMust(t, err, "read destination table columns")
		encoded := make([]string, 0)
		for rows.Next() {
			values, pointers := make([]any, len(columns)), make([]any, len(columns))
			for i := range pointers {
				pointers[i] = &values[i]
			}
			nativeMust(t, rows.Scan(pointers...), "read destination table row")
			data, err := json.Marshal(values)
			nativeMust(t, err, "encode private destination snapshot row")
			if len(encoded) >= 10000 {
				t.Fatal("owned destination snapshot exceeded its bound")
			}
			encoded = append(encoded, string(data))
		}
		nativeMust(t, rows.Err(), "complete destination table rows")
		nativeMust(t, rows.Close(), "close destination table rows")
		sort.Strings(encoded)
		data, err := json.Marshal([]any{schema, columns, encoded})
		nativeMust(t, err, "encode private complete destination snapshot")
		result[name] = sha256.Sum256(data)
	}
	nativeMust(t, tx.Commit(), "finish read-only destination snapshot")
	return result
}
