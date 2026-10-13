//go:build destination_pressure && linux

package destaudit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// This gate measures the real production queue with separately decoded request
// bodies. It does not claim to measure a running App, Node or SQL worker.
func TestNativeDestinationQueuePressure(t *testing.T) {
	path := os.Getenv("PSP_NATIVE_QUEUE_METRICS")
	root, err := filepath.EvalSymlinks(os.Getenv("RUNNER_TEMP"))
	dir, dirErr := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil || dirErr != nil || root != dir || filepath.Base(path) != "destination-queue-pressure-metrics.json" || os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" || os.Getenv("GITHUB_REPOSITORY") != "KazuhaHub/Passwall-Sub-Panel" {
		t.Fatal("queue pressure requires owned evidence on the hosted Linux runner")
	}
	result := testing.Benchmark(BenchmarkQueueDecodedHeap)
	if result.N != 1 {
		t.Fatal("queue pressure must perform the complete single benchmark iteration")
	}
	metric := func(name string) uint64 {
		t.Helper()
		value, ok := result.Extra[name]
		if !ok || value < 0 || value >= 1<<53 || value != float64(uint64(value)) {
			t.Fatal("queue pressure lacks an exact bounded measurement")
		}
		return uint64(value)
	}
	type laneEvidence struct {
		Kind    string `json:"kind"`
		Batches uint64 `json:"batches"`
		Bytes   uint64 `json:"canonical_bytes"`
		Wire    uint64 `json:"batch_wire_bytes"`
		Full    uint64 `json:"queue_full"`
	}
	report := struct {
		Schema     int            `json:"schema_version"`
		Iterations int            `json:"iterations"`
		Batches    uint64         `json:"batches"`
		Bytes      uint64         `json:"canonical_bytes"`
		Retained   uint64         `json:"retained_heap_bytes"`
		Peak       uint64         `json:"sampled_peak_heap_bytes"`
		Lanes      []laneEvidence `json:"lanes"`
	}{Schema: 1, Iterations: result.N, Batches: metric("batches"), Bytes: metric("canonical-bytes"), Retained: metric("retained-heap-bytes"), Peak: metric("sampled-peak-heap-bytes")}
	for _, kind := range []string{"block", "observe", "trial", "usage"} {
		report.Lanes = append(report.Lanes, laneEvidence{kind, metric(kind + "-batches"), metric(kind + "-canonical-bytes"), metric(kind + "-batch-wire-bytes"), metric(kind + "-queue-full")})
	}
	data, err := json.Marshal(report)
	if err != nil || os.WriteFile(path, append(data, '\n'), 0644) != nil {
		t.Fatal("cannot write bounded queue pressure evidence")
	}
}
