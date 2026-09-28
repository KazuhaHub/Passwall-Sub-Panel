// Pure helpers for the risk center's tabs. No I/O, no React: each one decides
// what an admin reads (flagText, the labels, the judgement colour), so they
// are pinned by unit tests rather than by rendering.
import type { GeoAnomaly, GeoEvidence, GeoTier } from '@/api/geoAnomalies'
import { EXCLUSION_REASONS, type FlagRecord } from '@/api/riskCenter'
import { RISK_KINDS, type RiskKind, type RiskState } from '@/api/riskSignals'
import { reasonText, type Translate } from './geoAnomaly'
import { riskCodeText } from './riskSignals'

const RC = 'admin:risk_center.'

/**
 * Every source a flag record can come from, in the Flags filter's order: a
 * copy of domain.FlagSources() — the concurrent-location verdict, its
 * automatic suspension, then each risk kind in RISK_KINDS order, which is the
 * one list to extend for a new kind.
 */
export const FLAG_SOURCES: readonly string[] = ['geo', 'geo_auto', ...RISK_KINDS]

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

function holdLabel(reason: string, t: Translate): string {
  const key = HOLD_LABEL[reason]
  return key ? t(key, { defaultValue: reason }) : reason
}

/**
 * "The reason at the time": one record's sentence, built the way its own
 * source's tab builds it, from the numbers stored WITH the record — so it
 * says why the level moved then, under the policy of that moment, not what
 * the account looks like now.
 *
 * - geo: the params are the streak counters plus the evidence
 *   (domain.GeoFlagParams); they are laid out as the row the Geo tab's
 *   reasonText reads, with the record's code as the stored reason.
 * - a risk kind: the params ARE the verdict's evidence, read by its kind's
 *   own sentence.
 * - geo_auto: the producer's numbers (tier, spread, minutes; or the hold that
 *   replaced it).
 *
 * Anything missing — no params, a source or code this build does not know —
 * falls back to the code: a record always says something.
 */
export function flagText(rec: FlagRecord, t: Translate): string {
  const p = paramsOf(rec)
  if (rec.source === 'geo') {
    const ev = p.evidence as GeoEvidence | undefined
    if (!ev || typeof ev !== 'object') return rec.code
    const row: GeoAnomaly = {
      user_id: rec.user_id, state: rec.state as GeoAnomaly['state'], reason: rec.code,
      tier: (p.tier as GeoTier | undefined) ?? '', flagged: p.flagged === true,
      over_streak: Number(p.over ?? 0), under_streak: Number(p.under ?? 0), ban_streak: Number(p.ban_over ?? 0),
      evidence: ev, places: [], live_ips: 0, concurrent_ips: 0, excluded_ips: 0, complete: true,
      updated_at_ms: rec.at_ms,
    }
    return reasonText(row, t)
  }
  if (rec.source === 'geo_auto') {
    const d = { defaultValue: rec.code }
    switch (rec.code) {
      case 'country':
      case 'region':
      case 'city':
        return t(`${RC}flags.geo_auto.${rec.code}`, { ...d, spread: p.spread, minutes: p.duration_minutes })
      case 'expired':
        return t(`${RC}flags.geo_auto.expired`, { ...d, minutes: p.duration_minutes })
      case 'admin_resume':
        return t(`${RC}flags.geo_auto.admin_resume`, d)
      case 'replaced':
        return t(`${RC}flags.geo_auto.replaced`, { ...d, reason: holdLabel(String(p.replaced_by ?? ''), t) })
      default:
        return rec.code
    }
  }
  if ((RISK_KINDS as readonly string[]).includes(rec.source)) {
    return riskCodeText({
      kind: rec.source as RiskKind, state: rec.state as RiskState, code: rec.code,
      evidence: rec.params, updated_at_ms: rec.at_ms,
    }, t)
  }
  return rec.code
}

/** A list's separator in the UI's language: 「、」 for Chinese, ", "
 *  otherwise. Here rather than in the views, which carry no Chinese text of
 *  their own (i18n/riskKeys.test.ts). */
export function listSeparator(language: string | undefined): string {
  return (language ?? '').toLowerCase().startsWith('zh') ? '、' : ', '
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
