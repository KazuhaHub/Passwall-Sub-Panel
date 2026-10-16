//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destaudit"
	mysqldriver "github.com/go-sql-driver/mysql"
)

type nativeMixedSetup struct {
	observePort       int
	memberReady       func(int)
	memberConcurrency int
}

type nativeMixedNodeSample struct {
	ElapsedMS int64                  `json:"elapsed_ms"`
	Reading   nativeMixedNodeReading `json:"reading"`
}

type nativeMixedProbe struct {
	mu      sync.Mutex
	agent   string
	start   time.Time
	invalid bool
	// Bits retain only fixed failure categories; private reports and output stay private.
	invalidCauses         uint64
	httpFailures          uint64
	lastHTTPFailureStatus int
	failed                uint64
	succeeded             uint64
	frozen                [4]uint64
	recovered             [4]uint64
	digests               [4][32]byte
	node                  nativeMixedNodeReading
	samples               []nativeMixedNodeSample
}

func newNativeMixedProbe(agent string) *nativeMixedProbe { return &nativeMixedProbe{agent: agent} }

type nativeMixedResponse struct {
	http.ResponseWriter
	status int
}

func (w *nativeMixedResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *nativeMixedResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

// Fail the owned HTTP transport for the first real 75 seconds, before PSP sees the
// report. The real Node freezes all four subtrees and retries them naturally.
// After recovery, forward the original body to the original authenticated
// handler, then compare the first successful canonical retry privately.
func (p *nativeMixedProbe) handler(original http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		start := p.start
		p.mu.Unlock()
		if start.IsZero() || r.URL.Path != "/v1/node/sync" {
			original.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, protocol.MaxSyncBodyBytes+1))
		_ = r.Body.Close()
		var report protocol.NodeReport
		valid := err == nil && int64(len(body)) <= protocol.MaxSyncBodyBytes && json.Unmarshal(body, &report) == nil && report.AgentID == p.agent
		index, digest := -1, [32]byte{}
		if valid && report.Audit != nil {
			valid = protocol.ValidateAuditObservation(*report.Audit) == nil
			if valid && !nativeUnitAuditRows(report.Audit) {
				// Fail acceptance without changing an otherwise valid control
				// request or inventing an additional transport outage.
				p.mu.Lock()
				p.invalid, p.invalidCauses = true, p.invalidCauses|1
				p.mu.Unlock()
			}
			for i, kind := range nativeMixedKinds {
				if report.Audit.Kind == kind {
					index = i
				}
			}
			canonical, marshalErr := json.Marshal(report.Audit)
			valid = valid && marshalErr == nil && index >= 0
			digest = sha256.Sum256(canonical)
		}
		partition := time.Since(start) < 75*time.Second
		if !valid || partition {
			p.mu.Lock()
			p.invalid = p.invalid || !valid
			if !valid {
				p.invalidCauses |= 1
			}
			p.failed++
			if valid && index >= 0 {
				if p.frozen[index] > 0 && p.digests[index] != digest {
					p.invalid = true
					p.invalidCauses |= 2
				}
				p.digests[index] = digest
				p.frozen[index]++
			}
			p.mu.Unlock()
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		response := &nativeMixedResponse{ResponseWriter: w}
		original.ServeHTTP(response, r)
		p.mu.Lock()
		defer p.mu.Unlock()
		if response.status == http.StatusOK {
			p.succeeded++
			if index >= 0 && p.recovered[index] == 0 {
				p.invalid = p.invalid || p.frozen[index] == 0 || p.digests[index] != digest
				if p.frozen[index] == 0 {
					p.invalidCauses |= 4
				}
				if p.digests[index] != digest {
					p.invalidCauses |= 8
				}
				p.recovered[index]++
			}
		} else {
			p.invalid = true
			p.invalidCauses |= 16
			p.httpFailures++
			p.lastHTTPFailureStatus = response.status
		}
	})
}

type nativeMixedOutput struct {
	mu      sync.Mutex
	private *nativePrivateOutput
	probe   *nativeMixedProbe
	partial []byte
}

