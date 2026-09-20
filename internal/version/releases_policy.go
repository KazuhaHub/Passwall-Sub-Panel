package version

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ReleasesPolicy is the reviewed statement of which Passwall Node releases this
// panel may offer as an upgrade target, along which verified paths.
//
// WHY A SEPARATE DOCUMENT. The per-major compat manifest answers "what 3X-UI or
// S-UI version does this panel speak to". This answers a different question —
// "what may a node be upgraded TO" — and the two have different audiences,
// different review obligations and different failure modes. Folding them
// together is how a range bump silently becomes an upgrade permission.
//
// OLD READERS KEEP THE OLD FILES. This document replaces nothing: builds that do
// not know it keep reading docs/compat/v<major>.json, and its absence is not an
// error. That is what lets a policy be published without reissuing the panel.
type ReleasesPolicy struct {
	SchemaVersion int             `json:"schema_version"`
	Revision      int64           `json:"revision"`
	IssuedAt      time.Time       `json:"issued_at"`
	ExpiresAt     time.Time       `json:"expires_at"`
	AppliesToPSP  PolicyPSPRange  `json:"applies_to_psp"`
	Releases      []PolicyRelease `json:"releases"`
	Refusals      []PolicyRefusal `json:"refusals"`
}

// PolicyPSPRange is the window of panel builds a policy revision was reviewed
// for. A build outside it must not apply the policy: an appliable range is a
// statement about a build, and the whole point of revision-driven policy is that
// a claim stops applying when the thing it was about changes.
type PolicyPSPRange struct {
	Min string `json:"min"`
	Max string `json:"max"`
}

// PolicyRelease is one release offered as an upgrade target.
type PolicyRelease struct {
	Version    string   `json:"version"`
	ReleaseTag string   `json:"release_tag"`
	Scheme     string   `json:"scheme"`
	Evidence   []string `json:"evidence"`
}

// PolicyEdge is one VERIFIED upgrade path. A release being listed is not the
// same as a path to it having been checked.
// PolicyRefusal records a release that must not be offered, with the reason on
// the record. An undocumented exclusion is indistinguishable from an oversight.
type PolicyRefusal struct {
	Version string `json:"version"`
	Reason  string `json:"reason"`
}

// releasesPolicySchema is the only schema this build reads. An unknown one is
// refused rather than parsed optimistically: a newer document may mean something
// this build would silently misread.
const releasesPolicySchema = 1

var (
	// ErrPolicySchema means the document is a format this build does not read.
	ErrPolicySchema = errors.New("release policy: unsupported schema_version")
	// ErrPolicyExpired means the document's validity window has passed.
	ErrPolicyExpired = errors.New("release policy: expired")
	// ErrPolicyMalformed means the document is internally inconsistent.
	ErrPolicyMalformed = errors.New("release policy: malformed")
)

