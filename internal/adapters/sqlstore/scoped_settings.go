package sqlstore

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// scopedSettings resolves effective per-scope settings = the global value
// (already cached + defaulted by the wrapped SettingsRepo) overlaid with a
// group's sparse overrides. It lives in package mysql because the merge reuses
// the unexported settingDescriptors — the exact same Unmarshal path Load uses —
// so a per-group value is decoded identically to the global one.
//
// No per-scope cache here (yet). The override set is read fresh per call and
// merged onto the global-cached base. This is deliberately the simplest correct
// design for the Phase-0 consumer (authpolicy.MustEnroll, not a hot path) and it
// has NO torn-read surface: the torn read the design guards against only arises
// from caching the override set under a SECOND generation. When a hot-path
// consumer (render / sub) migrates in a later phase, add a per-scope cache that
// reuses the single shared-gen seqlock discipline already pinned by
// settings_cache_test.go.
type scopedSettings struct {
	global ports.SettingsRepo
	scope  ports.ScopeSettingsRepo
}

// NewScopedSettings composes the (cached) global settings repo with the
// per-scope override repo into a ports.ScopedSettings resolver.
func NewScopedSettings(global ports.SettingsRepo, scope ports.ScopeSettingsRepo) ports.ScopedSettings {
	return &scopedSettings{global: global, scope: scope}
}

func (s *scopedSettings) Load(ctx context.Context, defaults ports.UISettings) (ports.UISettings, error) {
	return s.global.Load(ctx, defaults)
}

func (s *scopedSettings) LoadForGroup(ctx context.Context, groupID int64, defaults ports.UISettings) (ports.UISettings, error) {
	base, err := s.global.Load(ctx, defaults)
	if err != nil {
		return base, err
	}
	if groupID == 0 {
		// No group → pure global (mirrors authpolicy's existing GroupID==0
		// fail-safe). Don't even query the override table.
		return base, nil
	}
	overrides, err := s.scope.ListOverrides(ctx, "group", groupID)
	if err != nil {
		return base, err
	}
	if len(overrides) == 0 {
		return base, nil // sparse: no overrides = inherit global
	}
	return applyScopeOverrides(base, overrides), nil
}

func (s *scopedSettings) LoadForUser(ctx context.Context, u *domain.User, defaults ports.UISettings) (ports.UISettings, error) {
	if u == nil {
		return s.global.Load(ctx, defaults)
	}
	return s.LoadForGroup(ctx, u.GroupID, defaults)
}

// applyScopeOverrides writes each override's raw value into a copy of base via
// the SAME settingDescriptor.Unmarshal used by kvSettingsRepo.Load, so a
// per-group value decodes exactly like the global one. base is already
// applyUISettingsDefaults'd by the global Load; overrides apply ON TOP and
// defaults are NOT re-run (re-defaulting could re-floor a value the group
// deliberately lowered). Encrypted overrides are rejected at write time, so
// every value here is plaintext; an override whose key has no descriptor (should
// be impossible — SetOverride rejects unknown keys) is skipped defensively.
func applyScopeOverrides(base ports.UISettings, overrides []ports.ScopeOverride) ports.UISettings {
	out := base
	descs := settingDescriptors(&out)
	byKey := make(map[string]settingDescriptor, len(descs))
	for _, d := range descs {
		byKey[d.Type+"."+d.Name] = d
	}
	for _, o := range overrides {
		// Defense-in-depth: never APPLY a non-overridable key, even if a stray row
		// exists (the admin handler blocks writing one). This gates the EFFECT, so
		// the global/group partition holds regardless of what's stored.
		if !ports.OverridableScopeKeys[o.Type+"."+o.Name] {
			continue
		}
		d, ok := byKey[o.Type+"."+o.Name]
		if !ok {
			continue
		}
		// Best-effort: a decode error leaves the inherited global value in place
		// rather than corrupting the field.
		_ = d.Unmarshal(o.Value)
	}
	return out
}
