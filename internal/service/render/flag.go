package render

import "strings"

// applyRegionFlagPrefix mutates items in place, prepending the Unicode flag
// for each real node's Region (followed by a space). Separators and nodes
// without a parseable Region are left untouched. Idempotent only against
// already-flag-prefixed names if the source is identical — admins who pre-
// flag their DisplayName manually should leave this toggle off.
func applyRegionFlagPrefix(items []renderItem) {
	for i := range items {
		it := &items[i]
		if it.isSeparator || it.node == nil {
			continue
		}
		flag := regionFlagEmoji(it.node.Region)
		if flag == "" {
			continue
		}
		it.name = flag + " " + it.name
	}
}

// regionFlagEmoji returns the Unicode flag emoji for an ISO 3166-1 alpha-2
// region code. Empty string for empty or malformed input. Case-insensitive.
//
// Each letter of a 2-letter ISO code maps to its regional indicator symbol
// (🇦 = U+1F1E6, …, 🇿 = U+1F1FF). A pair of regional indicators is rendered
// as the corresponding flag by browsers and proxy clients that ship an emoji
// font. Clients without emoji support fall back to showing the two
// letter-boxes, which is still legible.
//
// We do NOT validate that the resulting pair is an assigned country code —
// that would force a hard-coded allow-list. Any 2-letter input passes; the
// caller is the admin who set the node's region, and an unassigned code
// just renders as two letter-boxes rather than a flag. Harmless either way.
func regionFlagEmoji(region string) string {
	if len(region) != 2 {
		return ""
	}
	r := strings.ToUpper(region)
	a, b := rune(r[0]), rune(r[1])
	if a < 'A' || a > 'Z' || b < 'A' || b > 'Z' {
		return ""
	}
	return string([]rune{0x1F1E6 + (a - 'A'), 0x1F1E6 + (b - 'A')})
}