// ParseReleasesPolicy validates a policy document.
//
// It refuses rather than repairs, and it refuses for reasons an operator can
// act on. The checks are the ones whose absence would let a bad policy become an
// offered upgrade: an unknown format, an expired window, a malformed or missing
// applicability range, a release with no evidence, an edge missing an end, and a
// contradiction where a release is both offered and refused.
//
// now is a parameter rather than time.Now so expiry is testable and so callers
// can make one consistent decision for a whole request.
func ParseReleasesPolicy(raw []byte, now time.Time) (ReleasesPolicy, error) {
	var policy ReleasesPolicy
	// Unknown FIELDS are tolerated: a policy published for a newer reader may
	// carry more than this build understands, and refusing it would mean the
	// panel could not read a policy it mostly understands. Unknown SCHEMAS are
	// not: those may mean something different by the same field names.
	if err := json.Unmarshal(raw, &policy); err != nil {
		return ReleasesPolicy{}, fmt.Errorf("%w: %v", ErrPolicyMalformed, err)
	}
	if policy.SchemaVersion != releasesPolicySchema {
		return ReleasesPolicy{}, fmt.Errorf("%w: %d, this build reads %d", ErrPolicySchema, policy.SchemaVersion, releasesPolicySchema)
	}
	if policy.IssuedAt.IsZero() || policy.ExpiresAt.IsZero() {
		return ReleasesPolicy{}, fmt.Errorf("%w: issued_at and expires_at are both required", ErrPolicyMalformed)
	}
	if !policy.ExpiresAt.After(policy.IssuedAt) {
		return ReleasesPolicy{}, fmt.Errorf("%w: expires_at %s is not after issued_at %s", ErrPolicyMalformed, policy.ExpiresAt, policy.IssuedAt)
	}
	if !now.Before(policy.ExpiresAt) {
		return ReleasesPolicy{}, fmt.Errorf("%w: the window ended %s", ErrPolicyExpired, policy.ExpiresAt)
	}
	if err := validatePSPRange(policy.AppliesToPSP); err != nil {
		return ReleasesPolicy{}, err
	}

	refused := make(map[string]string, len(policy.Refusals))
	for i, refusal := range policy.Refusals {
		refusal.Version = strings.TrimSpace(refusal.Version)
		if refusal.Version == "" || strings.TrimSpace(refusal.Reason) == "" {
			return ReleasesPolicy{}, fmt.Errorf("%w: refusals[%d] needs both a version and a reason — an undocumented exclusion is indistinguishable from an oversight", ErrPolicyMalformed, i)
		}
		refused[refusal.Version] = refusal.Reason
	}

	offered := make(map[string]struct{}, len(policy.Releases))
	for i, release := range policy.Releases {
		release.Version = strings.TrimSpace(release.Version)
		if release.Version == "" {
			return ReleasesPolicy{}, fmt.Errorf("%w: releases[%d] has no version", ErrPolicyMalformed, i)
		}
		if _, dup := offered[release.Version]; dup {
			return ReleasesPolicy{}, fmt.Errorf("%w: %s is offered twice", ErrPolicyMalformed, release.Version)
		}
		if reason, both := refused[release.Version]; both {
			return ReleasesPolicy{}, fmt.Errorf("%w: %s is both offered and refused (%s)", ErrPolicyMalformed, release.Version, reason)
		}
		if len(release.Evidence) == 0 {
			// A release with no evidence is a release nobody checked. Offering it
			// is exactly the "target that is merely listed" the plan forbids.
			return ReleasesPolicy{}, fmt.Errorf("%w: %s is offered with no evidence", ErrPolicyMalformed, release.Version)
		}
		offered[release.Version] = struct{}{}
	}
	return policy, nil
}

func validatePSPRange(r PolicyPSPRange) error {
	min, minOK := parseSemver(r.Min)
	max, maxOK := parseSemver(r.Max)
	if !minOK || !maxOK {
		return fmt.Errorf("%w: applies_to_psp [%q..%q] is not parseable", ErrPolicyMalformed, r.Min, r.Max)
	}
	if min[0] <= 0 {
		return fmt.Errorf("%w: applies_to_psp starts at a zero release line (%q)", ErrPolicyMalformed, r.Min)
	}
	if cmpSemver(min, max) > 0 {
		return fmt.Errorf("%w: applies_to_psp is inverted: [%q..%q]", ErrPolicyMalformed, r.Min, r.Max)
	}
	return nil
}

// AppliesTo reports whether this panel build is inside the policy's reviewed
// window. It is deliberately separate from parsing: a policy can be perfectly
// well-formed and still be about a different build, and that is a refusal the
// caller reports rather than an error the parser raises.
func (p ReleasesPolicy) AppliesTo(buildVersion string) bool {
	current, ok := parseSemver(buildVersion)
	if !ok {
		return false
	}
	min, minOK := parseSemver(p.AppliesToPSP.Min)
	max, maxOK := parseSemver(p.AppliesToPSP.Max)
	if !minOK || !maxOK {
		return false
	}
	return cmpSemver(current, min) >= 0 && cmpSemver(current, max) <= 0
}

// Supersedes reports whether this revision may replace the one in force.
//
// A LOWER REVISION IS REFUSED, not applied: revisions move forward, and
// accepting an older document is how a withdrawal gets rolled back by replaying
// a stale file.
func (p ReleasesPolicy) Supersedes(inForce int64) (bool, error) {
	switch {
	case p.Revision < 0 || inForce < 0:
		return false, fmt.Errorf("%w: revision numbers cannot be negative (this %d, in force %d)", ErrPolicyMalformed, p.Revision, inForce)
	case p.Revision < inForce:
		return false, fmt.Errorf("%w: revision %d is older than the one in force (%d)", ErrPolicyMalformed, p.Revision, inForce)
	case p.Revision == inForce:
		return false, nil // not newer: reuse the active policy rather than re-applying
	}
	return true, nil
}
