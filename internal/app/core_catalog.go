package app

import (
	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/corecatalogdoc"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// newCoreCatalog builds the reader for the reviewed core catalog.
//
// IT CANNOT FAIL FOR LACK OF A RELEASE SOURCE. It used to take a nil-tolerance
// branch, because the Node release catalog was itself absent for a build whose
// stamp named no release line — and a build that could not name a release could
// not name the document either. That case is gone: the release catalog is the set
// of releases this project has published, which is the same answer for every
// build. So the reader is built directly, and an unreadable document is a
// failure at read time with a reason in it rather than a permanently absent
// catalog nobody is told about.
func newCoreCatalog(releases ports.NodeReleaseCatalog) (ports.CoreCatalog, error) {
	return corecatalogdoc.New(corecatalogdoc.Options{Releases: releases})
}
