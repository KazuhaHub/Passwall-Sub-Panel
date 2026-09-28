import { Box, Tooltip, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'

import type { GeoAnomaly, GeoEvidence } from '@/api/geoAnomalies'
import { countryFlag } from '@/utils/geo'
import { groupSpots, spreadKm, type SpotTree } from '@/utils/geoAnomaly'
import { regionNamer, type RegionNamer } from '@/utils/regionName'

/**
 * One line per country: "🇨🇳 CN 3: Guangdong 2 (Shenzhen 2) · Hunan 1".
 * Counts are judged sources, so the line reads as "how many were where". A
 * name the database did not resolve prints as "?" — it is a real source that
 * could only be placed as far as its parent, and dropping it would make the
 * counts stop adding up. Country-only evidence prints as the country alone.
 *
 * "Resolved below the country" means a region OR a city. A location record
 * can carry a city and no subdivision (the geoip reader takes the two from
 * separate fields), and the server counts that city for the city tier on its
 * own (ObserveGeo adds regions and cities independently). Keyed on the region
 * alone, such a country printed as its head only, so a city-tier flag could
 * stand next to a Places cell that names no city. It prints as
 * "? n (City n, …)" instead.
 *
 * Regions are named by nameRegion: a Chinese UI reads a CN province with a
 * known ISO code in Chinese, every other region and UI reads the database's
 * name. Cities always print as the database gives them.
 */
export function spotLine(c: SpotTree, nameRegion: RegionNamer): string {
  const name = (s: string) => s || '?'
  const named = c.regions.some(r => r.region !== '' || r.cities.some(ci => ci.city !== ''))
  const regions = c.regions.map(r => {
    const cities = r.cities.some(ci => ci.city !== '')
      ? ` (${r.cities.map(ci => `${name(ci.city)} ${ci.n}`).join(', ')})`
      : ''
    return `${nameRegion({ cc: c.cc, region: r.region, rc: r.rc }) || '?'} ${r.n}${cities}`
  })
  const head = [countryFlag(c.cc), c.cc, String(c.n)].filter(Boolean).join(' ')
  return named ? `${head}: ${regions.join(' · ')}` : head
}

/**
 * Where the account was at once, one line per country (spotLine). evidence.v
 * 0 is a row an older build wrote: nothing recorded, so its places (whatever
 * that build stored) are all there is.
 */
export function GeoPlaces({ row }: { row: Pick<GeoAnomaly, 'places' | 'evidence'> }) {
  const { t, i18n } = useTranslation(['admin'])
  const nameRegion = regionNamer(t, i18n.language)
  const spots = row.evidence?.v > 0 && row.evidence.spots.length ? groupSpots(row.evidence.spots) : null
  if (spots) {
    return <>{spots.map(c => <Box key={c.cc} sx={{ whiteSpace: 'nowrap' }}>{spotLine(c, nameRegion)}</Box>)}</>
  }
  return <>{row.places.length ? row.places.join(' · ') : '—'}</>
}

/**
 * How far apart the farthest two concurrent sources were, on its own line and
 * never inside the reason: the verdict does not read it, and a distance woven
 * into "flagged" would read as a travel finding it is not. Nothing where none
 * was measured, or on a row from before the field existed (spreadKm).
 */
export function GeoDistance({ evidence }: { evidence: GeoEvidence | undefined }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const km = spreadKm(evidence)
  if (km <= 0) return null
  return (
    <Tooltip title={t('admin:geo_anomalies.max_km_hint')}>
      <Box sx={{ whiteSpace: 'nowrap', color: md.onSurfaceVariant }}>
        {/* String(km), not a locale-grouped number: the server already
            rounded it, and a separator would differ by browser locale. */}
        {t('admin:geo_anomalies.max_km', { km: String(km) })}
      </Box>
    </Tooltip>
  )
}
