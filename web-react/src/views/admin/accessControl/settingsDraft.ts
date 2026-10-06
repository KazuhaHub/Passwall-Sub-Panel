import type { AccessControlSettingKey, AccessControlSettings, AccessControlSettingsView } from '@/api/accessControl'

export const ACCESS_SETTINGS_FIELDS: Array<{ key: AccessControlSettingKey; min: number; max: number }> = [
  { key: 'dest_hit_retention_days', min: 1, max: 365 },
  { key: 'dest_trial_retention_days', min: 1, max: 30 },
  { key: 'dest_usage_retention_days', min: 1, max: 30 },
  { key: 'dest_list_refresh_hours', min: 6, max: 168 },
  { key: 'dest_policy_apply_min_seconds', min: 30, max: 3600 },
]
export type AccessSettingsErrors = Partial<Record<AccessControlSettingKey, 'range' | 'trial_exceeds_hit'>>

export function changedAccessSettings(draft: AccessControlSettings, seed: AccessControlSettings): Partial<AccessControlSettings> {
  return Object.fromEntries(ACCESS_SETTINGS_FIELDS.filter(({ key }) => !Object.is(draft[key], seed[key])).map(({ key }) => [key, draft[key]]))
}

export function validateAccessSettings(draft: AccessControlSettings, defaults: AccessControlSettings): AccessSettingsErrors {
  const errors: AccessSettingsErrors = {}
  for (const { key, min, max } of ACCESS_SETTINGS_FIELDS) {
    const v = draft[key]
    if (!Number.isSafeInteger(v) || (v !== 0 && (v < min || v > max))) errors[key] = 'range'
  }
  const hit = draft.dest_hit_retention_days || defaults.dest_hit_retention_days
  const trial = draft.dest_trial_retention_days || defaults.dest_trial_retention_days
  if (!errors.dest_hit_retention_days && !errors.dest_trial_retention_days && trial > hit) {
    errors.dest_hit_retention_days = errors.dest_trial_retention_days = 'trial_exceeds_hit'
  }
  return errors
}

/** The shortest reduced retention in this write; zero selects the served default. */
export function shortenedRetentionDays(draft: AccessControlSettings, baseline: AccessControlSettingsView): number | null {
  const changed = changedAccessSettings(draft, baseline.settings)
  const reduced = ACCESS_SETTINGS_FIELDS.slice(0, 3).filter(({ key }) => key in changed)
    .map(({ key }) => ({ next: draft[key] || baseline.defaults[key], prior: baseline.effective[key] }))
    .filter(({ next, prior }) => next < prior).map(({ next }) => next)
  return reduced.length ? Math.min(...reduced) : null
}
