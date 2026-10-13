package destaudit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

// Keep the fake repository calls above the histogram's microsecond resolution.
// This exercises elapsed instrumentation without assuming a minimum cost for
// an in-memory slice append on a particular runner.
type timingWorkerStore struct{ *workerStore }

func (s timingWorkerStore) BeginDestinationAudit(ctx context.Context, batch domain.DestAuditBatch) (domain.DestAuditBegin, error) {
	time.Sleep(time.Millisecond)
	return s.workerStore.BeginDestinationAudit(ctx, batch)
}

func (s timingWorkerStore) WriteDestinationAuditChunk(ctx context.Context, chunk domain.DestAuditChunk) (string, error) {
	time.Sleep(time.Millisecond)
	return s.workerStore.WriteDestinationAuditChunk(ctx, chunk)
}

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
			worker.repo = timingWorkerStore{repo}
			offerWorker(t, worker, workerBody(kind, 1, 1001))
			drainWorker(t, worker)
			afterBegin, afterChunk := auditTiming(t, beginName), auditTiming(t, chunkName)
			chunks := int64(1)
			if failed == 1 {
				chunks = 0
			}
			if afterBegin.Count != beforeBegin.Count+1 || afterChunk.Count != beforeChunk.Count+chunks || afterBegin.Sum-beforeBegin.Sum < 1 || chunks > 0 && afterChunk.Sum-beforeChunk.Sum < 1 {
				t.Fatalf("kind=%s failure_step=%d begin_attempts=%d chunk_attempts=%d; missing elapsed repository measurement", kind, failed, afterBegin.Count-beforeBegin.Count, afterChunk.Count-beforeChunk.Count)
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
