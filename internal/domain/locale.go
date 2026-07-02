package domain

// LocalePack is a runtime-uploaded UI language pack. Operators upload one JSON
// file per language; it is stored as <ConfigDir>/locales/<code>.json and served
// to the SPA, which registers it on top of the two compiled-in built-ins
// (zh-CN / en-US) via i18next.addResourceBundle. Packs are purely additive: the
// built-ins live in the JS bundle and can never be overwritten or deleted, so
// there is no "reset to default" concept for uploaded languages.
type LocalePack struct {
	// Format is the pack schema version (the JSON `psp_language_pack` field).
	// Bumped on breaking format changes so old panels reject packs they can't read.
	Format int
	// Code is the i18next language code AND the on-disk filename stem. Must match
	// ^[A-Za-z0-9_-]+$ and must not collide with a reserved built-in code.
	Code string
	// Name is the endonym (self-declared display name, e.g. "Français"). Shown
	// in the language switcher regardless of the currently-active UI language.
	Name string
	// Author / BaseLanguage / BaseVersion are advisory metadata. BaseVersion lets
	// the UI warn that a pack was translated against an older panel build.
	Author       string
	BaseLanguage string
	BaseVersion  string
	// Namespaces maps a known namespace (common, appearance, language, auth, nav,
	// admin, user) to its translation tree. Values may be nested objects; the SPA
	// flattens them at register time. Every leaf must be a string — enforced by
	// service/locale.Validate before a pack is ever persisted.
	Namespaces map[string]map[string]any
}

// LocaleMeta is the lightweight manifest row (no translation bodies) returned by
// the pack list + the public /api/i18n/langs endpoint. ETag lets the SPA do a
// conditional fetch of the full bundle.
type LocaleMeta struct {
	Code        string
	Name        string
	Author      string
	BaseVersion string
	ETag        string
}
