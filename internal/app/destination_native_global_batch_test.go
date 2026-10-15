//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destaudit"
)

type nativeGlobalBatchOwner struct {
	Ordinal   int                    `json:"owner"`
	Delivered [4]uint64              `json:"delivered"`
	Controls  uint64                 `json:"control_successes"`
	Frozen    [4]uint64              `json:"frozen_transmissions"`
	Retried   [4]uint64              `json:"verified_canonical_retries"`
	Ingress   nativeMixedIngress     `json:"access_ingress"`
	Node      nativeMixedNodeReading `json:"final_node"`
	Ledgers   []nativeMixedLedger    `json:"ledgers"`
}

type nativeGlobalBatchMetrics struct {
	Schema         int                       `json:"schema_version"`
	Candidate      string                    `json:"candidate"`
	Core           string                    `json:"core_version"`
	Database       string                    `json:"database"`
	Stage          int                       `json:"stage"`
	Owners         int                       `json:"native_owners"`
	Users          int                       `json:"shared_trial_users"`
	UserDeliveries []uint64                  `json:"trial_user_deliveries"`
	TrafficMS      int64                     `json:"traffic_ms"`
	ObservationMS  int64                     `json:"observation_ms"`
	Failures       uint64                    `json:"transport_failures"`
	QueueReads     uint64                    `json:"queue_reads"`
	Violations     uint64                    `json:"queue_bound_violations"`
	Saturation     uint64                    `json:"simultaneous_batch_saturation_samples"`
	PeakBatches    int                       `json:"max_total_batches"`
	PeakBytes      int                       `json:"max_total_canonical_bytes"`
	Peaks          [4]destaudit.LanePressure `json:"queue_peaks"`
	SQLHeld        uint64                    `json:"held_sql_entries"`
	SQLEarly       uint64                    `json:"delegated_begins_while_held"`
	Details        []nativeGlobalBatchOwner  `json:"owners"`
	PSP            []nativeMixedPSPSample    `json:"psp_samples"`
	Histograms     []nativeLatencyHistogram  `json:"histograms"`
	BatchDone      int                       `json:"global_batch_complete"`
	BytesDone      int                       `json:"byte_saturation_complete"`
	KeysDone       int                       `json:"key_saturation_complete"`
	PressureDone   int                       `json:"pressure_complete"`
}

