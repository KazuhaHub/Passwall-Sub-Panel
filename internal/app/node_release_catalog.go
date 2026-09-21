package app

import (
	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/noderelease"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// newNodeReleaseCatalog builds the catalog of Node releases this panel can offer.
//
// IT TAKES NO VERSION. It used to be handed the panel's own stamp, to select the
// reviewed set for that major; the catalog is now the releases this project has
// PUBLISHED, which is the same answer for every build. That also removed the case
// where an unreadable stamp left the panel with no catalog at all.
func newNodeReleaseCatalog() (ports.NodeReleaseCatalog, error) {
	return noderelease.New(noderelease.Options{})
}
