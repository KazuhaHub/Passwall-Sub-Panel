//go:build destination_acceptance && linux

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type nativeGlobalPeer struct {
	agent   *domain.NodeAgent
	node    *domain.Node
	secret  string
	dir     string
	process *exec.Cmd
	output  *nativePrivateOutput
	live    func()
	probe   *nativeMixedProbe
	sync    nativeGlobalSyncObservation
}

type nativeGlobalCohortOptions struct {
	decorate     func(ports.DestAuditStore) ports.DestAuditStore
	auditMetrics bool
	transport    func(http.Handler, []*nativeGlobalPeer) http.Handler
}

type nativeGlobalCohort struct {
	fixture     destinationPolicyFixture
	peers       []*nativeGlobalPeer
	ready       func(*nativeGlobalPeer) bool
	readyReason func(*nativeGlobalPeer) string
	server      *httptest.Server
}

// Bootstrap prerequisite only. It proves 65 independently authenticated real
// daemons/cores and their membership/streams; it does not claim queue overload.
func TestNativeDestinationGlobalCohortBootstrap(t *testing.T) {
	_ = nativeBootstrapGlobalCohort(t, nativeGlobalCohortOptions{})
}

// Keep one actual provisioning/cache/membership path for the subsequent global
// pressure driver. Every returned process, stream and private state has already
// passed the original bootstrap checks, and all cleanup remains owned by t.
func nativeBootstrapGlobalCohort(t *testing.T, options nativeGlobalCohortOptions) *nativeGlobalCohort {
	t.Helper()
	binary, evidence := os.Getenv("PSP_NATIVE_NODE_BIN"), os.Getenv("PSP_NATIVE_LOAD_METRICS")
	root, e1 := filepath.EvalSymlinks(os.Getenv("RUNNER_TEMP"))
	dir, e2 := filepath.EvalSymlinks(filepath.Dir(evidence))
	info, err := os.Stat(binary)
	if e1 != nil || e2 != nil || root != dir || filepath.Base(evidence) != "destination-global-bootstrap-metrics.json" || !filepath.IsAbs(binary) || filepath.Base(binary) != "destination-node" || err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || os.Getenv("PSP_NATIVE_CORE_VERSION") != "26.6.27" {
		t.Fatal("global bootstrap requires the immutable hosted candidate and owned evidence")
	}
	f := buildNativeDestinationPolicyFixtureWithAuditStore(t, options.decorate)
	a := f.a
	gate, err := newNativeGlobalAuditStart(a.destAudit)
	nativeMust(t, err, "bind actual global audit runner")
	owners, valid := nativeGlobalOwners(gate.Pressure())
	if !valid || owners != 65 {
		t.Fatal("global bootstrap cannot cover the reserved owner limits")
	}
	m := struct {
		Schema       int    `json:"schema_version"`
		Candidate    string `json:"candidate"`
		Core         string `json:"core_version"`
		Database     string `json:"database"`
		Owners       int    `json:"requested_owners"`
		Stage        int    `json:"bootstrap_stage"`
		Started      int    `json:"started_native_processes"`
		Parents      int    `json:"unique_live_parents"`
		Cores        int    `json:"unique_live_cores"`
		States       int    `json:"independent_fresh_states"`
		Copies       int    `json:"public_cache_copies"`
		Streams      int    `json:"complete_stream_pairs"`
		Controls     int    `json:"nonce_controls"`
		SharedUsers  int    `json:"shared_users"`
		PressureDone int    `json:"pressure_complete"`
	}{Schema: 1, Candidate: "96693d3c128d82f0d154774f593c497a276a1105", Core: "26.6.27", Database: a.cfg.DBKind(), Owners: owners, SharedUsers: 1}
	m.Stage = 1
	t.Cleanup(func() {
		body, err := json.Marshal(m)
		nativeMust(t, err, "encode numeric global bootstrap evidence")
		nativeMust(t, os.WriteFile(evidence, append(body, '\n'), 0644), "save numeric global bootstrap evidence")
	})
	settings, err := a.settings.Load(t.Context(), ports.UISettings{})
	nativeMust(t, err, "load actual global cadence")
	settings.NodePollSeconds, settings.FullReportSeconds = 2, 0
	nativeMust(t, a.settings.Save(t.Context(), settings), "set owned global fixture cadence")
	portsUsed := make(map[int]bool)
	port := func() int {
		for {
			value := nativePort(t)
			if !portsUsed[value] {
				portsUsed[value] = true
				return value
			}
		}
	}
	peers := make([]*nativeGlobalPeer, owners)
	for i := range peers {
		peer := &nativeGlobalPeer{agent: f.agent, node: f.node, secret: f.credential, dir: t.TempDir(), output: &nativePrivateOutput{}}
		if i != 0 {
			peer.secret = "fixture-global-" + strconv.Itoa(i) + "-credential-with-enough-entropy"
			digest := sha256.Sum256([]byte(peer.secret))
			peer.agent = &domain.NodeAgent{AgentID: "agt_global_" + strconv.Itoa(i), Epoch: 1, CredentialSHA256: hex.EncodeToString(digest[:]), DesiredCoreEngine: domain.NodeCoreXray, DesiredCoreVersion: "26.6.27"}
			panel := &domain.Panel{Name: "Owned global peer " + strconv.Itoa(i), Kind: domain.PanelKindPSP, URL: "psp://" + peer.agent.AgentID}
			nativeMust(t, a.repos.NativeAgentProvisioning.Create(t.Context(), panel, peer.agent), "provision independent global owner")
			nativeMust(t, a.xuiPool.Add(panel), "register production global native adapter")
			peer.node = &domain.Node{PanelID: panel.ID, InboundID: 1, DisplayName: panel.Name, ServerAddress: "global.example.test", DesiredProtocol: "vless", ObservedProtocol: "vless", InboundListen: "127.0.0.1", InboundSettings: `{"decryption":"none"}`, StreamSettings: `{"network":"tcp","security":"none"}`, Sniffing: `{}`, Allocate: `{}`, Enabled: true, ConfigSyncState: domain.ConfigSyncPending}
			peer.node.DesiredPort = port()
			peer.node.ObservedPort = peer.node.DesiredPort
			nativeMust(t, a.repos.Node.Create(t.Context(), peer.node), "create actual global inbound")
		} else {
			peer.node.DesiredPort, peer.node.ObservedPort, peer.node.InboundListen = port(), 0, "127.0.0.1"
			peer.node.ObservedPort = peer.node.DesiredPort
			nativeMust(t, a.repos.Node.Update(t.Context(), peer.node), "configure first actual global inbound")
		}
		if options.auditMetrics {
			peer.probe = newNativeMixedProbe(peer.agent.AgentID)
		}
		peers[i] = peer
	}
	m.Stage = 2
	a.destAudit = gate
	a.server.Addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port()))
	runDone := make(chan error, 1)
	go func() { runDone <- a.Run() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		nativeMust(t, a.Shutdown(ctx), "drain global production application")
		select {
		case err := <-runDone:
			if err != nil && err != http.ErrServerClosed {
				t.Error("global production application failed")
			}
		case <-ctx.Done():
			t.Error("global production application did not stop")
		}
	})
	t.Cleanup(gate.release) // after shutdown registration: release first on failure
	nativeWait(t, 30*time.Second, "actual global production health", func() bool {
		response, err := (&http.Client{Timeout: time.Second}).Get("http://" + a.server.Addr + "/health")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == 200
	})
	m.Stage = 3
	handler := a.server.Handler
	if options.transport != nil {
		handler = options.transport(handler, peers)
	}
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	nativeMust(t, os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600), "write private scoped global CA")
	readyReason := func(peer *nativeGlobalPeer) string {
		agent, e1 := a.repos.NodeAgent.GetByAgentID(t.Context(), peer.agent.AgentID)
		cfg, e2 := a.repos.NodeAgent.GetStream(t.Context(), peer.agent.AgentID, domain.NodeAgentStreamConfig)
		roster, e3 := a.repos.NodeAgent.GetStream(t.Context(), peer.agent.AgentID, domain.NodeAgentStreamRoster)
		panel, panelErr := a.repos.XUIPanel.GetByID(t.Context(), peer.agent.PanelID)
		if e1 != nil || e2 != nil || e3 != nil || panelErr != nil || panel == nil || agent == nil || cfg == nil || roster == nil {
			return "store"
		}
		if panel.XrayVersion != "26.6.27" || agent.Epoch != peer.agent.Epoch || agent.AllowRestrictedReality || agent.ObservedCoreEngine != domain.NodeCoreXray || agent.DesiredCoreEngine != domain.NodeCoreXray || agent.DesiredCoreVersion != "26.6.27" || agent.LastSeen == nil || !slicesContainsNative(agent.ObservedCapabilities, protocol.CapabilityDestinationPolicy) {
			return "metadata"
		}
		if !cfg.Converged() || cfg.AppliedEpoch != agent.Epoch {
			return "config"
		}
		if !roster.Converged() || roster.AppliedEpoch != agent.Epoch {
			return "roster"
		}
		adapter, err := a.xuiPool.Get(peer.agent.PanelID)
		if err != nil {
			return "adapter"
		}
		status, e4 := adapter.GetServerStatus(t.Context())
		inbound, e5 := adapter.GetInbound(t.Context(), peer.node.InboundID)
		if errors.Is(e4, ports.ErrNativePanelSnapshotMissing) || errors.Is(e5, ports.ErrNativePanelSnapshotMissing) {
			return "inbound_missing"
		}
		if errors.Is(e4, ports.ErrNativePanelAgentOffline) || errors.Is(e5, ports.ErrNativePanelAgentOffline) {
			return "inbound_offline"
		}
		if errors.Is(e4, ports.ErrNativePanelSnapshotStale) || errors.Is(e5, ports.ErrNativePanelSnapshotStale) {
			return "inbound_stale"
		}
		if errors.Is(e5, domain.ErrNotFound) {
			return "inbound_absent"
		}
		if e4 != nil || e5 != nil {
			return "inbound_read"
		}
		if status == nil || inbound == nil {
			return "inbound_absent"
		}
		if status.CoreEngine != "xray" || status.XrayVersion != "26.6.27" || status.XrayState != "running" {
			return "inbound_runtime"
		}
		if !nativeTrialProvisioningReady(status, inbound, peer.node.InboundID, peer.node.DesiredPort) {
			return "inbound_shape"
		}
		return ""
	}
	ready := func(peer *nativeGlobalPeer) bool { return readyReason(peer) == "" }
	for i, peer := range peers {
		dataDir := filepath.Join(peer.dir, "node")
		if i != 0 {
			// Provisioning can legitimately replace the initial empty-roster
			// core. Public cache reuse needs a current verified donor, not an
			// idle-stability claim across deployment. Final PID binding remains
			// below, after real member traffic and fresh paired receipts.
			nativeWait(t, 180*time.Second, "current actual donor core for public cache reuse", func() bool {
				return ready(peers[0]) && nativeGlobalCurrentPublicCore(peers[0].process.Process.Pid, filepath.Join(peers[0].dir, "node"))
			})
			nativeMust(t, nativeCopyPublicCoreCache(filepath.Join(peers[0].dir, "node"), dataDir), "reuse only the first real verified public core cache")
			m.Copies++
		}
		if _, err := os.Lstat(filepath.Join(dataDir, "state.db")); !os.IsNotExist(err) {
			t.Fatal("global peer borrowed existing private state")
		}
		key := filepath.Join(peer.dir, "credential")
		nativeMust(t, os.WriteFile(key, []byte(peer.secret), 0600), "write independent private global credential")
		peer.process = exec.Command(binary, "--endpoint", server.URL+"/v1/node/sync", "--agent-id", peer.agent.AgentID, "--credential-file", key, "--data-dir", dataDir, "--xray-api-listen", net.JoinHostPort("127.0.0.1", strconv.Itoa(port())))
		peer.process.Env = append(os.Environ(), "SSL_CERT_FILE="+ca)
		peer.process.Stdout, peer.process.Stderr = peer.output, peer.output
		if options.auditMetrics {
			peer.process.Args = append(peer.process.Args, "--audit-metrics")
			reading := &nativeMixedOutput{private: peer.output, probe: peer.probe}
			peer.process.Stdout, peer.process.Stderr = reading, reading
		}
		nativeMust(t, peer.process.Start(), "start independent real global daemon")
		m.Started++
		done := make(chan error, 1)
		go func(command *exec.Cmd) { done <- command.Wait() }(peer.process)
		t.Cleanup(func() {
			_ = peer.process.Process.Signal(syscall.SIGTERM)
			select {
			case err := <-done:
				nativeMust(t, err, "stop global native daemon")
			case <-time.After(15 * time.Second):
				_ = peer.process.Process.Kill()
				<-done
				t.Error("global daemon did not drain")
			}
			for _, private := range []string{peer.secret, f.user.UUID, f.user.Email, f.user.SubToken, " accepted ", " rejected "} {
				if strings.Contains(peer.output.text(), private) {
					t.Error("global daemon exposed private data")
				}
			}
		})
		if i == 0 {
			nativeWait(t, 180*time.Second, "first real core for audited public cache reuse", func() bool { return ready(peer) })
			m.Stage = 4
		}
	}
	m.Stage = 5
	nativeWait(t, 180*time.Second, "all independent real global cores and streams", func() bool {
		for _, peer := range peers {
			if !ready(peer) {
				return false
			}
		}
		return true
	})
	m.Stage = 6
	memberAfter := time.UnixMilli(time.Now().UnixMilli())
	nativeMust(t, a.user.ResyncMembershipOrEnqueue(t.Context(), f.user.ID, "global cohort membership"), "reconcile the real shared principal through production membership")
	m.Stage = 7
	_, address, targetPort, nonce, _ := nativeTarget(t)
	t.Cleanup(func() {
		for _, peer := range peers {
			if strings.Contains(peer.output.text(), nonce) {
				t.Error("global control nonce escaped into daemon output")
			}
		}
	})
	parentIDs, coreIDs := make(map[int]bool), make(map[int]bool)
	var states []os.FileInfo
	for _, peer := range peers {
		nativeWait(t, 120*time.Second, "actual global member traffic and fresh paired receipts", func() bool {
			cfg, e1 := a.repos.NodeAgent.GetStream(t.Context(), peer.agent.AgentID, domain.NodeAgentStreamConfig)
			roster, e2 := a.repos.NodeAgent.GetStream(t.Context(), peer.agent.AgentID, domain.NodeAgentStreamRoster)
			return e1 == nil && e2 == nil && cfg != nil && roster != nil && cfg.LastSeen != nil && roster.LastSeen != nil && cfg.LastSeen.After(memberAfter) && roster.LastSeen.After(memberAfter) && ready(peer) && nativeVLESSHost(t.Context(), peer.node.DesiredPort, address, targetPort, f.user.UUID, nonce, "global-control.example.test") == nil
		})
		// Membership deployment may legitimately replace the empty-roster core.
		// Bind the final real process only after that deployment serves traffic.
		peer.live = nativeBindLiveCore(t, peer.process.Process.Pid, filepath.Join(peer.dir, "node"), "xray")
		peer.live()
		parent := peer.process.Process.Pid
		if parentIDs[parent] {
			t.Fatal("global cohort reused a parent process")
		}
		parentIDs[parent] = true
		m.Parents++
		core := 0
		for _, pid := range nativeCoreChildren(parent) {
			if nativeCoreHasParent(pid, parent) && nativeOwnedLiveCoreCommand(nativeCoreArgs(pid), "xray", filepath.Join(peer.dir, "node", "runtime", "xray")) {
				if core != 0 || coreIDs[pid] {
					t.Fatal("global cohort reused or duplicated a core process")
				}
				core = pid
			}
		}
		if core == 0 {
			t.Fatal("global cohort lacks its original actual core")
		}
		coreIDs[core] = true
		m.Cores++
		state, err := os.Stat(filepath.Join(peer.dir, "node", "state.db"))
		nativeMust(t, err, "bind independently created global SQLite state")
		if !state.Mode().IsRegular() {
			t.Fatal("global cohort state is not a regular file")
		}
		for _, previous := range states {
			if os.SameFile(previous, state) {
				t.Fatal("global cohort shares private SQLite state")
			}
		}
		states = append(states, state)
		m.States++
		m.Streams++
		m.Controls++
	}
	m.Stage = 8
	gate.release()
	for _, peer := range peers {
		peer.live()
	}
	m.Stage = 9
	return &nativeGlobalCohort{fixture: f, peers: peers, ready: ready, readyReason: readyReason, server: server}
}

func nativeGlobalCurrentPublicCore(parent int, dataDir string) bool {
	// Read-only point-in-time donor proof. No signals, process replacement,
	// synthetic version/metadata or relaxed final-cohort identity checks.
	expected := filepath.Join(dataDir, "cores", "xray", "versions", "26.6.27", "xray")
	file, err := os.Stat(expected)
	if err != nil || !file.Mode().IsRegular() {
		return false
	}
	count := 0
	for _, pid := range nativeCoreChildren(parent) {
		args := nativeCoreArgs(pid)
		if len(args) == 0 || args[0] != expected || !nativeCoreHasParent(pid, parent) || !nativeOwnedLiveCoreCommand(args, "xray", filepath.Join(dataDir, "runtime", "xray")) {
			continue
		}
		live, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
		if err == nil && os.SameFile(file, live) {
			count++
		}
	}
	return count == 1
}
