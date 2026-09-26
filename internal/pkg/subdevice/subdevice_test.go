package subdevice

import (
	"crypto/hmac"
	"crypto/sha256"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// The x-hwid header is a stable identifier a client volunteers about the
// device it runs on. These tests pin the three properties the panel owes the
// person behind it: the raw value never survives the request (only a keyed,
// per-account digest does), what an admin sees is a short sanitized label and
// nothing a client could smuggle through it, and a hostile header costs a
// bounded amount of work however large it is.

func TestValidHWID(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{"empty", "", "", false},
		{"seven bytes", "abcdefg", "", false},
		{"eight bytes", "abcdefgh", "abcdefgh", true},
		{"128 bytes", strings.Repeat("a", 128), strings.Repeat("a", 128), true},
		{"129 bytes", strings.Repeat("a", 129), "", false},
		{"inner space", "abcd efgh", "", false},
		{"tab", "abcd\tefgh", "", false},
		{"non-ASCII", "abcdéfgh", "", false},
		{"DEL", "abcdefg\x7f", "", false},
		{"NUL", "abcd\x00efgh", "", false},
		// Case is not identity: a client that upper-cases its id in one
		// release and not the next is still the same device.
		{"trimmed and lowercased", "  AbCdEfGh  ", "abcdefgh", true},
		{"uuid shaped", "3F2504E0-4F89-11D3-9A0C-0305E82C3301", "3f2504e0-4f89-11d3-9a0c-0305e82c3301", true},
		{"printable punctuation", "!~a=b-c_d/e+f", "!~a=b-c_d/e+f", true},
		{"one MiB", strings.Repeat("a", 1<<20), "", false},
		// The cut happens before the trim: 250 spaces leave only "abcdef" of
		// the id inside the first 256 bytes. Were the whole header trimmed
		// first, a megabyte of padding would be scanned on the request path.
		{"cut precedes trim", strings.Repeat(" ", 250) + "abcdefgh", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ValidHWID(tc.raw)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("ValidHWID(%.40q) = (%.40q, %v), want (%.40q, %v)", tc.raw, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// The derivation is part of the stored data: every device id already in
// sub_logs was computed this way, so a change here silently turns each
// account's devices into new ones. The golden value was computed outside Go
// (Python's hmac module) from the documented recipe.
func TestHasher_IDIsStablePerUser(t *testing.T) {
	const golden = "79aae7e4631cf91a"
	a := NewHasher("panel-secret-material")
	b := NewHasher("  panel-secret-material\n")
	if got := a.ID(7, "abcd1234efgh5678"); got != golden {
		t.Fatalf("ID = %q, want the documented derivation %q", got, golden)
	}
	// Material is trimmed exactly as the at-rest key is (sqlstore.ConfigureSecretKey),
	// so a trailing newline in a hand-edited config is not a key rotation.
	if got := b.ID(7, "abcd1234efgh5678"); got != golden {
		t.Fatalf("ID with padded material = %q, want %q", got, golden)
	}
}

// Per account, so one physical device is not linkable across accounts: two
// subscribers sharing a phone read as two unrelated digests.
func TestHasher_IDDiffersAcrossUsers(t *testing.T) {
	h := NewHasher("panel-secret-material")
	if a, b := h.ID(7, "abcd1234efgh5678"), h.ID(8, "abcd1234efgh5678"); a == b {
		t.Fatalf("users 7 and 8 share device id %q", a)
	}
	// "1" + ":" + "23..." and "12" + ":" + "3..." must not collide: the
	// separator is what keeps the user id and the hwid apart.
	if a, b := h.ID(1, "23456789a"), h.ID(12, "3456789a"); a == b {
		t.Fatalf("user/hwid boundary is ambiguous: both %q", a)
	}
}

// Keyed, so ids are not linkable across panels either, and a leaked table
// cannot be matched against a list of known hwids without the panel secret.
func TestHasher_IDDiffersAcrossKeys(t *testing.T) {
	a := NewHasher("panel-one").ID(7, "abcd1234efgh5678")
	b := NewHasher("panel-two").ID(7, "abcd1234efgh5678")
	if a == "" || a == b {
		t.Fatalf("different panel secrets gave %q and %q", a, b)
	}
}

// sub_logs.device_id is size:16; the admin view shows its first four.
func TestHasher_IDIs16LowerHex(t *testing.T) {
	id := NewHasher("panel-secret-material").ID(42, "3f2504e0-4f89-11d3-9a0c-0305e82c3301")
	if len(id) != IDHexLen {
		t.Fatalf("len(ID) = %d (%q), want %d", len(id), id, IDHexLen)
	}
	for _, c := range id {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("ID %q has non-lowercase-hex rune %q", id, c)
		}
	}
}

// A nil Hasher is how capture is switched off at the wiring level (no key
// material); it must record nothing rather than panic on a request.
func TestHasher_NilHasherReturnsEmpty(t *testing.T) {
	var h *Hasher
	if got := h.ID(7, "abcd1234efgh5678"); got != "" {
		t.Fatalf("nil Hasher ID = %q, want empty", got)
	}
}

func TestNewHasher_BlankMaterialIsNil(t *testing.T) {
	for _, m := range []string{"", "   ", "\n\t"} {
		if h := NewHasher(m); h != nil {
			t.Fatalf("NewHasher(%q) = %+v, want nil", m, h)
		}
	}
}

// The device key must be neither the at-rest AES key (SHA-256 of the same
// material, sqlstore/secrets.go) nor the material itself: an id is shown to
// admins, and it must reveal nothing that helps against the encryption of
// stored panel credentials.
func TestNewHasher_KeyIsDomainSeparated(t *testing.T) {
	const material = "panel-secret-material"
	h := NewHasher("  " + material + "  ")
	if h == nil {
		t.Fatal("NewHasher returned nil for non-blank material")
	}
	mac := hmac.New(sha256.New, []byte(material))
	mac.Write([]byte(keyLabel))
	var want [32]byte
	copy(want[:], mac.Sum(nil))
	if h.key != want {
		t.Fatalf("key = %x, want HMAC-SHA256(material, %q) = %x", h.key, keyLabel, want)
	}
	if h.key == sha256.Sum256([]byte(material)) {
		t.Fatal("device key equals the at-rest AES key")
	}
}

func TestLabel(t *testing.T) {
	cases := []struct {
		name             string
		os, version, mdl string
		want             string
	}{
		{"full", "iOS", "17.5", "iPhone15,2", "iOS 17.5 · iPhone15,2"},
		{"os only", "Android", "", "", "Android"},
		{"version and model", "", "14", "Pixel 8", "14 · Pixel 8"},
		{"model only", "", "", "Pixel 8", "Pixel 8"},
		{"all empty", "", "", "", ""},
		{"nothing survives", "\u202e\x00", "  ", "\u200b\ufeff", ""},
		// A NUL, a right-to-left override (which would let a label render
		// as something else in the admin table) and zero-width runes go.
		{"control and format runes", "iOS\x00", "17\u202e.5", "iPh\u200bone\ufeff", "iOS 17.5 · iPhone"},
		{"whitespace collapses", "a \t b", "", "", "a b"},
		{"unicode spaces collapse", "Pixel\u00a0\u3000 8", "", "", "Pixel 8"},
		{"line separators collapse", "Pixel\u2028\u20298", "", "", "Pixel 8"},
		{"trimmed", "  iOS  ", " 17.5 ", "  iPhone  ", "iOS 17.5 · iPhone"},
		{"invalid UTF-8 dropped", "iOS\xff\xfe", "17\xc3", "", "iOS 17"},
		{"non-Latin kept", "鸿蒙", "4.2", "华为 Mate 60", "鸿蒙 4.2 · 华为 Mate 60"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Label(tc.os, tc.version, tc.mdl); got != tc.want {
				t.Fatalf("Label(%q, %q, %q) = %q, want %q", tc.os, tc.version, tc.mdl, got, tc.want)
			}
		})
	}
}

func TestLabel_PartsAndTotalAreCapped(t *testing.T) {
	long := strings.Repeat("a", 40)
	if got := Label(long, "", ""); got != strings.Repeat("a", partMaxRunes) {
		t.Fatalf("one long part = %q (%d runes), want %d runes", got, utf8.RuneCountInString(got), partMaxRunes)
	}
	if got := Label("", "", strings.Repeat("字", 40)); got != strings.Repeat("字", partMaxRunes) {
		t.Fatalf("a long multi-byte part = %q, want %d runes", got, partMaxRunes)
	}
	// Three full parts are 32+1+32+3+32 runes; the column holds 64.
	got := Label(strings.Repeat("o", 32), strings.Repeat("v", 32), strings.Repeat("m", 32))
	if n := utf8.RuneCountInString(got); n > LabelMaxRunes {
		t.Fatalf("label has %d runes, want at most %d: %q", n, LabelMaxRunes, got)
	}
	if !strings.HasPrefix(got, strings.Repeat("o", 32)+" v") {
		t.Fatalf("label %q does not keep the leading OS part", got)
	}
	// A cut that lands inside the separator must not leave it dangling.
	got = Label(strings.Repeat("o", 30), strings.Repeat("v", 30), "model")
	if strings.HasSuffix(got, " ") || strings.HasSuffix(got, "·") {
		t.Fatalf("label %q ends in a dangling separator", got)
	}
	if n := utf8.RuneCountInString(got); n > LabelMaxRunes {
		t.Fatalf("label has %d runes, want at most %d", n, LabelMaxRunes)
	}
}

// Every part is cut to 256 bytes before anything looks at it, so a 10 KB
// header costs the same as a short one. The probe: 300 bytes of override
// runes followed by "ok". Only if the cut comes first does "ok" fall away.
func TestLabel_HugeInputsAreBounded(t *testing.T) {
	huge := strings.Repeat("x", 10<<10)
	got := Label(huge, huge, huge)
	if n := utf8.RuneCountInString(got); n > LabelMaxRunes || !utf8.ValidString(got) {
		t.Fatalf("10 KB parts gave %d runes (valid UTF-8: %v)", n, utf8.ValidString(got))
	}
	if got := Label(strings.Repeat("\u202e", 100)+"ok", "", ""); got != "" {
		t.Fatalf("Label read past the first %d bytes of a part: %q", inputMaxBytes, got)
	}
	// A multi-byte rune split by the cut is invalid UTF-8 and is dropped,
	// never half-kept. The zero-width runes ahead of it are dropped too, so
	// the split byte would be all that is left.
	split := strings.Repeat("\u200b", 85) + "字"
	if got := Label("", "", split); got != "" {
		t.Fatalf("a rune split by the cut left %q, want nothing", got)
	}
}

// The package must be unable to log what it handles: the raw hwid and the
// device headers pass through it on every subscription fetch. Only these
// standard-library imports are allowed, so a logger (or fmt, the usual first
// step towards one) cannot creep in unnoticed.
func TestSubdeviceImportsNoLogger(t *testing.T) {
	allowed := map[string]bool{
		"crypto/hmac": true, "crypto/sha256": true, "encoding/hex": true,
		"strconv": true, "strings": true, "unicode": true, "unicode/utf8": true,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		seen++
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if !allowed[path] {
				t.Errorf("%s imports %q; subdevice may import only %v", name, path, allowed)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no non-test source files found; the guard checked nothing")
	}
}
