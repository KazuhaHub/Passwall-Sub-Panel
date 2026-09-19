package compatadmission

import (
	"testing"
	"time"
)

var now = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

func policy() Policy {
	return Policy{
		Revision:           "2026-09-19.1",
		MinProtocolVersion: 1,
		MaxProtocolVersion: 1,
		LegacyZeroMapsTo:   1,
		MaxObservationAge:  time.Hour,
		RequiredCapabilities: map[Operation][]string{
			OperationBaseSync:      {},
			OperationConfigWrite:   {"client.write.v1"},
			OperationRemoteUpgrade: {"task.execution.v1", "task.expiry.v1", "task.agent.upgrade.v1"},
		},
		KnownBad: map[string]string{},
	}
}

func fresh(protocol int, capabilities ...string) *Observation {
	return &Observation{ObservedAt: now.Add(-time.Minute), ProtocolVersion: protocol, Capabilities: capabilities}
}

func decide(t *testing.T, mutate func(*Request)) Decision {
	t.Helper()
	request := Request{
		PeerVersion: "v0.0.1-beta11",
		Operation:   OperationBaseSync,
		Observed:    fresh(1, "client.write.v1", "task.execution.v1", "task.expiry.v1", "task.agent.upgrade.v1"),
		Now:         now,
		Policy:      policy(),
	}
	if mutate != nil {
		mutate(&request)
	}
	return Decide(request)
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Request)
		allowed bool
		status  Status
		reason  Reason
	}{
		{
			name:    "a fresh observation that satisfies the operation is allowed",
			mutate:  nil,
			allowed: true, status: StatusVerified, reason: ReasonAllowed,
		},
		{
			name:    "a plain read never needs a compatibility verdict",
			mutate:  func(r *Request) { r.Operation = OperationRead; r.Observed = nil },
			allowed: true, status: StatusVerified, reason: ReasonAllowed,
		},
		{
			// Never observed, so nothing is known — and "nothing is known" must
			// not read as "fine". The read case above is the only exception,
			// because a read is what establishes the observation.
			name:   "an operation with no observation at all is not allowed",
			mutate: func(r *Request) { r.Observed = nil },
			status: StatusUnverified, reason: ReasonUnverified,
		},
		{
			// A network failure must not erase the last valid observation, so the
			// stale one is still carried — but it may not authorise high-risk
			// work, and it must not be presented as freshly verified.
			name: "a stale observation blocks a high-risk operation",
			mutate: func(r *Request) {
				r.Operation = OperationRemoteUpgrade
				r.Observed.ObservedAt = now.Add(-2 * time.Hour)
				r.UpgradeEdgeVerified = true
			},
			status: StatusUnverified, reason: ReasonObservationStale,
		},
		{
			name:    "a stale observation still carries base sync",
			mutate:  func(r *Request) { r.Observed.ObservedAt = now.Add(-2 * time.Hour) },
			allowed: true, status: StatusLimited, reason: ReasonAllowed,
		},
		{
			// Capabilities are a current fact, not a sticky promise: the same
			// agent that could upgrade an hour ago is refused once the helper is
			// disabled and the capability disappears from its report.
			name: "a capability the operation requires and the peer withdrew is a refusal",
			mutate: func(r *Request) {
				r.Operation = OperationRemoteUpgrade
				r.Observed.Capabilities = []string{"task.execution.v1"}
				r.UpgradeEdgeVerified = true
			},
			status: StatusLimited, reason: ReasonCapabilityMissing,
		},
		{
			name:   "a protocol generation outside the reviewed range is incompatible",
			mutate: func(r *Request) { r.Observed.ProtocolVersion = 7 },
			status: StatusIncompatible, reason: ReasonProtocolIncompatible,
		},
		{
			// The legacy spelling. A zero version is protocol v1, and cannot be
			// generalised into "accept anything missing".
			name:    "an omitted version is the legacy spelling of v1",
			mutate:  func(r *Request) { r.Observed.ProtocolVersion = 0 },
			allowed: true, status: StatusVerified, reason: ReasonAllowed,
		},
		{
			name:   "a remote upgrade with no verified edge is refused on the edge, not on evidence",
			mutate: func(r *Request) { r.Operation = OperationRemoteUpgrade; r.UpgradeEdgeVerified = false },
			status: StatusLimited, reason: ReasonUpgradeEdgeMissing,
		},
		{
			name: "a known-bad peer version is refused whatever else it reports",
			mutate: func(r *Request) {
				r.PeerVersion = "v0.0.1-beta7"
				r.Policy.KnownBad = map[string]string{"v0.0.1-beta7": "installer regression"}
			},
			status: StatusIncompatible, reason: ReasonKnownBad,
		},
		{
			// Force is for evidence that is merely thin. It is not a way past a
			// protocol generation the panel cannot speak, and not a way past a
			// release that is known to break.
			name:   "force cannot override a hard protocol incompatibility",
			mutate: func(r *Request) { r.Observed.ProtocolVersion = 7; r.Force = true },
			status: StatusIncompatible, reason: ReasonProtocolIncompatible,
		},
		{
			name:    "force can carry an operation whose only problem is thin evidence",
			mutate:  func(r *Request) { r.Observed = nil; r.Force = true },
			allowed: true, status: StatusUnverified, reason: ReasonAllowed,
		},
		{
			name: "force cannot invent a capability the peer does not have",
			mutate: func(r *Request) {
				r.Operation = OperationRemoteUpgrade
				r.Observed.Capabilities = nil
				r.Force = true
				r.UpgradeEdgeVerified = true
			},
			status: StatusLimited, reason: ReasonCapabilityMissing,
		},
		{
			name:   "a known-bad release is not overridable either",
			mutate: func(r *Request) { r.Policy.KnownBad = map[string]string{"v0.0.1-beta11": "withdrawn"}; r.Force = true },
			status: StatusIncompatible, reason: ReasonKnownBad,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decide(t, tc.mutate)
			if got.Allowed != tc.allowed {
				t.Fatalf("allowed = %v, want %v (reason %s: %s)", got.Allowed, tc.allowed, got.Reason, got.Detail)
			}
			if got.Status != tc.status {
				t.Fatalf("status = %s, want %s", got.Status, tc.status)
			}
			if got.Reason != tc.reason {
				t.Fatalf("reason = %s, want %s", got.Reason, tc.reason)
			}
			// The reason a caller branches on must never be empty, and the
			// revision must always name the policy it was decided under, so a
			// stored decision can be re-read after the policy moves.
			if got.Detail == "" {
				t.Fatal("every decision must explain itself, including the allowed ones")
			}
			if got.EvidenceRevision != "2026-09-19.1" {
				t.Fatalf("evidence revision = %q, want the policy revision", got.EvidenceRevision)
			}
		})
	}
}

