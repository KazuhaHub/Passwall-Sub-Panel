package domain

import "sort"

// LineSlot is one entry of a subscription's output sequence as the line
// placement rule sees it. The sequence is what applyLayout + expandRelays
// produce today: nodes' direct entries, their relay lines right after them,
// and separators.
//
// Key is the entry's position on the shared sort_order scale (nodes, standalone
// separators and line positions all live on it):
//   - a direct entry: its node's sort_order
//   - a standalone separator: its own sort_order
//   - a relay line with no stored position (attached): its landing's sort_order,
//     because it is emitted right after the landing exactly as it always was
//   - a relay line with a stored position (floating): that position
//   - a group positional separator (Layout.Separators): none; it is placed by
//     index in the pre-expansion list and has no place on the scale (Keyed false)
type LineSlot struct {
	Key      int
	Keyed    bool
	Floating bool
}

// PlaceFloatingLines is the placement rule of per-line subscription ordering.
// It returns the output sequence as a permutation of the input indices.
//
// Compatibility guarantee: when no slot is Floating the result is the identity.
// A deployment with no stored line position therefore renders exactly the bytes
// it rendered before this feature existed, for every group and every format —
// the rule is an overlay on the old pipeline, not a replacement of it.
//
// Otherwise floating lines are lifted out of the sequence and each goes
// immediately after the LAST non-floating, keyed slot whose key is <= its own,
// after any attached lines of that slot (they carry its key) and before any
// keyless slot that follows it, so a positional group separator keeps its place
// between the entries it was inserted between. A line below every key goes to
// the very start. Floating lines that share an anchor are ordered by key, then
// by their place in the input (landing order, then declaration order), which
// makes the result deterministic for equal positions.
//
// "Last slot whose key is <= k" rather than "the slot before the first larger
// key" is deliberate: group sort weights can leave the stream's keys
// non-monotonic, and only the former is defined for any sequence. For the
// monotonic streams an unweighted group produces the two are the same.
func PlaceFloatingLines(slots []LineSlot) []int {
	order := make([]int, 0, len(slots))
	var floating []int
	for i, s := range slots {
		if s.Floating {
			floating = append(floating, i)
		}
	}
	if len(floating) == 0 {
		for i := range slots {
			order = append(order, i)
		}
		return order
	}
	// Stable: equal keys keep their input order (landing order, declaration order).
	sort.SliceStable(floating, func(a, b int) bool { return slots[floating[a]].Key < slots[floating[b]].Key })

	after := make(map[int][]int, len(floating))
	for _, f := range floating {
		anchor := -1
		for i, s := range slots {
			if !s.Floating && s.Keyed && s.Key <= slots[f].Key {
				anchor = i
			}
		}
		after[anchor] = append(after[anchor], f)
	}
	order = append(order, after[-1]...)
	for i, s := range slots {
		if s.Floating {
			continue
		}
		order = append(order, i)
		order = append(order, after[i]...)
	}
	return order
}
