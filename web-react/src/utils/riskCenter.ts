// Pure helpers for the risk center's tabs. No I/O, no React: each one decides
// what an admin reads (flagText, the labels, the judgement colour), so they
// are pinned by unit tests rather than by rendering.
import type { GeoAnomaly, GeoEvidence, GeoTier } from '@/api/geoAnomalies'
import { ATTENTION_SOURCES, EXCLUSION_REASONS, type FlagRecord } from '@/api/riskCenter'
import { RISK_CODES, RISK_KINDS, type RiskKind, type RiskState } from '@/api/riskSignals'
import { reasonText, type Translate } from './geoAnomaly'
import { riskCodeText } from './riskSignals'
import { sentence } from './sentence'

const RC = 'admin:risk_center.'

/**
 * Every source a flag record can come from, in the Flags filter's order: a
 * copy of domain.FlagSources() — the concurrent-location verdict, its
 * automatic suspension, each risk kind in RISK_KINDS order (the one list to
 * extend for a new kind), and last an admin's review actions, which are
 * recorded beside the attention changes but are never attention themselves.
 */
export const FLAG_SOURCES: readonly string[] = ['geo', 'geo_auto', ...RISK_KINDS, 'review']

function isExclusionReason(x: string): boolean {
  return (EXCLUSION_REASONS as readonly string[]).includes(x)
}

/**
 * The label key for why a source was set aside, or null for a judged source
 * ('') and for a reason this build does not know — the caller shows the raw
 * reason then, never a raw key.
 */
export function exclusionLabelKey(exclusion: string): string | null {
  return isExclusionReason(exclusion) ? `${RC}live.exclusion.${exclusion}` : null
}

/**
 * The judgement chip's colour. Never the success colour: an excluded source
 * was not judged at all, and drawn green it would read as "checked, and
 * fine"; a judged source is not fine either, only counted.
 */
export function judgementColor(exclusion: string): 'primary' | 'default' {
  return exclusion === '' ? 'primary' : 'default'
}

export function flagEventKey(ev: string): string {
  return `${RC}flags.event.${ev}`
}

export function flagSourceKey(src: string): string {
  return `${RC}flags.source.${src}`
}

/** A record's level is '' when it moved to no attention (every leave and
 *  every lift); the level filter calls that "cleared", and so does the label. */
export function flagLevelKey(level: string): string {
  return `${RC}flags.level.${level === '' ? 'cleared' : level}`
}

function paramsOf(rec: FlagRecord): Record<string, unknown> {
  return rec.params && typeof rec.params === 'object' ? rec.params as Record<string, unknown> : {}
}

// The service holds another suspension can replace geo_auto with, named by
// the Users page's own status labels. domain.AutoDisabledReason values.
//
// geo_anomaly, a person's suspension from the location evidence, has its own
// label: the Users page folds it into "service suspended", but the domain
// keeps it apart from service_manual on purpose (resuming it is how a false
// positive is counted), and a flag record that named it as a generic staff
// suspension would lose exactly that. Without an entry it rendered as the
// raw code.
const HOLD_LABEL: Record<string, string> = {
  traffic_exceeded: 'admin:users.status.traffic_exhausted',
  expired: 'admin:users.status.expired',
  blocked_client: 'admin:users.status.blocked',
  service_manual: 'admin:users.status.service_suspended',
  geo_anomaly: `${RC}flags.hold.geo_anomaly`,
  manual: 'admin:users.status.manual_disabled',
}

/** The label key of a hold that replaced geo_auto, or null for one this
 *  build cannot name. */
export function holdLabelKey(reason: string): string | null {
  return HOLD_LABEL[reason] ?? null
}

function holdLabel(reason: string, t: Translate): string {
  const key = holdLabelKey(reason)
  return key ? t(key, { defaultValue: reason }) : reason
}

/** What a record says when its own sentence cannot be built: the change it
 *  records, in the admin's words. An event this build does not know reads as
 *  itself, exactly as its chip beside the sentence does. */
