package app

import (
	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/noderelease"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func newNodeReleaseCatalog(stampedVersion string) (ports.NodeReleaseCatalog, error) {
	major, err := noderelease.PSPMajorForVersion(stampedVersion)
	if err != nil {
		// Custom development stamps must neither inherit reviewed v4
		// compatibility nor make an optional metadata feature prevent startup.
		log.Warn("Node release catalog disabled: PSP build has no canonical release identity")
		return nil, nil
	}
	return noderelease.New(noderelease.Options{PSPMajor: major})
}
