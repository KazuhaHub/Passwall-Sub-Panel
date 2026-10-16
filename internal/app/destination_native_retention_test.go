//go:build destination_acceptance && linux

package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type nativeRetentionPhase struct {
	Name string                `json:"name"`
	AtMS int64                 `json:"at_unix_ms"`
	SQL  [2]nativeRetentionSQL `json:"original_sql"`
}

type nativeRetentionReport struct {
	Schema           int                       `json:"schema_version"`
	Candidate        string                    `json:"candidate"`
	Core             string                    `json:"core_version"`
	Database         string                    `json:"database"`
	Owners           int                       `json:"native_owners"`
	StartMS          int64                     `json:"started_unix_ms"`
	EndMS            int64                     `json:"ended_unix_ms"`
	WindowMS         int64                     `json:"window_ms"`
	Controls         int                       `json:"control_successes"`
	Samples          int                       `json:"fresh_node_samples"`
	OriginalHours    [2]int64                  `json:"original_hour_ms"`
	ExpectedEvents   [2]int64                  `json:"original_events"`
	PendingBefore48  int                       `json:"observe_pending_before_expiry"`
	PendingAfter48   int                       `json:"observe_pending_after_expiry"`
	BoundaryFixtures [3]nativeRetentionSQL     `json:"authenticated_boundary_fixtures"`
	OutOfRangeRows   int64                     `json:"out_of_range_rows"`
	Phases           []nativeRetentionPhase    `json:"phases"`
	Fault            nativeRetentionFaultFacts `json:"transport"`
	Complete         int                       `json:"complete"`
}

