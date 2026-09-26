// Package subdevice turns what a subscription client declares about its own
// device into the two values the panel may keep: a keyed, per-account device
// id and a short sanitized label.
//
// Some clients send a stable hardware id (x-hwid) and a description of the
// device with every subscription fetch. The raw id is a long-lived identifier
// of a physical device, so it never leaves the request: it is neither stored
// nor logged, and only an HMAC under a panel-derived key, salted with the
// account id, is kept. Two accounts sharing one phone therefore read as two
// unrelated ids, and a leaked sub_logs table cannot be matched against a list
// of known hardware ids without the panel secret.
//
// The package is deliberately pure: no logging, no errors, and only the
// standard-library imports TestSubdeviceImportsNoLogger allows. Every input is
// attacker-controlled request data, so every input is cut to a fixed size
// before anything else looks at it.
package subdevice

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Header names follow the convention some clients use (Remnawave's HWID
// headers). UNVERIFIED against a captured request: confirm before release.
const (
	HeaderHWID        = "X-Hwid"
	HeaderDeviceOS    = "X-Device-Os"
	HeaderOSVersion   = "X-Ver-Os"
	HeaderDeviceModel = "X-Device-Model"
	IDHexLen          = 16 // stored device-id length: 64 bits of a per-user HMAC
	LabelMaxRunes     = 64 // sub_logs.device_label size:64
	partMaxRunes      = 32
	inputMaxBytes     = 256 // each header is cut to this before any processing
	hwidMinLen        = 8
	hwidMaxLen        = 128
	// keyLabel separates the device key from every other key derived from the
	// same panel secret: the at-rest AES key is SHA-256 of the material itself
	// (sqlstore.ConfigureSecretKey) and the JWT HMAC signs a dotted base64url
	// input, which can never equal this label (it has '/' and no '.').
	keyLabel = "psp/sub-device-id/v1"
)

// Hasher turns a declared HWID into a keyed, per-user device id. The raw value
// never leaves the request: it is neither stored nor logged. A Hasher is
// immutable once built and safe for concurrent use.
type Hasher struct{ key [32]byte }

// NewHasher derives the key as HMAC-SHA256(key=TrimSpace(material), msg=keyLabel).
// Blank material returns nil; a nil Hasher records nothing.
//
// The material is trimmed exactly as the at-rest key's is, so a trailing
// newline in a hand-edited config is not a key rotation. A real rotation of
// the material (encryption_key, or jwt_secret on a legacy config) changes
// every device id; old and new ids then coexist until sub_logs retention
// ages the old ones out.
func NewHasher(material string) *Hasher {
	material = strings.TrimSpace(material)
	if material == "" {
		return nil
	}
	mac := hmac.New(sha256.New, []byte(material))
	mac.Write([]byte(keyLabel))
	h := &Hasher{}
	copy(h.key[:], mac.Sum(nil))
	return h
}

// ID = lowercase hex(HMAC-SHA256(key, "hwid:v1:" + decimal(userID) + ":" + hwid))[:16].
// hwid is ValidHWID's normalized value. Per user, so one physical device is not
// linkable across accounts or panels. "" when h is nil.
//
// 64 bits is plenty for counting one account's devices (a collision needs
// billions of devices on ONE account) while keeping the stored value short
// and useless as a global device identifier. The derivation is part of the
// stored data: changing it turns every stored id into a new device.
func (h *Hasher) ID(userID int64, hwid string) string {
	if h == nil {
		return ""
	}
	mac := hmac.New(sha256.New, h.key[:])
	mac.Write([]byte("hwid:v1:"))
	mac.Write([]byte(strconv.FormatInt(userID, 10)))
	// The separator keeps (1, "23…") and (12, "3…") apart: a hwid is at
	// least 8 printable bytes, but it may start with a digit.
	mac.Write([]byte{':'})
	mac.Write([]byte(hwid))
	return hex.EncodeToString(mac.Sum(nil)[:IDHexLen/2])
}

// ValidHWID cuts raw to inputMaxBytes, trims, accepts 8..128 bytes each in
// 0x21..0x7e, and returns the value ASCII-lowercased (a client that changes
// case between versions is still one device). Anything else is an anonymous
// fetch. A constant placeholder a client sends in place of a real id collapses
// that account's devices into one: an undercount, the safe direction.
func ValidHWID(raw string) (string, bool) {
	// Cut first: a megabyte of header must not be trimmed or scanned.
	if len(raw) > inputMaxBytes {
		raw = raw[:inputMaxBytes]
	}
	v := strings.TrimSpace(raw)
	if len(v) < hwidMinLen || len(v) > hwidMaxLen {
		return "", false
	}
	for i := 0; i < len(v); i++ {
		if c := v[i]; c < 0x21 || c > 0x7e {
			return "", false
		}
	}
	// v is printable ASCII here, so strings.ToLower is exactly an ASCII
	// lowercase (its ASCII fast path) and cannot change the length.
	return strings.ToLower(v), true
}

// Label builds "<os> <version> · <model>". For each part: cut to inputMaxBytes,
// drop invalid UTF-8, drop unicode.IsControl and unicode.Cf runes (bidi
// overrides included), collapse whitespace runs, trim, cap at 32 runes; join os
// and version with a space, then non-empty groups with " · "; cap the result at
// 64 runes. "" when nothing survives.
//
// The label is shown to admins in the sub-log table. Format runes are the
// dangerous ones there: a right-to-left override would let a client make its
// label render as different text, and zero-width runes would make two labels
// that look identical compare different.
func Label(os, version, model string) string {
	head := joinNonEmpty(" ", sanitizePart(os), sanitizePart(version))
	out := joinNonEmpty(" · ", head, sanitizePart(model))
	if utf8.RuneCountInString(out) <= LabelMaxRunes {
		return out
	}
	out = capRunes(out, LabelMaxRunes)
	// The cut may land inside or just after the " · " separator; a label
	// ending in a dangling dot or space reads as truncated garbage.
	return strings.TrimSuffix(strings.TrimRight(out, " "), " ·")
}

// sanitizePart applies the per-part rules in one pass. Whitespace is only
// ever written as a single space in front of a kept rune, which collapses
// runs and trims both ends without a second scan.
func sanitizePart(s string) string {
	if len(s) > inputMaxBytes {
		s = s[:inputMaxBytes]
	}
	var b strings.Builder
	n := 0 // runes written
	space := false
	for i := 0; i < len(s) && n < partMaxRunes; {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == utf8.RuneError && size == 1:
			// Invalid UTF-8, including a rune split by the cut above.
			continue
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			// Dropped before whitespace is considered, so a tab or newline
			// between two spaces cannot survive as a second separator.
			continue
		case unicode.IsSpace(r):
			space = n > 0
			continue
		}
		if space {
			if n+2 > partMaxRunes {
				// No room for the space and a rune after it; stopping here
				// is the trim.
				break
			}
			b.WriteByte(' ')
			n++
			space = false
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

func joinNonEmpty(sep, a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + sep + b
}

// capRunes returns s cut to its first n runes. s is valid UTF-8 here: every
// part went through sanitizePart.
func capRunes(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}
