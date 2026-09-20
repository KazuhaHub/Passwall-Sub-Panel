package version_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// THE VECTORS ARE THE CONTRACT, and this is the Go consumer of the half of them
// that PSP implements.
//
// The file is released-passwall-node's `releaseid/testdata/vectors.json`, copied
// byte for byte — the same copy the front end reads, so both consumers are held
// to one piece of data. It is copied rather than fetched because a test may not
// reach into another module at run time; keeping it current is a re-copy, and it
// is worth doing because the alternative is two implementations of one rule with
// nothing but a reviewer in between.
//
// IT READS THREE OF THE SECTIONS, NOT ALL OF THEM. `normalize` and `order`
// describe the product-version PARSER and COMPARATOR, which live in Passwall
// Node and in the front end's TypeScript; PSP has neither, so it cannot be
// checked against them and pretending otherwise would be a test that passes for
// the wrong reason.
//
// WHY THIS EXISTS AT ALL. PSP has its own implementation of the release-version
// and release-tag shape because the module it pins does not carry releaseid yet.
// Two implementations of one rule is the arrangement the extraction plan permits
// only "配合共享向量" — with the vectors. So the vectors run here, and a PSP rule
// that drifts from the released data fails in this repository rather than at a
// user's node.
type releaseVectors struct {
	Format int `json:"format"`

	Tags []struct {
		In      string `json:"in"`
		Scheme  string `json:"scheme"`
		Version string `json:"version"`
	} `json:"tags"`

	RejectTags []struct {
		In  string `json:"in"`
		Why string `json:"why"`
	} `json:"reject_tags"`

	Reject []struct {
		In  string `json:"in"`
		Why string `json:"why"`
	} `json:"reject"`

	Versions []struct {
		In     string `json:"in"`
		Scheme string `json:"scheme"`
		OK     bool   `json:"ok"`
		Why    string `json:"why"`
	} `json:"versions"`
}

func loadReleaseVectors(t *testing.T) releaseVectors {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "web-react", "src", "utils", "productVersion.vectors.json"))
	if err != nil {
		t.Fatalf("read the shared vectors: %v", err)
	}
	var vectors releaseVectors
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if vectors.Format != 1 {
		t.Fatalf("vectors format = %d, want 1", vectors.Format)
	}
	if len(vectors.Tags) == 0 || len(vectors.RejectTags) == 0 || len(vectors.Reject) == 0 || len(vectors.Versions) == 0 {
		t.Fatal("the vectors lost a section this test reads; a section that vanished would make this pass vacuously")
	}
	return vectors
}

// Every tag the released data calls a release tag, in both schemes.
func TestReleaseVersionVectorsAcceptTheTags(t *testing.T) {
	for _, tc := range loadReleaseVectors(t).Tags {
		t.Run(tc.In, func(t *testing.T) {
			if !version.IsReleaseTag(tc.In) {
				t.Fatalf("IsReleaseTag(%q) = false, and the released data calls it a %s tag", tc.In, tc.Scheme)
			}
			versionOfTag, ok := version.VersionOfReleaseTag(tc.In)
			if !ok {
				t.Fatalf("VersionOfReleaseTag(%q) refused a released tag", tc.In)
			}
			// The vectors spell the version for a product tag and omit it for a
			// legacy one, where the tag IS the version.
			want := tc.Version
			if tc.Scheme == "legacy" {
				want = tc.In
			}
			if versionOfTag != want {
				t.Errorf("VersionOfReleaseTag(%q) = %q, want %q", tc.In, versionOfTag, want)
			}
			// And back: deriving the tag from the version must return the tag
			// the data published, or a caller addresses one release and
			// compares against another.
			tag, ok := version.ReleaseTagFor(versionOfTag)
			if !ok || tag != tc.In {
				t.Errorf("ReleaseTagFor(%q) = %q, %v; want %q", versionOfTag, tag, ok, tc.In)
			}
		})
	}
}

// Every string the released data refuses as a TAG.
func TestReleaseVersionVectorsRejectTheRejectedTags(t *testing.T) {
	for _, tc := range loadReleaseVectors(t).RejectTags {
		t.Run(tc.In, func(t *testing.T) {
			if version.IsReleaseTag(tc.In) {
				t.Errorf("IsReleaseTag(%q) = true, and the released data refuses it: %s", tc.In, tc.Why)
			}
		})
	}
}

// Every string the released data refuses as a PRODUCT VERSION.
//
// PSP HAS NO PRODUCT-VERSION PREDICATE, and that is why the assertion is a
// consequence rather than an equality. The section is about what the product
// scheme accepts; PSP asks the wider question "is this a release version at
// all", so a string here may still be a version — as a LEGACY one. `v102.1.0`
// is exactly that case, and it is the point of that vector: a v-prefixed form
// is a legacy identity and is never normalised into the product scheme.
//
// So the assertions are: nothing here may be read as a release version unless it
// carries the legacy v, and nothing here may be given a product tag.
func TestReleaseVersionVectorsRejectTheRejectedProductVersions(t *testing.T) {
	for _, tc := range loadReleaseVectors(t).Reject {
		t.Run(tc.In, func(t *testing.T) {
			if version.IsReleaseVersion(tc.In) && !strings.HasPrefix(tc.In, "v") {
				t.Errorf("IsReleaseVersion(%q) = true; the released data refuses it as a product version: %s", tc.In, tc.Why)
			}
			if tag, ok := version.ReleaseTagFor(tc.In); ok && tag != tc.In {
				t.Errorf("ReleaseTagFor(%q) = %q; a refused product version must not acquire a product tag", tc.In, tag)
			}
		})
	}
}

// The version-shape vectors, which both consumers check.
//
// This is the section that keeps PSP's implementation and the released package
// from drifting APART rather than merely each being self-consistent: a shape
// PSP accepts and the released rule refuses shows up as a failure here, and the
// vector's own reason is what the failure prints.
//
// The MAJOR is not part of this section. PSP reads one out of the accepted
// strings and the released package has no equivalent accessor, so there is no
// shared data to hold it to — its own table covers it, and the split is stated
// rather than implied.
func TestReleaseVersionVectorsAcceptAndRefuseVersions(t *testing.T) {
	for _, tc := range loadReleaseVectors(t).Versions {
		t.Run(tc.In, func(t *testing.T) {
			if got := version.IsReleaseVersion(tc.In); got != tc.OK {
				t.Fatalf("IsReleaseVersion(%q) = %v, and the released data says %v (%s)", tc.In, got, tc.OK, tc.Why)
			}
			if !tc.OK {
				return
			}
			// An accepted string is a version in exactly one scheme, and the tag
			// it is published under follows from which.
			want := tc.In
			if tc.Scheme == "product" {
				want = version.ProductTagNamespace + tc.In
			} else if tc.Scheme != "legacy" {
				t.Fatalf("%q is accepted with scheme %q, which is neither", tc.In, tc.Scheme)
			}
			tag, ok := version.ReleaseTagFor(tc.In)
			if !ok || tag != want {
				t.Errorf("ReleaseTagFor(%q) = %q, %v; want %q", tc.In, tag, ok, want)
			}
		})
	}
}
