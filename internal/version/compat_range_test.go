package version

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A FIX RELEASE IS A RELEASE.
//
// The product version carries an optional FOURTH segment — the incremental fix — and
// this lookup could not read one: the document that answers for a line sat in memory,
// in range, while every four-segment build was told its compatibility was unknown.
// The bounds are written in the same rule, and the fourth segment PARTICIPATES: a fix
// of a release is above that release, so an interval ending at the base does not
// cover it.
func TestLookupForPSPVersionReadsAFourSegmentProductBuild(t *testing.T) {
	payload := remoteCompatPayload{
		SchemaVersion: 1,
		Entries: []remoteCompatPSPEntry{
			{PSPMin: "4.0.0", PSPMax: "4.99.99", MinXUI: "3.4.2", MaxTestedXUI: "3.8.5", Notes: "the line"},
			{PSPMin: "5.0.0", PSPMax: "5.0.1", MaxTestedXUI: "9.9.9", Notes: "stops at the base"},
		},
		SUIEntries: []remoteCompatSUIEntry{
			{PSPMin: "4.0.0", PSPMax: "4.99.99", MaxTestedSUI: "1.6.3"},
		},
	}
	for _, c := range []struct {
		build   string
		wantMax string
		wantOK  bool
	}{
		{"4.0.1.4", "3.8.5", true}, // the build this was found on
		{"4.0.1.1", "3.8.5", true}, // and the first fix of the line
		{"4.0.0", "3.8.5", true},   // the base itself
		{"4.99.99", "3.8.5", true}, // upper bound inclusive
		{"4.100.0", "", false},     // past the bound
		{"5.0.1", "9.9.9", true},   // the base of the second interval
		{"5.0.1.1", "", false},     // a fix OF that base is above the interval
		{"dev", "", false},         // unparseable
	} {
		entry, ok := lookupForPSPVersion(payload, c.build)
		if ok != c.wantOK || (ok && entry.MaxTestedXUI != c.wantMax) {
			t.Errorf("lookupForPSPVersion(%q) = (%q, %v), want (%q, %v)", c.build, entry.MaxTestedXUI, ok, c.wantMax, c.wantOK)
		}
		// The S-UI twin answers for the same build, through sui_entries.
		if _, ok := lookupSUIForPSPVersion(payload, c.build); ok != (c.build == "4.0.1.4" || c.build == "4.0.1.1" || c.build == "4.0.0" || c.build == "4.99.99") {
			t.Errorf("lookupSUIForPSPVersion(%q): ok=%v", c.build, ok)
		}
	}
}

// AND THE DOCUMENTS THIS TREE SHIPS ANSWER FOR A FOUR-SEGMENT BUILD. That is the
// property the failure was really about: the file on disk covers a build of the line
// it claims, and every fix release is one.
func TestTheShippedCompatDocumentsCoverAFourSegmentBuild(t *testing.T) {
	for _, name := range []string{"3x-ui-v4.json", "sui-v4.json"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "compat", name))
		if err != nil {
			t.Fatal(err)
		}
		var payload remoteCompatPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		for _, build := range []string{"4.0.0", "4.0.1", "4.0.1.4", "4.99.99"} {
			// A DOCUMENT IS ONE PRODUCT'S, and the other product's lookup correctly
			// finds nothing in it: only the one it is about has to answer.
			if payload.Product == "3x-ui" {
				if _, ok := lookupForPSPVersion(payload, build); !ok {
					t.Errorf("%s covers no 3X-UI entry for the product build %s", name, build)
				}
				continue
			}
			if _, ok := lookupSUIForPSPVersion(payload, build); !ok {
				t.Errorf("%s covers no S-UI entry for the product build %s", name, build)
			}
		}
	}
}
