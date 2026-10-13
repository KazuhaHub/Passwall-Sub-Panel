package app

import (
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

func nativeFleetOfflineStatusReady(rows []destpolicy.DestinationNodeStatus, panel int64, agent string, capable bool, seen time.Time, poll time.Duration, now time.Time) bool {
	if panel <= 0 || agent == "" || seen.IsZero() || poll <= 0 || !now.After(seen.Add(3*poll)) {
		return false
	}
	matches := 0
	want := "unsupported_version"
	if capable {
		want = "offline"
	}
	for _, row := range rows {
		if row.PanelID != panel {
			continue
		}
		matches++
		if row.Kind != domain.PanelKindPSP || row.AgentID == nil || *row.AgentID != agent || row.Supports.Policy != capable || row.LastReportAt == nil || *row.LastReportAt != seen.UnixMilli() || row.State != want {
			return false
		}
	}
	return matches == 1
}

func TestNativeFleetOfflineWaitsForActualStatusAndFullHeartbeatWindow(t *testing.T) {
	seen, poll := time.Unix(1_800_000_000, 0).UTC(), 2*time.Second
	agent, stamp := "fixture-offline-agent", seen.UnixMilli()
	row := destpolicy.DestinationNodeStatus{PanelID: 7, Kind: domain.PanelKindPSP, AgentID: &agent, LastReportAt: &stamp, State: "offline", Supports: destpolicy.DestinationSupports{Policy: true}}
	ready := func(rows []destpolicy.DestinationNodeStatus, capable bool, elapsed time.Duration) bool {
		return nativeFleetOfflineStatusReady(rows, 7, agent, capable, seen, poll, seen.Add(elapsed))
	}
	if ready([]destpolicy.DestinationNodeStatus{row}, true, 4*time.Second) || ready([]destpolicy.DestinationNodeStatus{row}, true, 6*time.Second) || !ready([]destpolicy.DestinationNodeStatus{row}, true, 6*time.Second+time.Millisecond) {
		t.Fatal("failed attempts were mistaken for the full production offline window")
	}
	for _, change := range []func(*destpolicy.DestinationNodeStatus){
		func(r *destpolicy.DestinationNodeStatus) { r.PanelID++ },
		func(r *destpolicy.DestinationNodeStatus) { other := "fixture-online-agent"; r.AgentID = &other },
		func(r *destpolicy.DestinationNodeStatus) { r.AgentID = nil },
		func(r *destpolicy.DestinationNodeStatus) { r.State = "applied" },
		func(r *destpolicy.DestinationNodeStatus) { other := stamp + 1; r.LastReportAt = &other },
		func(r *destpolicy.DestinationNodeStatus) { r.Supports.Policy = false },
	} {
		changed := row
		change(&changed)
		if ready([]destpolicy.DestinationNodeStatus{changed}, true, 7*time.Second) {
			t.Fatal("offline status borrowed another identity, heartbeat or execution state")
		}
	}
	if ready(nil, true, 7*time.Second) || ready([]destpolicy.DestinationNodeStatus{row, row}, true, 7*time.Second) {
		t.Fatal("offline status accepted missing or duplicate selected peer")
	}
	legacy := row
	legacy.State, legacy.Supports.Policy = "unsupported_version", false
	if ready([]destpolicy.DestinationNodeStatus{legacy}, false, 4*time.Second) || !ready([]destpolicy.DestinationNodeStatus{legacy}, false, 7*time.Second) {
		t.Fatal("legacy outage changed its capability state or skipped the heartbeat window")
	}
}