func (w *nativeMixedOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.private.Write(data)
	w.partial = append(w.partial, data...)
	for {
		end := bytes.IndexByte(w.partial, '\n')
		if end < 0 {
			break
		}
		reading, found, valid := nativeReadMixedNodeLine(w.partial[:end])
		w.partial = w.partial[end+1:]
		w.probe.mu.Lock()
		w.probe.invalid = w.probe.invalid || !valid
		if !valid {
			w.probe.invalidCauses |= 32
		}
		if found && valid {
			w.probe.node = reading
			if !w.probe.start.IsZero() {
				w.probe.samples = append(w.probe.samples, nativeMixedNodeSample{time.Since(w.probe.start).Milliseconds(), reading})
			}
		}
		w.probe.mu.Unlock()
	}
	if len(w.partial) > 64*1024 {
		w.probe.mu.Lock()
		w.probe.invalid = true
		w.probe.invalidCauses |= 64
		w.probe.mu.Unlock()
		w.partial = nil
	}
	return n, err
}

type nativeMixedPSPSample struct {
	ElapsedMS int64                     `json:"elapsed_ms"`
	HeapAlloc uint64                    `json:"go_heap_alloc_bytes"`
	HeapInuse uint64                    `json:"go_heap_inuse_bytes"`
	Queue     [4]destaudit.LanePressure `json:"queue"`
}

type nativeMixedLedger struct {
	Kind         string           `json:"kind"`
	Expected     uint64           `json:"expected_events"`
	StoredRows   int64            `json:"stored_rows"`
	StoredEvents int64            `json:"stored_events"`
	Identified   int64            `json:"trial_identified_rows"`
	Budget       int64            `json:"budget_max_rows"`
	Losses       []nativeLoadLoss `json:"losses"`
}

type nativeMixedMetrics struct {
	Schema          int                       `json:"schema_version"`
	Database        string                    `json:"database"`
	ReservedProbe   int                       `json:"reserved_loss_column_reproduced"`
	Candidate       string                    `json:"candidate"`
	CoreVersion     string                    `json:"core_version"`
	Duration        int64                     `json:"traffic_ms"`
	Partition       int64                     `json:"partition_ms"`
	Owners          int                       `json:"native_owners"`
	Delivered       [4]uint64                 `json:"delivered"`
	Failures        uint64                    `json:"transport_failures"`
	Late            uint64                    `json:"late_delivered"`
	Windows         [4][]uint64               `json:"delivery_windows"`
	Controls        uint64                    `json:"control_successes"`
	Users           []uint64                  `json:"user_deliveries"`
	Targets         [3]int64                  `json:"target_requests"`
	FailedSync      uint64                    `json:"partition_sync_failures"`
	Synced          uint64                    `json:"recovered_sync_successes"`
	Frozen          [4]uint64                 `json:"frozen_transmissions"`
	Recovered       [4]uint64                 `json:"verified_canonical_retries"`
	QueueReads      uint64                    `json:"queue_reads"`
	QueueViolations uint64                    `json:"queue_bound_violations"`
	MaxBatches      int                       `json:"max_total_batches"`
	MaxBytes        int                       `json:"max_total_canonical_bytes"`
	QueuePeaks      [4]destaudit.LanePressure `json:"queue_peaks"`
	Node            []nativeMixedNodeSample   `json:"node_samples"`
	PSP             []nativeMixedPSPSample    `json:"psp_samples"`
	Ledgers         []nativeMixedLedger       `json:"ledgers"`
	Ingress         nativeMixedIngress        `json:"access_ingress"`
	Window          int64                     `json:"observation_ms"`
	Histograms      []nativeLatencyHistogram  `json:"histograms"`
}

// The disposable hosted job owns these .test mappings and the numeric target
// /128s. Domain-form VLESS is required: routeOnly HTTP sniffing does not change
// the original destination recorded by Xray's accepted-connection log. Restore
// the original hosts file on every exit; never alter DNS or query public names.
func nativeMixedHosts(t *testing.T, nonce, address, blockAddress, observeAddress, controlAddress string) func(int, int) string {
	original, err := os.ReadFile("/etc/hosts")
	nativeMust(t, err, "read disposable hosted name mappings")
	info, err := os.Stat("/etc/hosts")
	nativeMust(t, err, "read disposable hosted mapping permissions")
	name := func(kind, seq int) string { return fmt.Sprintf("mixed-%s-%d-%06d.test", nonce, kind, seq) }
	var additions strings.Builder
	for kind, perSecond := range []int{800, 400, 1100, 1000, 1} {
		ip := address
		if kind == 0 {
			ip = blockAddress
		}
		if kind == 1 {
			ip = observeAddress
		}
		if kind == 4 {
			ip = controlAddress
		}
		for seq := 0; seq < perSecond*90; seq++ {
			fmt.Fprintf(&additions, "%s %s\n", ip, name(kind, seq))
		}
	}
	fmt.Fprintf(&additions, "%s %s\n", address, name(5, 0))
	// A collision would borrow an existing mapping rather than our nonce target.
	if bytes.Contains(original, []byte("mixed-"+nonce+"-")) || net.ParseIP(address) == nil || net.ParseIP(blockAddress) == nil || net.ParseIP(observeAddress) == nil || net.ParseIP(controlAddress) == nil {
		t.Fatal("mixed name fixture has foreign ownership")
	}
	t.Cleanup(func() {
		nativeMust(t, os.WriteFile("/etc/hosts", original, info.Mode().Perm()), "restore disposable hosted name mappings")
	})
	content := append(append(append([]byte(nil), original...), '\n'), []byte(additions.String())...)
	nativeMust(t, os.WriteFile("/etc/hosts", content, info.Mode().Perm()), "install owned disposable domain mappings")
	return name
}

