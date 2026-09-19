package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/config"
)

// samlDigestExcludedPaths names the config paths deliberately left OUT of the
// digest, with the reason for each. Everything else in config.SAMLConfig is
// covered automatically, because the walker includes new fields by default —
// that way a field added later cannot silently escape the digest. This table is
// the only place an omission can hide, which is why a test asserts every path
// here still exists (a rename would otherwise turn an exclusion into a no-op).
var samlDigestExcludedPaths = map[string]string{
	"IDP.MetadataRefreshInterval": "changes the polling cadence, not who is trusted or how a principal maps",
	"RoleRules.Note":              "admin-facing documentation the resolver never reads",
	"GroupRules.Note":             "admin-facing documentation the resolver never reads",
}

// SAMLConfigDigest returns a deterministic digest of the configuration that
// decides trust and business mapping for a SAML login.
//
// It exists so a login begun under one configuration cannot be completed under
// another: the digest is stored with the login request and compared when the
// response comes back (ADR 0036 D2). What is deliberately NOT in it matters just
// as much — the IdP metadata CONTENT is not hashed, only its URL, so an upstream
// certificate rotation does not invalidate logins already in flight.
//
// The rendering is order-stable by construction: struct fields are walked in
// declaration order, slices in index order, and the config contains no maps,
// whose iteration order would make the result unreproducible.
func SAMLConfigDigest(cfg *config.SAMLConfig) string {
	var b strings.Builder
	appendDigestValue(&b, reflect.ValueOf(cfg), "")
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// appendDigestValue renders one node of the config into b, tagging every leaf
// with its path so that two different shapes cannot render to the same string.
func appendDigestValue(b *strings.Builder, v reflect.Value, path string) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			fmt.Fprintf(b, "%s=<nil>\n", path)
			return
		}
		appendDigestValue(b, v.Elem(), path)
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue // unexported: not part of the config's contract
			}
			child := f.Name
			if path != "" {
				child = path + "." + f.Name
			}
			if _, skip := samlDigestExcludedPaths[digestSchemaPath(child)]; skip {
				continue
			}
			appendDigestValue(b, v.Field(i), child)
		}
	case reflect.Slice, reflect.Array:
		// Length is part of the value, and element order is significant rather
		// than incidental: the role and group matchers are first-match-wins, so
		// reordering two rules can change what a principal is granted.
		fmt.Fprintf(b, "%s:len=%d\n", path, v.Len())
		for i := 0; i < v.Len(); i++ {
			appendDigestValue(b, v.Index(i), fmt.Sprintf("%s[%d]", path, i))
		}
	default:
		fmt.Fprintf(b, "%s=%v\n", path, v.Interface())
	}
}

// digestSchemaPath strips slice and array indices so an exclusion key can be
// written once in the index-free notation the schema uses ("RoleRules.Note")
// rather than once per element.
func digestSchemaPath(path string) string {
	if !strings.Contains(path, "[") {
		return path
	}
	var b strings.Builder
	depth := 0
	for _, r := range path {
		switch r {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}
