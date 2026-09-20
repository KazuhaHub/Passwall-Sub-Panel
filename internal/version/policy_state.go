package version

import (
	"sync/atomic"
	"time"
)

// The active release policy.
//
// ONE SOURCE, NO SECOND OPINION. Once an authenticated policy is in force it is
// the only thing that decides which releases may be offered and along which
// paths. The compat manifest's own edge list is a FALLBACK for the state before
// any policy exists — not an alternative that stays available afterwards. Two
// lists that can each authorise an upgrade is how a target nobody reviewed
// stays reachable through the list that was not updated.
type policyState struct {
	policy *ReleasesPolicy
}

var activeReleasesPolicy atomic.Value // policyState

// SetActiveReleasesPolicy installs an authenticated, validated policy. Passing
// nil clears it, which returns the panel to the manifest-only fallback — the
// state a deployment is in before it has been given a trust root and a policy.
func SetActiveReleasesPolicy(policy *ReleasesPolicy) {
	if policy == nil {
		activeReleasesPolicy.Store(policyState{})
		return
	}
	copied := *policy
	activeReleasesPolicy.Store(policyState{policy: &copied})
}

// ActiveReleasesPolicy returns the policy in force, or nil when none is
// installed.
func ActiveReleasesPolicy() *ReleasesPolicy {
	state, _ := activeReleasesPolicy.Load().(policyState)
	if state.policy == nil {
		return nil
	}
	copied := *state.policy
	return &copied
}

// PolicyInForce reports whether an authenticated policy that applies to THIS
// build is deciding. A policy installed for another build is not in force here.
func PolicyInForce() bool { return applicablePolicy(Version) != nil }

// PolicyOffersRelease reports whether the policy in force lists this release as
// an upgrade target.
//
// A release with no policy is NOT offered: "not listed" and "listed" are the
// only two answers a policy gives, and treating the absence of a policy as
// permission would make a missing file an authorisation.
func PolicyOffersRelease(version string) bool {
	policy := applicablePolicy(Version)
	if policy == nil {
		return false
	}
	for _, release := range policy.Releases {
		if release.Version == version {
			return true
		}
	}
	return false
}

// PolicyEdgeVerified reports whether the policy in force lists a verified path
// from one release to another. Like PolicyOffersRelease, an absent policy
// answers false.
func PolicyEdgeVerified(from, to string) bool {
	policy := applicablePolicy(Version)
	if policy == nil {
		return false
	}
	for _, edge := range policy.UpgradeEdges {
		if edge.From == from && edge.To == to {
			return true
		}
	}
	return false
}

// LoadReleasesPolicy verifies, validates and installs a policy document.
//
// The order is the point. Verification comes first because a document that has
// not been authenticated must not be parsed into anything a caller could mistake
// for reviewed content, and installation comes last so a policy that fails any
// check leaves the previous one in force rather than clearing it — a bad update
// must not be able to withdraw the policy a panel is running on.
//
// A caller that has no trust root configured gets ErrPolicyUntrustedKey for
// every document, which is the intended fail-closed state.
func LoadReleasesPolicy(raw, signatureDoc []byte, root *PolicyTrustRoot, now time.Time) (bool, error) {
	policy, err := ParseSignedReleasesPolicy(raw, signatureDoc, root, now)
	if err != nil {
		return false, err
	}
	if inForce := ActiveReleasesPolicy(); inForce != nil {
		if _, err := policy.Supersedes(inForce.Revision); err != nil {
			return false, err
		}
		// An equal revision is not newer: the policy already in force is the
		// same one, and re-installing it would only make the active identity
		// depend on when it was fetched.
		if policy.Revision == inForce.Revision {
			return false, nil
		}
	}
	SetActiveReleasesPolicy(&policy)
	return true, nil
}

// applicablePolicy reports whether the policy in force covers this build. A
// policy for another build is not in force, even though it is installed: the
// caller asked whether THIS panel may do something, and a document reviewed for
// a different build has not answered that.
func applicablePolicy(buildVersion string) *ReleasesPolicy {
	policy := ActiveReleasesPolicy()
	if policy == nil || !policy.AppliesTo(buildVersion) {
		return nil
	}
	return policy
}

// The trust root the panel verifies policies against.
//
// COMPILED OR CONFIGURED, NEVER FETCHED. A trust root obtained over the same
// channel as the thing it authenticates authenticates nothing, so it comes from
// configuration and never from the network. Empty is the default and is a real
// state: it means no policy can be verified, which is correct for a deployment
// that has not been given keys — failing closed is "no policy", not "any policy".
var activeTrustRoot atomic.Value // *PolicyTrustRoot

// SetPolicyTrustRoot installs the keys this build accepts. Passing nil restores
// the empty root, which trusts nothing.
func SetPolicyTrustRoot(root *PolicyTrustRoot) {
	if root == nil {
		activeTrustRoot.Store((*PolicyTrustRoot)(nil))
		return
	}
	activeTrustRoot.Store(root)
}

// ActivePolicyTrustRoot returns the trusted keys, or nil when none are
// configured.
func ActivePolicyTrustRoot() *PolicyTrustRoot {
	root, _ := activeTrustRoot.Load().(*PolicyTrustRoot)
	return root
}
