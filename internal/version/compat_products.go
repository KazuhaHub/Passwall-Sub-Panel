package version

import (
	"fmt"
	"strconv"
)

// ONE DOCUMENT PER PRODUCT, ADDRESSED BY THE PANEL MAJOR.
//
// WHAT THIS REPLACED. The ranges arrived either in a per-major manifest carrying
// BOTH panels (v3.json, and v4.json with a range overlay beside it) or in one
// document reached by a fixed name and stating its own window. Either way a
// single install set both ceilings, so a review of one panel rewrote the file
// that carried the other's, and a range published in two places had to be kept
// equal by a test rather than by there being one place.
//
// WHY THE NAME CARRIES THE MAJOR. A product version's first segment is a RELEASE
// LINE, so a build derives its own document names instead of being reached by
// one: 4.0.0 reads the v4 documents. That is also why the window stayed — the
// name says which file, and `applies_to_psp` says which builds the file is a
// claim about, and a name is not evidence.
const (
	productXUI = "3x-ui"
	productSUI = "sui"
)

// The published document names. A pattern rather than a constant because the
// major is part of the name: the v4 pair and a future v5 pair are different
// documents, and a build asks for its own.
//
// THE NODE DOCUMENT IS NOT IN THIS SET. It was, briefly, when the release catalog
// was served from it — the catalog now lists the releases the project has
// published, so nothing fetches that document at runtime and no pattern is needed
// for it. It is still a published document: the CI matrix reads it.
const (
	xuiDocumentPattern = "3x-ui-v%d.json"
	suiDocumentPattern = "sui-v%d.json"
)

// knownProduct reports whether a document named a product this build reads.
func knownProduct(product string) bool {
	return product == productXUI || product == productSUI
}

// compatSource is one document this build reads, and WHICH product it is. The
// product is known from the address the build derived, and it is what the apply
// path checks a document against — a document is not allowed to say it is
// something other than what was asked for.
type compatSource struct {
	// Product is "" for a per-major manifest, which carries both subsets in one
	// document and has no product to be checked against. Empty is a real answer
	// rather than a missing one: that path is the frozen legacy route.
	Product string
	Name    string
	URL     string
}

// compatDocumentSources lists the documents THIS build reads.
//
// A LEGACY BUILD READS ONE. Its major names a single manifest that carries both
// panels, and that route is frozen rather than removed — the v3 line's builds
// fetch v3.json from a name their own version derives, and nothing in this split
// is allowed to reach them.
//
// A PRODUCT BUILD READS ONE PER PRODUCT, named from its own major.
func compatDocumentSources() ([]compatSource, error) {
	if major, ok := pspMajor(Version); ok {
		name := "v" + strconv.Itoa(major) + ".json"
		return []compatSource{{Name: name, URL: defaultRemoteCompatURLBase + name}}, nil
	}
	major, ok := MajorOfRelease(Version)
	if !ok || major < 1 {
		return nil, fmt.Errorf("version %q is neither a legacy v-prefixed build nor a release version, so no compat document applies to it; "+
			"its supported ceiling stays unknown until one is", Version)
	}
	xui := fmt.Sprintf(xuiDocumentPattern, major)
	sui := fmt.Sprintf(suiDocumentPattern, major)
	return []compatSource{
		{Product: productXUI, Name: xui, URL: defaultRemoteCompatURLBase + xui},
		{Product: productSUI, Name: sui, URL: defaultRemoteCompatURLBase + sui},
	}, nil
}
