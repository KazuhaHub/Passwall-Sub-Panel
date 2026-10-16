//go:build destination_acceptance && linux

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destaudit"
)

// Separate from batch/key pressure: these are actual connections and original
// admission decisions, with one actual owner held back until carrier byte-only
// refusals have occurred. Packet-size arithmetic alone never completes a gate.
type nativeGlobalByteOwner struct {
	Ordinal   int                      `json:"owner"`
	Attempted [4]uint64                `json:"attempted"`
	Delivered [4]uint64                `json:"delivered"`
	Controls  uint64                   `json:"control_successes"`
	Durable   [4]int                   `json:"durable_first_packets"`
	Packets   nativeByteFormationFacts `json:"packets"`
	Admission nativeAdmissionFacts     `json:"admission"`
	Ingress   nativeMixedIngress       `json:"access_ingress"`
	Stored    [4]int64                 `json:"stored_events"`
	Loss      [4]int64                 `json:"loss_rows"`
	Receiver  [4][]nativeLoadLoss      `json:"receiver_losses"`
	Node      nativeMixedNodeReading   `json:"node"`
}

type nativeGlobalByteMetrics struct {
	Schema       int                            `json:"schema_version"`
	Candidate    string                         `json:"candidate"`
	Core         string                         `json:"core_version"`
	Database     string                         `json:"database"`
	Stage        int                            `json:"stage"`
	Owners       int                            `json:"native_owners"`
	Users        int                            `json:"trial_users"`
	Fresh        int                            `json:"fresh_owner"`
	Counts       [4]int                         `json:"new_connections_per_owner"`
	Minimum      [4]int                         `json:"required_canonical_bytes_exclusive"`
	FaultMS      int64                          `json:"carrier_response_fault_ms"`
	FreshDelayMS int64                          `json:"fresh_owner_extra_hold_ms"`
	Failures     uint64                         `json:"transport_failures"`
	SQLHeld      uint64                         `json:"held_sql_entries"`
	SQLEarly     uint64                         `json:"delegated_begins_while_held"`
	Carrier      [4]int                         `json:"carrier_byte_witness_owner"`
	Witness      [4]destaudit.AdmissionPressure `json:"fresh_owner_byte_witness"`
	WitnessRows  [4]int                         `json:"fresh_owner_candidate_rows"`
	Held         [4]destaudit.LanePressure      `json:"held_queue"`
	Details      []nativeGlobalByteOwner        `json:"owners"`
	PSP          []nativeMixedPSPSample         `json:"psp_samples"`
	Histograms   []nativeLatencyHistogram       `json:"latency_histograms"`
	ByteDone     int                            `json:"byte_saturation_complete"`
	BatchDone    int                            `json:"batch_saturation_complete"`
	KeyDone      int                            `json:"key_saturation_complete"`
	Complete     int                            `json:"pressure_complete"`
}

func nativeGlobalByteOnly(p destaudit.AdmissionPressure, kind int) bool {
	return kind >= 0 && kind < 4 && p.BatchLimit == [4]int{128, 32, 32, 64}[kind] &&
		p.ByteLimit == [4]int{32 << 20, 8 << 20, 8 << 20, 16 << 20}[kind] && p.OwnerLimit == [4]int{4, 1, 1, 1}[kind] &&
		p.OwnerBatches == 0 && p.Batches > 0 && p.Batches < p.BatchLimit && p.Bytes >= 0 && p.Bytes <= p.ByteLimit && p.Bytes <= p.Batches*(1<<20) &&
		p.CandidateBytes > 0 && p.CandidateBytes <= 1<<20 && p.CandidateBytes > p.ByteLimit-p.Bytes
}

