import type { FlagRecord } from '@/api/riskCenter'
import type { GeoTier } from '@/api/geoAnomalies'
import { formatMsDualTz } from '@/utils/datetime'
import { tierLabelKey, type Translate } from '@/utils/geoAnomaly'
import { holdLabelKey, reviewActor, reviewLevelsText } from '@/utils/riskCenter'

// A FLAG RECORD'S NUMBERS, IN WORDS. The expanded row of 记录 used to print a
// record's params as JSON; an admin read `"ban_over": 0` and a tier code. The
// rows below say the same values under their names, each formatted the way
// the rest of the page formats that kind of value, and the JSON stays one
// click further (原始数据). Pure: no I/O, no React.

/**
 * The params keys that have a name, in the order the rows list them: the
 * geo streak counters, the tier and latch, the automatic suspension's
 * numbers, the hold that replaced it, and a review's admin and levels. A key
 * not listed here — the evidence object (drawn as places or as its kind's
 * panel), a version number, a field a newer server added — is not a row: a
 * row would have to show its raw key.
 */
export const RECORD_PARAM_KEYS = [
  'over', 'under', 'ban_over', 'tier', 'flagged', 'spread', 'duration_minutes', 'suspended_at_ms', 'replaced_by',
  'by', 'levels',
] as const

export interface ParamRow {
  key: string
  label: string
  value: string
}

/**
 * One value, formatted by its key, or null when it has nothing to show
 * (a tier with no name, an instant of 0, a value of an unexpected type):
 *
 * - `tier` → the tier's label (跨国 / 跨省 / 跨城);
 * - `replaced_by` → the hold's one name, and a hold this build cannot name
 *   → the generic service suspension, never its code;
 * - any `*_at_ms` → the instant in panel time, like every time on the page;
 * - booleans → 是 / 否;
 * - `levels` → the accepted levels, as the review sentence joins them;
 * - `by` → the admin by current name, else `#id` (reviewActor);
 * - numbers as themselves.
 */
function formatValue(
  key: string, v: unknown, rec: FlagRecord, t: Translate, panelTz: string, language: string | undefined,
): string | null {
  switch (key) {
    case 'tier': {
      const k = typeof v === 'string' ? tierLabelKey(v as GeoTier) : null
      return k ? t(k) : null
    }
    case 'replaced_by':
      return t(holdLabelKey(String(v ?? '')) ?? 'admin:users.status.service_suspended')
    case 'levels':
      return reviewLevelsText(v, t, language) || null
    case 'by':
      return reviewActor(rec)
  }
  if (key.endsWith('_at_ms')) {
    return typeof v === 'number' && v > 0 ? formatMsDualTz(v, panelTz) : null
  }
  if (typeof v === 'boolean') return t(v ? 'admin:risk_center.flags.value_yes' : 'admin:risk_center.flags.value_no')
  if (typeof v === 'number' && Number.isFinite(v)) return String(v)
  return null
}

/**
 * A record's params as named rows, for the expanded row of 记录: every key
 * of RECORD_PARAM_KEYS the record carries, in that order, with its label
 * (`risk_center.flags.param.<key>`) and its formatted value. Keys without a
 * label are left out; a record without params has no rows.
 */
export function paramRows(rec: FlagRecord, t: Translate, panelTz: string, language: string | undefined): ParamRow[] {
  const p = rec.params
  if (!p || typeof p !== 'object' || Array.isArray(p)) return []
  const params = p as Record<string, unknown>
  const out: ParamRow[] = []
  for (const key of RECORD_PARAM_KEYS) {
    if (!(key in params)) continue
    const value = formatValue(key, params[key], rec, t, panelTz, language)
    if (value === null) continue
    out.push({ key, label: t(`admin:risk_center.flags.param.${key}`), value })
  }
  return out
}