function eventText(ev: string, t: Translate): string {
  return t(flagEventKey(ev), { defaultValue: ev })
}

// The states a risk verdict is JUDGED in. Each is stored with the evidence it
// was judged on, and its sentence reads numbers from it; a record in one of
// them without params would print those numbers as blanks.
const JUDGED_STATES: ReadonlySet<string> = new Set(['flagged', 'suspect', 'clean'])

/**
 * A review record's admin: the CURRENT UPN the server resolved when it
 * listed the record, else `#<id>` (the admin's account is gone; the record
 * itself stores only the id — flag records carry no names). Null when the
 * record names nobody at all.
 */
export function reviewActor(rec: FlagRecord): string | null {
  if (rec.actor_upn) return rec.actor_upn
  const by = paramsOf(rec).by
  return typeof by === 'number' && Number.isFinite(by) ? `#${by}` : null
}

/**
 * The levels a dismissal accepted, 「<source> <level>」 joined in the UI's
 * language, sources in the queue's order whatever order the JSON had (a
 * source this build does not know goes last, by its own name). '' for none.
 */
export function reviewLevelsText(levels: unknown, t: Translate, language: string | undefined): string {
  if (!levels || typeof levels !== 'object' || Array.isArray(levels)) return ''
  const m = levels as Record<string, unknown>
  const known = (ATTENTION_SOURCES as readonly string[]).filter(src => src in m)
  const rest = Object.keys(m).filter(src => !known.includes(src))
  return [...known, ...rest]
    .filter(src => typeof m[src] === 'string' && m[src] !== '')
    .map(src => {
      const level = String(m[src])
      return `${t(flagSourceKey(src), { defaultValue: src })} ${t(flagLevelKey(level), { defaultValue: level })}`
    })
    .join(listSeparator(language))
}

/** A review action's sentence, or null when the record names no admin or
 *  its event is not a review event this build knows. */
function reviewText(rec: FlagRecord, t: Translate, language: string | undefined): string | null {
  const by = reviewActor(rec)
  if (by === null) return null
  switch (rec.event) {
    case 'dismissed':
      return t(`${RC}flags.review.dismissed`, {
        by, levels: reviewLevelsText(paramsOf(rec).levels, t, language) || '—',
      })
    case 'undismissed':
    case 'trusted':
    case 'untrusted':
      return t(`${RC}flags.review.${rec.event}`, { by })
    default:
      return null
  }
}

/**
 * "The reason at the time": one record's sentence, built the way its own
 * source's tab builds it, from the numbers stored WITH the record — so it
 * says why the level moved then, under the policy of that moment, not what
 * the account looks like now.
 *
 * - geo: the params are the streak counters plus the evidence
 *   (domain.GeoFlagParams); they are laid out as the row the Geo tab's
 *   reasonText reads, with the localized event as the stored reason — what
 *   reasonText says for evidence it cannot read.
 * - a risk kind: the params ARE the verdict's evidence, read by its kind's
 *   own sentence.
 * - geo_auto: the producer's numbers (tier, spread, minutes; or the hold that
 *   replaced it).
 * - review: who acted (reviewActor) and, for a dismissal, the levels the
 *   admin accepted, joined in the UI's `language`.
 *
 * Anything missing — no params where the sentence needs them, a source or
 * code this build does not know — falls back to the localized EVENT, never
 * to the code: a record always says something, and a code in the 当时的依据
 * column would read as a broken page rather than a reason. Params that are
 * there but lack a number the sentence names read the sentence without its
 * numbers (utils/sentence), or the event where it would say no more.
 */