// Queue-full row accounting comes from two independent boundaries: the rows of
// each canonical request whose original authenticated handler completed, and
// the original receiver's persisted reason ledger. Unrelated losses cannot
// satisfy the equality, even if the total delivered/stored/lost sum matches.
func nativeGlobalByteLossLedger(losses []nativeLoadLoss, facts nativeAdmissionFacts, kind int) (int64, bool) {
	const maxSafeRows = 1<<53 - 1
	if kind < 0 || kind >= 4 || facts.RejectedRows[kind] > maxSafeRows || len(losses) > 3 {
		return 0, false
	}
	seen := map[string]bool{}
	var queueRows int64
	for _, loss := range losses {
		if seen[loss.Reason] || loss.Rows < 0 || loss.Rows > maxSafeRows || loss.Events != 0 || loss.Unmatched != 0 {
			return 0, false
		}
		seen[loss.Reason] = true
		switch loss.Reason {
		case "queue_full":
			queueRows = loss.Rows
		case "node_dropped", "node_unmatched":
			if loss.Rows != 0 {
				return 0, false
			}
		default:
			return 0, false
		}
	}
	return queueRows, uint64(queueRows) == facts.RejectedRows[kind]
}

func TestNativeDestinationGlobalBytePressure(t *testing.T) {
	evidence := os.Getenv("PSP_NATIVE_GLOBAL_BYTE_METRICS")
	root, e1 := filepath.EvalSymlinks(os.Getenv("RUNNER_TEMP"))
	dir, e2 := filepath.EvalSymlinks(filepath.Dir(evidence))
	if e1 != nil || e2 != nil || root != dir || filepath.Base(evidence) != "destination-global-byte-metrics.json" {
		t.Fatal("global byte pressure requires owned hosted evidence")
	}
	m := nativeGlobalByteMetrics{Schema: 2, Candidate: "61f30239e09d62b05bd59df7d9d28a5071790556", Core: "26.6.27", Fresh: 64,
		Counts: [4]int{1400, 700, 1300, 200}, Details: []nativeGlobalByteOwner{}, PSP: []nativeMixedPSPSample{}, Histograms: []nativeLatencyHistogram{}}
	var failures atomic.Uint64
	var attempted, delivered [65][4]atomic.Uint64
	var controls [65]atomic.Uint64
	var storage *nativeGlobalAuditStorage
	var faults []*nativeByteResponseFault
	binding := &nativeAdmissionBinding{}
	write := func() {
		m.Failures = failures.Load()
		for owner := range m.Details {
			for kind := range 4 {
				m.Details[owner].Attempted[kind], m.Details[owner].Delivered[kind] = attempted[owner][kind].Load(), delivered[owner][kind].Load()
			}
			m.Details[owner].Controls = controls[owner].Load()
			if owner < len(faults) {
				m.Details[owner].Packets, _, _ = faults[owner].snapshot()
				m.Details[owner].Admission, _ = binding.snapshot(faults[owner].agent)
			}
		}
		data, err := json.Marshal(m)
		nativeMust(t, err, "encode numeric global byte evidence")
		nativeMust(t, os.WriteFile(evidence, append(data, '\n'), 0644), "save owned global byte evidence")
	}
	t.Cleanup(write)
	write()
	cohort := nativeBootstrapGlobalCohort(t, nativeGlobalCohortOptions{admission: binding, auditMetrics: true,
		decorate: func(original ports.DestAuditStore) ports.DestAuditStore {
			storage = &nativeGlobalAuditStorage{DestAuditStore: original}
			return storage
		},
		transport: func(original http.Handler, peers []*nativeGlobalPeer) http.Handler {
			for _, peer := range peers {
				faults = append(faults, &nativeByteResponseFault{agent: peer.agent.AgentID})
			}
			elapsed := metrics.NewHistogram("fixture_destination_sync_handler_ms", "Owned byte-pressure original sync completion.", "ms", metrics.LatencyBucketsMS)
			timed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/node/sync" {
					began := time.Now()
					defer elapsed.ObserveSince(began)
				}
				original.ServeHTTP(w, r)
			})
			handler, err := nativeGlobalByteTransport(timed, peers, faults)
			nativeMust(t, err, "bind checked global byte response faults")
			return handler
		},
	})
	f, a := cohort.fixture, cohort.fixture.a
	m.Owners, m.Database, m.Stage = len(cohort.peers), a.cfg.DBKind(), 1
	write()
	if m.Owners != 65 || len(faults) != 65 || storage == nil {
		t.Fatal("global byte pressure lacks 65 independent real processes")
	}
	token := destinationRefreshAdminToken(t, a)
	_, address, targetPort, nonce, _ := nativeTarget(t)
	_, observeAddress, observePort, observeNonce, _ := nativeTargetAtPort(t, nativePort(t))
	_, controlAddress, controlPort, controlNonce, _ := nativeTargetAtPort(t, nativePort(t))
	stage := "capability"
	users, group, policy, blockAddress, blockPort, blockReached := nativePrepareTrialLoad(t, f, cohort.server.Client(), cohort.server.URL, token, f.node.DesiredPort, nativeGlobalBatchOutput(cohort.peers), &stage, &nativeMixedSetup{observePort: observePort, memberConcurrency: 8})
	m.Users = len(users)
	if m.Users != 200 || blockPort == observePort || blockPort == targetPort || blockPort == controlPort || observePort == targetPort || observePort == controlPort || targetPort == controlPort {
		t.Fatal("global byte principals and targets are not independent")
	}
	ready := nativeGlobalCollectionPublication(t, cohort, token)
	m.Stage = 2
	write()
	readiness := "state"
	var waitingPeer *nativeGlobalPeer
	t.Cleanup(func() {
		if m.Stage == 2 {
			nativeLogGlobalReadiness(t, readiness, waitingPeer)
		}
	})
	nativeWait(t, 240*time.Second, "global byte current policies and paired 200-user rosters", func() bool {
		readiness, waitingPeer = ready(users, group.ID)
		return readiness == "complete"
	})
	name := nativeByteFormationHosts(t, nonce, address, blockAddress, observeAddress, 1400)
	controlName := nativeGlobalByteControlHosts(t, nonce, controlAddress)
	queue, ok := a.destAudit.(interface {
		Pressure() [4]destaudit.LanePressure
	})
	if !ok {
		t.Fatal("global byte pressure lost the original queue")
	}
	for i, lane := range queue.Pressure() {
		m.Minimum[i], ok = nativeByteFormationMinimum(lane, m.Owners)
		if !ok {
			t.Fatal("global byte packet strategy cannot cover original capacities")
		}
	}
	readStored := func(owner, kind int) (int64, error) {
		query, args := nativeByteFormationStoredQuery(cohort.peers[owner].agent.PanelID, policy.ID, group.ID, kind)
		var count int64
		err := a.database.QueryRowContext(t.Context(), nativeDestinationCountSQL(m.Database, query), args...).Scan(&count)
		return count, err
	}
	var baseline [65]nativeMixedAccess
	var storedBefore [65][4]int64
	for _, peer := range cohort.peers {
		nativeMust(t, nativeVLESSHost(t.Context(), peer.node.DesiredPort, name(3, 0), targetPort, f.user.UUID, nonce, name(3, 0)), "warm actual global byte route")
		nativeMust(t, nativeVLESSHost(t.Context(), peer.node.DesiredPort, controlAddress, controlPort, f.user.UUID, controlNonce, "global-byte-control.example.test"), "verify independent global byte control target")
		peer.live = nativeBindLiveCore(t, peer.process.Process.Pid, filepath.Join(peer.dir, "node"), "xray")
	}
	nativeWait(t, 150*time.Second, "global byte warmup fully drained", func() bool {
		for owner, peer := range cohort.peers {
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
			for kind := range 4 {
				value, err := readStored(owner, kind)
				if err != nil || value != [4]int64{0, 0, 0, 2}[kind] {
					return false
				}
				storedBefore[owner][kind] = value
			}
			baseline[owner] = reading.Access
		}
		for _, lane := range queue.Pressure() {
			if lane.Batches != 0 {
				return false
			}
		}
		return true
	})
	for owner := range 65 {
		row := nativeGlobalByteOwner{Ordinal: owner}
		for kind := range 4 {
			row.Receiver[kind] = []nativeLoadLoss{}
		}
		m.Details = append(m.Details, row)
	}
	names := append([]string(nil), nativeTrialLatencyNames[:4]...)
	for _, kind := range nativeMixedKinds {
		metrics.DestAuditBeginMS.With(kind)
		metrics.DestAuditChunkMS.With(kind)
		names = append(names, "psp_dest_audit_begin_ms{kind="+kind+"}", "psp_dest_audit_chunk_ms{kind="+kind+"}")
	}
	metrics.PollStageDuration.With("sink_flush")
	beforeLatency := nativeLatencySnapshot(t, names)
	start := time.Now()
	var samplesMu sync.Mutex
	var samples []nativeMixedPSPSample
	sampleContext, stopSamples := context.WithCancel(t.Context())
	samplesDone := make(chan struct{})
	go func() {
		defer close(samplesDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			sample := nativeMixedPSPSample{time.Since(start).Milliseconds(), memory.HeapAlloc, memory.HeapInuse, queue.Pressure()}
			samplesMu.Lock()
			if len(samples) < 1201 {
				samples = append(samples, sample)
			}
			samplesMu.Unlock()
			select {
			case <-sampleContext.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	var saveOnce sync.Once
	saveSamples := func() {
		saveOnce.Do(func() {
			stopSamples()
			<-samplesDone
			samplesMu.Lock()
			m.PSP = append([]nativeMixedPSPSample(nil), samples...)
			samplesMu.Unlock()
		})
	}
	t.Cleanup(saveSamples)
	for i, fault := range faults {
		if _, ok := fault.arm(); !ok {
			t.Fatal("global byte response fault already armed")
		}
		t.Cleanup(fault.release)
		cohort.peers[i].probe.mu.Lock()
		cohort.peers[i].probe.start = start
		cohort.peers[i].probe.mu.Unlock()
	}
	faultEnd := start.Add(75 * time.Second)
	ctx, cancel := context.WithDeadline(t.Context(), faultEnd)
	defer cancel()
	// Failure diagnostics observe the original clock; no phase gets a new budget.
	// Ready-lane formation overlaps preparation, so its work is included in the
	// first phase rather than attributed to a separate, non-overlapping window.
	phase, phaseSince := 0, start
	var phaseMS [4]int64
	advancePhase := func() {
		now := time.Now()
		phaseMS[phase] = now.Sub(phaseSince).Milliseconds()
		phase++
		phaseSince = now
	}
	defer func() {
		if !t.Failed() {
			return
		}
		if phase < len(phaseMS) {
			phaseMS[phase] = time.Since(phaseSince).Milliseconds()
		}
		for i, name := range []string{"priming_ms", "first_sql_ms", "formation_ms", "live_keys_ms"} {
			if i <= phase {
				t.Logf("native global byte phase: %s=%d", name, phaseMS[i])
			}
		}
	}()
	// Priming and bulk traffic overlap, sharing the original 512-request bound.
	requests := make(chan struct{}, 512)
	send := func(ctx context.Context, owner, kind, seq int, probe *nativeVLESSWorkerProbe) error {
		select {
		case requests <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		defer func() { <-requests }()
		peer := cohort.peers[owner]
		host := name(kind, seq)
		if kind == 0 {
			header, err := nativePressureBlockRequest(f.user.UUID, host, blockPort)
			if err != nil {
				return err
			}
			return nativeLoadBlockedRequestFailure(ctx, peer.node.DesiredPort, header, func() {})
		}
		port, uuid, want := targetPort, f.user.UUID, nonce
		if kind == 1 {
			port, want = observePort, observeNonce
		} else if kind == 2 {
			uuid = users[seq%len(users)].UUID
		}
		return probe.request(ctx, peer.node.DesiredPort, host, port, uuid, want, host)
	}
	type job struct{ owner, kind, seq int }
	jobs := make(chan job, 4096)
	var workers sync.WaitGroup
	var closeJobs sync.Once
	for range 512 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			var probe nativeVLESSWorkerProbe
			for j := range jobs {
				if send(ctx, j.owner, j.kind, j.seq, &probe) != nil {
					failures.Add(1)
				} else {
					delivered[j.owner][j.kind].Add(1)
				}
			}
		}()
	}
	t.Cleanup(func() { cancel(); closeJobs.Do(func() { close(jobs) }); workers.Wait() })
	// Keep reverse priming order, 260 original requests and the same 75s
	// deadline. Feed each lane only after its original packet froze, so live
	// keys can form while other owners/lanes still wait. First SQL durability
	// and every live-key witness still precede the original SQL hold.
	var primeProbes [65]nativeVLESSWorkerProbe
	primed := make(chan error, 1)
	primingExited := make(chan struct{})
	go func() {
		defer close(primingExited)
		primed <- nativePrimeGlobalByteOwnersWithReady(ctx, 65, func(ctx context.Context, owner, kind int) error {
			attempted[owner][kind].Add(1)
			err := send(ctx, owner, kind, 1, &primeProbes[owner])
			if err != nil {
				failures.Add(1)
			} else {
				delivered[owner][kind].Add(1)
			}
			return err
		}, func(owner, kind int) bool {
			facts, packets, valid := faults[owner].snapshot()
			return valid && packets[kind].id != "" && facts.Suppressed[kind] > 0
		}, func(ctx context.Context, owner, kind int) error {
			for seq := 2; seq <= m.Counts[kind]+1; seq++ {
				select {
				case jobs <- job{owner, kind, seq}:
					attempted[owner][kind].Add(1)
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		})
	}()
	t.Cleanup(func() { cancel(); <-primingExited })
	progress := time.NewTicker(time.Second)
	defer progress.Stop()
	var primeErr error
	for waiting := true; waiting; {
		select {
		case primeErr = <-primed:
			waiting = false
		case <-progress.C:
			write()
		}
	}
	progress.Stop()
	if primeErr != nil {
		if failures.Load() != 0 {
			t.Fatal("global byte priming traffic failed")
		}
		if t.Context().Err() != nil {
			t.Fatal("acceptance cancelled")
		}
		t.Fatal("timed out waiting for global byte first class frozen in every actual owner; private diagnostics withheld")
	}
	advancePhase()
	nativeWait(t, max(time.Millisecond, time.Until(faultEnd)), "global byte first packets durable in original SQL", func() bool {
		for owner, fault := range faults {
			facts, packets, valid := fault.snapshot()
			if !valid {
				return false
			}
			for kind, packet := range packets {
				if packet.id == "" || facts.FirstRows[kind] < 1 || facts.FirstRows[kind] > 8 {
					return false
				}
				if a.database.QueryRowContext(t.Context(), nativeDestinationCountSQL(m.Database, "SELECT COUNT(*) FROM dest_audit_batches WHERE agent_id=? AND batch_id=? AND kind=?"), cohort.peers[owner].agent.AgentID, packet.id, nativeMixedKinds[kind]).Scan(&m.Details[owner].Durable[kind]) != nil || m.Details[owner].Durable[kind] != 1 {
					return false
				}
			}
		}
		for _, lane := range queue.Pressure() {
			if lane.Batches != 0 {
				return false
			}
		}
		return true
	})
	advancePhase()
	m.Stage = 3
	write()
	closeJobs.Do(func() { close(jobs) })
	workers.Wait()
	if failures.Load() != 0 {
		t.Fatal("global byte real formation traffic failed")
	}
	advancePhase()
	nativeWait(t, max(time.Millisecond, time.Until(faultEnd)), "global byte original live keys retained behind frozen packets", func() bool {
		for _, peer := range cohort.peers {
			peer.probe.mu.Lock()
			reading, invalid := peer.probe.node, peer.probe.invalid
			peer.probe.mu.Unlock()
			if invalid || len(reading.Lanes) != 4 {
				return false
			}
			for kind, lane := range reading.Lanes {
				want := m.Counts[kind]
				if kind == 3 {
					want = m.Counts[1] + m.Counts[2] + m.Counts[3]
				}
				if lane.Pending != 1 || lane.Keys < want || lane.Dropped != 0 || lane.Unmatched != 0 {
					return false
				}
			}
		}
		return true
	})
	advancePhase()
	pause, err := storage.pause()
	nativeMust(t, err, "hold original global byte SQL")
	t.Cleanup(func() { storage.resume(pause) })
	beforeBegins, beforeHeld := storage.begins.Load(), storage.blocked.Load()
	if delay := time.Until(faultEnd); delay > 0 {
		time.Sleep(delay)
	}
	for owner, fault := range faults {
		if owner != m.Fresh {
			fault.release()
		}
	}
	m.FaultMS = time.Since(start).Milliseconds()
	m.Stage = 4
	write()
	if m.FaultMS < 75000 || m.FaultMS > 76000 {
		t.Fatal("global byte carrier fault exceeded original window")
	}
	// The fresh owner's additional hold is separate numeric evidence. It is
	// bounded and is never represented as another 75-second carrier fault.
	nativeWait(t, 120*time.Second, "global byte carrier admission-time exclusive refusals", func() bool {
		found := [4]int{-1, -1, -1, -1}
		for owner, peer := range cohort.peers {
			if owner == m.Fresh {
				continue
			}
			facts, valid := binding.snapshot(peer.agent.AgentID)
			if !valid {
				return false
			}
			for kind := range 4 {
				if facts.ByteExclusive[kind] > 0 && nativeGlobalByteOnly(facts.Last[kind], kind) {
					found[kind] = owner
				}
			}
		}
		for kind, owner := range found {
			if owner < 0 {
				return false
			}
			m.Carrier[kind] = owner
		}
		return true
	})
	faults[m.Fresh].release()
	m.FreshDelayMS = time.Since(faultEnd).Milliseconds()
	write()
	nativeWait(t, 120*time.Second, "global byte fresh owner four original exclusive witnesses", func() bool {
		facts, valid := binding.snapshot(cohort.peers[m.Fresh].agent.AgentID)
		if !valid {
			return false
		}
		for kind := range 4 {
			if facts.ByteExclusive[kind] == 0 || facts.LastRows[kind] <= 8 || !nativeGlobalByteOnly(facts.Last[kind], kind) {
				return false
			}
			m.Witness[kind] = facts.Last[kind]
			m.WitnessRows[kind] = facts.LastRows[kind]
		}
		m.Held = queue.Pressure()
		return true
	})
	// A distinct control destination per producer avoids turning multiple events
	// into one row and then treating queue-full row loss as an event count.
	for owner, peer := range cohort.peers {
		host := controlName(owner)
		if err := nativeVLESSHost(t.Context(), peer.node.DesiredPort, host, controlPort, f.user.UUID, controlNonce, host); err != nil {
			failures.Add(1)
		} else {
			controls[owner].Add(1)
		}
	}
	m.SQLHeld, m.SQLEarly = storage.blocked.Load()-beforeHeld, storage.begins.Load()-beforeBegins
	if m.SQLHeld != 1 || m.SQLEarly != 0 || failures.Load() != 0 {
		t.Fatal("global byte original SQL hold or control path failed")
	}
	storage.resume(pause)
	m.Stage = 5
	write()
	nativeWait(t, 180*time.Second, "global byte canonical recovery and all independent ledgers drain", func() bool {
		for owner, peer := range cohort.peers {
			row := &m.Details[owner]
			peer.probe.mu.Lock()
			row.Node = peer.probe.node
			invalid := peer.probe.invalid
			peer.probe.mu.Unlock()
			if invalid || len(row.Node.Lanes) != 4 || !row.Node.Access.quiescent() {
				return false
			}
			var valid bool
			row.Packets, _, valid = faults[owner].snapshot()
			if !valid || row.Packets.OriginalError != 0 {
				return false
			}
			row.Admission, valid = binding.snapshot(peer.agent.AgentID)
			if !valid {
				return false
			}
			row.Controls = controls[owner].Load()
			for kind := range 4 {
				row.Attempted[kind], row.Delivered[kind] = attempted[owner][kind].Load(), delivered[owner][kind].Load()
				if row.Delivered[kind] != uint64(m.Counts[kind]+1) || row.Attempted[kind] != row.Delivered[kind] || row.Packets.Recovered[kind] != 1 || row.Packets.MaxNewBytes[kind] <= m.Minimum[kind] {
					return false
				}
			}
			allowed := row.Delivered[1] + row.Delivered[2] + row.Delivered[3] + row.Controls
			row.Ingress, valid = nativeMixedIngressBetween(baseline[owner], row.Node.Access, allowed, row.Delivered[0])
			if !valid || row.Ingress.AllowedNotReceived != 0 || row.Ingress.DeniedNotReceived != 0 {
				return false
			}
			for kind, lane := range row.Node.Lanes {
				if lane.Keys != 0 || lane.Pending != 0 || lane.Dropped != 0 || lane.Unmatched != 0 {
					return false
				}
				count, err := readStored(owner, kind)
				if err != nil {
					return false
				}
				row.Stored[kind] = count - storedBefore[owner][kind]
				rows, err := a.database.QueryContext(t.Context(), nativeDestinationCountSQL(m.Database, nativeMixedLossSQL), peer.agent.PanelID, nativeMixedKinds[kind])
				if err != nil {
					return false
				}
				row.Receiver[kind] = []nativeLoadLoss{}
				for rows.Next() {
					var loss nativeLoadLoss
					if rows.Scan(&loss.Reason, &loss.Rows, &loss.Events, &loss.Unmatched) != nil {
						_ = rows.Close()
						return false
					}
					row.Receiver[kind] = append(row.Receiver[kind], loss)
				}
				err = rows.Err()
				_ = rows.Close()
				if err != nil {
					return false
				}
				row.Loss[kind], valid = nativeGlobalByteLossLedger(row.Receiver[kind], row.Admission, kind)
				if !valid {
					return false
				}
				expected := row.Delivered[kind]
				if kind == 3 {
					expected = allowed
				}
				if row.Stored[kind] < 0 || row.Loss[kind] < 0 || uint64(row.Stored[kind]+row.Loss[kind]) != expected {
					return false
				}
			}
			peer.live()
		}
		for _, lane := range queue.Pressure() {
			if lane.Batches != 0 {
				return false
			}
		}
		return true
	})
	afterLatency := nativeLatencySnapshot(t, names)
	if beforeLatency.sinceUnixMS != afterLatency.sinceUnixMS {
		t.Fatal("global byte latency metrics reset")
	}
	for i, h := range beforeLatency.histograms {
		delta, valid := nativeLatencyDifference(h, afterLatency.histograms[i])
		if !valid {
			t.Fatal("global byte latency evidence incomplete")
		}
		m.Histograms = append(m.Histograms, delta)
	}
	saveSamples()
	if blockReached.Load() != 0 {
		t.Fatal("global byte blocked traffic escaped")
	}
	m.Stage, m.ByteDone = 6, 1
	write()
}

func nativeGlobalByteControlHosts(t *testing.T, nonce, address string) func(int) string {
	t.Helper()
	original, err := os.ReadFile("/etc/hosts")
	nativeMust(t, err, "read disposable byte control mappings")
	info, err := os.Stat("/etc/hosts")
	nativeMust(t, err, "read byte control mapping permissions")
	name := func(owner int) string {
		host, _ := nativeByteFormationName("bc-"+nonce[:12]+"-", 3, owner)
		return host
	}
	var entries strings.Builder
	for owner := range 65 {
		fmt.Fprintf(&entries, "%s %s\n", address, name(owner))
	}
	t.Cleanup(func() {
		nativeMust(t, os.WriteFile("/etc/hosts", original, info.Mode().Perm()), "restore disposable byte control mappings")
	})
	nativeMust(t, os.WriteFile("/etc/hosts", append(append([]byte(nil), original...), []byte("\n"+entries.String())...), info.Mode().Perm()), "install distinct real byte control sites")
	return name
}

func TestNativeGlobalByteWitnessRequiresOriginalExclusiveGeometry(t *testing.T) {
	for kind := range 4 {
		p := destaudit.AdmissionPressure{CandidateBytes: 600000, Batches: [4]int{32, 8, 8, 16}[kind], Bytes: [4]int{32 << 20, 8 << 20, 8 << 20, 16 << 20}[kind] - 500000, BatchLimit: [4]int{128, 32, 32, 64}[kind], ByteLimit: [4]int{32 << 20, 8 << 20, 8 << 20, 16 << 20}[kind], OwnerLimit: [4]int{4, 1, 1, 1}[kind]}
		if !nativeGlobalByteOnly(p, kind) {
			t.Fatal("original byte-only witness rejected")
		}
		for scenario := range 8 {
			changed := p
			switch scenario {
			case 0:
				changed.OwnerBatches = 1
			case 1:
				changed.Batches = changed.BatchLimit
			case 2:
				changed.CandidateBytes = changed.ByteLimit - changed.Bytes
			case 3:
				changed.ByteLimit++
			case 4:
				changed.Bytes = changed.ByteLimit + 1
			case 5:
				changed.CandidateBytes = (1 << 20) + 1
			case 6:
				changed.Batches = 0
			case 7:
				changed.Batches = 1
			}
			if nativeGlobalByteOnly(changed, kind) {
				t.Fatal("nonexclusive or altered-cap witness accepted")
			}
		}
	}
}

func TestNativeGlobalByteLossLedgerRejectsUnboundAndUnexpectedLosses(t *testing.T) {
	facts := nativeAdmissionFacts{RejectedRows: [4]uint64{3}}
	valid := []nativeLoadLoss{{Reason: "node_dropped"}, {Reason: "node_unmatched"}, {Reason: "queue_full", Rows: 3}}
	if rows, ok := nativeGlobalByteLossLedger(valid, facts, 0); !ok || rows != 3 {
		t.Fatal("independent bound queue-full rows did not reconcile")
	}
	if rows, ok := nativeGlobalByteLossLedger(nil, nativeAdmissionFacts{}, 1); !ok || rows != 0 {
		t.Fatal("empty admitted lane acquired a receiver loss")
	}
	for name, losses := range map[string][]nativeLoadLoss{
		"missing":                   {},
		"different_owner_rows":      {{Reason: "queue_full", Rows: 4}},
		"collect_off_substitution":  {{Reason: "collect_off", Rows: 3}},
		"ingest_error_substitution": {{Reason: "ingest_error", Rows: 3}},
		"over_budget_substitution":  {{Reason: "over_budget", Rows: 3}},
		"duplicate_reason":          {{Reason: "queue_full", Rows: 1}, {Reason: "queue_full", Rows: 2}},
		"dropped_events":            {{Reason: "queue_full", Rows: 3}, {Reason: "node_dropped", Events: 1}},
		"unmatched_events":          {{Reason: "queue_full", Rows: 3}, {Reason: "node_unmatched", Unmatched: 1}},
		"negative_rows":             {{Reason: "queue_full", Rows: -1}},
		"unsafe_rows":               {{Reason: "queue_full", Rows: 1 << 53}},
		"unexpected_even_if_zero":   {{Reason: "queue_full", Rows: 3}, {Reason: "out_of_range"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := nativeGlobalByteLossLedger(losses, facts, 0); ok {
				t.Fatal("unbound or unexpected receiver loss satisfied canonical rejection accounting")
			}
		})
	}
	if _, ok := nativeGlobalByteLossLedger(valid, facts, 4); ok {
		t.Fatal("unknown lane acquired a bound loss ledger")
	}
	if _, ok := nativeGlobalByteLossLedger(valid, nativeAdmissionFacts{RejectedRows: [4]uint64{1 << 53}}, 0); ok {
		t.Fatal("unsafe canonical row count acquired a bound loss ledger")
	}
}
