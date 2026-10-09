package nodesync

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-protocol/protocol"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type failingCachedPolicyMint struct{ ports.NodePolicyCandidateRepo }

func (f failingCachedPolicyMint) MintConfigWithPolicyCandidate(_ context.Context, _ string, canonical []byte, _ domain.DestPolicyMint, _ time.Time) (*domain.NodeAgentStream, bool, error) {
	if len(canonical) > 0 {
		canonical[0] = 0
	}
	return nil, false, domain.ErrUnavailable
}

func cachePolicySyncFixture(t *testing.T) (*configAppliedFixture, *PolicyCandidate, *int) {
	t.Helper()
	f := newConfigAppliedFixture(t)
	f.service.policyCandidates = f.repos.NodeAgent.(ports.NodePolicyCandidateRepo)
	candidate := policySyncCandidate()
	candidate.CacheKey = "immutable-compiled-policy"
	f.service.policies = &testPolicyCoordinator{
		observe: func(context.Context, string, *protocol.PolicyStatus, []string) error { return nil },
		compile: func(context.Context, *domain.NodeAgent, *ports.NativeDesiredSnapshot, []string, protocol.ConfigBody) (PolicyCandidate, error) {
			return candidate, nil
		},
	}
	encoded := new(int)
	f.service.policyConfigEncode = func(body protocol.ConfigBody) ([]byte, error) {
		*encoded++
		return json.Marshal(body)
	}
	return f, &candidate, encoded
}

func TestPolicyConfigCacheSkipsEncodingButStillMintsChangedSource(t *testing.T) {
	f, candidate, encoded := cachePolicySyncFixture(t)
	report := policySyncReport(f)
	first, err := f.service.Sync(t.Context(), report)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Mint.Generation = 2
	second, err := f.service.Sync(t.Context(), report)
	if err != nil {
		t.Fatal(err)
	}
	state, err := f.repos.DestAgentPolicy.Get(t.Context(), f.agent.AgentID, false)
	if err != nil || state.MintedGeneration != 2 || first.Config.ETag != second.Config.ETag || *encoded != 1 {
		t.Fatalf("cached source-only mint: state=%+v encoded=%d etag changed=%t / %v", state, *encoded, first.Config.ETag != second.Config.ETag, err)
	}
	f.service.policyCandidates = failingCachedPolicyMint{f.service.policyCandidates}
	if _, err := f.service.Sync(t.Context(), report); !errors.Is(err, domain.ErrUnavailable) || *encoded != 1 {
		t.Fatalf("cached bytes skipped failed persistence: encoded=%d / %v", *encoded, err)
	}
	f.service.policyCandidates = f.repos.NodeAgent.(ports.NodePolicyCandidateRepo)
	recovered, err := f.service.Sync(t.Context(), report)
	if err != nil || recovered.Config.ETag != first.Config.ETag || *encoded != 1 {
		t.Fatalf("failed adapter mutated cached canonical bytes: encoded=%d / %v", *encoded, err)
	}
}

func TestPolicyConfigCacheInvalidatesBaseAndCompileKeyAndDoesNotCacheUntracked(t *testing.T) {
	f, candidate, encoded := cachePolicySyncFixture(t)
	report := policySyncReport(f)
	first, err := f.service.Sync(t.Context(), report)
	if err != nil {
		t.Fatal(err)
	}
	changed := f.storedNode(t)
	changed.DesiredPort++
	if err := f.repos.Node.UpdateInboundConfig(t.Context(), changed); err != nil {
		t.Fatal(err)
	}
	second, err := f.service.Sync(t.Context(), report)
	if err != nil || first.Config.ETag == second.Config.ETag || *encoded != 2 {
		t.Fatalf("base change reused bytes: encoded=%d / %v", *encoded, err)
	}
	// A changed immutable compiler key must encode the new policy even when
	// the listener configuration is identical.
	candidate.CacheKey = "different-compiled-policy"
	candidate.Policy.Rules[0].Ports = "80"
	candidate.Mint.DesiredSHA256 = protocol.PolicyDigest(candidate.Policy)
	third, err := f.service.Sync(t.Context(), report)
	if err != nil || third.Config.ETag == second.Config.ETag || third.Config.Body.Policy.Rules[0].Ports != "80" || *encoded != 3 {
		t.Fatalf("policy change reused bytes: encoded=%d / %v", *encoded, err)
	}
	candidate.CacheKey = ""
	for range 2 {
		if _, err := f.service.Sync(t.Context(), report); err != nil {
			t.Fatal(err)
		}
	}
	if *encoded != 5 {
		t.Fatalf("untracked compiler result cached: %d", *encoded)
	}
}

func TestPolicyConfigCacheDoesNotCacheEncodingErrors(t *testing.T) {
	f, _, encoded := cachePolicySyncFixture(t)
	encode := f.service.policyConfigEncode
	f.service.policyConfigEncode = func(protocol.ConfigBody) ([]byte, error) { return nil, domain.ErrUnavailable }
	if _, err := f.service.Sync(t.Context(), policySyncReport(f)); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("encoding error ignored: %v", err)
	}
	f.service.policyConfigEncode = encode
	for range 2 {
		if _, err := f.service.Sync(t.Context(), policySyncReport(f)); err != nil {
			t.Fatal(err)
		}
	}
	if *encoded != 1 {
		t.Fatalf("failed encoding poisoned/hot encoding missed cache: %d", *encoded)
	}
}
