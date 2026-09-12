package app

import (
	"context"
	"testing"
)

func TestNodeReleaseCatalogCustomStampDisablesOnlyCatalog(t *testing.T) {
	for _, stamped := range []string{"ci", "custom-build", "", "4.0.0", "v4.0.0+local"} {
		catalog, err := newNodeReleaseCatalog(stamped)
		if err != nil || catalog != nil {
			t.Fatalf("custom stamp %q must disable only optional catalog: catalog=%v err=%v", stamped, catalog, err)
		}
	}
}

func TestNodeReleaseCatalogCompositionUsesActualMajorWithoutNetwork(t *testing.T) {
	for _, stamped := range []string{"dev", "v4.0.0-beta.2", "v3.9.2", "v5.0.0"} {
		catalog, err := newNodeReleaseCatalog(stamped)
		if err != nil || catalog == nil {
			t.Fatalf("valid stamp %q failed local catalog construction: %v", stamped, err)
		}
		if stamped == "v3.9.2" || stamped == "v5.0.0" {
			// No reviewed entry for these majors: List must return an empty
			// catalog without contacting any release source.
			list, err := catalog.List(context.Background())
			if err != nil || len(list.Releases) != 0 || list.CheckedAt.IsZero() {
				t.Fatalf("v4 compatibility leaked into %q: %+v, %v", stamped, list, err)
			}
		}
	}
}
