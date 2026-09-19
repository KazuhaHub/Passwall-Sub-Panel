package auth

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
)

// The property D6 exists for: a configuration whose provider cannot be built must
// leave the generation with NO provider, never with the previous one. Pairing new
// mapping rules with an old trust anchor (or the reverse) is the failure this
// guards against, and "it compiled and still logs people in" is exactly how it
// would hide.
func TestReload_FailureLeavesNoProviderRatherThanTheStaleOne(t *testing.T) {
	svc, _ := testSAML(t)
	if !svc.Enabled() {
		t.Fatal("precondition: the harness should start enabled")
	}
	before := svc.snapshot()

	broken := config.CloneSAMLConfig(before.cfg)
	broken.IDP.MetadataURL = "https://unreachable.invalid/saml/metadata"
	if err := svc.Reload(context.Background(), broken); err == nil {
		t.Fatal("expected the provider build to fail for an unreachable metadata URL")
	}

	if svc.Enabled() {
		t.Fatal("a failed reload left SAML enabled: the new rules would be served with the old trust anchor")
	}
	after := svc.snapshot()
	if after.cfg != broken {
		t.Fatal("the new configuration was not stored, so the admin's edit was silently lost")
	}
	if after.generation <= before.generation {
		t.Fatalf("generation did not advance: %d then %d", before.generation, after.generation)
	}
	if after.sp != nil {
		t.Fatal("a failed build still published a provider")
	}
}

// Disabling SSO must not require the new configuration to be valid first: an
// admin has to be able to switch it off even while replacing broken values.
func TestReload_DisablingTearsDownTheProvider(t *testing.T) {
	svc, _ := testSAML(t)

	disabled := config.CloneSAMLConfig(svc.snapshot().cfg)
	disabled.Enabled = false
	if err := svc.Reload(context.Background(), disabled); err != nil {
		t.Fatalf("disabling SAML returned an error: %v", err)
	}
	if svc.Enabled() {
		t.Fatal("a disabled configuration left SAML enabled")
	}
}

// A snapshot that a request already took must keep working after a later reload,
// and must not be mutated by it. This is the concurrency boundary the plan
// describes: requests already past the snapshot point finish on their snapshot.
func TestInFlightSnapshotSurvivesALaterReload(t *testing.T) {
	svc, _ := testSAML(t)
	inFlight := svc.snapshot()
	digestBefore := inFlight.digest

	disabled := config.CloneSAMLConfig(inFlight.cfg)
	disabled.Enabled = false
	if err := svc.Reload(context.Background(), disabled); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if !inFlight.enabled() {
		t.Fatal("the in-flight snapshot was mutated by a reload; a request holding it would change behaviour mid-flight")
	}
	if inFlight.digest != digestBefore {
		t.Fatal("the in-flight snapshot's digest changed underneath its holder")
	}
	// ...while the live generation is disabled. That asymmetry is the point of
	// taking one snapshot per request, so it is asserted rather than implied.
	if svc.Enabled() {
		t.Fatal("the new generation should be disabled")
	}
}

func TestReload_GenerationIsStrictlyIncreasing(t *testing.T) {
	svc, _ := testSAML(t)
	seen := svc.snapshot().generation

	for i := 0; i < 3; i++ {
		next := config.CloneSAMLConfig(svc.snapshot().cfg)
		next.Enabled = false
		if err := svc.Reload(context.Background(), next); err != nil {
			t.Fatalf("Reload %d: %v", i, err)
		}
		got := svc.snapshot().generation
		if got <= seen {
			t.Fatalf("generation did not increase: %d then %d", seen, got)
		}
		seen = got
	}
}

// A metadata refresh carries the generation it started under. One that finished
// after an admin saved a new configuration must be dropped, or a slow response
// for an old metadata URL would resurrect the configuration it belonged to.
func TestCommitMetadataRefresh_RefusesASupersededGeneration(t *testing.T) {
	svc, idp := testSAML(t)
	stale := svc.snapshot()

	// An admin saves while the fetch is in flight.
	changed := config.CloneSAMLConfig(stale.cfg)
	changed.RoleRules = append(changed.RoleRules, config.SSORoleRule{
		Attribute: "groups", Value: "ops", Role: "operator",
	})
	svc.setConfigForTest(changed)

	if svc.commitMetadataRefresh(stale, idp.idp.Metadata()) {
		t.Fatal("a refresh belonging to a superseded generation was applied")
	}
}

// A refresh of the CURRENT generation applies, and deliberately does not bump the
// generation or change the digest: the trust anchor rotated, the rules did not, so
// logins already in flight on this snapshot keep working.
func TestCommitMetadataRefresh_AppliesWithoutBumpingTheGeneration(t *testing.T) {
	svc, idp := testSAML(t)
	before := svc.snapshot()

	refreshed := idp.idp.Metadata()
	refreshed.EntityID = "https://idp.example.com/saml/metadata-refreshed"
	if !svc.commitMetadataRefresh(before, refreshed) {
		t.Fatal("a refresh of the current generation was refused")
	}

	after := svc.snapshot()
	if after.generation != before.generation {
		t.Fatalf("generation changed on a metadata-only refresh: %d then %d", before.generation, after.generation)
	}
	if after.digest != before.digest {
		t.Fatal("the configuration digest changed on a metadata-only refresh")
	}
	if after.sp == before.sp {
		t.Fatal("the provider was not replaced, so the rotated certificate never took effect")
	}
	if after.sp.IDPMetadata == before.sp.IDPMetadata {
		t.Fatal("the metadata was not actually swapped")
	}
	if before.sp.IDPMetadata == refreshed {
		t.Fatal("the in-flight snapshot's metadata was mutated in place")
	}
}
