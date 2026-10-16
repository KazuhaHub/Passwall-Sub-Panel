//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore"
	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destaudit"
)

// This proves authenticated App/collector/SQL event binding with protocol
// vectors and original limits. It does not prove real-daemon byte saturation.
func TestNativeGlobalAdmissionObserverKeepsAuthenticationRetryAndSQL(t *testing.T) {
	events := make(chan destaudit.Event, 128)
	var overflow atomic.Bool
	binding := &nativeAdmissionBinding{}
	directory := t.TempDir()
	a, err := buildWithDestinationAuditObserver(t.Context(), &config.Config{
		Listen: "127.0.0.1:0", JWTSecret: strings.Repeat("j", 48), EncryptionKey: strings.Repeat("e", 48),
		ConfigDir: filepath.Join(directory, "config"), DataDir: filepath.Join(directory, "data"),
	}, nil, func(e destaudit.Event) {
		binding.observe(e)
		select {
		case events <- e:
		default:
			overflow.Store(true)
		}
	})
	nativeMust(t, err, "build original App with memory-only admission observer")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		nativeMust(t, a.Shutdown(ctx), "close admission observer fixture")
		sqlstore.ConfigureSecretKey("")
	})
	f := seedDestinationPolicyFixture(t, a)
	bound, err := binding.handler(a.server.Handler, []*nativeGlobalPeer{{agent: f.agent}})
	nativeMust(t, err, "bind original authenticated route to canonical observations")
	a.server.Handler = bound
	gate, err := newNativeGlobalAuditStart(a.destAudit)
	nativeMust(t, err, "hold original collector lifecycle")
	a.destAudit = gate
	a.startDestinationAudit()
	t.Cleanup(gate.release)
	f.report.Capabilities = []string{protocol.CapabilityAuditHits}
	f.report.CoreEngine = "xray"
	f.report.Audit = fixtureAudit(t, f, 1)
	f.report.Audit.Dropped, f.report.Audit.Unmatched = 0, 0
	f.report.Audit.Hits[0].Count = 1

	// The observer is downstream of actual credential validation, never a
	// wrapper that identifies a producer solely from untrusted report JSON.
	wire, err := json.Marshal(f.report)
	nativeMust(t, err, "encode original report")
	request := httptest.NewRequest(http.MethodPost, "/v1/node/sync", bytes.NewReader(wire))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer wrong-owned-fixture-credential")
	response := httptest.NewRecorder()
	a.server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatal("admission observer changed the original authentication rejection")
	}
	if len(events) != 0 || gate.Pressure()[0].Batches != 0 {
		t.Fatal("unauthenticated input reached the original collector")
	}

	next := func(want string) destaudit.Event {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case e := <-events:
				if e.Outcome == "stored" || e.Outcome == "node_diagnostics" {
					continue // Original asynchronous SQL completion is separate.
				}
				if e.Outcome != want || e.AgentID != f.agent.AgentID || e.PanelID != f.agent.PanelID || e.Kind != "block" || e.ReceivedHour <= 0 || e.ReceivedHour%protocol.AuditHourMS != 0 {
					t.Fatalf("authenticated admission event mismatch: outcome=%s", e.Outcome)
				}
				return e
			case <-timer.C:
				t.Fatal("original collector did not publish authenticated admission")
			}
		}
	}
	syncReport := func() {
		t.Helper()
		r := syncNativeCacheFixture(t, a, f.credential, f.report)
		if r.Config.Body == nil || r.Roster.Body == nil {
			t.Fatal("admission observation changed original control streams")
		}
	}
	canonicalBytes := 0
	var first, rejected *protocol.AuditObservation
	for id := 1; id <= 5; id++ {
		f.report.Audit = fixtureAudit(t, f, id)
		f.report.Audit.Dropped, f.report.Audit.Unmatched = 0, 0
		f.report.Audit.Hits[0].Count = 1
		f.report.Audit.Hits[0].Dest = fmt.Sprintf("admission-%d.example.test", id)
		wire, err := json.Marshal(f.report.Audit)
		nativeMust(t, err, "encode exact canonical admission candidate")
		if id == 1 {
			first = f.report.Audit
		}
		syncReport()
		if id <= 4 {
			e := next("accepted")
			if e.Admission != (destaudit.AdmissionPressure{}) {
				t.Fatal("accepted report fabricated a rejection witness")
			}
			canonicalBytes += len(wire)
			continue
		}
		rejected = f.report.Audit
		want := destaudit.AdmissionPressure{CandidateBytes: len(wire), Batches: 4, Bytes: canonicalBytes, OwnerBatches: 4, BatchLimit: 128, ByteLimit: 32 << 20, OwnerLimit: 4}
		for attempt := 0; attempt < 2; attempt++ {
			if attempt != 0 {
				syncReport() // A rejected candidate must remain retry eligible.
			}
			e := next("queue_full")
			if e.Admission != want || e.Rows != 1 {
				t.Fatal("rejection lost exact canonical bytes, original limits or row units")
			}
		}
	}
	f.report.Audit = first
	syncReport()
	if next("duplicate_batch").Admission != (destaudit.AdmissionPressure{}) {
		t.Fatal("accepted retransmission fabricated capacity rejection")
	}
	lane := gate.Pressure()[0]
	if lane.Batches != 4 || lane.Bytes != canonicalBytes || lane.Active != 0 || lane.Waiting != 4 || auditTableCount(t, a, "dest_hits") != 0 {
		t.Fatal("observer altered the original held queue or persisted early")
	}
	gate.release()
	waitAuditRows(t, a, "dest_hits", 4)
	nativeWait(t, 5*time.Second, "original reservations drained", func() bool { return gate.Pressure()[0].Batches == 0 })
	f.report.Audit = rejected
	syncReport()
	if next("accepted").Admission != (destaudit.AdmissionPressure{}) {
		t.Fatal("rejected candidate did not retain original retry eligibility")
	}
	waitAuditRows(t, a, "dest_hits", 5)
	nativeWait(t, 5*time.Second, "retry reservation drained", func() bool { return gate.Pressure()[0].Batches == 0 })

	token := destinationRefreshAdminToken(t, a)
	changed := serverAuditRequest(t, a, token, "PUT", fmt.Sprintf("/%d", f.agent.PanelID), map[string]any{"audit_collect": "off"})
	if changed.Code != http.StatusOK {
		t.Fatal("observer blocked original collection revocation")
	}
	f.report.Audit = fixtureAudit(t, f, 6)
	syncReport()
	if next("collect_off").Admission != (destaudit.AdmissionPressure{}) {
		t.Fatal("permission rejection fabricated capacity evidence")
	}
	gate.StopOffers()
	a.bgWG.Wait()
	var count, loss int64
	nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT SUM(count) FROM dest_hits").Scan(&count), "read original stored hit counts")
	nativeMust(t, a.database.QueryRowContext(t.Context(), "SELECT SUM(rows) FROM dest_audit_loss_hourly WHERE reason = 'queue_full'").Scan(&loss), "read original queue rejection loss rows")
	if count != 5 || loss != 2 || gate.Pressure()[0].Batches != 0 || overflow.Load() {
		t.Fatal("observed admissions did not reconcile with original SQL and loss rows")
	}
	facts, valid := binding.snapshot(f.agent.AgentID)
	if !valid || facts.Accepted != [4]uint64{5} || facts.Rejected != [4]uint64{2} || facts.RejectedRows != [4]uint64{uint64(loss)} || facts.LastRows != [4]int{1} || facts.Retried != [4]uint64{1} || facts.ByteExclusive != [4]uint64{} || facts.Last[0].Bytes != canonicalBytes {
		t.Fatal("canonical request binding changed authentication, retry or rejection attribution")
	}
}
