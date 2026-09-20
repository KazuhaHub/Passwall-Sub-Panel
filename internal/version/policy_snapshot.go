package version

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
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
const policySnapshotSchema = 1

type policySnapshot struct {
	SnapshotSchema int             `json:"snapshot_schema"`
	PolicySchema   int             `json:"policy_schema"`
	Revision       string          `json:"revision"`
	Source         string          `json:"source"`
	FetchedAt      time.Time       `json:"fetched_at"`
	Digest         string          `json:"digest"`
	Payload        json.RawMessage `json:"payload"`
}

// storePolicySnapshot persists the document that was just applied.
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
	snapshot := policySnapshot{
		SnapshotSchema: policySnapshotSchema,
		PolicySchema:   payload.SchemaVersion,
		Revision:       payload.UpdatedAt,
		Source:         compatSnapshotSource(payload),
		FetchedAt:      time.Now().UTC(),
		Digest:         hex.EncodeToString(sum[:]),
		Payload:        body,
	}
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
	target := filepath.Join(dir, policySnapshotFile)
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
	if len(snapshot.Payload) == 0 {
		return errors.New("policy snapshot carries no payload")
	}
	sum := sha256.Sum256(snapshot.Payload)
	if hex.EncodeToString(sum[:]) != snapshot.Digest {
		return errors.New("policy snapshot digest does not match its payload; refusing to install a document that fails its own integrity check")
	}
	var payload remoteCompatPayload
	if err := json.Unmarshal(snapshot.Payload, &payload); err != nil {
		return fmt.Errorf("decode policy snapshot payload: %w", err)
	}
	// The SAME apply path the fetch used. This is what makes "startup re-matches
	// against the current build" true rather than aspirational: there is no
	// second, weaker rule for the boot case.
	if err := applyCompatPayload(payload); err != nil {
		return fmt.Errorf("policy snapshot does not apply to this build: %w", err)
	}
	return nil
}
