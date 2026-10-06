import { describe, expect, it } from 'vitest'
import type { AccessControlSettingsView } from '@/api/accessControl'
import { changedAccessSettings, shortenedRetentionDays, validateAccessSettings } from './settingsDraft'

export const defaultView: AccessControlSettingsView = {
  settings: { dest_hit_retention_days: 0, dest_trial_retention_days: 0, dest_usage_retention_days: 0, dest_list_refresh_hours: 0, dest_policy_apply_min_seconds: 0 },
  defaults: { dest_hit_retention_days: 30, dest_trial_retention_days: 7, dest_usage_retention_days: 7, dest_list_refresh_hours: 24, dest_policy_apply_min_seconds: 60 },
  effective: { dest_hit_retention_days: 30, dest_trial_retention_days: 7, dest_usage_retention_days: 7, dest_list_refresh_hours: 24, dest_policy_apply_min_seconds: 60 },
}

describe('destination settings drafts', () => {
  it('submits only edited keys, including an explicit reset to default', () => {
    const seed = { ...defaultView.settings, dest_hit_retention_days: 60 }
    expect(changedAccessSettings({ ...seed, dest_hit_retention_days: 0, dest_list_refresh_hours: 48 }, seed))
      .toEqual({ dest_hit_retention_days: 0, dest_list_refresh_hours: 48 })
    expect(changedAccessSettings(seed, seed)).toEqual({})
  })
  it('uses served defaults and marks both fields for the cross-field retention constraint', () => {
    const defaults = { ...defaultView.defaults, dest_trial_retention_days: 12 }
    expect(validateAccessSettings({ ...defaultView.settings, dest_hit_retention_days: 10 }, defaults))
      .toEqual({ dest_hit_retention_days: 'trial_exceeds_hit', dest_trial_retention_days: 'trial_exceeds_hit' })
    expect(validateAccessSettings(defaultView.settings, defaults)).toEqual({})
  })
  it.each([-1, 0.5, 1.5, NaN, Infinity, 366])('rejects invalid hit retention %s', value => {
    expect(validateAccessSettings({ ...defaultView.settings, dest_hit_retention_days: value }, defaultView.defaults))
      .toHaveProperty('dest_hit_retention_days', 'range')
  })
  it.each([
    ['dest_trial_retention_days', 31], ['dest_usage_retention_days', 31],
    ['dest_list_refresh_hours', 5], ['dest_list_refresh_hours', 169],
    ['dest_policy_apply_min_seconds', 29], ['dest_policy_apply_min_seconds', 3601],
  ] as const)('enforces the server range for %s=%s', (key, value) => {
    expect(validateAccessSettings({ ...defaultView.settings, [key]: value }, defaultView.defaults)).toHaveProperty(key, 'range')
  })
  it('compares effective retention, including resets; never confirms refresh or apply intervals', () => {
    expect(shortenedRetentionDays({ ...defaultView.settings, dest_hit_retention_days: 10, dest_trial_retention_days: 5 }, defaultView)).toBe(5)
    const stored = { ...defaultView, settings: { ...defaultView.settings, dest_hit_retention_days: 60 }, effective: { ...defaultView.effective, dest_hit_retention_days: 60 } }
    expect(shortenedRetentionDays(defaultView.settings, stored)).toBe(30)
    expect(shortenedRetentionDays({ ...defaultView.settings, dest_list_refresh_hours: 6, dest_policy_apply_min_seconds: 30 }, defaultView)).toBeNull()
    expect(shortenedRetentionDays({ ...defaultView.settings, dest_hit_retention_days: 60 }, defaultView)).toBeNull()
  })
})