func nativeMixedLoad(t *testing.T, f destinationPolicyFixture, client *http.Client, endpoint, token string, parent int, dataDir string, inbound int, address string, targetPort int, nonce string, reached *atomic.Int64, controlAddress string, controlPort int, controlNonce, evidence string, output func() string, probe *nativeMixedProbe) {
	a := f.a
	m := nativeMixedMetrics{Schema: 2, Candidate: "61f30239e09d62b05bd59df7d9d28a5071790556", CoreVersion: "26.6.27", Partition: 75000, Owners: 1, Users: make([]uint64, 200), Node: []nativeMixedNodeSample{}, PSP: []nativeMixedPSPSample{}, Ledgers: []nativeMixedLedger{}, Histograms: []nativeLatencyHistogram{}}
	m.Database = a.cfg.DBKind() // buildNativeDestinationPolicyFixture verified the actual owned server.
	sql := func(query string) string { return nativeDestinationCountSQL(m.Database, query) }
	t.Cleanup(func() {
		probe.mu.Lock()
		m.Node = append([]nativeMixedNodeSample{}, probe.samples...)
		m.FailedSync, m.Synced, m.Frozen, m.Recovered = probe.failed, probe.succeeded, probe.frozen, probe.recovered
		probe.mu.Unlock()
		data, err := json.Marshal(m)
		nativeMust(t, err, "encode numeric mixed load evidence")
		nativeMust(t, os.WriteFile(evidence, append(data, '\n'), 0644), "save owned numeric mixed load evidence")
	})
	if m.Database == "mysql" {
		// Reproduce the old assertion's exact syntax failure against this
		// disposable actual MySQL before traffic. Export only a numeric receipt.
		var total int64
		err := a.database.QueryRowContext(t.Context(), "SELECT COALESCE(SUM(rows),0) FROM dest_audit_loss_hourly").Scan(&total)
		var syntax *mysqldriver.MySQLError
		if !errors.As(err, &syntax) || syntax.Number != 1064 {
			t.Fatal("owned MySQL did not reproduce the old reserved-column assertion")
		}
		nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT COALESCE(SUM(dest_audit_loss_hourly.rows),0) FROM dest_audit_loss_hourly").Scan(&total), "verify owned MySQL qualified loss assertion")
		if total != 0 {
			t.Fatal("reserved-column reproduction lacks an empty owned baseline")
		}
		m.ReservedProbe = 1
	}
	_, observeAddress, observePort, observeNonce, observeReached := nativeTargetAtPort(t, nativePort(t))
	if targetPort == observePort || controlPort == observePort {
		t.Fatal("mixed observe policy selected another lane's numeric port")
	}
	stage := "capability"
	users, group, policy, blockAddress, blockPort, blockReached := nativePrepareTrialLoad(t, f, client, endpoint, token, inbound, output, &stage, &nativeMixedSetup{observePort: observePort})
	if blockPort == targetPort || blockPort == observePort || blockPort == controlPort {
		t.Fatal("mixed block policy selected an allowed lane's numeric port")
	}
	// Each accepted destination must be unique, including trial and control,
	// so persisted row losses can be reconciled as events for this fixture only.
	name := nativeMixedHosts(t, nonce, address, blockAddress, observeAddress, controlAddress)
	t.Cleanup(func() {
		if strings.Contains(output(), observeNonce) {
			t.Error("mixed target nonce escaped into candidate output")
		}
	})
	live := nativeBindLiveCore(t, parent, dataDir, "xray")
	nativeMust(t, nativeVLESSHost(t.Context(), inbound, name(5, 0), targetPort, f.user.UUID, nonce, name(5, 0)), "verify owned mixed domain target")
	queue, ok := a.destAudit.(interface {
		Pressure() [4]destaudit.LanePressure
	})
	if !ok {
		t.Fatal("mixed load lacks the actual production queue observer")
	}
	nativeWait(t, 90*time.Second, "mixed warmup audit drain", func() bool {
		var stored int64
		if a.database.QueryRowContext(t.Context(), sql("SELECT COALESCE(SUM(count),0) FROM dest_usage_hourly WHERE panel_id=?"), f.agent.PanelID).Scan(&stored) != nil || stored != 1 {
			return false
		}
		probe.mu.Lock()
		node, invalid := probe.node, probe.invalid
		probe.mu.Unlock()
		if invalid || len(node.Lanes) != 4 || !node.Access.quiescent() || node.Access.Private != 1 || node.Access.Usage != 1 {
			return false
		}
		for _, lane := range node.Lanes {
			if lane.Keys != 0 || lane.Pending != 0 || lane.Dropped != 0 || lane.Unmatched != 0 {
				return false
			}
		}
		for _, lane := range queue.Pressure() {
			if lane.Batches != 0 {
				return false
			}
		}
		return true
	})
	var usageBaseline int64
	nativeMust(t, a.database.QueryRowContext(t.Context(), sql("SELECT COALESCE(SUM(count),0) FROM dest_usage_hourly WHERE panel_id=?"), f.agent.PanelID).Scan(&usageBaseline), "bind actual warmup usage baseline")
	names := append([]string(nil), nativeTrialLatencyNames[:4]...)
	for _, kind := range nativeMixedKinds {
		names = append(names, "psp_dest_audit_begin_ms{kind="+kind+"}", "psp_dest_audit_chunk_ms{kind="+kind+"}")
	}
	before := nativeLatencySnapshot(t, names)
	baseline := reached.Load()
	start := time.Now()
	probe.mu.Lock()
	ingressBefore := probe.node.Access
	probe.start = start
	probe.mu.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	controlCtx, cancelControls := context.WithDeadline(ctx, start.Add(90*time.Second))
	defer cancelControls()
	type job struct{ kind, seq int }
	jobs := make(chan job, 2048)
	var wg sync.WaitGroup
	var delivered [4]atomic.Uint64
	var windows [4][90]atomic.Uint64
	var perUser [200]atomic.Uint64
	var failures, late, controls atomic.Uint64
	var closeJobs sync.Once
	defer func() { cancel(); closeJobs.Do(func() { close(jobs) }); wg.Wait() }()
	for range 768 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// One serial worker owns the scratch buffers; each request still
			// establishes a fresh connection and verifies its current nonce.
			var probe nativeVLESSWorkerProbe
			for j := range jobs {
				if j.kind == 4 {
					host := name(4, j.seq)
					if probe.request(controlCtx, inbound, host, controlPort, f.user.UUID, controlNonce, host) != nil || time.Since(start) >= 90*time.Second {
						failures.Add(1)
					} else {
						controls.Add(1)
					}
					continue
				}
				if j.kind == 0 {
					header, err := nativePressureBlockRequest(f.user.UUID, name(0, j.seq), blockPort)
					if err != nil || !nativeLoadBlockedRequest(ctx, inbound, header, func() {}) {
						failures.Add(1)
						continue
					}
				} else {
					port, uuid, wantNonce := targetPort, f.user.UUID, nonce
					if j.kind == 1 {
						port, wantNonce = observePort, observeNonce
					}
					if j.kind == 2 {
						uuid = users[j.seq%200].UUID
					}
					host := name(j.kind, j.seq)
					if probe.request(ctx, inbound, host, port, uuid, wantNonce, host) != nil {
						failures.Add(1)
						continue
					}
					if j.kind == 2 {
						perUser[j.seq%200].Add(1)
					}
				}
				delivered[j.kind].Add(1)
				second := int(time.Since(start) / time.Second)
				if second < 90 {
					windows[j.kind][second].Add(1)
				} else {
					late.Add(1)
				}
			}
		}()
	}
	// One coherent actual reservation view every 10 ms; sampled whole heaps
	// once a second. No fake repository, forced GC or production metric reset.
	queueDone := make(chan struct{})
	go func() {
		defer close(queueDone)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		lastMemory := int64(-1)
		for {
			view := queue.Pressure()
			m.QueueReads++
			totalBatches, totalBytes := 0, 0
			for i, lane := range view {
				totalBatches += lane.Batches
				totalBytes += lane.Bytes
				if lane.Kind != nativeMixedKinds[i] || lane.BatchLimit != [4]int{128, 32, 32, 64}[i] || lane.ByteLimit != [4]int{32 << 20, 8 << 20, 8 << 20, 16 << 20}[i] || lane.OwnerLimit != [4]int{4, 1, 1, 1}[i] || lane.Batches != lane.Waiting+lane.Active || lane.Batches < 0 || lane.Batches > lane.BatchLimit || lane.Bytes < 0 || lane.Bytes > lane.ByteLimit || lane.MaxOwner > lane.OwnerLimit || lane.Owners > 1 || lane.Active < 0 || lane.Active > 1 {
					m.QueueViolations++
				}
				if lane.Bytes > m.QueuePeaks[i].Bytes || m.QueuePeaks[i].Kind == "" {
					m.QueuePeaks[i] = lane
				}
			}
			if totalBatches > 256 || totalBytes > 64<<20 {
				m.QueueViolations++
			}
			m.MaxBatches, m.MaxBytes = max(m.MaxBatches, totalBatches), max(m.MaxBytes, totalBytes)
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
	defer func() { cancel(); <-queueDone }()
	// Keep the same per-lane rates and total identities, but spread the owned
	// producer over approximately 10ms slices. A 330-request FIFO burst every 100ms favours
	// the first lanes and can push the later lanes across a second boundary.
	// Preserve the original final send at 89.9s, leaving the same 100ms for
	// actual responses before the strict 90s deadline. No late gate changes.
	const ticks = 9000
	const lastSend = 90*time.Second - 100*time.Millisecond
	for tick := 0; tick < ticks; tick++ {
		if delay := time.Until(start.Add(time.Duration(tick) * lastSend / (ticks - 1))); delay > 0 {
			time.Sleep(delay)
		}
		for kind, count := range []int{8, 4, 11, 10} {
			for n := 0; n < count; n++ {
				select {
				case jobs <- job{kind, tick*count + n}:
				default:
					failures.Add(1)
				}
			}
		}
		if tick%100 == 0 {
			live()
			// A real control reply must not stop all four traffic producers.
			// It uses the same bounded worker pool and original 90s deadline.
			select {
			case jobs <- job{4, tick / 100}:
			default:
				failures.Add(1)
			}
		}
	}
	if delay := time.Until(start.Add(90 * time.Second)); delay > 0 {
		time.Sleep(delay)
	}
	m.Duration = time.Since(start).Milliseconds()
	closeJobs.Do(func() { close(jobs) })
	wg.Wait()
	m.Controls = controls.Load()
	for i := range delivered {
		m.Delivered[i] = delivered[i].Load()
		m.Windows[i] = make([]uint64, 90)
		for j := range windows[i] {
			m.Windows[i][j] = windows[i][j].Load()
		}
	}
	for i := range perUser {
		m.Users[i] = perUser[i].Load()
	}
	m.Failures = failures.Load()
	m.Late = late.Load()
	m.Targets = [3]int64{observeReached.Load(), reached.Load() - baseline, blockReached.Load()}
	if blockReached.Load() != 0 {
		t.Fatal("mixed load bypassed the actual block policy")
	}
	readLedger := func() bool {
		probe.mu.Lock()
		node, invalid := probe.node, probe.invalid
		// Require three unchanged, complete ingress observations after traffic,
		// spanning two real seconds. No counter reset or artificial flush.
		stable := len(probe.samples) >= 3
		if stable {
			last := probe.samples[len(probe.samples)-3:]
			stable = last[0].ElapsedMS >= m.Duration && last[2].ElapsedMS-last[0].ElapsedMS >= 1800
			for _, sample := range last {
				stable = stable && sample.Reading.Access == node.Access
			}
		}
		probe.mu.Unlock()
		if invalid || !stable || len(node.Lanes) != 4 {
			return false
		}
		var valid bool
		m.Ingress, valid = nativeMixedIngressBetween(ingressBefore, node.Access, m.Delivered[1]+m.Delivered[2]+m.Delivered[3]+m.Controls, m.Delivered[0])
		if !valid {
			return false
		}
		m.Ledgers = nil
		for i, kind := range nativeMixedKinds {
			ledger := nativeMixedLedger{Kind: kind, Expected: m.Delivered[i], Losses: []nativeLoadLoss{}}
			if i == 3 {
				ledger.Expected = m.Ingress.AllowedReceived
				nativeMust(t, a.database.QueryRowContext(t.Context(), sql("SELECT COUNT(*),COALESCE(SUM(count),0) FROM dest_usage_hourly WHERE panel_id=?"), f.agent.PanelID).Scan(&ledger.StoredRows, &ledger.StoredEvents), "read actual mixed usage")
				ledger.StoredEvents -= usageBaseline
				ledger.StoredRows -= usageBaseline // warmup is one unique row
			} else {
				source := fmt.Sprintf("p%d", policy.ID)
				if i == 2 {
					source = fmt.Sprintf("g%d", group.ID)
				}
				where := "panel_id=? AND source=?"
				args := []any{f.agent.PanelID, source}
				if i == 1 {
					where = "panel_id=? AND action='observe' AND user_id>0"
					args = []any{f.agent.PanelID}
				}
				nativeMust(t, a.database.QueryRowContext(t.Context(), sql("SELECT COUNT(*),COALESCE(SUM(count),0),COALESCE(SUM(CASE WHEN source LIKE 'g%' AND (user_id<>0 OR port<>0) THEN 1 ELSE 0 END),0) FROM dest_hits WHERE "+where), args...).Scan(&ledger.StoredRows, &ledger.StoredEvents, &ledger.Identified), "read actual mixed hit ledger")
			}
			nativeMust(t, a.database.QueryRowContext(t.Context(), sql("SELECT COALESCE(MAX(rows_reserved),0) FROM dest_audit_ingest_budget WHERE agent_id=? AND kind=?"), f.agent.AgentID, kind).Scan(&ledger.Budget), "read mixed hourly reservation")
			rows, err := a.database.QueryContext(t.Context(), sql(nativeMixedLossSQL), f.agent.PanelID, kind)
			nativeMust(t, err, "read mixed categorized losses")
			accounted := ledger.StoredEvents
			for rows.Next() {
				var loss nativeLoadLoss
				nativeMust(t, rows.Scan(&loss.Reason, &loss.Rows, &loss.Events, &loss.Unmatched), "read numeric mixed loss")
				ledger.Losses = append(ledger.Losses, loss)
				accounted += loss.Rows + loss.Events + loss.Unmatched
			}
			err = rows.Err()
			_ = rows.Close()
			nativeMust(t, err, "complete mixed loss read")
			m.Ledgers = append(m.Ledgers, ledger)
			if accounted < 0 || uint64(accounted) != ledger.Expected {
				return false
			}
		}
		for _, lane := range node.Lanes {
			if lane.Keys != 0 || lane.Pending != 0 || lane.Dropped != 0 || lane.Unmatched != 0 {
				return false
			}
		}
		for _, lane := range queue.Pressure() {
			if lane.Batches != 0 {
				return false
			}
		}
		return true
	}
	nativeWait(t, 150*time.Second, "mixed canonical retry and persisted four-lane ledger", readLedger)
	live()
	cancel()
	<-queueDone
	m.Window = time.Since(start).Milliseconds()
	var finalMemory runtime.MemStats
	runtime.ReadMemStats(&finalMemory)
	if len(m.PSP) == 0 || m.Window > m.PSP[len(m.PSP)-1].ElapsedMS {
		m.PSP = append(m.PSP, nativeMixedPSPSample{m.Window, finalMemory.HeapAlloc, finalMemory.HeapInuse, queue.Pressure()})
	}
	after := nativeLatencySnapshot(t, names)
	if before.sinceUnixMS != after.sinceUnixMS {
		t.Fatal("mixed latency metrics reset")
	}
	for i, h := range before.histograms {
		delta, valid := nativeLatencyDifference(h, after.histograms[i])
		if !valid {
			t.Fatal("mixed latency snapshot inconsistent")
		}
		m.Histograms = append(m.Histograms, delta)
	}
	cancel()
	<-queueDone
	if m.Failures != 0 || m.Late != 0 || m.Controls != 90 || m.Duration < 90000 || m.Duration > 95000 || usageBaseline != 1 {
		t.Fatal("mixed traffic lost delivery or its owned baseline")
	}
}
