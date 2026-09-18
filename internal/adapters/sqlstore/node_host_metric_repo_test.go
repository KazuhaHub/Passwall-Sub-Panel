package sqlstore

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// These are WP4's acceptance conditions: idempotency, late-write precedence, the
// caller-owned throttle, orphan-free deletion and integer-boundary agreement
// across dialects.

var hostBaseTime = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

func hostTestRepos(t *testing.T) (ports.NodeHostMetricRepo, *gorm.DB) {
	t.Helper()
	db, err := openTestDB(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTestSchema(db); err != nil {
		t.Fatal(err)
	}
	return NewRepos(db).NodeHostMetric, db
}

func hostObservation(agentID, sampleID, digest string, receivedAt time.Time) *domain.NodeHostObservation {
	return &domain.NodeHostObservation{
		AgentID: agentID, SampleID: sampleID, PayloadDigest: digest,
		CollectedAt: receivedAt.Add(-time.Second), ReceivedAt: receivedAt,
		BootID: "boot-1", ResourceScope: "host",
		SnapshotJSON: []byte(`{"sample_id":"` + sampleID + `","unavailable":["conntrack"]}`),
	}
}

func hostSample(agentID, sampleID, digest string, receivedAt time.Time) *domain.NodeHostMetricSample {
	logicalCPUs := 4
	total := uint64(1000)
	available := uint64(400)
	return &domain.NodeHostMetricSample{
		AgentID: agentID, SampleID: sampleID, PayloadDigest: digest,
		CollectedAt: receivedAt.Add(-time.Second), ReceivedAt: receivedAt, BootID: "boot-1",
		Deployment: "systemd", ResourceScope: "host", CgroupVersion: 2,
		DataFilesystemScope: "host_mount", LogicalCPUs: logicalCPUs,
		SystemMemoryTotalBytes: &total, SystemMemoryAvailableBytes: &available,
	}
}

func hostInterfaces(agentID, sampleID string, receivedAt time.Time) []domain.NodeInterfaceMetricSample {
	return []domain.NodeInterfaceMetricSample{{
		AgentID: agentID, SampleID: sampleID, InterfaceIndex: 2, InterfaceName: "eth0",
		IsDefaultIPv4: true, MTU: 1500, Up: true,
		RXBytes: 100, TXBytes: 200, ReceivedAt: receivedAt,
	}}
}

// THE WRITE THAT MUST BE IDEMPOTENT. A retried report carries the sample the
// panel already stored, and the FIRST receipt time has to survive: it is when
// the observation actually arrived, and moving it forward would make a retry
// look like a fresh sample.
func TestNodeHostPersistTreatsARetryAsAnIdempotentDuplicate(t *testing.T) {
	repo, _ := hostTestRepos(t)
	ctx := context.Background()
	agent, sampleID, digest := "agt_1", strings.Repeat("a", 32), strings.Repeat("b", 64)

	first := hostObservation(agent, sampleID, digest, hostBaseTime)
	result, err := repo.Persist(ctx, domain.NodeHostPersistRequest{
		Observation: first, Sample: hostSample(agent, sampleID, digest, hostBaseTime),
	})
	if err != nil || !result.LatestUpdated || !result.HistoryInserted {
		t.Fatalf("first persist = (%+v, %v)", result, err)
	}

	later := hostBaseTime.Add(5 * time.Second)
	retry := hostObservation(agent, sampleID, digest, later)
	retry.SnapshotJSON = first.SnapshotJSON
	result, err = repo.Persist(ctx, domain.NodeHostPersistRequest{
		Observation: retry, Sample: hostSample(agent, sampleID, digest, later),
	})
	if err != nil || !result.Duplicate || result.LatestUpdated {
		t.Fatalf("retry persist = (%+v, %v)", result, err)
	}
	stored, err := repo.Latest(ctx, agent)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.ReceivedAt.Equal(hostBaseTime) {
		t.Fatalf("received_at = %s, want the first receipt %s", stored.ReceivedAt, hostBaseTime)
	}
	// And the history did not gain a second row for the same sample.
	samples, err := repo.RawRange(ctx, agent, hostBaseTime.Add(-time.Hour), hostBaseTime.Add(time.Hour), false)
	if err != nil || len(samples) != 1 {
		t.Fatalf("history after a retry = (%d rows, %v)", len(samples), err)
	}
}

// One sample id, two payloads. Neither can be trusted to be "the" sample, so the
// stored one stands and the core sync still succeeds.
func TestNodeHostPersistReportsAnIdentityConflictWithoutWriting(t *testing.T) {
	repo, _ := hostTestRepos(t)
	ctx := context.Background()
	agent, sampleID := "agt_1", strings.Repeat("a", 32)

	if _, err := repo.Persist(ctx, domain.NodeHostPersistRequest{
		Observation: hostObservation(agent, sampleID, strings.Repeat("b", 64), hostBaseTime),
	}); err != nil {
		t.Fatal(err)
	}
	conflict := hostObservation(agent, sampleID, strings.Repeat("c", 64), hostBaseTime.Add(time.Minute))
	result, err := repo.Persist(ctx, domain.NodeHostPersistRequest{Observation: conflict})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IdentityConflict || result.LatestUpdated {
		t.Fatalf("conflicting persist = %+v", result)
	}
	stored, err := repo.Latest(ctx, agent)
	if err != nil {
		t.Fatal(err)
	}
	if stored.PayloadDigest != strings.Repeat("b", 64) {
		t.Fatalf("the stored snapshot was replaced by the conflicting one: %s", stored.PayloadDigest)
	}
}

// The panel is the one that knows receipt order, so a late request must not
// rewind the detail view to a state the host has already moved past.
func TestNodeHostPersistRefusesALateSnapshot(t *testing.T) {
	repo, _ := hostTestRepos(t)
	ctx := context.Background()
	agent := "agt_1"

	if _, err := repo.Persist(ctx, domain.NodeHostPersistRequest{
		Observation: hostObservation(agent, strings.Repeat("a", 32), strings.Repeat("b", 64), hostBaseTime),
	}); err != nil {
		t.Fatal(err)
	}
	late := hostObservation(agent, strings.Repeat("c", 32), strings.Repeat("d", 64), hostBaseTime.Add(-time.Minute))
	result, err := repo.Persist(ctx, domain.NodeHostPersistRequest{Observation: late})
	if err != nil {
		t.Fatal(err)
	}
	if result.LatestUpdated || result.Duplicate || result.IdentityConflict {
		t.Fatalf("a late snapshot did something: %+v", result)
	}
	stored, err := repo.Latest(ctx, agent)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SampleID != strings.Repeat("a", 32) {
		t.Fatalf("a late snapshot overwrote a newer one: %s", stored.SampleID)
	}
}

// THE THROTTLE IS THE CALLER'S DECISION. The repository writes what it is given,
// because the panel's raw-write interval is a policy the service owns — and a
// repository that applied its own would silently disagree with it.
func TestNodeHostPersistWritesHistoryOnlyWhenGivenASample(t *testing.T) {
	repo, _ := hostTestRepos(t)
	ctx := context.Background()
	agent := "agt_1"

	if _, err := repo.Persist(ctx, domain.NodeHostPersistRequest{
		Observation: hostObservation(agent, strings.Repeat("a", 32), strings.Repeat("b", 64), hostBaseTime),
	}); err != nil {
		t.Fatal(err)
	}
	result, err := repo.Persist(ctx, domain.NodeHostPersistRequest{
		Observation: hostObservation(agent, strings.Repeat("c", 32), strings.Repeat("d", 64), hostBaseTime.Add(30*time.Second)),
	})
	if err != nil || !result.LatestUpdated || result.HistoryInserted {
		t.Fatalf("throttled persist = (%+v, %v)", result, err)
	}
	samples, err := repo.RawRange(ctx, agent, hostBaseTime.Add(-time.Hour), hostBaseTime.Add(time.Hour), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 0 {
		t.Fatalf("a throttled round wrote %d history rows", len(samples))
	}
}

// A rate needs two samples, so the first point of a window would have nothing to
// subtract without the row before it. The predecessor is a BASELINE and must be
// distinguishable from a window row — it precedes `from`.
func TestNodeHostRawRangeReturnsThePredecessorAsABaseline(t *testing.T) {
	repo, _ := hostTestRepos(t)
	ctx := context.Background()
	agent := "agt_1"
	from := hostBaseTime.Add(time.Minute)

	if _, err := repo.Persist(ctx, domain.NodeHostPersistRequest{
		Observation: hostObservation(agent, strings.Repeat("a", 32), strings.Repeat("b", 64), hostBaseTime),
		Sample:      hostSample(agent, strings.Repeat("a", 32), strings.Repeat("b", 64), hostBaseTime),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Persist(ctx, domain.NodeHostPersistRequest{
		Observation: hostObservation(agent, strings.Repeat("c", 32), strings.Repeat("d", 64), from.Add(30*time.Second)),
		Sample:      hostSample(agent, strings.Repeat("c", 32), strings.Repeat("d", 64), from.Add(30*time.Second)),
	}); err != nil {
		t.Fatal(err)
	}

	without, err := repo.RawRange(ctx, agent, from, from.Add(time.Hour), false)
	if err != nil || len(without) != 1 {
		t.Fatalf("without predecessor = (%d rows, %v)", len(without), err)
	}
	with, err := repo.RawRange(ctx, agent, from, from.Add(time.Hour), true)
	if err != nil || len(with) != 2 {
		t.Fatalf("with predecessor = (%d rows, %v)", len(with), err)
	}
	if !with[0].ReceivedAt.Before(from) {
		t.Fatalf("the first row is not a baseline: %s", with[0].ReceivedAt)
	}
	// The rows after the baseline are strictly inside the window, which is what
	// lets a caller tell a baseline from a data point.
	for _, row := range with[1:] {
		if row.ReceivedAt.Before(from) {
			t.Fatalf("a window row precedes the window: %s", row.ReceivedAt)
		}
	}
}

// The list is keyed by panel id and only the agent row knows which panel an agent
// belongs to, so the join is what makes the batched read possible at all.
func TestNodeHostLatestBatchJoinsThroughTheAgentTable(t *testing.T) {
	repo, db := hostTestRepos(t)
	ctx := context.Background()
	agents := NewRepos(db).NodeAgent
	for index, pair := range []struct {
		agentID string
		panelID int64
	}{{"agt_1", 10}, {"agt_2", 20}, {"agt_3", 30}} {
		// The credential digest is unique per agent, so the fixture varies it.
		if err := agents.Create(ctx, &domain.NodeAgent{
			AgentID: pair.agentID, PanelID: pair.panelID,
			CredentialSHA256: fmt.Sprintf("%02x", index) + strings.Repeat("ab", 31),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, agentID := range []string{"agt_1", "agt_2"} {
		if _, err := repo.Persist(ctx, domain.NodeHostPersistRequest{
			Observation: hostObservation(agentID, strings.Repeat("a", 32), strings.Repeat("b", 64), hostBaseTime),
		}); err != nil {
			t.Fatal(err)
		}
	}

	summaries, err := repo.LatestBatchByPanelIDs(ctx, []int64{10, 20, 30})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("summaries = %d, want 2 (agt_3 has no telemetry)", len(summaries))
	}
	// The unavailable token count comes from the stored snapshot without decoding
	// it, which is what keeps the list endpoint off the JSON parser.
	if summaries[10].Unavailable != 1 {
		t.Fatalf("panel 10 unavailable = %d, want 1", summaries[10].Unavailable)
	}
	if summaries[10].ResourceScope != "host" || summaries[10].AgentID != "agt_1" {
		t.Fatalf("panel 10 summary = %+v", summaries[10])
	}
	empty, err := repo.LatestBatchByPanelIDs(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty batch = (%d, %v)", len(empty), err)
	}
}

// The rollup recomputes buckets, so the write has to replace rather than append —
// otherwise a crash mid-rollup would leave two rows claiming to be the same hour.
func TestNodeHostUpsertHourlyIsIdempotent(t *testing.T) {
	repo, _ := hostTestRepos(t)
	ctx := context.Background()
	agent := "agt_1"
	bucket := hostBaseTime.Truncate(time.Hour)
	average := 42.5

	row := domain.NodeHostMetricHourly{
		BucketStart: bucket, AgentID: agent, SampleCount: 60, CoverageSeconds: 3600,
		SystemCPUAverage: &average,
	}
	if err := repo.UpsertHourly(ctx, []domain.NodeHostMetricHourly{row}); err != nil {
		t.Fatal(err)
	}
	recomputed := row
	recomputedAverage := 43.5
	recomputed.SystemCPUAverage = &recomputedAverage
	recomputed.SampleCount = 61
	if err := repo.UpsertHourly(ctx, []domain.NodeHostMetricHourly{recomputed}); err != nil {
		t.Fatal(err)
	}

	rows, err := repo.HourlyRange(ctx, agent, bucket.Add(-time.Hour), bucket.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("a recomputed bucket produced %d rows", len(rows))
	}
	if rows[0].SampleCount != 61 || rows[0].SystemCPUAverage == nil || *rows[0].SystemCPUAverage != 43.5 {
		t.Fatalf("the recomputation did not replace the row: %+v", rows[0])
	}
}

// Nothing else would clean up after a deleted node, and an interrupted delete
// would leave interface rows whose sample no longer exists — visible as gaps in
// an interface chart with nothing to explain them.
func TestNodeHostDeleteByAgentIDLeavesNothingBehind(t *testing.T) {
	repo, _ := hostTestRepos(t)
	ctx := context.Background()
	agent, sampleID := "agt_1", strings.Repeat("a", 32)

	if _, err := repo.Persist(ctx, domain.NodeHostPersistRequest{
		Observation: hostObservation(agent, sampleID, strings.Repeat("b", 64), hostBaseTime),
		Sample:      hostSample(agent, sampleID, strings.Repeat("b", 64), hostBaseTime),
		Interfaces:  hostInterfaces(agent, sampleID, hostBaseTime),
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpsertHourly(ctx, []domain.NodeHostMetricHourly{{
		BucketStart: hostBaseTime.Truncate(time.Hour), AgentID: agent, SampleCount: 1, CoverageSeconds: 60,
	}}); err != nil {
		t.Fatal(err)
	}
	// A second agent's data must survive the first one's deletion.
	other := "agt_2"
	if _, err := repo.Persist(ctx, domain.NodeHostPersistRequest{
		Observation: hostObservation(other, strings.Repeat("c", 32), strings.Repeat("d", 64), hostBaseTime),
		Sample:      hostSample(other, strings.Repeat("c", 32), strings.Repeat("d", 64), hostBaseTime),
	}); err != nil {
		t.Fatal(err)
	}

	if err := repo.DeleteByAgentID(ctx, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Latest(ctx, agent); err == nil {
		t.Fatal("the latest snapshot survived the delete")
	}
	samples, err := repo.RawRange(ctx, agent, hostBaseTime.Add(-time.Hour), hostBaseTime.Add(time.Hour), false)
	if err != nil || len(samples) != 0 {
		t.Fatalf("history survived the delete: %d rows, %v", len(samples), err)
	}
	interfaces, err := repo.InterfaceRange(ctx, agent, "eth0", hostBaseTime.Add(-time.Hour), hostBaseTime.Add(time.Hour))
	if err != nil || len(interfaces) != 0 {
		t.Fatalf("interface rows survived the delete: %d rows, %v", len(interfaces), err)
	}
	hourly, err := repo.HourlyRange(ctx, agent, hostBaseTime.Add(-time.Hour), hostBaseTime.Add(time.Hour))
	if err != nil || len(hourly) != 0 {
		t.Fatalf("hourly rows survived the delete: %d rows, %v", len(hourly), err)
	}

	if _, err := repo.Latest(ctx, other); err != nil {
		t.Fatalf("the other agent's snapshot was deleted with it: %v", err)
	}
}

// Retention is applied in bounded batches: a large installation's cleanup would
// otherwise hold one long write transaction, and SQLite serialises writes.
func TestNodeHostPruneRemovesInBoundedBatches(t *testing.T) {
	repo, _ := hostTestRepos(t)
	ctx := context.Background()
	agent := "agt_1"
	for index := 0; index < 5; index++ {
		at := hostBaseTime.Add(time.Duration(index) * time.Minute)
		sampleID := strings.Repeat("a", 31) + string(rune('0'+index))
		if _, err := repo.Persist(ctx, domain.NodeHostPersistRequest{
			Observation: hostObservation(agent, sampleID, strings.Repeat("b", 64), at),
			Sample:      hostSample(agent, sampleID, strings.Repeat("b", 64), at),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// A limit of two must remove two, not all five.
	fresh, err := repo.Prune(ctx, domain.NodeHostPruneRequest{
		RawBefore: hostBaseTime.Add(time.Hour), HourlyBefore: hostBaseTime.Add(time.Hour), Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.RawDeleted != 2 {
		t.Fatalf("raw deleted = %d, want the batch limit", fresh.RawDeleted)
	}
	remaining, err := repo.RawRange(ctx, agent, hostBaseTime.Add(-time.Hour), hostBaseTime.Add(time.Hour), false)
	if err != nil || len(remaining) != 3 {
		t.Fatalf("after a bounded prune = (%d rows, %v)", len(remaining), err)
	}
	// The cutoff is exclusive: nothing newer than it may go.
	none, err := repo.Prune(ctx, domain.NodeHostPruneRequest{
		RawBefore: hostBaseTime.Add(-time.Hour), HourlyBefore: hostBaseTime.Add(-time.Hour), Limit: 100,
	})
	if err != nil || none.RawDeleted != 0 {
		t.Fatalf("prune before the cutoff removed %d rows", none.RawDeleted)
	}
}

// THE CORRUPTION GUARD. The wire validated every counter against MaxInt64, so a
// negative value in storage is damage — reinterpreting it as 2^64-x would turn a
// damaged row into a plausible reading, which is worse than refusing it.
func TestNodeHostReadRefusesANegativeCounter(t *testing.T) {
	repo, db := hostTestRepos(t)
	ctx := context.Background()
	agent, sampleID := "agt_1", strings.Repeat("a", 32)
	if _, err := repo.Persist(ctx, domain.NodeHostPersistRequest{
		Observation: hostObservation(agent, sampleID, strings.Repeat("b", 64), hostBaseTime),
		Sample:      hostSample(agent, sampleID, strings.Repeat("b", 64), hostBaseTime),
	}); err != nil {
		t.Fatal(err)
	}
	// Write the damage directly: nothing in the repository can produce it.
	if err := db.Exec(`UPDATE node_host_metric_samples SET system_cpu_total = -1`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RawRange(ctx, agent, hostBaseTime.Add(-time.Hour), hostBaseTime.Add(time.Hour), false); err == nil {
		t.Fatal("a negative counter was read back as a value")
	}
}

// Large counters have to survive all three dialects identically, which is why the
// wire caps them at MaxInt64 in the first place.
func TestNodeHostCountersRoundTripAtTheBoundary(t *testing.T) {
	repo, _ := hostTestRepos(t)
	ctx := context.Background()
	agent, sampleID := "agt_1", strings.Repeat("a", 32)
	maximum := uint64(math.MaxInt64)

	sample := hostSample(agent, sampleID, strings.Repeat("b", 64), hostBaseTime)
	sample.SystemCPUTotal = &maximum
	sample.SystemCPUIdle = &maximum
	if _, err := repo.Persist(ctx, domain.NodeHostPersistRequest{
		Observation: hostObservation(agent, sampleID, strings.Repeat("b", 64), hostBaseTime),
		Sample:      sample,
	}); err != nil {
		t.Fatal(err)
	}
	samples, err := repo.RawRange(ctx, agent, hostBaseTime.Add(-time.Hour), hostBaseTime.Add(time.Hour), false)
	if err != nil || len(samples) != 1 {
		t.Fatalf("boundary sample = (%d rows, %v)", len(samples), err)
	}
	if samples[0].SystemCPUTotal == nil || *samples[0].SystemCPUTotal != maximum {
		t.Fatalf("system_cpu_total = %v, want %d", samples[0].SystemCPUTotal, maximum)
	}
	if samples[0].SystemCPUIOWait != nil {
		t.Fatal("a column that was never set came back as a value")
	}
}
