//go:build destination_acceptance && linux

package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
)

type nativeRetentionShortWriter struct{}

func (nativeRetentionShortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

// Real short SQLite shutdown/rebuild and authenticated replay. This proves the
// driver's persistent path and exact witness queries, never elapsed26/48/72h.
func TestNativeRetentionRestartPreservesActualSQLiteAndQuota(t *testing.T) {
	if !nativeOwnedCohortScope(1, "destination-retention-bootstrap-metrics.json", true, false) || nativeOwnedCohortScope(1, "destination-retention-bootstrap-metrics.json", true, true) || nativeOwnedCohortScope(1, "destination-global-bootstrap-metrics.json", false, false) {
		t.Fatal("retention borrowed pressure fixture scope or injection")
	}
	var archive bytes.Buffer
	output := &nativePrivateOutput{spill: &archive, retain: 64}
	input := bytes.Repeat([]byte("private retained output\n"), 1000)
	for start := 0; start < len(input); start += 97 {
		end := min(start+97, len(input))
		n, err := output.Write(input[start:end])
		if err != nil || n != end-start {
			t.Fatal("private output archive lost bytes")
		}
	}
	if !bytes.Equal(archive.Bytes(), input) || output.text() != string(input[len(input)-64:]) {
		t.Fatal("long output archive or bounded memory tail lost data")
	}
	short := &nativePrivateOutput{spill: nativeRetentionShortWriter{}, retain: 64}
	if _, err := short.Write([]byte("original")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal("private output short write was hidden")
	}
	f := buildDestinationPolicyFixture(t)
	t.Setenv("RUNNER_TEMP", filepath.Dir(f.a.cfg.DataDir))
	path := nativeRetentionDatabasePath(t, f.a)
	var requests [2][]byte
	var reports [2]protocol.NodeReport
	var before [2]nativeRetentionSQL
	f.a.startDestinationAudit()
	for i, kind := range []string{"block", "observe"} {
		reports[i] = f.report
		reports[i].CoreEngine = "xray"
		reports[i].Capabilities = []string{protocol.CapabilityAuditHits}
		reports[i].Audit = fixtureAudit(t, f, i+21)
		reports[i].Audit.Kind, reports[i].Audit.Hits[0].Action = kind, kind
		var err error
		requests[i], err = json.Marshal(reports[i])
		nativeMust(t, err, "encode real short restart original")
		_ = syncNativeCacheFixture(t, f.a, f.credential, reports[i])
	}
	waitAuditRows(t, f.a, "dest_hits", 2)
	for i := range before {
		before[i] = nativeRetentionSQLRead(t, f.a, f.agent.AgentID, f.agent.PanelID, requests[i], 0)
		if before[i].Rows != 1 || before[i].Events != 4 || before[i].Markers != 1 || before[i].BudgetRows != 1 || before[i].TotalBudgetRows != 1 || before[i].ReceivedMS <= 0 {
			t.Fatal("actual original SQL witness differs from stored batch")
		}
	}
	f.a.pruneDestAudit(t.Context())
	next := nativeRetentionRestart(t, f.a)
	if nativeRetentionDatabasePath(t, next) != path {
		t.Fatal("short restart borrowed another SQLite file")
	}
	next.startDestinationAudit()
	for i := range reports {
		got := nativeRetentionSQLRead(t, next, f.agent.AgentID, f.agent.PanelID, requests[i], before[i].ReceivedMS)
		if got != before[i] {
			t.Fatal("actual cleanup/reopen changed durable original")
		}
		for range 2 {
			_ = syncNativeCacheFixture(t, next, f.credential, reports[i])
		}
	}
	// Shutdown drains the real duplicate offers before the second reopen reads
	// their committed result; an early unchanged SQL read is not sufficient.
	final := nativeRetentionRestart(t, next)
	for i := range reports {
		got := nativeRetentionSQLRead(t, final, f.agent.AgentID, f.agent.PanelID, requests[i], before[i].ReceivedMS)
		if got != before[i] {
			t.Fatal("authenticated replay reset receipt, duplicated events or consumed quota")
		}
	}
	nativeRetentionStart(t, final)
}
