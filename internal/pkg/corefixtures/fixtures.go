// Package corefixtures builds reviewed core catalog documents for tests.
//
// IT EXISTS BECAUSE FOUR PACKAGES NEEDED THE SAME ONE. The panel reads the catalog
// in its selector, its node adapter, its offline-conversion gate and its migration
// preview, and each of those has cases about what the panel does with a reviewed
// release. Four copies of the fixture would be four descriptions of what the
// catalog contains, and they would drift the first time a release was added to it.
//
// IT IS NOT THE PUBLISHED DOCUMENT. Reading and verifying the document is
// internal/adapters/corecatalogdoc's to test, against a signed fixture origin. What
// these releases are for is everything downstream of the read.
package corefixtures

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Document is a reviewed catalog covering both engines, with one release at each
// tier the panel gates on.
//
// THE VERSIONS MIRROR WHAT THE PROJECT PUBLISHES — a recommended xray baseline, a
// verified one, a restricted one that requires acknowledgment, and a sing-box
// release — because cases assert against them by version.
func Document() ports.CoreCatalogDocument {
	reviewed := func(engine, version, tier string, restricted bool, minClientVer string) ports.CoreRelease {
		return ports.CoreRelease{
			Engine: engine, Version: version, Tier: tier, Selectable: true,
			RequiresConfirmation: restricted,
			PublishedAt:          time.Date(2026, 6, 27, 13, 20, 31, 0, time.UTC),
			Reality:              ports.CoreReality{Xray: "supported", Mihomo: "supported", SingBox: "supported", URIList: "supported", ServerMinClientVer: minClientVer},
			Evidence: ports.CoreEvidence{
				SourceAudited: true, ConfigTested: true, HandshakeTested: true,
			},
		}
	}
	return ports.CoreCatalogDocument{
		SchemaVersion: 1,
		UpdatedAt:     time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC),
		Releases: []ports.CoreRelease{
			// THE COMPATIBILITY COLUMN MIRRORS WHAT THE PROJECT PUBLISHES, because
			// the migration preview warns on it: 26.7.28 is the release that carries a
			// server-side client floor, and a fixture without one silently stops
			// exercising that warning.
			reviewed("xray", "26.6.27", domain.CoreTierRecommended, false, ""),
			reviewed("xray", "26.7.28", domain.CoreTierVerified, false, "0.0.0"),
			reviewed("xray", "26.9.9", domain.CoreTierRestricted, true, ""),
			reviewed("sing-box", "1.14.0", domain.CoreTierRecommended, false, ""),
		},
	}
}

// Static is a core catalog that serves the fixture document.
type Static struct{}

var _ ports.CoreCatalog = Static{}

func (Static) Document(context.Context) (ports.CoreCatalogDocument, error) {
	return Document(), nil
}