// Actual global batch saturation and retry/ledger component. Byte/key
// saturation and the complete mixed-pressure acceptance remain separate gates.
func TestNativeDestinationGlobalBatchPressure(t *testing.T) {
	profile := os.Getenv("PSP_NATIVE_SETUP_PROFILE")
	if profile != "" && profile != "0" && profile != "1" {
		t.Fatal("unknown setup profile selection")
	}
	profileStage := os.Getenv("PSP_NATIVE_SETUP_PROFILE_STAGE")
	if profileStage == "" {
		profileStage = "membership"
	}
	if profileStage != "membership" && profileStage != "receipt" {
		t.Fatal("unknown setup profile stage")
	}
	evidence := os.Getenv("PSP_NATIVE_GLOBAL_BATCH_METRICS")
	root, e1 := filepath.EvalSymlinks(os.Getenv("RUNNER_TEMP"))
	dir, e2 := filepath.EvalSymlinks(filepath.Dir(evidence))
	if e1 != nil || e2 != nil || root != dir || filepath.Base(evidence) != "destination-global-batch-metrics.json" {
		t.Fatal("global batch evidence must belong to the dedicated hosted job")
	}
	m := nativeGlobalBatchMetrics{Schema: 1, Candidate: "b35b29780406e4e7272c7b87f80ed2fe2d69bc42", Core: "26.6.27", Details: []nativeGlobalBatchOwner{}, PSP: []nativeMixedPSPSample{}, Histograms: []nativeLatencyHistogram{}}
	writeMetrics := func() {
		body, err := json.Marshal(m)
		nativeMust(t, err, "encode closed numeric global batch evidence")
		nativeMust(t, os.WriteFile(evidence, append(body, '\n'), 0644), "save closed numeric global batch evidence")
	}
	writeStageDiagnostics := func() {
		observed := metrics.Take()
		for _, counter := range observed.Counters {
			for _, phase := range []string{"catalog", "build", "compile", "repository", "capability", "encode", "mint"} {
				for _, cause := range []string{"canceled", "deadline", "conflict", "validation", "unavailable", "other"} {
					name := phase + "_" + cause
					if counter.Name == "psp_node_control_config_error_total{reason="+name+"}" && counter.Value > 0 && counter.Value <= 1000000 {
						t.Logf("native global config error: %s=%d", name, counter.Value)
					}
				}
			}
		}
		for _, h := range observed.Histograms {
			for _, name := range []string{"desired", "ingest", "policy", "panel", "config", "roster", "directives", "tasks", "finish"} {
				if h.Name != "psp_node_control_sync_stage_ms{stage="+name+"}" || h.Count <= 0 {
					continue
				}
				t.Logf("native global stage: %s_count=%d", name, h.Count)
				for field, value := range map[string]float64{"mean_ms": h.Mean, "p95_ms": h.P95, "max_ms": h.Max} {
					if !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1200000 {
						t.Logf("native global stage: %s_%s=%d", name, field, int64(math.Ceil(value)))
					}
				}
			}
		}
	}
	t.Cleanup(writeMetrics)
	var storage *nativeGlobalAuditStorage
	cohort := nativeBootstrapGlobalCohort(t, nativeGlobalCohortOptions{
		decorate: func(original ports.DestAuditStore) ports.DestAuditStore {
			storage = &nativeGlobalAuditStorage{DestAuditStore: original}
			return storage
		},
		auditMetrics: true,
		transport: func(original http.Handler, peers []*nativeGlobalPeer) http.Handler {
			elapsed := metrics.NewHistogram("fixture_destination_sync_handler_ms", "Owned global batch handler completion; excludes network transit and partition.", "ms", metrics.LatencyBucketsMS)
			timed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/node/sync" {
					began := time.Now()
					defer elapsed.ObserveSince(began)
				}
				original.ServeHTTP(w, r)
			})
			metrics.PollStageDuration.With("sink_flush")
			for _, kind := range nativeMixedKinds {
				metrics.DestAuditBeginMS.With(kind)
				metrics.DestAuditChunkMS.With(kind)
			}
			handler, err := nativeGlobalMixedTransport(timed, peers)
			nativeMust(t, err, "bind actual global transport identities")
			return handler
		},
	})
	f, a := cohort.fixture, cohort.fixture.a
	m.Database, m.Owners, m.Stage = a.cfg.DBKind(), len(cohort.peers), 1
	if m.Owners != 65 || storage == nil {
		t.Fatal("global batch traffic lacks its actual checked owner cohort")
	}
	// Persist only closed numeric setup milestones before sampling starts. A
	// whole-test timeout may skip cleanup; it must not erase which real phase
	// was reached. Missing completion/root PASS still fails the normal verifier.
	writeMetrics()
	t.Log("native setup stage: administrator")
	token := destinationRefreshAdminToken(t, a)
	t.Log("native setup stage: allowed_target")
	_, address, targetPort, nonce, _ := nativeTarget(t)
	t.Log("native setup stage: observe_target")
	_, observeAddress, observePort, observeNonce, _ := nativeTargetAtPort(t, nativePort(t))
	t.Log("native setup stage: control_target")
	_, controlAddress, controlPort, controlNonce, _ := nativeTargetAtPort(t, nativePort(t))
	stage := "capability"
	var setupCPU func()
	if profile == "1" {
		setupCPU = nativeSetupCPUSampler(t, cohort.peers)
		t.Cleanup(func() {
			if m.Stage <= 2 {
				setupCPU()
			}
		})
	}
	users, group, policy, blockAddress, blockPort, blockReached := nativePrepareTrialLoad(t, f, cohort.server.Client(), cohort.server.URL, token, f.node.DesiredPort, nativeGlobalBatchOutput(cohort.peers), &stage, &nativeMixedSetup{observePort: observePort, memberConcurrency: 8, memberReady: func(completed int) {
		m.Users = completed
		writeMetrics()
		if completed%50 == 0 {
			writeStageDiagnostics()
			if setupCPU != nil {
				setupCPU()
			}
		}
		if completed == 50 && profile == "1" && profileStage == "membership" {
			t.Cleanup(nativeStartSetupProfile(t, root, 60*time.Second).stop)
		}
	}})
	m.Users = len(users)
	if m.Users != 200 || targetPort == blockPort || targetPort == observePort || controlPort == blockPort || controlPort == observePort || controlPort == targetPort {
		t.Fatal("global batch principals or lane targets are not independent")
	}
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
	m.Stage = 2
	writeMetrics()
	readiness := "state"
	var waitingPeer *nativeGlobalPeer
	t.Cleanup(func() {
		if m.Stage == 2 {
			writeStageDiagnostics()
			t.Log("global batch readiness: " + readiness)
			if waitingPeer != nil {
				for name, value := range waitingPeer.sync.facts() {
					t.Logf("native global sync: %s=%d", name, value)
				}
				alive := waitingPeer.process != nil && waitingPeer.process.Process != nil && waitingPeer.process.Process.Signal(syscall.Signal(0)) == nil
				value := 0
				if alive {
					value = 1
				}
				t.Logf("native global sync: parent_alive=%d", value)
			}
		}
	})
	var stopReceiptProfile func()
	if profile == "1" && profileStage == "receipt" {
		stopReceiptProfile = nativeStartSetupProfile(t, root, 60*time.Second).stop
		t.Cleanup(stopReceiptProfile)
	}
	nativeWait(t, 240*time.Second, "all real global trial policies and 200-user paired rosters", func() bool {
		readiness = "state"
		state, err := a.destDefinitions.State(t.Context())
		if err != nil || state.Paused {
			return false
		}
		for i, peer := range cohort.peers {
			waitingPeer = peer
			p, e1 := a.repos.DestAgentPolicy.Get(t.Context(), peer.agent.AgentID, true)
			agent, e2 := a.repos.NodeAgent.GetByAgentID(t.Context(), peer.agent.AgentID)
			panel, e3 := a.repos.XUIPanel.GetByID(t.Context(), peer.agent.PanelID)
			roster, e4 := a.repos.NodeAgent.GetStream(t.Context(), peer.agent.AgentID, domain.NodeAgentStreamRoster)
			readiness = "store"
			if e1 != nil || e2 != nil || e3 != nil || e4 != nil || p == nil || agent == nil || panel == nil || roster == nil {
				return false
			}
			readiness = "capability"
			if !slicesContainsNative(agent.ObservedCapabilities, protocol.CapabilityAuditUsage) {
				return false
			}
			readiness = "streams"
			if reason := cohort.readyReason(peer); reason != "" {
				readiness = "streams_" + reason
				return false
			}
			readiness = "receipt"
			if !nativeMutationReceiptMatches(prior[i], *p, state.PublishedGeneration, agent.LastSeen, afterMutation) {
				return false
			}
			readiness = "collection"
			if !nativeCollectionReceiptMatches(*p, protocol.CollectLevel("hits_and_usage"), uint64(panel.AuditCollectRevision)) {
				return false
			}
			var body protocol.DestinationPolicy
			var members protocol.RosterBody
			readiness = "shape"
			if json.Unmarshal(p.MintedBody, &body) != nil || json.Unmarshal(roster.DesiredBody, &members) != nil || len(body.Rules) != 3 {
				return false
			}
			trial := false
			for _, rule := range body.Rules {
				if rule.ID == fmt.Sprintf("g%d", group.ID) && rule.CatchAll && rule.Action == protocol.RuleObserve && len(rule.Subjects) == 200 {
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
				return false
			}
			readiness = "roster"
			if !seen[protocol.NewSubjectKey(f.user.ID)] {
				return false
			}
			for _, user := range users {
				if !seen[protocol.NewSubjectKey(user.ID)] {
					return false
				}
			}
		}
		readiness = "complete"
		return true
	})
	if stopReceiptProfile != nil {
		stopReceiptProfile()
	}
	const seconds, rate = 120, 10
	name := nativeGlobalBatchHosts(t, nonce, address, blockAddress, observeAddress, controlAddress, seconds, rate)
	for _, peer := range cohort.peers {
		nativeMust(t, nativeVLESSHost(t.Context(), peer.node.DesiredPort, name(5, 0), targetPort, f.user.UUID, nonce, name(5, 0)), "verify actual global allowed domain traffic")
		peer.live = nativeBindLiveCore(t, peer.process.Process.Pid, filepath.Join(peer.dir, "node"), "xray")
	}
	queue, ok := a.destAudit.(interface {
		Pressure() [4]destaudit.LanePressure
	})
	if !ok {
		t.Fatal("global pressure lost the real queue observer")
	}
	baseline := make([]nativeMixedAccess, len(cohort.peers))
	usageBaseline := make([]int64, len(cohort.peers))
	sql := func(query string) string { return nativeDestinationCountSQL(m.Database, query) }
	nativeWait(t, 150*time.Second, "global warmup fully drained before holding original SQL", func() bool {
		for i, peer := range cohort.peers {
			peer.probe.mu.Lock()
			reading, invalid := peer.probe.node, peer.probe.invalid
			peer.probe.mu.Unlock()
			if invalid || len(reading.Lanes) != 4 || !reading.Access.quiescent() {
				return false
			}
			for _, lane := range reading.Lanes {
				if lane.Keys != 0 || lane.Pending != 0 || lane.Dropped != 0 || lane.Unmatched != 0 {
					return false
				}
			}
			if a.database.QueryRowContext(t.Context(), sql("SELECT COALESCE(SUM(count),0) FROM dest_usage_hourly WHERE panel_id=?"), peer.agent.PanelID).Scan(&usageBaseline[i]) != nil || usageBaseline[i] != 1 {
				return false
			}
			baseline[i] = reading.Access
		}
		for _, lane := range queue.Pressure() {
			if lane.Batches != 0 {
				return false
			}
		}
		return true
	})
	m.Stage = 3
	writeMetrics()
	names := append([]string(nil), nativeTrialLatencyNames[:4]...)
	for _, kind := range nativeMixedKinds {
		names = append(names, "psp_dest_audit_begin_ms{kind="+kind+"}", "psp_dest_audit_chunk_ms{kind="+kind+"}")
	}
	beforeLatency := nativeLatencySnapshot(t, names)
	pause, err := storage.pause()
	nativeMust(t, err, "hold original SQL after real warmup")
	t.Cleanup(func() { storage.resume(pause) })
	beforeBegins, beforeHeld := storage.begins.Load(), storage.blocked.Load()
	start := time.Now()
	for _, peer := range cohort.peers {
		peer.probe.mu.Lock()
		peer.probe.start = start
		peer.probe.mu.Unlock()
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var delivered [65][4]atomic.Uint64
	var controls [65]atomic.Uint64
	var userDeliveries [200]atomic.Uint64
	var failures atomic.Uint64
	type job struct{ owner, kind, seq int }
	jobs := make(chan job, 4096)
	var workers sync.WaitGroup
	var closeJobs sync.Once
	for range 512 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := range jobs {
				peer := cohort.peers[j.owner]
				if j.kind == 0 {
					header, err := nativePressureBlockRequest(f.user.UUID, name(0, j.seq), blockPort)
					if err != nil || !nativeLoadBlockedRequest(ctx, peer.node.DesiredPort, header, func() {}) {
						failures.Add(1)
						continue
					}
				} else {
					port, uuid, want := targetPort, f.user.UUID, nonce
					if j.kind == 1 {
						port, want = observePort, observeNonce
					}
					if j.kind == 2 {
						uuid = users[j.seq%200].UUID
					}
					if j.kind == 4 {
						port, want = controlPort, controlNonce
					}
					host := name(j.kind, j.seq)
					if nativeVLESSHost(ctx, peer.node.DesiredPort, host, port, uuid, want, host) != nil {
						failures.Add(1)
						continue
					}
				}
				if j.kind == 4 {
					controls[j.owner].Add(1)
				} else {
					delivered[j.owner][j.kind].Add(1)
					if j.kind == 2 {
						userDeliveries[j.seq%200].Add(1)
					}
				}
			}
		}()
	}
	t.Cleanup(func() { cancel(); closeJobs.Do(func() { close(jobs) }); workers.Wait() })
	observationsDone := make(chan struct{})
	go func() {
		defer close(observationsDone)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		lastMemory := int64(-1)
		for {
			view := queue.Pressure()
			m.QueueReads++
			total, size, saturated := 0, 0, true
			for i, lane := range view {
				if lane.Kind != nativeMixedKinds[i] || lane.BatchLimit != [4]int{128, 32, 32, 64}[i] || lane.ByteLimit != [4]int{32 << 20, 8 << 20, 8 << 20, 16 << 20}[i] || lane.OwnerLimit != [4]int{4, 1, 1, 1}[i] || lane.Batches != lane.Waiting+lane.Active || lane.Batches > lane.BatchLimit || lane.Bytes > lane.ByteLimit || lane.MaxOwner > lane.OwnerLimit || lane.Owners > 65 {
					m.Violations++
				}
				total += lane.Batches
				size += lane.Bytes
				saturated = saturated && lane.Batches == lane.BatchLimit
				if lane.Batches > m.Peaks[i].Batches || lane.Batches == m.Peaks[i].Batches && lane.Bytes > m.Peaks[i].Bytes {
					m.Peaks[i] = lane
				}
			}
			if total > 256 || size > 64<<20 {
				m.Violations++
			}
			if saturated {
				m.Saturation++
			}
			m.PeakBatches, m.PeakBytes = max(m.PeakBatches, total), max(m.PeakBytes, size)
			elapsed := time.Since(start).Milliseconds()
			if elapsed/1000 > lastMemory {
				lastMemory = elapsed / 1000
				var memory runtime.MemStats
				runtime.ReadMemStats(&memory)
				m.PSP = append(m.PSP, nativeMixedPSPSample{elapsed, memory.HeapAlloc, memory.HeapInuse, view})
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	t.Cleanup(func() { cancel(); <-observationsDone })
	// Spread the same deliveries across the original enqueue window instead of
	// a 2665-job FIFO burst once a second. Keep all 512 workers, the queue bound,
	// per-owner totals, first/final enqueue and strict 120s/130s traffic gates.
	const ticks = seconds * rate
	const lastSend = (seconds - 1) * time.Second
	for tick := 0; tick < ticks; tick++ {
		if delay := time.Until(start.Add(time.Duration(tick) * lastSend / (ticks - 1))); delay > 0 {
			time.Sleep(delay)
		}
		for owner, peer := range cohort.peers {
			if tick%rate == 0 {
				peer.live()
			}
			for kind := range 4 {
				select {
				case jobs <- job{owner, kind, tick}:
				default:
					failures.Add(1)
				}
			}
			if tick%rate == 0 {
				select {
				case jobs <- job{owner, 4, tick / rate}:
				default:
					failures.Add(1)
				}
			}
		}
	}
	if delay := time.Until(start.Add(seconds * time.Second)); delay > 0 {
		time.Sleep(delay)
	}
	closeJobs.Do(func() { close(jobs) })
	workers.Wait()
	m.TrafficMS, m.Failures = time.Since(start).Milliseconds(), failures.Load()
	m.SQLHeld, m.SQLEarly = storage.blocked.Load()-beforeHeld, storage.begins.Load()-beforeBegins
	storage.resume(pause)
	m.Stage = 4
	var detail []nativeGlobalBatchOwner
	drainReason, drainComplete := "state", false
	drainFacts := map[string]uint64{}
	t.Cleanup(func() {
		if t.Failed() && !drainComplete {
			t.Log("global batch drain: " + drainReason)
			for name, value := range drainFacts {
				t.Logf("native global drain: %s=%d", name, value)
			}
		}
	})
	nativeWait(t, 180*time.Second, "global real canonical retry, SQL drain and all owner loss ledgers", func() bool {
		detail = nil
		for owner, peer := range cohort.peers {
			peer.probe.mu.Lock()
			reading, invalid := peer.probe.node, peer.probe.invalid
			frozen, retried := peer.probe.frozen, peer.probe.recovered
			invalidCauses := peer.probe.invalidCauses
			samples := peer.probe.samples
			peer.probe.mu.Unlock()
			drainFacts = map[string]uint64{"owner": uint64(owner + 1), "samples": uint64(len(samples)), "traffic_ms": uint64(m.TrafficMS), "owners_finished": uint64(len(detail))}
			drainReason = "node_samples"
			drainFacts["node_lanes"], drainFacts["node_invalid_causes"] = uint64(len(reading.Lanes)), invalidCauses
			if invalid {
				drainFacts["node_invalid"] = 1
			} else {
				drainFacts["node_invalid"] = 0
			}
			if invalid || len(reading.Lanes) != 4 || len(samples) < 3 {
				return false
			}
			last := samples[len(samples)-3:]
			drainReason = "sample_window"
			drainFacts["sample_first_ms"], drainFacts["sample_last_ms"] = uint64(last[0].ElapsedMS), uint64(last[2].ElapsedMS)
			if last[0].ElapsedMS < m.TrafficMS || last[2].ElapsedMS-last[0].ElapsedMS < 1800 {
				return false
			}
			for _, sample := range last {
				drainReason = "access_quiescence"
				if sample.Reading.Access != reading.Access {
					return false
				}
			}
			row := nativeGlobalBatchOwner{Ordinal: owner, Controls: controls[owner].Load(), Frozen: frozen, Retried: retried, Node: reading, Ledgers: []nativeMixedLedger{}}
			for i := range 4 {
				row.Delivered[i] = delivered[owner][i].Load()
				drainReason = "frozen_retry"
				drainFacts["lane"], drainFacts["frozen"], drainFacts["retried"] = uint64(i+1), frozen[i], retried[i]
				if frozen[i] == 0 || retried[i] != 1 {
					return false
				}
			}
			allowed := row.Delivered[1] + row.Delivered[2] + row.Delivered[3] + row.Controls
			delete(drainFacts, "lane")
			delete(drainFacts, "frozen")
			delete(drainFacts, "retried")
			drainReason = "ingress_accounting"
			drainFacts["delivered_allowed"], drainFacts["delivered_block"] = allowed, row.Delivered[0]
			for _, sample := range []struct {
				prefix string
				access nativeMixedAccess
			}{{"before", baseline[owner]}, {"after", reading.Access}} {
				for name, value := range map[string]uint64{"private": sample.access.Private, "usage": sample.access.Usage, "denied": sample.access.Denied, "unmatched": sample.access.Unmatched, "dropped": sample.access.Dropped, "stale": sample.access.Stale, "pending_dropped": sample.access.PendingDropped, "pending_unmatched": sample.access.PendingUnmatched} {
					drainFacts[sample.prefix+"_"+name] = value
				}
				if sample.access.Queued >= 0 {
					drainFacts[sample.prefix+"_queued"] = uint64(sample.access.Queued)
				}
			}
			var valid bool
			row.Ingress, valid = nativeMixedIngressBetween(baseline[owner], reading.Access, allowed, row.Delivered[0])
			if !valid {
				return false
			}
			for i, kind := range nativeMixedKinds {
				ledger := nativeMixedLedger{Kind: kind, Expected: row.Delivered[i], Losses: []nativeLoadLoss{}}
				drainFacts["lane"] = uint64(i + 1)
				// Preserve the same lane's pending state even when ledger equality
				// fails first. Otherwise a backlog and an unexplained loss have
				// indistinguishable diagnostics; neither is accepted as drained.
				lane := reading.Lanes[i]
				delete(drainFacts, "node_keys")
				delete(drainFacts, "node_pending")
				if lane.Keys >= 0 {
					drainFacts["node_keys"] = uint64(lane.Keys)
				}
				if lane.Pending >= 0 {
					drainFacts["node_pending"] = uint64(lane.Pending)
				}
				drainFacts["node_dropped"], drainFacts["node_unmatched"] = lane.Dropped, lane.Unmatched
				if i == 3 {
					ledger.Expected = row.Ingress.AllowedReceived
					drainReason = "usage_read"
					if a.database.QueryRowContext(t.Context(), sql("SELECT COUNT(*),COALESCE(SUM(count),0) FROM dest_usage_hourly WHERE panel_id=?"), peer.agent.PanelID).Scan(&ledger.StoredRows, &ledger.StoredEvents) != nil {
						return false
					}
					ledger.StoredRows -= usageBaseline[owner]
					ledger.StoredEvents -= usageBaseline[owner]
				} else {
					where, args := "panel_id=? AND source=?", []any{peer.agent.PanelID, fmt.Sprintf("p%d", policy.ID)}
					if i == 1 {
						where, args = "panel_id=? AND action='observe' AND user_id>0", []any{peer.agent.PanelID}
					}
					if i == 2 {
						args = []any{peer.agent.PanelID, fmt.Sprintf("g%d", group.ID)}
					}
					drainReason = "hit_read"
					if a.database.QueryRowContext(t.Context(), sql("SELECT COUNT(*),COALESCE(SUM(count),0),COALESCE(SUM(CASE WHEN source LIKE 'g%' AND (user_id<>0 OR port<>0) THEN 1 ELSE 0 END),0) FROM dest_hits WHERE "+where), args...).Scan(&ledger.StoredRows, &ledger.StoredEvents, &ledger.Identified) != nil {
						return false
					}
				}
				drainReason = "budget_read"
				if a.database.QueryRowContext(t.Context(), sql("SELECT COALESCE(MAX(rows_reserved),0) FROM dest_audit_ingest_budget WHERE agent_id=? AND kind=?"), peer.agent.AgentID, kind).Scan(&ledger.Budget) != nil {
					return false
				}
				drainReason = "loss_query"
				rows, err := a.database.QueryContext(t.Context(), sql(nativeMixedLossSQL), peer.agent.PanelID, kind)
				if err != nil {
					return false
				}
				accounted := ledger.StoredEvents
				for rows.Next() {
					var loss nativeLoadLoss
					drainReason = "loss_scan"
					if rows.Scan(&loss.Reason, &loss.Rows, &loss.Events, &loss.Unmatched) != nil {
						_ = rows.Close()
						return false
					}
					ledger.Losses = append(ledger.Losses, loss)
					accounted += loss.Rows + loss.Events + loss.Unmatched
				}
				err = rows.Err()
				_ = rows.Close()
				drainReason = "ledger_accounting"
				if err != nil {
					drainReason = "loss_cursor"
				}
				drainFacts["expected"] = ledger.Expected
				if accounted >= 0 {
					drainFacts["accounted"] = uint64(accounted)
				} else {
					delete(drainFacts, "accounted")
					drainFacts["accounted_negative"] = 1
				}
				if err != nil || accounted < 0 || uint64(accounted) != ledger.Expected {
					return false
				}
				row.Ledgers = append(row.Ledgers, ledger)
			}
			for i, lane := range reading.Lanes {
				drainReason = "node_pending"
				drainFacts["lane"] = uint64(i + 1)
				if lane.Keys >= 0 {
					drainFacts["node_keys"] = uint64(lane.Keys)
				}
				if lane.Pending >= 0 {
					drainFacts["node_pending"] = uint64(lane.Pending)
				}
				drainFacts["node_dropped"], drainFacts["node_unmatched"] = lane.Dropped, lane.Unmatched
				if lane.Keys != 0 || lane.Pending != 0 || lane.Dropped != 0 || lane.Unmatched != 0 {
					return false
				}
			}
			detail = append(detail, row)
		}
		drainFacts = map[string]uint64{"owners_finished": uint64(len(detail))}
		for i, lane := range queue.Pressure() {
			drainReason = "psp_pending"
			drainFacts["lane"] = uint64(i + 1)
			if lane.Batches >= 0 {
				drainFacts["queue_batches"] = uint64(lane.Batches)
			}
			if lane.Batches != 0 {
				return false
			}
		}
		return true
	})
	drainComplete = true
	cancel()
	<-observationsDone
	m.Details, m.ObservationMS = detail, time.Since(start).Milliseconds()
	for i := range userDeliveries {
		m.UserDeliveries = append(m.UserDeliveries, userDeliveries[i].Load())
	}
	afterLatency := nativeLatencySnapshot(t, names)
	if beforeLatency.sinceUnixMS != afterLatency.sinceUnixMS {
		t.Fatal("global pressure metrics were reset")
	}
	for i, h := range beforeLatency.histograms {
		delta, valid := nativeLatencyDifference(h, afterLatency.histograms[i])
		if !valid {
			t.Fatal("global pressure latency window incomplete")
		}
		m.Histograms = append(m.Histograms, delta)
	}
	if m.Failures != 0 || m.TrafficMS < seconds*1000 || m.TrafficMS > (seconds+10)*1000 || m.SQLEarly != 0 || m.SQLHeld != 1 || m.Violations != 0 || m.PeakBatches != 256 || m.Saturation == 0 || blockReached.Load() != 0 {
		t.Fatal("actual global batch capacity, traffic or original SQL hold did not complete")
	}
	var queueFull [4]int64
	for _, row := range m.Details {
		for _, n := range row.Delivered {
			if n != seconds*rate {
				t.Fatal("global owner lost actual lane traffic")
			}
		}
		if row.Controls != seconds {
			t.Fatal("global owner control traffic incomplete")
		}
		for i, ledger := range row.Ledgers {
			if ledger.Identified != 0 || ledger.Budget > [4]int64{20000, 20000, 20000, 40000}[i] {
				t.Fatal("global trial privacy or independent hourly budget failed")
			}
			for _, loss := range ledger.Losses {
				if loss.Reason == "queue_full" {
					queueFull[i] += loss.Rows
				}
			}
		}
	}
	for _, n := range queueFull {
		if n <= 0 {
			t.Fatal("global saturation did not record every lane's actual overload rejection")
		}
	}
	for _, n := range m.UserDeliveries {
		if n != 65*seconds*rate/200 {
			t.Fatal("global trial principal traffic incomplete")
		}
	}
	for _, peer := range cohort.peers {
		peer.live()
		for _, private := range []string{nonce, observeNonce, controlNonce} {
			if strings.Contains(peer.output.text(), private) {
				t.Fatal("private global target nonce escaped")
			}
		}
	}
	m.Stage, m.BatchDone = 5, 1
}

func nativeGlobalBatchOutput(peers []*nativeGlobalPeer) func() string {
	return func() string {
		var all strings.Builder
		for _, peer := range peers {
			all.WriteString(peer.output.text())
		}
		return all.String()
	}
}

func nativeGlobalBatchHosts(t *testing.T, nonce, address, blockAddress, observeAddress, controlAddress string, seconds, rate int) func(int, int) string {
	t.Helper()
	original, err := os.ReadFile("/etc/hosts")
	nativeMust(t, err, "read owned global mappings")
	info, err := os.Stat("/etc/hosts")
	nativeMust(t, err, "read owned global mapping permissions")
	name := func(kind, seq int) string { return fmt.Sprintf("global-batch-%s-%d-%05d.test", nonce, kind, seq) }
	if bytes.Contains(original, []byte("global-batch-"+nonce+"-")) || net.ParseIP(address) == nil || net.ParseIP(blockAddress) == nil || net.ParseIP(observeAddress) == nil || net.ParseIP(controlAddress) == nil {
		t.Fatal("global mappings lack exclusive ownership")
	}
	var additions strings.Builder
	for kind := 0; kind <= 4; kind++ {
		ip, count := address, seconds*rate
		if kind == 0 {
			ip = blockAddress
		}
		if kind == 1 {
			ip = observeAddress
		}
		if kind == 4 {
			ip, count = controlAddress, seconds
		}
		for seq := 0; seq < count; seq++ {
			fmt.Fprintf(&additions, "%s %s\n", ip, name(kind, seq))
		}
	}
	fmt.Fprintf(&additions, "%s %s\n", address, name(5, 0))
	t.Cleanup(func() {
		nativeMust(t, os.WriteFile("/etc/hosts", original, info.Mode().Perm()), "restore owned global mappings")
	})
	content := append(append(append([]byte(nil), original...), '\n'), []byte(additions.String())...)
	nativeMust(t, os.WriteFile("/etc/hosts", content, info.Mode().Perm()), "install scoped global mappings")
	return name
}
