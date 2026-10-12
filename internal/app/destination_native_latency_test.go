//go:build destination_acceptance && linux

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

var nativeTrialLatencyNames = []string{
	"fixture_destination_sync_handler_ms", "psp_node_control_sync_ms",
	"psp_poll_ms", "psp_poll_stage_ms{stage=sink_flush}",
	"psp_dest_audit_begin_ms{kind=block}", "psp_dest_audit_begin_ms{kind=trial}",
	"psp_dest_audit_chunk_ms{kind=block}", "psp_dest_audit_chunk_ms{kind=trial}",
}

type nativeTrialLatencyReading struct {
	sinceUnixMS int64
	histograms  []metrics.HistogramSnapshot
}

func nativeTrialLatencySnapshot(t *testing.T) nativeTrialLatencyReading {
	return nativeLatencySnapshot(t, nativeTrialLatencyNames)
}

func nativeLatencySnapshot(t *testing.T, names []string) nativeTrialLatencyReading {
	t.Helper()
	// Take is intentionally non-atomic. Retry a bounded read when bucket and
	// count loads straddle an observation; never reset or pause production work.
	for range 100 {
		snapshot := metrics.Take()
		all := snapshot.Histograms
		out := make([]metrics.HistogramSnapshot, 0, len(names))
		for _, name := range names {
			for _, h := range all {
				if h.Name == name {
					if _, ok := nativeLatencyDifference(h, h); ok {
						out = append(out, h)
					}
					break
				}
			}
		}
		if len(out) == len(names) {
			return nativeTrialLatencyReading{snapshot.SinceUnixMS, out}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("consistent bounded latency snapshot unavailable")
	return nativeTrialLatencyReading{}
}

func nativeWriteTrialLatency(t *testing.T, evidence string, duration int64, before nativeTrialLatencyReading) {
	t.Helper()
	after := nativeTrialLatencySnapshot(t)
	if before.sinceUnixMS != after.sinceUnixMS {
		t.Fatal("production metrics reset during the latency window")
	}
	report := struct {
		Schema     int                      `json:"schema_version"`
		WindowMS   int64                    `json:"window_ms"`
		Histograms []nativeLatencyHistogram `json:"histograms"`
	}{Schema: 1, WindowMS: duration}
	for i := range before.histograms {
		h, ok := nativeLatencyDifference(before.histograms[i], after.histograms[i])
		if !ok {
			t.Fatal("latency window reset or changed its bounded bucket layout")
		}
		report.Histograms = append(report.Histograms, h)
	}
	data, err := json.Marshal(report)
	nativeMust(t, err, "encode bounded trial latency evidence")
	nativeMust(t, os.WriteFile(filepath.Join(filepath.Dir(evidence), "destination-trial-load-latency.json"), append(data, '\n'), 0644), "write owned trial latency evidence")
}
