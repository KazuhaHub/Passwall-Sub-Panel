package domain

import (
	"encoding/json"
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

// ResolveRouteLineLabels is ResolveRelayLabels for one VLESS route line;
// prior is nil for a newly created line.
func ResolveRouteLineLabels(line *VlessRouteLine, prior *VlessRouteLine, region string, tags []string) {
	if line.RegionUnresolved {
		line.Region = region
		if prior != nil {
			line.Region = prior.Region
		}
		line.RegionUnresolved = false
	}
	if line.TagsUnresolved {
		line.Tags = CopyLineTags(tags)
		if prior != nil {
			line.Tags = CopyLineTags(prior.Tags)
		}
		line.TagsUnresolved = false
	}
	if line.Tags == nil {
		line.Tags = []string{}
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

// CheckRouteLabelsResolved guards the storage boundary.
func CheckRouteLabelsResolved(lines []VlessRouteLine) error {
	for _, line := range lines {
		if line.RegionUnresolved || line.TagsUnresolved {
			return ErrUnresolvedLineLabels
		}
	}
	return nil
}

// splitRouteLineDisplay is the single definition of a VLESS route line's
// display fields: name, name-only, Region and Tags. They only change PSP's own
// subscription output, never the 3X-UI routing rule. A field added to
// VlessRouteLine later stays in the rule half, and therefore requires the
// remote path, unless it is explicitly moved here.
func splitRouteLineDisplay(line VlessRouteLine) (rule, display VlessRouteLine) {
	display = VlessRouteLine{Name: line.Name, OnlyLineName: line.OnlyLineName, Region: line.Region, Tags: CopyLineTags(line.Tags)}
	rule = line
	rule.Name, rule.OnlyLineName, rule.Region, rule.Tags = "", false, "", nil
	rule.RegionUnresolved, rule.TagsUnresolved = false, false
	return rule, display
}

// CompareRouteLineDisplay reports whether two lines agree on every
// rule-relevant field and whether their display fields differ.
func CompareRouteLineDisplay(saved, next VlessRouteLine) (sameRule, displayChanged bool) {
	savedRule, savedDisplay := splitRouteLineDisplay(saved)
	nextRule, nextDisplay := splitRouteLineDisplay(next)
	return sameEncoding(savedRule, nextRule), !sameEncoding(savedDisplay, nextDisplay)
}

// sameEncoding compares the persisted form, so nil and empty slices that the
// JSON column cannot distinguish never count as a change.
func sameEncoding(a, b VlessRouteLine) bool {
	left, errA := json.Marshal(a)
	right, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(left) == string(right)
}
