package version

import (
	"crypto/ed25519"
	"errors"
	"testing"
)

const policyBody = `{
  "schema_version": 1, "revision": 7,
  "issued_at": "2026-09-19T00:00:00Z", "expires_at": "2026-09-26T00:00:00Z",
  "applies_to_psp": {"min": "4.0.0", "max": "4.99.99"},
  "releases": [{"version": "v0.0.1-beta11", "release_tag": "v0.0.1-beta11", "scheme": "legacy", "evidence": ["node-wire-v1"]}],
  "upgrade_edges": [{"from": "v0.0.1-beta3", "to": "v0.0.1-beta11", "evidence": ["upgrade-mechanism"]}],
  "refusals": []
}`

// policyFor returns a policy document with a different revision, so a second
// install is a genuine supersede rather than a re-install.
func policyBodyWithRevision(revision int) []byte {
	return []byte(replaceOnce(policyBody, `"revision": 7`, `"revision": `+itoa(revision)))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func replaceOnce(haystack, old, new string) string {
	i := indexOf(haystack, old)
	if i < 0 {
		return haystack
	}
	return haystack[:i] + new + haystack[i+len(old):]
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func installPolicy(t *testing.T, body []byte, revision int) (*PolicyTrustRoot, ed25519.PrivateKey) {
	t.Helper()
	id, pub, priv := signingKey(t)
	doc, err := SignReleasesPolicy(body, id, priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	root := trustRoot(t, id, pub)
	installed, err := LoadReleasesPolicy(body, doc, root, policyNow())
	if err != nil {
		t.Fatalf("install revision %d: %v", revision, err)
	}
	if !installed {
		t.Fatalf("revision %d was not installed", revision)
	}
	return root, priv
}

// With no policy, the manifest is the only source — and the answers are "no",
// because the manifest's edges are a fallback for that state rather than a
// permission the policy has to beat.
func TestWithoutAPolicyNothingIsAuthorisedByIt(t *testing.T) {
	isolatedCompatCache(t, "v4.0.0")
	t.Cleanup(func() { SetActiveReleasesPolicy(nil) })
	SetActiveReleasesPolicy(nil)
	SetActiveUpgradeEdges([]UpgradeEdge{{ID: "manifest", From: "v0.0.1-beta3", To: "v0.0.1-beta11"}})
	t.Cleanup(func() { SetActiveUpgradeEdges(nil) })

	if PolicyInForce() {
		t.Fatal("no policy is installed, so none is in force")
	}
	if PolicyOffersRelease("v0.0.1-beta11") {
		t.Fatal("a release must not be offered by a policy that does not exist")
	}
	if PolicyEdgeVerified("v0.0.1-beta3", "v0.0.1-beta11") {
		t.Fatal("a path must not be verified by a policy that does not exist")
	}
	// The manifest edge is still what decides, which is the pre-policy state.
	if !UpgradeEdgeVerified("v0.0.1-beta3", "v0.0.1-beta11") {
		t.Fatal("the manifest's edges must still decide while no policy exists")
	}
}

func TestAnInstalledPolicyDecidesReleasesAndEdges(t *testing.T) {
	isolatedCompatCache(t, "v4.0.0")
	t.Cleanup(func() { SetActiveReleasesPolicy(nil) })
	installPolicy(t, []byte(policyBody), 7)

	if !PolicyInForce() {
		t.Fatal("the policy applies to this build and should be in force")
	}
	if !PolicyOffersRelease("v0.0.1-beta11") {
		t.Fatal("a listed release must be offered")
	}
	if PolicyOffersRelease("v0.0.1-beta12") {
		t.Fatal("a release the policy does not list must not be offered")
	}
	if !PolicyEdgeVerified("v0.0.1-beta3", "v0.0.1-beta11") {
		t.Fatal("a listed path must verify")
	}
	if PolicyEdgeVerified("v0.0.1-beta11", "v0.0.1-beta3") {
		t.Fatal("the reverse of a listed path is not listed")
	}
}

// The precedence that keeps a removed target removed: once a policy is in force,
// a manifest edge it does not list must NOT verify. If the two lists could each
// authorise, the one nobody updated would stay a way through.
func TestAPolicyReplacesTheManifestEdgesRatherThanAddingToThem(t *testing.T) {
	isolatedCompatCache(t, "v4.0.0")
	t.Cleanup(func() { SetActiveReleasesPolicy(nil) })
	SetActiveUpgradeEdges([]UpgradeEdge{{ID: "manifest-only", From: "v0.0.1-beta9", To: "v0.0.1-beta10"}})
	t.Cleanup(func() { SetActiveUpgradeEdges(nil) })

	if !UpgradeEdgeVerified("v0.0.1-beta9", "v0.0.1-beta10") {
		t.Fatal("the harness is wrong: the manifest edge should decide before a policy exists")
	}

	installPolicy(t, []byte(policyBody), 7)

	if UpgradeEdgeVerified("v0.0.1-beta9", "v0.0.1-beta10") {
		t.Fatal("an edge only the manifest lists survived the policy taking over")
	}
	if !UpgradeEdgeVerified("v0.0.1-beta3", "v0.0.1-beta11") {
		t.Fatal("the policy's own edge should decide")
	}
	if HasUpgradeEdgeFrom("v0.0.1-beta9") {
		t.Fatal("a list question answered from the superseded manifest")
	}
}

// A policy reviewed for a different build is installed but is not in force here.
func TestAPolicyForAnotherBuildIsNotInForce(t *testing.T) {
	isolatedCompatCache(t, "v3.9.2")
	t.Cleanup(func() { SetActiveReleasesPolicy(nil) })
	installPolicy(t, []byte(policyBody), 7)

	if ActiveReleasesPolicy() == nil {
		t.Fatal("the policy is installed")
	}
	if PolicyInForce() {
		t.Fatal("a policy whose applies_to_psp excludes this build must not be in force")
	}
	if PolicyOffersRelease("v0.0.1-beta11") {
		t.Fatal("a policy for another build must not offer releases to this one")
	}
}

func TestAFailedUpdateLeavesThePolicyInForceAlone(t *testing.T) {
	isolatedCompatCache(t, "v4.0.0")
	t.Cleanup(func() { SetActiveReleasesPolicy(nil) })
	root, priv := installPolicy(t, []byte(policyBody), 7)
	before := ActiveReleasesPolicy()

	sign := func(body []byte) []byte {
		t.Helper()
		doc, err := SignReleasesPolicy(body, "key-1", priv, nil)
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}

	// A lower revision, correctly signed: refused, and the running policy stays.
	older := policyBodyWithRevision(6)
	if installed, err := LoadReleasesPolicy(older, sign(older), root, policyNow()); err == nil || installed {
		t.Fatalf("a lower revision must be refused: installed=%v err=%v", installed, err)
	}
	after := ActiveReleasesPolicy()
	if after == nil || after.Revision != before.Revision {
		t.Fatalf("a refused update changed the policy in force: %v -> %v", before.Revision, after)
	}

	// A valid signature over DIFFERENT bytes: the document was edited after it
	// was signed, so the signature does not cover it.
	newer := policyBodyWithRevision(8)
	if installed, err := LoadReleasesPolicy(newer, sign([]byte(policyBody)), root, policyNow()); !errors.Is(err, ErrPolicyBadSignature) || installed {
		t.Fatalf("a signature over other bytes must be refused: installed=%v err=%v", installed, err)
	}
	if got := ActiveReleasesPolicy(); got == nil || got.Revision != before.Revision {
		t.Fatal("a refused update changed the policy in force")
	}
}

func TestAHigherRevisionSupersedes(t *testing.T) {
	isolatedCompatCache(t, "v4.0.0")
	t.Cleanup(func() { SetActiveReleasesPolicy(nil) })
	root, priv := installPolicy(t, []byte(policyBody), 7)

	next := policyBodyWithRevision(8)
	doc, err := SignReleasesPolicy(next, "key-1", priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := LoadReleasesPolicy(next, doc, root, policyNow())
	if err != nil || !installed {
		t.Fatalf("a newer revision must supersede: installed=%v err=%v", installed, err)
	}
	if got := ActiveReleasesPolicy(); got == nil || got.Revision != 8 {
		t.Fatalf("policy in force = %v, want revision 8", got)
	}

	// The SAME revision again is not newer, and re-installing it would make the
	// active identity depend on when it was fetched.
	installed, err = LoadReleasesPolicy(next, doc, root, policyNow())
	if err != nil || installed {
		t.Fatalf("an equal revision must be reused, not re-installed: installed=%v err=%v", installed, err)
	}
}

// The trust root is what a policy signature is checked against, so "no keys" has
// to be a distinct, visible state rather than a root that happens to accept
// everything. Nothing fetches a policy yet, so installing this changes no
// decision; it only makes verification possible for the code that will.
func TestTheTrustRootHolderIsFailClosed(t *testing.T) {
	t.Cleanup(func() { SetPolicyTrustRoot(nil) })

	SetPolicyTrustRoot(nil)
	if ActivePolicyTrustRoot() != nil {
		t.Fatal("an unconfigured trust root is nil, not an empty-but-present one")
	}
	// With no root, nothing verifies — the empty root is what enforces that.
	root, err := NewPolicyTrustRoot(nil)
	if err != nil {
		t.Fatal(err)
	}
	SetPolicyTrustRoot(root)
	if ActivePolicyTrustRoot() == nil {
		t.Fatal("a root was installed")
	}
	if ActivePolicyTrustRoot().Trusts("anything") {
		t.Fatal("an empty root must trust nothing")
	}

	id, pub, _ := signingKey(t)
	SetPolicyTrustRoot(trustRoot(t, id, pub))
	if !ActivePolicyTrustRoot().Trusts(id) {
		t.Fatalf("the installed root does not hold %s", id)
	}
}
