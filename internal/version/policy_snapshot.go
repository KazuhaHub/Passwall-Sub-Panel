package version

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
)

// policySnapshotFile is the on-disk record of the last policy document this
// build validated and applied.
//
// WHY THE WHOLE DOCUMENT AND NOT A VALUE. The previous cache stored one number —
// max_tested_xui — plus the PSP version that read it. That is enough to show a
// range, and not enough to justify one. A reader replaying it cannot tell what
// else came with it (the floor, the S-UI range, the advisories, the upgrade
// edges), cannot tell whether the document applied to ITS version, and cannot
// tell whether the file is the document that was validated or something edited
// afterwards. So it stored a conclusion without its premises.
//
// This file stores the validated document verbatim, its digest, where it came
// from, and which revision it is. Boot replays it through the SAME apply path
// the fetch used, so a snapshot can only install what a fetch would have
// installed. A build that no entry covers installs nothing, which is the point:
// an inapplicable cached range must not become an eligibility claim.
const policySnapshotFile = "compat-policy-snapshot.json"

// policySnapshotSchema is the SNAPSHOT's own format number, separate from the
// policy document's schema_version. They move independently: the document format
// is a wire contract, this is a local file.
//
// 2 IS THE CONTAINER. Format 1 held ONE payload, because one document carried
// both panels; with a document per product there are two to replay, and a file
// that holds only the last one would restore half a policy at boot. The older
// format is refused rather than read for what it happens to contain: its payload
// has no product, so a reader would have to guess which product it belongs to,
// and a guess about which ceiling to install is how an instance comes back
// believing a range nobody published.
const policySnapshotSchema = 2

type policySnapshot struct {
	SnapshotSchema int `json:"snapshot_schema"`
	// Documents is keyed by product. ONE FILE RATHER THAN ONE PER PRODUCT,
	// because a boot has to restore them together: two files have no cross-file
	// atomicity, so a crash between two writes leaves a snapshot that disagrees
	// with itself about the day it was taken. The empty key is the per-major
	// manifest, which carries both panels in one document — the frozen legacy
	// route.
	Documents map[string]policySnapshotDocument `json:"documents"`
}

type policySnapshotDocument struct {
	PolicySchema int             `json:"policy_schema"`
	Revision     string          `json:"revision"`
	Source       string          `json:"source"`
	FetchedAt    time.Time       `json:"fetched_at"`
	Digest       string          `json:"digest"`
	Payload      json.RawMessage `json:"payload"`
}

// storePolicySnapshotOrWarn records the document that was applied, and reports a
// failure to record it as the DEGRADATION it is rather than as a failed refresh.
//
// THE DOCUMENT IS ALREADY APPLIED AT THIS POINT. Returning this failure as the
// apply's made the panel say "the most recent refresh failed" while it was running
// the newest policy and showing the ceiling from the one before — a banner that
// contradicts itself, about a local file. That is what an operator saw on a MySQL
// deployment: nothing else had ever written to the data directory, so this
// snapshot was the first thing to try, and it ran as a user with no permission
// there.
//
// What is lost is the NEXT boot's replay when it cannot fetch. That path already
// treats a missing snapshot as "nothing cached" and fetches, which is where it
// starts without a snapshot at all — so this is a named degradation, logged
// rather than swallowed, and not a reason to claim the policy did not arrive.
func storePolicySnapshotOrWarn(payload remoteCompatPayload) {
	if err := storePolicySnapshot(payload); err != nil {
		log.Warn("policy snapshot not stored; the document is applied, and a boot that cannot fetch will fall back to the compiled baseline", "err", err)
	}
}

// snapshotWriteMu serializes the read-modify-write below. Two products are stored
// by two different fetches, and an interleaving that lost one of them would leave
// the next boot restoring one ceiling and fetching the other.
var snapshotWriteMu sync.Mutex

