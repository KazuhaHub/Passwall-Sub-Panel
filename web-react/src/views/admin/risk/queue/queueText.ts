import type { GeoAnomaly } from '@/api/geoAnomalies'
import type { QueueRow } from '@/api/riskCenter'
import { formatMsDualTz } from '@/utils/datetime'
import { countryFlag } from '@/utils/geo'
import { groupSpots, reasonText, spreadKm, tierLabelKey, type Translate } from '@/utils/geoAnomaly'
import type { RegionNamer } from '@/utils/regionName'
import { riskCodeText } from '@/utils/riskSignals'

// What one queue row SAYS, as pure functions: the reason cell's headline and
// its tooltip, the places a location verdict names, and the signal chips'
// labels. No React, so each wording rule is pinned by a unit test.

/** At most this many places in a headline; the rest are counted. */
const PLACES_SHOWN = 3

/** What a headline needs besides the row: how to name a province, the panel
 *  timezone for a time, and the list separator of the UI's language
 *  (utils/riskCenter listSeparator). */
export interface HeadlineContext {
  nameRegion: RegionNamer
  panelTz: string
  sep: string
}

/**
 * The places a verdict names, AT ITS TIER, from the same spot data the
 * drawer's place lines read: the countries for a cross-border verdict, the
 * provinces of the country the tier counted for a cross-region one, that
 * country's cities for a cross-city one. Most sources first. The bucket the
 * database placed no further than the parent is not a place of its own, so
 * it is left out rather than printed as "?". A row an older build wrote
 * carries no spots; its stored countries are all there is.
 */
function placeNames(geo: GeoAnomaly, nameRegion: RegionNamer): string[] {
  const ev = geo.evidence
  const tree = ev && ev.v > 0 && ev.spots.length ? groupSpots(ev.spots) : []
  if (tree.length === 0) return geo.places
  const inCountry = (cc: string) => (cc ? tree.filter(c => c.cc === cc) : tree)
  switch (geo.tier) {
    case 'region': {
      const names = inCountry(ev.spread.region_country).flatMap(c => c.regions
        .filter(r => r.region !== '')
        .map(r => nameRegion({ cc: c.cc, region: r.region, rc: r.rc }) || r.region))
      if (names.length > 0) return names
      break
    }
    case 'city': {
      // A city's sources can sit under several region buckets (one named,
      // one the database left unnamed); they are one city here.
      const cities = new Map<string, number>()
      for (const c of inCountry(ev.spread.city_country)) {
        for (const r of c.regions) {
          for (const ci of r.cities) {
            if (ci.city !== '') cities.set(ci.city, (cities.get(ci.city) ?? 0) + ci.n)
          }
        }
      }
      if (cities.size > 0) {
        return [...cities].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([city]) => city)
      }
      break
    }
  }
  return tree.map(c => [countryFlag(c.cc), c.cc].filter(Boolean).join(' '))
}

/** The places a location verdict names, three at most and the rest counted
 *  (「广东、湖南、四川 等 1 处」). */
export function placesText(geo: GeoAnomaly, t: Translate, ctx: Pick<HeadlineContext, 'nameRegion' | 'sep'>): string {
  const names = placeNames(geo, ctx.nameRegion)
  if (names.length === 0) return '—'
  const shown = names.slice(0, PLACES_SHOWN).join(ctx.sep)
  const more = names.length - PLACES_SHOWN
  return more > 0 ? `${shown} ${t('admin:risk_center.queue.places_more', { count: more })}` : shown
}

/** The source the headline speaks for: the first that is not the detector's
 *  hold, which has its own chip and card. */
function leadSource(row: QueueRow): string | undefined {
  return row.sources.find(s => s.source !== 'geo_auto')?.source
}

/** A latched location flag whose current sample is not over (idle or
 *  unknown): the streak froze, so the account is still flagged. */
function latched(geo: GeoAnomaly): boolean {
  return geo.flagged && geo.state !== 'flagged'
}

/**
 * The reason cell's one line, from the row alone (no extra read):
 *
 * - a location verdict names its places and, when measured, how far apart
 *   the farthest two were — the sentence behind it goes in the tooltip;
 * - a latched verdict that is not over right now says it is still flagged,
 *   the tier that raised it and why it cannot be judged now, since its
 *   places are from before (「仍在标记中 · 跨省 · 此刻没有连接」);
 * - a risk kind reads its code sentence;
 * - the detector's hold alone reads when it began;
 * - a row with nothing at attention (a trusted account under 已信任 or 全部)
 *   says so.
 */
export function headlineText(row: QueueRow, t: Translate, ctx: HeadlineContext): string {
  const lead = leadSource(row)
  if (lead === 'geo' && row.geo) {
    const g = row.geo
    if (latched(g)) {
      const tierKey = tierLabelKey(g.tier)
      return [t('admin:geo_anomalies.latched'), ...(tierKey ? [t(tierKey)] : []), reasonText(g, t)].join(' · ')
    }
    const km = spreadKm(g.evidence)
    const places = placesText(g, t, ctx)
    return km > 0 ? `${places} · ${t('admin:geo_anomalies.max_km', { km: String(km) })}` : places
  }
  if (lead) {
    const sig = row.signals.find(s => s.kind === lead)
    return sig ? riskCodeText(sig, t) : sourceLabel(lead, t)
  }
  if (row.auto_suspended) {
    return t('admin:geo_anomalies.auto_suspended_since', { time: formatMsDualTz(row.service_disabled_at_ms, ctx.panelTz) })
  }
  return t('admin:risk_center.queue.headline_no_signal')
}

/** The reason cell's tooltip: the location verdict's full sentence behind a
 *  places headline; '' where the headline already is the sentence. */
export function headlineTip(row: QueueRow, t: Translate): string {
  return leadSource(row) === 'geo' && row.geo && !latched(row.geo) ? reasonText(row.geo, t) : ''
}

function sourceLabel(src: string, t: Translate): string {
  return t(`admin:risk_center.flags.source.${src}`, { defaultValue: src })
}

/** A signal chip's label: the source, and for the location detector the
 *  tier it was over at (「异地并发 · 跨省」). */
export function sourceChipLabel(src: string, row: QueueRow, t: Translate): string {
  const tierKey = src === 'geo' && row.geo ? tierLabelKey(row.geo.tier) : null
  return tierKey ? `${sourceLabel(src, t)} · ${t(tierKey)}` : sourceLabel(src, t)
}
