package domain

import (
	"fmt"
	"strings"
)

// Line label bounds (ADR 0040). They apply to values an administrator
// submits for a line; values copied verbatim from the node are not re-checked
// so a create never fails because of the node's own labels.
const (
	MaxLineRegionBytes = 64
	MaxLineTags        = 32
	MaxLineTagBytes    = 64
)

// NormalizeLineRegion trims surrounding whitespace without changing case.
func NormalizeLineRegion(region string) (string, error) {
	region = strings.TrimSpace(region)
	if len(region) > MaxLineRegionBytes {
		return "", fmt.Errorf("%w: line region exceeds %d bytes", ErrValidation, MaxLineRegionBytes)
	}
	return region, nil
}

// NormalizeLineTags trims each tag, drops empty and duplicate tags while
// keeping the first occurrence's order, and never returns nil, so an explicit
// clear is stored as an empty array rather than an absent value.
func NormalizeLineTags(tags []string) ([]string, error) {
	out := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}
		if len(tag) > MaxLineTagBytes {
			return nil, fmt.Errorf("%w: line tag exceeds %d bytes", ErrValidation, MaxLineTagBytes)
		}
		seen[tag] = true
		out = append(out, tag)
	}
	if len(out) > MaxLineTags {
		return nil, fmt.Errorf("%w: at most %d tags per line", ErrValidation, MaxLineTags)
	}
	return out, nil
}

// CopyLineTags copies node tags verbatim into a line. It deliberately does not
// normalize: the backfill must keep rendered output byte-identical.
func CopyLineTags(tags []string) []string {
	return append(make([]string, 0, len(tags)), tags...)
}

// ResolveRelayLabels replaces omitted Region/Tags markers. A line whose ID
// exists in previous keeps its saved value; a new line takes the node's value
// after this save. IDs must already be reconciled (see ReconcileRelayIDs).
func ResolveRelayLabels(previous, next []RelayLine, region string, tags []string) {
	known := make(map[string]RelayLine, len(previous))
	for _, line := range previous {
		known[line.ID] = line
	}
	for i := range next {
		line := &next[i]
		prior, existed := known[line.ID]
		if line.ID == "" {
			existed = false
		}
		if line.RegionUnresolved {
			line.Region = region
			if existed {
				line.Region = prior.Region
			}
			line.RegionUnresolved = false
		}
		if line.TagsUnresolved {
			line.Tags = CopyLineTags(tags)
			if existed {
				line.Tags = CopyLineTags(prior.Tags)
			}
			line.TagsUnresolved = false
		}
		if line.Tags == nil {
			line.Tags = []string{}
		}
	}
}

// ErrUnresolvedLineLabels is returned by storage when a parsing step forgot to
// resolve an omitted field. Failing loudly prevents a silent clear.
var ErrUnresolvedLineLabels = fmt.Errorf("%w: line region/tags were not resolved before storage", ErrValidation)

// CheckRelayLabelsResolved guards the storage boundary.
func CheckRelayLabelsResolved(lines []RelayLine) error {
	for _, line := range lines {
		if line.RegionUnresolved || line.TagsUnresolved {
			return ErrUnresolvedLineLabels
		}
	}
	return nil
}
