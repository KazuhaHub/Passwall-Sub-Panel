// Package locale holds pure validation for runtime-uploaded UI language packs.
// It is deliberately dependency-light (domain only) so it can be unit-tested and
// reused by both the HTTP handler and the file-backed repo.
package locale

import (
	"fmt"
	"regexp"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Format is the current pack schema version (the JSON `psp_language_pack` field).
// Uploads carrying a different version are rejected so an old panel never tries
// to read a shape it doesn't understand.
const Format = 1

// Namespaces is the closed set of i18next namespaces the SPA ships. A pack may
// translate any subset; keys outside this set are rejected (a typo'd namespace
// would silently never load, so we fail loudly at upload time instead).
//
// Keep in sync with NAMESPACES in web-react/src/i18n/index.ts.
var Namespaces = []string{"common", "appearance", "language", "auth", "nav", "admin", "user"}

// reserved are the built-in language codes compiled into the SPA bundle. Uploaded
// packs must not collide with them: the built-ins are the always-present fallback
// and can never be overwritten or deleted via this feature.
var reserved = map[string]bool{"zh-CN": true, "en-US": true}

// codePattern mirrors the file-safe slug rule used by the YAML config repos
// (letters/digits/'_'/'-'), which is also a valid i18next code shape and blocks
// path traversal in the on-disk filename.
var codePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// IsReserved reports whether code is a compiled-in built-in language.
func IsReserved(code string) bool { return reserved[code] }

// Validate checks a pack's structure before it is ever persisted or served.
// Every failure wraps domain.ErrValidation so handlers map it to 400.
//
// The load-bearing guarantee is the recursive string-only leaf check: it ensures
// only {string:string} ever reaches the SPA's t(), so a malicious pack cannot
// smuggle objects/arrays/numbers into the interpolation path.
func Validate(p *domain.LocalePack) error {
	if p == nil {
		return fmt.Errorf("%w: nil language pack", domain.ErrValidation)
	}
	if p.Format != Format {
		return fmt.Errorf("%w: unsupported pack format %d (expected %d)", domain.ErrValidation, p.Format, Format)
	}
	if p.Code == "" {
		return fmt.Errorf("%w: language code required", domain.ErrValidation)
	}
	if !codePattern.MatchString(p.Code) {
		return fmt.Errorf("%w: language code may only contain letters, numbers, '_' and '-'", domain.ErrValidation)
	}
	if IsReserved(p.Code) {
		return fmt.Errorf("%w: %q is a built-in language and cannot be overwritten", domain.ErrValidation, p.Code)
	}
	if p.Name == "" {
		return fmt.Errorf("%w: display name required", domain.ErrValidation)
	}
	known := make(map[string]bool, len(Namespaces))
	for _, ns := range Namespaces {
		known[ns] = true
	}
	for ns, tree := range p.Namespaces {
		if !known[ns] {
			return fmt.Errorf("%w: unknown namespace %q", domain.ErrValidation, ns)
		}
		for k, v := range tree {
			if err := validateLeaf(ns, k, v); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateLeaf recurses a translation node: strings are valid leaves, objects
// (map[string]any, the shape JSON decodes nested objects into) are recursed, and
// everything else — numbers, bools, arrays, null — is rejected.
func validateLeaf(ns, key string, v any) error {
	switch t := v.(type) {
	case string:
		return nil
	case map[string]any:
		for k, child := range t {
			if err := validateLeaf(ns, key+"."+k, child); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: %s:%s must be a string (got %T)", domain.ErrValidation, ns, key, v)
	}
}