// storePolicySnapshot persists the document that was just applied, BESIDE whatever
// the other product already left here.
//
// Written to a temporary file and renamed into place: a reader either sees the
// previous snapshot or this one, never a half-written file. The temporary name
// is fixed rather than random so a crash leaves at most one stray file, which
// the next write overwrites.
func storePolicySnapshot(payload remoteCompatPayload) error {
	dir := getCacheDir()
	if dir == "" {
		return nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode policy snapshot: %w", err)
	}
	sum := sha256.Sum256(body)
	document := policySnapshotDocument{
		PolicySchema: payload.SchemaVersion,
		Revision:     payload.UpdatedAt,
		Source:       compatSnapshotSource(payload),
		FetchedAt:    time.Now().UTC(),
		Digest:       hex.EncodeToString(sum[:]),
		Payload:      body,
	}

	snapshotWriteMu.Lock()
	defer snapshotWriteMu.Unlock()

	// THE OTHER PRODUCT'S DOCUMENT IS CARRIED FORWARD, not dropped. Each document
	// is stored as it is applied, so replacing the file with only this one would
	// make the next boot restore a single ceiling and fetch for the other — the
	// half-a-policy state the container exists to prevent.
	snapshot := policySnapshot{SnapshotSchema: policySnapshotSchema, Documents: map[string]policySnapshotDocument{}}
	target := filepath.Join(dir, policySnapshotFile)
	if raw, err := os.ReadFile(target); err == nil {
		var existing policySnapshot
		// A file this build cannot read is overwritten rather than merged: it is
		// either a format from before the split or corrupt, and neither is a
		// document to carry forward.
		if json.Unmarshal(raw, &existing) == nil && existing.SnapshotSchema == policySnapshotSchema && existing.Documents != nil {
			snapshot.Documents = existing.Documents
		}
	}
	snapshot.Documents[payload.Product] = document

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure cache dir: %w", err)
	}
	// The digest covers the payload AS STORED, so the file must be written in the
	// same form that was hashed. json.Marshal writes a RawMessage compacted,
	// which is what `body` already is, so the bytes read back hash to the same
	// value. MarshalIndent would re-indent the embedded document — rewriting the
	// very bytes the digest is over — and every snapshot would then fail its own
	// integrity check on the next boot.
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode policy snapshot: %w", err)
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0o644); err != nil {
		return fmt.Errorf("write policy snapshot: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish policy snapshot: %w", err)
	}
	return nil
}

// compatSnapshotSource names where the document came from, for diagnostics. It
// is recorded rather than inferred: a snapshot with no provenance cannot be
// reported on when an operator asks why a range is what it is.
func compatSnapshotSource(payload remoteCompatPayload) string {
	if payload.Product != "" {
		return payload.Product + " ranges document"
	}
	if payload.Major > 0 {
		return fmt.Sprintf("per-major manifest v%d", payload.Major)
	}
	return "unknown"
}

// LoadPolicySnapshot replays the last validated snapshot at boot, so an instance
// that starts with no egress still has the range it had before the restart.
//
// It refuses rather than guesses. A snapshot whose digest does not match its own
// payload, whose format this build does not know, or whose document does not
// apply to this build is NOT installed — and the active state is left exactly as
// it was, which is the honest outcome: the previous value came from somewhere
// this file cannot vouch for, and inventing a replacement from a file that fails
// its own integrity check is how a corrupted cache becomes an upgrade
// eligibility.
//
// A missing file is not an error. Neither is a file that fails to parse: the
// caller logs it and carries on with whatever is already active.
func LoadPolicySnapshot() error {
	dir := getCacheDir()
	if dir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, policySnapshotFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read policy snapshot: %w", err)
	}
	var snapshot policySnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return fmt.Errorf("decode policy snapshot: %w", err)
	}
	if snapshot.SnapshotSchema != policySnapshotSchema {
		return fmt.Errorf("policy snapshot format %d, this PSP build only reads %d", snapshot.SnapshotSchema, policySnapshotSchema)
	}
	if len(snapshot.Documents) == 0 {
		return errors.New("policy snapshot carries no documents")
	}
	// EVERY DOCUMENT IS REPLAYED, AND EACH STANDS ON ITS OWN — the same rule the
	// fetch side follows. One product's document failing its integrity check says
	// nothing about the other's, and refusing both would drop a range that is
	// perfectly good because its neighbour is not.
	var failures []string
	for product, document := range snapshot.Documents {
		if err := replaySnapshotDocument(document); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", product, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("policy snapshot does not apply to this build: %s", strings.Join(failures, "; "))
	}
	return nil
}

// replaySnapshotDocument verifies one document and installs it through the SAME
// apply path the fetch used. That is what makes "startup re-matches against the
// current build" true rather than aspirational: there is no second, weaker rule
// for the boot case.
func replaySnapshotDocument(document policySnapshotDocument) error {
	if len(document.Payload) == 0 {
		return errors.New("carries no payload")
	}
	sum := sha256.Sum256(document.Payload)
	if hex.EncodeToString(sum[:]) != document.Digest {
		return errors.New("digest does not match its payload; refusing to install a document that fails its own integrity check")
	}
	var payload remoteCompatPayload
	if err := json.Unmarshal(document.Payload, &payload); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	if err := applyCompatPayload(payload); err != nil {
		return err
	}
	return nil
}
