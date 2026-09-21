package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// THE CORE CATALOG'S TIER NAMES, and their meaning is a support claim rather than
// a label.
//
// The catalog itself is published by the Node project as a signed document — the
// tier, the evidence matrix behind it, the REALITY compatibility table and the
// per-asset digests are the output of acceptance testing and are in no API. These
// constants exist because PSP GATES on the tier: it offers a core, refuses one, or
// asks for confirmation, and a tier name it cannot read would silently fall
// through every one of those decisions.
const (
	CoreTierRecommended    = "recommended"
	CoreTierVerified       = "verified"
	CoreTierConfigVerified = "config_verified"
	CoreTierRestricted     = "restricted"
)

// NormalizeCoreVersion reduces a core version to the canonical major.minor.patch
// form, so a stored or requested version can be compared with a published one.
//
// IT IS HERE AND NOT WITH THE CATALOG READER because the comparison happens in two
// places that must agree: the document's own lookup, and the reconciliation that
// compares a node's REPORTED core version against the catalog. Two normalizers
// would eventually disagree about a shape, and the disagreement would look like
// "the node is running an unknown core".
//
// A LEADING v IS ACCEPTED on input because upstream releases are tagged that way
// and operators paste what they see; it is never produced on output, because the
// catalog stores versions without it and the two forms have to compare equal.
func NormalizeCoreVersion(raw string) (string, error) {
	value := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(raw), "v"), "V")
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("core version %q must be major.minor.patch", raw)
	}
	for _, part := range parts {
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 || strconv.Itoa(parsed) != part {
			return "", fmt.Errorf("core version %q is not canonical", raw)
		}
	}
	return value, nil
}
