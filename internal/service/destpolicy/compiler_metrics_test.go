package destpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func compileMetricValue(result string) int64 {
	for _, counter := range metrics.Take().Counters {
		if counter.Name == "psp_dest_policy_compile_total{result="+result+"}" {
			return counter.Value
		}
	}
	return 0
}

type metricsQuotaInputs struct{ cacheCompilerInputs }

func (i *metricsQuotaInputs) ForNode(ctx context.Context, panel int64, snapshot *ports.NativeDesiredSnapshot, members bool) (RosterInput, RosterInput, error) {
	r, q, err := i.cacheCompilerInputs.ForNode(ctx, panel, snapshot, members)
	q.UserGroups = map[int64]int64{}
	q.UserIDs = nil
	for id := int64(1); id <= protocol.MaxDestinationSubjects+1; id++ {
		q.UserIDs = append(q.UserIDs, id)
		q.UserGroups[id] = 8
	}
	return r, q, err
}

func TestCompilerMetricsIdentifySniffingAndQuotaFallbackAfterRealPreflight(t *testing.T) {
	for _, reason := range []string{"sniffing", "over_limit"} {
		t.Run(reason, func(t *testing.T) {
			c, inputs, runtime, agent, snapshot, base := cachedCompilerFixture(t)
			defs := domain.DestDefinitions{State: domain.DestPolicyState{Generation: 1, PublishedGeneration: 1}, Policies: []domain.DestPolicy{{ID: 1, Enabled: true, Action: domain.DestBlock, Scope: domain.DestScopeGroups, GroupIDs: []int64{8}, Inline: domain.DestInline{Ports: "443"}}}}
			want := metrics.DestCompileFallbackOverLimit
			if reason == "sniffing" {
				defs.Policies[0].Inline.Protocols = []string{"bittorrent"}
				base.Core.Engine = "xray"
				base.Listeners = []protocol.Listener{sniffListener(t, 1, true, `{"enabled":true,"metadataOnly":true,"destOverride":["http","tls"]}`)}
				want = metrics.DestCompileFallbackSniffing
			} else {
				c.inputs = &metricsQuotaInputs{*inputs}
			}
			body, err := BuildDefinitionSnapshot(defs)
			if err != nil {
				t.Fatal(err)
			}
			c.definitions.(*cachedPublicationStore).snapshot.Body = body
			lkg := confirmedPortLKG(t)
			runtime.state.AppliedSHA256 = lkg.AppliedSHA256
			runtime.body = lkg.AppliedBody
			before := compileMetricValue(want)
			candidate, err := c.Compile(t.Context(), agent, snapshot, []string{protocol.CapabilityDestinationPolicy}, base)
			if err != nil || candidate.Mint.Kind != domain.DestCandidateFallback || runtime.state.FallbackReason != reason || compileMetricValue(want) != before+1 {
				t.Fatalf("real %s fallback not classified: err=%v kind=%s reason=%s", reason, err, candidate.Mint.Kind, runtime.state.FallbackReason)
			}
		})
	}
}

func TestCompilerMetricsDistinguishExecutableFallbackFromEmptyAndCachedFallback(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "confirmed"}[confirmed], func(t *testing.T) {
			c, _, runtime, agent, snapshot, base := cachedCompilerFixture(t)
			first, err := c.Compile(t.Context(), agent, snapshot, []string{protocol.CapabilityDestinationPolicy}, base)
			if err != nil {
				t.Fatal(err)
			}
			runtime.state.RejectedGeneration, runtime.state.RejectedContext = 1, first.Mint.Context
			runtime.state.FallbackReason = "rejected"
			want := metrics.DestCompileFallbackNil
			if confirmed {
				runtime.body, err = json.Marshal(first.Policy)
				if err != nil {
					t.Fatal(err)
				}
				runtime.state.AppliedSHA256 = protocol.PolicyDigest(first.Policy)
				want = metrics.DestCompileFallbackRejected
			}
			before, hit, samples := compileMetricValue(want), compileMetricValue(metrics.DestCompileCacheHit), compileMetricSamples()
			for range 2 {
				candidate, err := c.Compile(t.Context(), agent, snapshot, []string{protocol.CapabilityDestinationPolicy}, base)
				if err != nil || (candidate.Mint.Kind == domain.DestCandidateFallback) != confirmed {
					t.Fatal("fallback fixture failed")
				}
			}
			if compileMetricValue(want) != before+1 || compileMetricValue(metrics.DestCompileCacheHit) != hit+1 || compileMetricSamples() != samples+2 {
				t.Fatal("fallback outcome, cache or timing double counted")
			}
		})
	}
}
func compileMetricSamples() int64 {
	for _, h := range metrics.Take().Histograms {
		if h.Name == "psp_dest_policy_compile_ms" {
			return h.Count
		}
	}
	return 0
}
func TestCompilerMetricsObserveActualColdWarmAndFailedAttempts(t *testing.T) {
	c, _, runtime, agent, snapshot, base := cachedCompilerFixture(t)
	compiled, hit, invalid, samples := compileMetricValue("compiled"), compileMetricValue("cache_hit"), compileMetricValue("invalid"), compileMetricSamples()
	for range 2 {
		if _, err := c.Compile(t.Context(), agent, snapshot, []string{protocol.CapabilityDestinationPolicy}, base); err != nil {
			t.Fatal(err)
		}
	}
	runtime.fail = true
	if _, err := c.Compile(t.Context(), agent, snapshot, []string{protocol.CapabilityDestinationPolicy}, base); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("fixture did not fail")
	}
	if compileMetricValue("compiled") != compiled+1 || compileMetricValue("cache_hit") != hit+1 || compileMetricValue("invalid") != invalid+1 || compileMetricSamples() != samples+3 {
		t.Fatal("compiler metrics omitted actual attempts or labeled failed persistence as a cache success")
	}
}