// The same request re-evaluated against a newer observation must be able to
// change its answer. A cached verdict that outlives the observation is how a
// withdrawn capability keeps working.
func TestDecideReevaluatesRatherThanCaching(t *testing.T) {
	request := Request{
		PeerVersion:         "v0.0.1-beta11",
		Operation:           OperationRemoteUpgrade,
		Observed:            fresh(1, "task.execution.v1", "task.expiry.v1", "task.agent.upgrade.v1"),
		Now:                 now,
		Policy:              policy(),
		UpgradeEdgeVerified: true,
	}
	if got := Decide(request); !got.Allowed {
		t.Fatalf("the first evaluation should allow it: %s", got.Detail)
	}
	// The same agent reports again without the upgrade capability.
	request.Observed = fresh(1, "task.execution.v1")
	if got := Decide(request); got.Allowed || got.Reason != ReasonCapabilityMissing {
		t.Fatalf("re-evaluation must refuse: allowed=%v reason=%s", got.Allowed, got.Reason)
	}
}

// A decision has to be reproducible from what it recorded, so the reason codes
// are part of the contract rather than free text.
func TestReasonCodesAreStableStrings(t *testing.T) {
	for _, reason := range []Reason{
		ReasonAllowed, ReasonProtocolIncompatible, ReasonCapabilityMissing,
		ReasonUnverified, ReasonObservationStale, ReasonUpgradeEdgeMissing, ReasonKnownBad,
	} {
		if string(reason) == "" || string(reason) == " " {
			t.Fatalf("reason code %q is not usable by a caller", reason)
		}
	}
}

// The nil-policy case must not panic: a caller that has not loaded a policy yet
// is exactly the "no observation" situation, not a crash.
func TestDecideWithNoPolicyIsUnverified(t *testing.T) {
	got := Decide(Request{Operation: OperationBaseSync, Now: now})
	if got.Allowed || got.Reason != ReasonUnverified {
		t.Fatalf("an empty policy must read as unverified, got allowed=%v reason=%s", got.Allowed, got.Reason)
	}
}
