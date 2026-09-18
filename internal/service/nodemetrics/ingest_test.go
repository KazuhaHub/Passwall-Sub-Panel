package nodemetrics

import (
	"context"
	"errors"
	"testing"
	"time"

	nodeprotocol "github.com/KazuhaHub/passwall-node/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

// stubRepo records what the ingest asked it to do, and can be made to fail.
type stubRepo struct {
	persists  []domain.NodeHostPersistRequest
	result    domain.NodeHostPersistResult
	err       error
	previous  []domain.NodeHostMetricSample
	rangeCall int
	agentIDs  []string
	names     []string
}

func (r *stubRepo) AgentIDs(context.Context) ([]string, error) { return r.agentIDs, nil }

func (r *stubRepo) InterfaceNames(context.Context, string) ([]string, error) { return r.names, nil }

func (r *stubRepo) Persist(_ context.Context, request domain.NodeHostPersistRequest) (domain.NodeHostPersistResult, error) {
	r.persists = append(r.persists, request)
	if r.err != nil {
		return domain.NodeHostPersistResult{}, r.err
	}
	result := r.result
	if result == (domain.NodeHostPersistResult{}) {
		result = domain.NodeHostPersistResult{LatestUpdated: true, HistoryInserted: request.Sample != nil}
	}
	return result, nil
}

func (r *stubRepo) Latest(context.Context, string) (*domain.NodeHostObservation, error) {
	return nil, domain.ErrNotFound
}

func (r *stubRepo) LatestBatchByPanelIDs(context.Context, []int64) (map[int64]domain.NodeHostSummary, error) {
	return nil, nil
}

func (r *stubRepo) RawRange(context.Context, string, time.Time, time.Time, bool) ([]domain.NodeHostMetricSample, error) {
	r.rangeCall++
	return r.previous, nil
}

func (r *stubRepo) InterfaceRange(context.Context, string, string, time.Time, time.Time) ([]domain.NodeInterfaceMetricSample, error) {
	return nil, nil
}

func (r *stubRepo) HourlyRange(context.Context, string, time.Time, time.Time) ([]domain.NodeHostMetricHourly, error) {
	return nil, nil
}

func (r *stubRepo) UpsertHourly(context.Context, []domain.NodeHostMetricHourly) error { return nil }

func (r *stubRepo) DeleteByAgentID(context.Context, string) error { return nil }

func (r *stubRepo) Prune(context.Context, domain.NodeHostPruneRequest) (domain.NodeHostPruneResult, error) {
	return domain.NodeHostPruneResult{}, nil
}

var ingestBaseTime = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

func validObservation(sampleID string, collectedAt time.Time) *nodeprotocol.HostObservation {
	totalInodes, availableInodes := uint64(1000), uint64(500)
	return &nodeprotocol.HostObservation{
		SampleID: sampleID, CollectedAtMS: collectedAt.UnixMilli(), UptimeMS: 1000,
		BootID: "boot-1",
		Scope: nodeprotocol.HostScope{
			Deployment: nodeprotocol.DeploymentSystemd, ResourceScope: nodeprotocol.ScopeHost,
			DataFilesystemScope: nodeprotocol.FilesystemScopeHostMount,
		},
		Platform: nodeprotocol.PlatformObservation{OS: "linux", Arch: "amd64", LogicalCPUs: 4},
		CPU: &nodeprotocol.CPUObservation{
			System: &nodeprotocol.SystemCPUObservation{CounterEpoch: "boot-1", Total: 1000, Idle: 400, IOWait: 10},
		},
		Filesystem: &nodeprotocol.FilesystemObservation{
			TotalBytes: 1000, AvailableBytes: 500,
			TotalInodes: &totalInodes, AvailableInodes: &availableInodes,
		},
	}
}

func newIngestService(t *testing.T, repo *stubRepo) *Service {
	t.Helper()
	service, err := New(Options{Repo: repo, Now: func() time.Time { return ingestBaseTime }})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// A NODE THAT SENDS NO TELEMETRY IS THE ORDINARY CASE, not an error: the agent
// may be old, or this round may simply not be due.
func TestIngestWithoutASampleWritesNothing(t *testing.T) {
	repo := &stubRepo{}
	service := newIngestService(t, repo)
	result := service.Ingest(t.Context(), "agt_1", nil, ingestBaseTime)
	if result.Outcome != metrics.NodeHostOutcomeNoHost {
		t.Fatalf("outcome = %q", result.Outcome)
	}
	if len(repo.persists) != 0 {
		t.Fatal("a report with no telemetry reached the repository")
	}
}

// A SAMPLE THE CONTRACT REFUSES MUST NOT REACH SQL, from any direction. The wire
// boundary checks this too; this is the entry point a non-HTTP caller reaches.
func TestIngestDropsAnInvalidSampleBeforeStorage(t *testing.T) {
	repo := &stubRepo{}
	service := newIngestService(t, repo)
	invalid := validObservation("not-a-sample-id", ingestBaseTime)
	result := service.Ingest(t.Context(), "agt_1", invalid, ingestBaseTime)
	if result.Outcome != metrics.NodeHostOutcomeInvalid {
		t.Fatalf("outcome = %q", result.Outcome)
	}
	if len(repo.persists) != 0 {
		t.Fatal("an invalid sample reached the repository")
	}
}

// THE PROPERTY THE WHOLE FEATURE RESTS ON. A storage failure is invisible to the
// node by design, so it must also be invisible to the sync result — the agent
// gets its roster and its quota regardless.
func TestIngestNeverReturnsAnError(t *testing.T) {
	repo := &stubRepo{err: errors.New("disk on fire")}
	service := newIngestService(t, repo)
	result := service.Ingest(t.Context(), "agt_1", validObservation("0123456789abcdef0123456789abcdef", ingestBaseTime), ingestBaseTime)
	if result.Outcome != metrics.NodeHostOutcomeStorageError {
		t.Fatalf("outcome = %q", result.Outcome)
	}
	// The signature has no error to return, which is the point: the caller cannot
	// propagate one even by accident.
}

// The agent may report far more often than the panel charts; the interval is what
// bounds storage.
func TestIngestThrottlesHistoryWithinTheWriteInterval(t *testing.T) {
	previous := &domain.NodeHostMetricSample{
		SampleID: "0123456789abcdef0123456789abcdef", BootID: "boot-1",
		ResourceScope: "host", ReceivedAt: ingestBaseTime,
	}
	repo := &stubRepo{previous: []domain.NodeHostMetricSample{*previous}}
	service := newIngestService(t, repo)

	// Thirty seconds later: within the interval, so only the snapshot moves.
	near := ingestBaseTime.Add(30 * time.Second)
	result := service.Ingest(t.Context(), "agt_1", validObservation("0123456789abcdef0123456789abcdee", near), near)
	if result.HistoryOutcome != "" {
		t.Fatalf("a throttled round wrote history: %q", result.HistoryOutcome)
	}
	if len(repo.persists) != 1 || repo.persists[0].Sample != nil {
		t.Fatal("the throttled round carried a history row")
	}

	// Seventy seconds later: due.
	far := ingestBaseTime.Add(70 * time.Second)
	result = service.Ingest(t.Context(), "agt_1", validObservation("0123456789abcdef0123456789abcdef"[0:31]+"f", far), far)
	if result.HistoryOutcome != metrics.NodeHostHistoryInserted {
		t.Fatalf("a due round did not write history: %q", result.HistoryOutcome)
	}
}

// A BREAK IS ALWAYS DUE. The interval bounds storage; it must not suppress the
// discontinuity a chart exists to mark.
func TestIngestWritesHistoryImmediatelyOnABreak(t *testing.T) {
	previous := &domain.NodeHostMetricSample{
		SampleID: "0123456789abcdef0123456789abcdef", BootID: "boot-1",
		ResourceScope: "host", ReceivedAt: ingestBaseTime,
	}
	cases := []struct {
		name   string
		mutate func(*nodeprotocol.HostObservation)
	}{
		{"a reboot", func(observation *nodeprotocol.HostObservation) { observation.BootID = "boot-2" }},
		{"a scope change", func(observation *nodeprotocol.HostObservation) {
			observation.Scope.ResourceScope = nodeprotocol.ScopeMixed
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repo := &stubRepo{previous: []domain.NodeHostMetricSample{*previous}}
			service := newIngestService(t, repo)
			observation := validObservation("0123456789abcdef0123456789abcdee", ingestBaseTime.Add(10*time.Second))
			testCase.mutate(observation)
			result := service.Ingest(t.Context(), "agt_1", observation, ingestBaseTime.Add(10*time.Second))
			if result.HistoryOutcome != metrics.NodeHostHistoryInserted {
				t.Fatalf("a break was throttled: %q", result.HistoryOutcome)
			}
		})
	}
}

// THE REFRESH WINDOW CLOSES ON A DIFFERENT SAMPLE, not on any sample. A sample
// already in flight when the operator hit refresh would otherwise close it
// immediately and the refresh would appear to have done nothing.
func TestRefreshWindowClosesOnANewSampleOrOnExpiry(t *testing.T) {
	repo := &stubRepo{}
	now := ingestBaseTime
	service, err := New(Options{Repo: repo, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	service.RememberSample("agt_1", "0123456789abcdef0123456789abcdef")

	if service.WantsHostReport("agt_1") {
		t.Fatal("a window was open before anything asked for one")
	}
	service.RequestRefresh("agt_1")
	if !service.WantsHostReport("agt_1") {
		t.Fatal("the refresh window did not open")
	}
	// Still the baseline sample: the window stays open.
	if !service.WantsHostReport("agt_1") {
		t.Fatal("the window closed before a new sample arrived")
	}
	// A new sample answers it.
	service.RememberSample("agt_1", "0123456789abcdef0123456789abcdee")
	if service.WantsHostReport("agt_1") {
		t.Fatal("the window stayed open after the sample it waited for arrived")
	}

	// And a window nobody answers expires rather than asking forever.
	service.RequestRefresh("agt_1")
	now = now.Add(refreshWindowLifetime + time.Second)
	if service.WantsHostReport("agt_1") {
		t.Fatal("an unanswered refresh window did not expire")
	}
}
