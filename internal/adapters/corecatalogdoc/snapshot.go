package corecatalogdoc

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// THE REVIEW A BUILD SHIPS WITH, so that a panel that has never reached the origin
// still has one.
//
// WITHOUT IT, A FRESH INSTALL WITH NO NETWORK HAS NO CATALOG AT ALL: the selector
// is empty, the offline conversion gate refuses, and the node-sync path cannot
// check a selection — so a panel carried into an air-gapped network would look
// broken for as long as it stayed there, for want of data that was known when it
// was built.
//
// WHAT IT IS NOT IS AN ANSWER. It is a review of the releases that existed when
// this build was made, and it gets older from the moment the build is cut. A
// release pulled afterwards is still on this list. That is why it is the LAST
// resort — the disk snapshot below always wins when it is newer — and why the
// status it reports says which of the two an operator is looking at.
//
//go:embed snapshot.json
var shippedSnapshot []byte

// shippedOrigin is what the embedded document is, in words, for the status an
// operator reads. The file itself cannot say: it is the publisher's own output, and
// a comment in it would be a field the publisher does not emit.
const shippedOrigin = "shipped with this panel build; regenerated when this build is cut"

// diskSnapshotName is the file the panel keeps its own last read in. It lives
// beside the panel's other durable state, at a path the composition root supplies.
const diskSnapshotName = "core-catalog.json"

// DiskSnapshotPath names that file inside a data directory, so the name lives in one
// place rather than in every composition root that has to spell it.
func DiskSnapshotPath(dataDir string) string {
	return filepath.Join(dataDir, diskSnapshotName)
}

// snapshotDocument parses the embedded review, once.
func snapshotDocument() (ports.CoreCatalogDocument, error) {
	document, err := parseShipped(shippedSnapshot)
	if err != nil {
		return ports.CoreCatalogDocument{}, fmt.Errorf("the core catalog shipped with this build is unusable: %w", err)
	}
	return document, nil
}

// parseShipped reads the embedded document without the semantic checks: a build
// whose own constant is unusable cannot do anything about it, and refusing to start
// over it would be worse than serving it. The checks still run on anything read
// from the origin, which is where they can change.
func parseShipped(body []byte) (ports.CoreCatalogDocument, error) {
	var document ports.CoreCatalogDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return ports.CoreCatalogDocument{}, err
	}
	if document.SchemaVersion != supportedSchema {
		return ports.CoreCatalogDocument{}, fmt.Errorf("schema %d", document.SchemaVersion)
	}
	return document, nil
}

// readDiskSnapshot reads the review this panel last wrote.
//
// A MISSING FILE IS NOT AN ERROR, and neither is an unreadable one: this is a cache
// of something the panel can read again, and a deployment whose data directory is
// read-only is a named state rather than a reason to refuse to serve a catalog. What
// it must not do is serve a document it cannot vouch for, so anything that fails to
// parse or validate is discarded rather than used.
func (c *Catalog) readDiskSnapshot() (ports.CoreCatalogDocument, bool) {
	if c.snapshotPath == "" {
		return ports.CoreCatalogDocument{}, false
	}
	body, err := os.ReadFile(c.snapshotPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Warn("core catalog snapshot not read; the shipped review stays in force", "path", c.snapshotPath, "err", err)
		}
		return ports.CoreCatalogDocument{}, false
	}
	document, err := parse(body)
	if err != nil {
		log.Warn("core catalog snapshot is not usable and was ignored", "path", c.snapshotPath, "err", err)
		return ports.CoreCatalogDocument{}, false
	}
	return document, true
}

// writeDiskSnapshot records the document that was just verified, so the NEXT start
// has it.
//
// WRITTEN TO A TEMPORARY FILE AND RENAMED INTO PLACE: a reader either sees the
// previous snapshot or this one, never half of either. The temporary name is fixed
// rather than random so a crash leaves at most one stray file, which the next write
// overwrites.
//
// A FAILURE HERE IS A DEGRADATION, NOT A FAILED READ, and it is reported as one:
// the document is already in force, the panel is already serving it, and all that
// is lost is that the next start has to read it again — which is where a panel
// without a snapshot starts anyway. A read-only data directory must not turn a
// successful refresh into an error.
func (c *Catalog) writeDiskSnapshot(document ports.CoreCatalogDocument) {
	if c.snapshotPath == "" {
		return
	}
	body, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		log.Warn("core catalog snapshot not written; the review is in force and will be re-read on the next start", "err", err)
		return
	}
	body = append(body, '\n')
	dir := filepath.Dir(c.snapshotPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Warn("core catalog snapshot not written; the review is in force and will be re-read on the next start", "err", err)
		return
	}
	temporary := c.snapshotPath + ".tmp"
	if err := os.WriteFile(temporary, body, 0o644); err != nil {
		log.Warn("core catalog snapshot not written; the review is in force and will be re-read on the next start", "err", err)
		return
	}
	if err := os.Rename(temporary, c.snapshotPath); err != nil {
		_ = os.Remove(temporary)
		log.Warn("core catalog snapshot not written; the review is in force and will be re-read on the next start", "err", err)
	}
}

// newestReview is the most recent review this panel can serve WITHOUT the origin,
// and where it came from.
//
// THE COMPARISON IS THE POINT. A panel restarted after its last read must not fall
// back to the review it was BUILT with, because the two can disagree in the one
// direction that matters: a release the newer review has withdrawn is still on the
// older one, and serving the older one would re-open a decision somebody made
// deliberately. So the newest wins, whatever its source, and the status says which
// one that was.
//
// A REVIEW WITH NO DATE CANNOT BE COMPARED, so it loses to any dated one — which
// also means a document that somehow lost its date cannot displace a good one.
func (c *Catalog) newestReview() (ports.CoreCatalogDocument, string, bool) {
	best, source := c.shipped, shippedOrigin
	if c.have && c.good.UpdatedAt.After(best.UpdatedAt) {
		best, source = c.good, "the last review this panel read"
	}
	if disk, ok := c.readDiskSnapshot(); ok && disk.UpdatedAt.After(best.UpdatedAt) {
		best, source = disk, "the last review this panel read"
	}
	if best.UpdatedAt.IsZero() {
		// Nothing with a date, anywhere. The shipped review is still the best
		// answer available, and it is unusable only if the build is.
		if len(best.Releases) == 0 {
			return ports.CoreCatalogDocument{}, "", false
		}
		return best, source, true
	}
	return best, source, true
}

// CoreCatalogStatus is this reader's report of what it is serving and how it got it.
func (c *Catalog) Status() ports.CoreCatalogStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	status := ports.CoreCatalogStatus{
		Source:      c.source,
		FallingBack: c.fallingBack,
	}
	if !c.servedReviewTime.IsZero() {
		when := c.servedReviewTime
		status.ReviewTime = &when
	}
	if !c.lastSuccess.IsZero() {
		when := c.lastSuccess
		status.LastSuccess = &when
	}
	if c.lastError != nil {
		status.LastError = c.lastError.Error()
	}
	return status
}

var _ ports.CoreCatalogReporter = (*Catalog)(nil)
