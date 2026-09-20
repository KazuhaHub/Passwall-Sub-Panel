package app

import (
	"context"
	"testing"
)

// A stamp that is not a canonical release version disables the catalog and
// nothing else.
//
// `4.0.0` used to be in this list, and that was the defect: it is exactly what
// the product scheme stamps, so the first release named that way would have had
// its Node release catalog disabled as though the build had no identity at all.
// It is a valid stamp and is asserted as one below.
func TestNodeReleaseCatalogCustomStampDisablesOnlyCatalog(t *testing.T) {
	for _, stamped := range []string{"ci", "custom-build", "", "v4.0.0+local", "4.0", "release/4.0.0"} {
		catalog, err := newNodeReleaseCatalog(stamped)
		if err != nil || catalog != nil {
			t.Fatalf("custom stamp %q must disable only optional catalog: catalog=%v err=%v", stamped, catalog, err)
		}
	}
}

func TestNodeReleaseCatalogCompositionUsesActualMajorWithoutNetwork(t *testing.T) {
	// THE FORMS THE RELEASE WORKFLOW STAMPS, plus a release line the compiled
	// reviews do not cover. There is one scheme, so the legacy stamps that used
	// to sit here are absent rather than invalid: they are not versions at all
	// any more, and the case above asserts that.
	for _, stamped := range []string{"dev", "4.0.0", "4.0.0.1", "102.1.0"} {
		catalog, err := newNodeReleaseCatalog(stamped)
		if err != nil || catalog == nil {
			t.Fatalf("valid stamp %q failed local catalog construction: %v", stamped, err)
		}
		if stamped == "102.1.0" {
			// No reviewed entry for this release line: List must return an empty
			// catalog without contacting any release source.
			list, err := catalog.List(context.Background())
			if err != nil || len(list.Releases) != 0 || list.CheckedAt.IsZero() {
				t.Fatalf("v4 compatibility leaked into %q: %+v, %v", stamped, list, err)
			}
		}
	}
}