export function flagText(rec: FlagRecord, t: Translate, language?: string): string {
  const p = paramsOf(rec)
  const hasParams = rec.params !== null && typeof rec.params === 'object'
  const fallback = () => eventText(rec.event, t)
  if (rec.source === 'geo') {
    const ev = p.evidence as GeoEvidence | undefined
    if (!ev || typeof ev !== 'object') return fallback()
    const row: GeoAnomaly = {
      user_id: rec.user_id, state: rec.state as GeoAnomaly['state'], reason: fallback(),
      tier: (p.tier as GeoTier | undefined) ?? '', flagged: p.flagged === true,
      over_streak: Number(p.over ?? 0), under_streak: Number(p.under ?? 0), ban_streak: Number(p.ban_over ?? 0),
      evidence: ev, places: [], live_ips: 0, concurrent_ips: 0, excluded_ips: 0, complete: true,
      updated_at_ms: rec.at_ms,
    }
    return reasonText(row, t)
  }
  if (rec.source === 'geo_auto') {
    // Every code but admin_resume names the producer's numbers; the producer
    // always stores them, so a record without is one this build cannot read.
    switch (rec.code) {
      case 'country':
      case 'region':
      case 'city':
        // Without its numbers the tier still says where it was spread.
        return hasParams
          ? sentence(t, `${RC}flags.geo_auto.${rec.code}`, { spread: p.spread, minutes: p.duration_minutes },
            () => t(`${RC}flags.geo_auto_bare.${rec.code}`))
          : fallback()
      // Without the minutes or the hold, the event says all there is.
      case 'expired':
        return hasParams ? sentence(t, `${RC}flags.geo_auto.expired`, { minutes: p.duration_minutes }, fallback) : fallback()
      case 'admin_resume':
        return t(`${RC}flags.geo_auto.admin_resume`)
      case 'replaced':
        return hasParams
          ? sentence(t, `${RC}flags.geo_auto.replaced`, { reason: holdLabel(String(p.replaced_by ?? ''), t) }, fallback)
          : fallback()
      default:
        return fallback()
    }
  }
  if ((RISK_KINDS as readonly string[]).includes(rec.source)) {
    const kind = rec.source as RiskKind
    if (!RISK_CODES[kind].includes(rec.code)) return fallback()
    if (!hasParams && JUDGED_STATES.has(rec.state)) return fallback()
    return riskCodeText({
      kind, state: rec.state as RiskState, code: rec.code, evidence: rec.params, updated_at_ms: rec.at_ms,
    }, t)
  }
  // A review record is always stored with its params (the admin's id, the
  // accepted levels); without them it names nothing it can be trusted for.
  if (rec.source === 'review') return (hasParams ? reviewText(rec, t, language) : null) ?? fallback()
  return fallback()
}

/** A list's separator in the UI's language: 「、」 for Chinese, ", "
 *  otherwise. Here rather than in the views, which carry no Chinese text of
 *  their own (i18n/riskKeys.test.ts). */
export function listSeparator(language: string | undefined): string {
  return (language ?? '').toLowerCase().startsWith('zh') ? '、' : ', '
}

/** A label with a parenthesised note after it: 「开始（浏览器时间）」 in
 *  Chinese, whose full-width brackets carry their own spacing, "From
 *  (browser time)" otherwise. */
export function withNote(label: string, note: string, language: string | undefined): string {
  return (language ?? '').toLowerCase().startsWith('zh') ? `${label}${note}` : `${label} ${note}`
}

/** How long ago, in the coarsest whole unit, with the Users page's words. */
export function agoText(seconds: number, t: Translate): string {
  const s = Math.max(0, Math.floor(seconds))
  if (s < 60) return t('admin:users.relative_time.just_now', { defaultValue: '刚刚' })
  if (s < 3600) return t('admin:users.relative_time.minutes_ago', { count: Math.floor(s / 60), defaultValue: '{{count}} 分钟前' })
  if (s < 86400) return t('admin:users.relative_time.hours_ago', { count: Math.floor(s / 3600), defaultValue: '{{count}} 小时前' })
  return t('admin:users.relative_time.days_ago', { count: Math.floor(s / 86400), defaultValue: '{{count}} 天前' })
}

/** An account as the risk center names it: the UPN, else the display name,
 *  else its id — never blank. */
export function userLabel(u: { user_id: number; upn?: string; display_name?: string }): string {
  return u.upn || u.display_name || `#${u.user_id}`
}
