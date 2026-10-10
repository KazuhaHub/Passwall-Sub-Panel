package destaudit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"

	protocol "github.com/KazuhaHub/passwall-protocol/protocol"
)

// Run explicitly with -run '^$' -bench QueueDecodedHeap -benchtime=1x.
// Measure live decoded queue memory after GC as well as canonical JSON bytes;
// the wire-byte reservation is not a promise about actual heap consumption.
func BenchmarkQueueDecodedHeap(b *testing.B) {
	wires := map[string][]byte{}
	dest := strings.Repeat("a", 56) + "." + strings.Repeat("b", 56) + "." + strings.Repeat("c", 56) + "." + strings.Repeat("d", 56) + ".com"
	for _, kind := range []string{"block", "observe", "trial", "usage"} {
		body := queueFixture(kind, 1)
		for i := 1; i <= 2000; i++ {
			site := fmt.Sprintf("d%05d.%s", i, dest)
			if kind == "usage" {
				body.Usage = append(body.Usage, protocol.AuditUsage{Hour: body.Hour, Subject: protocol.NewSubjectKey(int64(i)), Site: site, Count: 1})
			} else {
				hit := mappingHit(fmt.Sprintf("p%d", i), "block", "usr_7", site, 443, 1, 1)
				if kind == "observe" {
					hit.Action = "observe"
				}
				if kind == "trial" {
					hit.RuleID = fmt.Sprintf("g%d", i)
					hit.Action = "observe"
					hit.Subject = ""
					hit.Port = 0
				}
				body.Hits = append(body.Hits, hit)
			}
		}
		if err := protocol.ValidateAuditObservation(body); err != nil {
			b.Fatal(err)
		}
		wire, err := json.Marshal(body)
		if err != nil || len(wire) > protocol.MaxAuditObservationBytes {
			b.Fatalf("invalid benchmark wire size %d: %v", len(wire), err)
		}
		wires[kind] = wire
	}
	b.ResetTimer()
	for range b.N {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		q := newBatchQueue()
		for _, kind := range []string{"block", "observe", "trial", "usage"} {
			for i := 0; i < 256; i++ {
				var decoded protocol.AuditObservation
				// Each simulated request owns its wire buffer, like the HTTP body.
				if err := json.Unmarshal(bytes.Clone(wires[kind]), &decoded); err != nil {
					b.Fatal(err)
				}
				result := q.offer(fmt.Sprintf("%s-%d", kind, i), 1, decoded.Hour, decoded)
				if result == "queue_full" {
					break
				}
				if result != "accepted" {
					b.Fatalf("benchmark offer %s", result)
				}
			}
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		count, bytes := q.pending()
		b.ReportMetric(float64(count), "batches")
		b.ReportMetric(float64(bytes), "canonical-bytes")
		b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc), "retained-heap-bytes")
		runtime.KeepAlive(q)
		runtime.KeepAlive(wires)
	}
}
