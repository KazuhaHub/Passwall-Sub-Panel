package ports

import (
	"context"
	"time"
)

// NodeReleaseCatalog lists reviewed Node-agent releases, not proxy-core builds.
// An unavailable source returns an error instead of an apparently empty catalog.
type NodeReleaseCatalog interface {
	List(context.Context) (NodeReleaseList, error)
}

type NodeReleasePlatform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type NodeReleaseCatalogEntry struct {
	// Version is the version the release is STAMPED with, which is what a node
	// reports about itself and what the upgrade request carries.
	Version string `json:"version"`
	// ProductVersion and ReleaseTag are the same identity SPLIT, and a consumer
	// that needs one must not derive it from the other.
	//
	// The two coincide in the legacy scheme, which is why one field was enough
	// while only that scheme existed. Under the product scheme the tag is
	// `v4.0.0` and the version is `4.0.0`, and a caller that builds an address out
	// of the version asks for a release that does not exist. Stating both here
	// means the front end validates a URL against what the panel says rather than
	// re-deriving the mapping — which no longer has one answer anyway: the four
	// releases published before the namespace changed are addressed as
	// `release/…`, and only this field knows that.
	//
	// ProductVersion is EMPTY for a legacy release, and that is the honest
	// answer: `v0.0.1-beta11` has no product version, and normalising it into one
	// would name an identity no release ever had.
	ProductVersion string `json:"product_version,omitempty"`
	ReleaseTag     string `json:"release_tag,omitempty"`
	// Scheme is "product" or "legacy", as releaseid names them.
	Scheme      string                `json:"scheme,omitempty"`
	Channel     string                `json:"channel"`
	PublishedAt time.Time             `json:"published_at"`
	ReleaseURL  string                `json:"release_url"`
	Notes       string                `json:"notes"`
	Methods     []string              `json:"methods"`
	Platforms   []NodeReleasePlatform `json:"platforms"`
}

type NodeReleaseList struct {
	Releases  []NodeReleaseCatalogEntry `json:"releases"`
	CheckedAt time.Time                 `json:"checked_at"`
}
