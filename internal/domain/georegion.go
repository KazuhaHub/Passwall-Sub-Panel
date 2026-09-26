package domain

import "strings"

// NormalizeRegionCode returns rc trimmed and upper-cased when it is a
// plausible ISO 3166-2 subdivision part: 1–3 bytes, each ASCII [A-Za-z0-9]
// (ISO allows up to three alphanumerics; MaxMind's iso_code is that part,
// and some are numeric, e.g. JP "13" or pre-2017 CN "22"). The ASCII check
// runs BEFORE upper-casing: strings.ToUpper maps "ſ" to "S" and "ı" to "I",
// so the other order would admit "gſ" as "GS". Anything else — "CN-GD",
// "GUAN", non-ASCII — is "", and readers fall back to the region name.
//
// A code is a display attribute (it names a province in the admin UI's
// language); it is never a key. Rejecting rather than repairing an odd value
// is therefore the safe direction: the worst a rejected code costs is the
// database's own spelling of the name, while a repaired one could name the
// wrong province.
func NormalizeRegionCode(rc string) string {
	rc = strings.TrimSpace(rc)
	if len(rc) == 0 || len(rc) > 3 {
		return ""
	}
	// Byte-wise on purpose: any byte of a multi-byte rune is >= 0x80 and
	// fails every range below, so no Unicode case folding can smuggle a
	// look-alike through.
	for i := 0; i < len(rc); i++ {
		c := rc[i]
		if !('0' <= c && c <= '9' || 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z') {
			return ""
		}
	}
	return strings.ToUpper(rc)
}

// PreferRegionCode is the code kept when a region's sources disagree: the
// bytewise-smaller non-empty of a and b (both already normalized). One region
// then carries one code per computation, whatever order its sources arrive in.
//
// Disagreement is a database defect, not a signal, and it is resolved here
// only so the display is stable: the region itself is still keyed by name, so
// two codes can never split it (see GeoSpot.RC).
func PreferRegionCode(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return min(a, b)
}
