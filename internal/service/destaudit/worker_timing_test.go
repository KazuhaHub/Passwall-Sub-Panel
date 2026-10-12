package destaudit

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

func auditTiming(t *testing.T, name string) metrics.HistogramSnapshot {
	t.Helper()
	for _, histogram := range metrics.Take().Histograms {
		if histogram.Name == name {
			return histogram
		}
	}
	t.Fatal("audit timing histogram missing")
	return metrics.HistogramSnapshot{}
}

func TestWorkerTimingSeparatesRealBeginAndChunkAttemptsIncludingFailures(t *testing.T) {
	for _, kind := range []string{"block", "observe", "trial", "usage"} {
		metrics.DestAuditBeginMS.With(kind)
		metrics.DestAuditChunkMS.With(kind)
		beginName, chunkName := "psp_dest_audit_begin_ms{kind="+kind+"}", "psp_dest_audit_chunk_ms{kind="+kind+"}"
		for _, failed := range []int{0, 1, 2} {
			beforeBegin, beforeChunk := auditTiming(t, beginName), auditTiming(t, chunkName)
			repo := &workerStore{failAt: failed, writeErr: errors.New("private SQL and destination marker")}
			worker, _, _ := workerFixture(t, repo)
			offerWorker(t, worker, workerBody(kind, 1, 1001))
			drainWorker(t, worker)
			afterBegin, afterChunk := auditTiming(t, beginName), auditTiming(t, chunkName)
			chunks := int64(1)
			if failed == 1 {
				chunks = 0
			}
			if afterBegin.Count != beforeBegin.Count+1 || afterChunk.Count != beforeChunk.Count+chunks || afterBegin.Sum <= beforeBegin.Sum || chunks > 0 && afterChunk.Sum <= beforeChunk.Sum {
				t.Fatal("actual write attempts were missing, merged or counted twice")
			}
			for _, value := range []metrics.HistogramSnapshot{afterBegin, afterChunk} {
				data, err := json.Marshal(value)
				if err != nil || value.Unit != "ms" || strings.Contains(string(data), "private SQL") || strings.Contains(value.Name, "agent") || strings.Contains(value.Name, "subject") {
					t.Fatal("audit timing carried private labels or error content")
				}
			}
		}
	}
}
