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
	Version     string                `json:"version"`
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
