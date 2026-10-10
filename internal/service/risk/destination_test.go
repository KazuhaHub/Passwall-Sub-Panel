package risk

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type destinationReaderFunc func(context.Context, time.Time) (map[int64]domain.DestBlockInput, error)

func (f destinationReaderFunc) ReadDestinationRisk(ctx context.Context, at time.Time) (map[int64]domain.DestBlockInput, error) {
	return f(ctx, at)
}

func TestDestinationBulkRefreshUsesGroupPolicyAndIgnoresLocationTrust(t *testing.T) {
	h := newHarness(usersInGroups(1, 1, 2, 3, 4))
	h.trust = &fakeTrust{ids: []int64{1, 2, 3}}
	h.settings.groups = map[int64]ports.UISettings{
		1: {RiskDestBlockThreshold: 20, GeoAnomalyScope: "off"},
		2: {RiskDestBlockThreshold: 50},
		3: {RiskDestBlockOff: true},
	}
	h.settings.groupErr = map[int64]error{4: errors.New("group unavailable")}
	s := h.service()
	calls := 0
	type contextKey struct{}
	ctx := context.WithValue(t.Context(), contextKey{}, "admitted")
	s.d.Destination = destinationReaderFunc(func(readCtx context.Context, at time.Time) (map[int64]domain.DestBlockInput, error) {
		calls++
		if !at.Equal(refreshNow) || readCtx.Value(contextKey{}) != "admitted" {
			t.Error("bulk read lost the run clock or admitted context")
		}
		return map[int64]domain.DestBlockInput{
			1:   {CollectingNodes: 1, Sources: []domain.DestBlockSource{{Source: "p12", Count: 37}}, Losses: domain.DestAuditLosses{Events: 3}},
			3:   {CollectingNodes: 2, Sources: []domain.DestBlockSource{{Source: "p12", Count: 37}}},
			4:   {CollectingNodes: 1, Sources: []domain.DestBlockSource{{Source: "p12", Count: 37}}},
			5:   {CollectingNodes: 1, Sources: []domain.DestBlockSource{{Source: "p12", Count: 37}}},
			999: {CollectingNodes: 1},
		}, nil
	})
	before := outcomes()
	if err := s.RefreshOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("bulk reads = %d", calls)
	}
	wantOutcome(t, before, "partial")
	rows := h.store.saved(t)
	for id, want := range map[int64]domain.GeoState{1: domain.GeoStateFlagged, 2: domain.GeoStateUnknown, 3: domain.GeoStateSuspect, 4: domain.GeoStateDisabled} {
		if row, ok := rows[id][domain.RiskKind("dest_block")]; !ok || row.State != want {
			t.Errorf("account %d destination verdict = %+v, want %s", id, row, want)
		}
	}
	for _, id := range []int64{5, 999} {
		if _, ok := rows[id][domain.RiskKind("dest_block")]; ok {
			t.Errorf("unreadable group or unlisted account %d was judged", id)
		}
	}
	var ev domain.DestBlockEvidence
	if err := json.Unmarshal(rows[1][domain.RiskKind("dest_block")].Evidence, &ev); err != nil || ev.Total != 37 || ev.Threshold != 20 || ev.Nodes != 1 || ev.CoverageComplete || ev.Losses.Complete || ev.Losses.Events != 3 {
		t.Fatalf("stored lower-bound evidence = %+v, err=%v", ev, err)
	}
}

func TestDestinationSourceFailurePreservesEnabledRowsButSavesExplicitOff(t *testing.T) {
	for _, trustErr := range []bool{false, true} {
		t.Run(map[bool]string{false: "trusted", true: "trust_unreadable"}[trustErr], func(t *testing.T) {
			h := newHarness(usersInGroups(1, 2))
			h.trust = &fakeTrust{ids: []int64{1}}
			if trustErr {
				h.trust.err = errors.New("trust unavailable")
			}
			h.settings.groups = map[int64]ports.UISettings{2: {RiskDestBlockOff: true}}
			s := h.service()
			fail := false
			s.d.Destination = destinationReaderFunc(func(context.Context, time.Time) (map[int64]domain.DestBlockInput, error) {
				data := map[int64]domain.DestBlockInput{1: {CollectingNodes: 1, Sources: []domain.DestBlockSource{{Source: "p1", Count: 37}}}}
				if fail {
					return data, errors.New("private source detail")
				}
				return data, nil
			})
			if err := s.RefreshOnce(t.Context()); err != nil {
				t.Fatal(err)
			}
			if row := h.store.saved(t)[1][domain.RiskKind("dest_block")]; row.State != domain.GeoStateFlagged {
				t.Fatalf("trust masked destination block: %+v", row)
			}
			fail = true
			before := outcomes()
			if err := s.RefreshOnce(t.Context()); err != nil {
				t.Fatal(err)
			}
			wantOutcome(t, before, "partial")
			rows := h.store.saved(t)
			if _, overwritten := rows[1][domain.RiskKind("dest_block")]; overwritten {
				t.Fatal("failed source overwrote the enabled signal")
			}
			if row := rows[2][domain.RiskKind("dest_block")]; row.State != domain.GeoStateDisabled || row.Code != domain.RiskCodeSignalOff || row.Evidence != nil {
				t.Fatalf("explicit off needed counters: %+v", row)
			}
		})
	}
}

func TestDestinationCancellationSavesNoVerdicts(t *testing.T) {
	h := newHarness(usersInGroups(1))
	s := h.service()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.d.Destination = destinationReaderFunc(func(context.Context, time.Time) (map[int64]domain.DestBlockInput, error) {
		cancel()
		return map[int64]domain.DestBlockInput{1: {CollectingNodes: 1}}, nil
	})
	if err := s.RefreshOnce(ctx); !errors.Is(err, context.Canceled) || h.store.saveCount() != 0 {
		t.Fatalf("cancelled refresh saved verdicts: err=%v saves=%d", err, h.store.saveCount())
	}
}

func TestDestinationAllExplicitlyOffDoesNotReadTelemetry(t *testing.T) {
	h := newHarness(usersInGroups(1, 1))
	h.settings.global.RiskDestBlockOff = true
	s := h.service()
	s.d.Destination = destinationReaderFunc(func(context.Context, time.Time) (map[int64]domain.DestBlockInput, error) {
		t.Fatal("off detector read telemetry")
		return nil, nil
	})
	if err := s.RefreshOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	for id, rows := range h.store.saved(t) {
		if r := rows[domain.RiskKindDestBlock]; r.State != domain.GeoStateDisabled || r.Code != domain.RiskCodeSignalOff {
			t.Fatalf("account %d did not save off verdict: %+v", id, r)
		}
	}
}