// Manual disposable-host gate: no duration overrides and no injected clock.
// Short boundary tests never select this root or count as long-run evidence.
func TestNativeDestinationRetentionReal72Hours(t *testing.T) {
	deadline, hasDeadline := t.Deadline()
	if os.Getenv("PSP_NATIVE_RETENTION") != "72h" || !hasDeadline || time.Until(deadline) < 75*time.Hour {
		t.Fatal("retention requires its explicit real72h host and at least75h test lifetime")
	}
	root, e1 := filepath.EvalSymlinks(os.Getenv("RUNNER_TEMP"))
	evidence := os.Getenv("PSP_NATIVE_RETENTION_METRICS")
	dir, e2 := filepath.EvalSymlinks(filepath.Dir(evidence))
	if e1 != nil || e2 != nil || root != dir || filepath.Base(evidence) != "destination-retention-metrics.json" {
		t.Fatal("retention evidence must belong to its dedicated private root")
	}
	start := time.Now()
	m := nativeRetentionReport{Schema: 1, Candidate: "61f30239e09d62b05bd59df7d9d28a5071790556", Core: "26.6.27", Database: "sqlite", Owners: 1, StartMS: start.UnixMilli(), Phases: []nativeRetentionPhase{}}
	var fault *nativeRetentionResponseFault
	write := func() {
		if fault != nil {
			var valid bool
			m.Fault, valid = fault.snapshot()
			if !valid {
				t.Error("retention transport evidence invalid")
			}
		}
		body, err := json.Marshal(m)
		nativeMust(t, err, "encode numeric retention checkpoint")
		nativeMust(t, os.WriteFile(evidence, append(body, '\n'), 0600), "save private retention checkpoint")
	}
	t.Cleanup(write)
	var sampleMu sync.Mutex
	var latest nativeMixedNodeReading
	var latestAt time.Time
	var privateFields []string
	c := nativeBootstrapOwnedCohort(t, nativeGlobalCohortOptions{auditMetrics: true, observeNode: func(_ int, r nativeMixedNodeReading) {
		sampleMu.Lock()
		latest, latestAt = r, time.Now()
		sampleMu.Unlock()
	}, transport: func(original http.Handler, peers []*nativeGlobalPeer) http.Handler {
		file, err := os.OpenFile(filepath.Join(root, "retention-node-output.private.log"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		nativeMust(t, err, "create fresh private retention output spool")
		peers[0].output.spill, peers[0].output.retain = file, 64*1024
		t.Cleanup(func() {
			defer file.Close()
			nativeMust(t, file.Sync(), "flush complete private retention daemon output")
			_, err := file.Seek(0, io.SeekStart)
			nativeMust(t, err, "read complete private retention output")
			scanner := bufio.NewScanner(file)
			scanner.Buffer(make([]byte, 4096), 64*1024)
			for scanner.Scan() {
				for _, private := range append(privateFields, peers[0].secret, " accepted ", " rejected ") {
					if private != "" && strings.Contains(scanner.Text(), private) {
						t.Error("retention daemon exposed private data")
					}
				}
			}
			nativeMust(t, scanner.Err(), "finish complete private retention output privacy scan")
		})
		fault = &nativeRetentionResponseFault{agent: peers[0].agent.AgentID, original: original}
		return fault
	}}, 1, "destination-retention-bootstrap-metrics.json", nativeBuildRetentionFixture)
	f, a, peer := c.fixture, c.fixture.a, c.peers[0]
	privateFields = []string{f.user.UUID, f.user.Email, f.user.SubToken}
	token := destinationRefreshAdminToken(t, a)
	_, observeAddress, observePort, observeNonce, _ := nativeTarget(t)
	_, controlAddress, controlPort, controlNonce, _ := nativeTargetAtPort(t, nativePort(t))
	_, _, blockPort, _, blockReached := nativeTargetAtPort(t, nativePort(t))
	for i, kind := range []string{"block", "observe"} {
		port := blockPort
		if i == 1 {
			port = observePort
		}
		input := destinationPolicyInput("Retention "+kind, kind)
		input["inline"] = map[string]any{"ports": strconv.Itoa(port)}
		if response := destinationListRequest(t, a, token, "POST", "policies", input); response.Code != 201 {
			t.Fatal("retention policy creation failed")
		}
	}
	if response := serverAuditRequest(t, a, token, "PUT", "/"+strconv.FormatInt(peer.agent.PanelID, 10), map[string]any{"audit_collect": "hits"}); response.Code != 200 {
		t.Fatal("retention collection update failed")
	}
	if response := destinationListRequest(t, a, token, "POST", "publish", nil); response.Code != 200 {
		t.Fatal("retention publication failed")
	}
	nativeWait(t, 180*time.Second, "retention real current policy and collection receipt", func() bool {
		p, err := a.repos.DestAgentPolicy.Get(t.Context(), peer.agent.AgentID, true)
		panel, e := a.repos.XUIPanel.GetByID(t.Context(), peer.agent.PanelID)
		var body protocol.DestinationPolicy
		return err == nil && e == nil && p != nil && panel != nil && c.ready(peer) && nativeCollectionReceiptMatches(*p, protocol.CollectHits, uint64(panel.AuditCollectRevision)) && json.Unmarshal(p.MintedBody, &body) == nil && len(body.Rules) == 2
	})
	var probe nativeVLESSWorkerProbe
	control := func() {
		peer.live()
		nativeMust(t, probe.request(t.Context(), peer.node.DesiredPort, controlAddress, controlPort, f.user.UUID, controlNonce, "retention-control.test"), "actual retention nonce control")
		m.Controls++
		sampleMu.Lock()
		r, at := latest, latestAt
		sampleMu.Unlock()
		if at.IsZero() || time.Since(at) > 10*time.Second || len(r.Lanes) != 4 || !r.Access.quiescent() || r.Access.Dropped != 0 || r.Access.Unmatched != 0 || r.Access.Stale != 0 {
			t.Fatal("retention lost fresh real Node metrics or access events")
		}
		for _, lane := range r.Lanes {
			if lane.Dropped != 0 || lane.Unmatched != 0 {
				t.Fatal("retention Node retained unexpected loss")
			}
		}
		m.Samples++
	}
	control()
	if !fault.arm() {
		t.Fatal("could not arm original retention response capture")
	}
	header, err := nativePressureBlockRequest(f.user.UUID, "retention-block.test", blockPort)
	nativeMust(t, err, "create original block request")
	nativeMust(t, nativeLoadBlockedRequestFailure(t.Context(), peer.node.DesiredPort, header, func() {}), "real retention block request")
	nativeMust(t, probe.request(t.Context(), peer.node.DesiredPort, observeAddress, observePort, f.user.UUID, observeNonce, "retention-observe.test"), "real retention observe request")
	var packets [2][]byte
	var base [2]nativeRetentionSQL
	nativeWait(t, 120*time.Second, "both original SQL commits despite suppressed responses", func() bool {
		facts, valid := fault.snapshot()
		if !valid || facts.CapturedRows != [2]int{1, 1} {
			return false
		}
		for i := range packets {
			packets[i] = fault.privateRequest(i)
			base[i] = nativeRetentionSQLRead(t, a, peer.agent.AgentID, peer.agent.PanelID, packets[i], 0)
			if base[i].Rows != 1 || base[i].Events != 1 || base[i].Markers != 1 || base[i].BudgetRows != 1 || base[i].TotalBudgetRows != 1 {
				return false
			}
			var report protocol.NodeReport
			nativeMust(t, json.Unmarshal(packets[i], &report), "read original retention hour")
			m.OriginalHours[i], m.ExpectedEvents[i] = report.Audit.Hour, 1
		}
		return true
	})
	phase := func(name string) {
		v := nativeRetentionPhase{Name: name, AtMS: time.Now().UnixMilli()}
		for i := range packets {
			v.SQL[i] = nativeRetentionSQLRead(t, a, peer.agent.AgentID, peer.agent.PanelID, packets[i], base[i].ReceivedMS)
		}
		m.Phases = append(m.Phases, v)
		write()
	}
	phase("captured")
	// Independent authenticated boundary fixtures use copies of the captured
	// report, fresh IDs and receiver wall time. They are NOT Node-generated
	// future events and never replace the two immutable captured originals.
	boundary := func(hour int64, destination string) []byte {
		var report protocol.NodeReport
		nativeMust(t, json.Unmarshal(packets[0], &report), "copy original for labelled boundary fixture")
		var id [16]byte
		_, err := rand.Read(id[:])
		nativeMust(t, err, "create independent boundary ID")
		report.ReportedAtMS = time.Now().UnixMilli()
		report.Audit.BatchID = hex.EncodeToString(id[:])
		report.Audit.Hour = hour
		report.Audit.Hits[0].Hour = hour
		report.Audit.Hits[0].FirstMS = hour
		report.Audit.Hits[0].LastMS = hour
		report.Audit.Hits[0].Dest = destination
		body, err := json.Marshal(report)
		nativeMust(t, err, "encode labelled authenticated boundary fixture")
		return body
	}
	submit := func(body []byte) {
		server := httptest.NewTLSServer(a.server.Handler)
		defer server.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/node/sync", bytes.NewReader(body))
		nativeMust(t, err, "create authenticated boundary request")
		r.Header.Set("Authorization", "Bearer "+peer.secret)
		r.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(r)
		nativeMust(t, err, "send authenticated boundary through original handler")
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("original authenticated boundary handler rejected transport")
		}
	}
	nowHour := time.Now().UTC().Truncate(time.Hour)
	if time.Until(nowHour.Add(time.Hour)) < 5*time.Second {
		nativeMust(t, nativeDayWait(t.Context(), nowHour.Add(time.Hour+time.Second)), "avoid ambiguous future-hour receiver boundary")
		nowHour = time.Now().UTC().Truncate(time.Hour)
	}
	future := boundary(nowHour.Add(time.Hour).UnixMilli(), "retention-future.test")
	submit(future)
	nativeWait(t, 30*time.Second, "future one-hour bucket actual commit", func() bool {
		m.BoundaryFixtures[0] = nativeRetentionSQLRead(t, a, peer.agent.AgentID, peer.agent.PanelID, future, time.Now().UnixMilli())
		return m.BoundaryFixtures[0].Events == 1 && m.BoundaryFixtures[0].Markers == 1
	})
	if m.BoundaryFixtures[0].HourMS <= m.BoundaryFixtures[0].ReceivedMS || m.BoundaryFixtures[0].HourMS > m.BoundaryFixtures[0].ReceivedMS+time.Hour.Milliseconds() {
		t.Fatal("accepted fixture did not prove a real future bucket within one hour")
	}
	tooFuture := boundary(time.Now().UTC().Truncate(time.Hour).Add(3*time.Hour).UnixMilli(), "retention-future-rejected.test")
	submit(tooFuture)
	nativeWait(t, 30*time.Second, "future beyond one-hour rejection marker", func() bool {
		m.BoundaryFixtures[1] = nativeRetentionSQLRead(t, a, peer.agent.AgentID, peer.agent.PanelID, tooFuture, time.Now().UnixMilli())
		return m.BoundaryFixtures[1].Markers == 1
	})
	if m.BoundaryFixtures[1].Events != 0 || m.BoundaryFixtures[1].TotalBudgetRows != 2 {
		t.Fatal("out-of-range future fixture consumed rows or quota")
	}
	phase("future_boundaries")
	if !fault.isolate() {
		t.Fatal("could not isolate original committed retention batches")
	}
	isolated := time.Now()
	wait := func(due time.Time) {
		for time.Until(due) > 0 {
			next := time.Now().Add(time.Minute)
			if due.Before(next) {
				next = due
			}
			nativeMust(t, nativeDayWait(t.Context(), next), "wait original real retention deadline")
			control()
			write()
		}
	}
	wait(isolated.Add(26 * time.Hour))
	phase("before_cleanup26")
	a.pruneDestAudit(t.Context())
	phase("after_cleanup26")
	a = nativeRetentionRestart(t, a)
	nativeRetentionStart(t, a)
	phase("after_restart26")
	if !fault.replace(a.server.Handler) || !fault.resumeBlock() {
		t.Fatal("could not resume original block after actual26h and App restart")
	}
	nativeWait(t, 120*time.Second, "real Node retransmission of original block after restart", func() bool { facts, valid := fault.snapshot(); return valid && facts.Recovered > 0 })
	phase("after_node_replay26")
	for _, p := range m.Phases[2:] {
		for i, s := range p.SQL {
			if s.Rows != 1 || s.Events != 1 || s.Markers != 1 || s.ReceivedMS != base[i].ReceivedMS || s.TotalBudgetRows != [2]int64{2, 1}[i] {
				t.Fatal("cleanup/restart/replay duplicated original row or consumed new quota")
			}
		}
	}
	expires := time.UnixMilli(m.OriginalHours[1]).Add(48 * time.Hour)
	wait(expires.Add(-time.Minute))
	sampleMu.Lock()
	m.PendingBefore48 = latest.Lanes[1].Pending
	sampleMu.Unlock()
	if m.PendingBefore48 != 1 {
		t.Fatal("observe original vanished before actual48h hour boundary")
	}
	wait(expires.Add(15 * time.Second))
	nativeWait(t, 120*time.Second, "real Node expiry of frozen observe", func() bool {
		sampleMu.Lock()
		defer sampleMu.Unlock()
		m.PendingAfter48 = latest.Lanes[1].Pending
		return time.Since(latestAt) < 10*time.Second && m.PendingAfter48 == 0
	})
	phase("after_node_expiry48")
	stale := boundary(m.OriginalHours[0], "retention-stale.test")
	submit(stale)
	nativeWait(t, 30*time.Second, "stale authenticated fixture marker", func() bool {
		m.BoundaryFixtures[2] = nativeRetentionSQLRead(t, a, peer.agent.AgentID, peer.agent.PanelID, stale, time.Now().UnixMilli())
		return m.BoundaryFixtures[2].Markers == 1
	})
	if m.BoundaryFixtures[2].Rows != 0 || m.BoundaryFixtures[2].TotalBudgetRows != 2 {
		t.Fatal("expired fixture stored rows or consumed new quota")
	}
	nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT COALESCE(SUM(rows),0) FROM dest_audit_loss_hourly WHERE panel_id=? AND kind='block' AND reason='out_of_range'", peer.agent.PanelID).Scan(&m.OutOfRangeRows), "read actual future/stale rejection loss rows")
	if m.OutOfRangeRows != 2 {
		t.Fatal("future and stale losses missing or counted in wrong units")
	}
	phase("after_stale_rejection48")
	settings, err := a.settings.Load(t.Context(), ports.UISettings{})
	nativeMust(t, err, "load retention settings")
	settings.DestHitRetentionDays, settings.DestTrialRetentionDays = 1, 1
	nativeMust(t, a.settings.Save(t.Context(), settings), "save real one-day hit retention")
	a.pruneDestAudit(t.Context())
	phase("after_hit_cleanup48")
	firstReceived := min(base[0].ReceivedMS, base[1].ReceivedMS)
	lastReceived := max(base[0].ReceivedMS, base[1].ReceivedMS)
	wait(time.UnixMilli(firstReceived).Add(72*time.Hour - 2*time.Second))
	a.pruneDestAudit(t.Context())
	phase("before_marker_expiry72")
	wait(time.UnixMilli(lastReceived).Add(72*time.Hour + 2*time.Second))
	a.pruneDestAudit(t.Context())
	phase("after_marker_expiry72")
	lastHour := time.UnixMilli(lastReceived).UTC().Truncate(time.Hour)
	wait(lastHour.Add(73*time.Hour + 2*time.Second))
	a.pruneDestAudit(t.Context())
	phase("after_budget_hour_expiry")
	wait(start.Add(72 * time.Hour))
	for i, p := range m.Phases {
		if i >= 8 {
			for _, s := range p.SQL {
				if s.Rows != 0 || s.Events != 0 {
					t.Fatal("one-day cleanup left original hits")
				}
			}
		}
		if p.Name == "before_marker_expiry72" {
			for _, s := range p.SQL {
				if s.Markers != 1 {
					t.Fatal("dedup marker deleted before actual72h")
				}
			}
		}
		if p.Name == "after_marker_expiry72" || p.Name == "after_budget_hour_expiry" {
			for _, s := range p.SQL {
				if s.Markers != 0 {
					t.Fatal("dedup marker retained after actual72h cleanup")
				}
			}
		}
		if p.Name == "after_budget_hour_expiry" {
			for _, s := range p.SQL {
				if s.BudgetKeys != 0 || s.BudgetRows != 0 {
					t.Fatal("original budget hour was not pruned")
				}
			}
		}
	}
	if blockReached.Load() != 0 {
		t.Fatal("blocked retention connection reached the target")
	}
	m.EndMS, m.WindowMS = time.Now().UnixMilli(), time.Since(start).Milliseconds()
	if m.WindowMS < 72*time.Hour.Milliseconds() || m.Controls < 4320 || m.Samples != m.Controls {
		t.Fatal("real retention lifetime or live controls incomplete")
	}
	m.Complete = 1
	write()
}
