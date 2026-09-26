import { describe, expect, it } from 'vitest'
import type { GeoAnomaly, GeoSpot } from '@/api/geoAnomalies'
import type { GeoIPStatus, UISettings } from '@/api/settings'
import { activeDbIsCountryOnly, geoTolerances, groupSpots, sortBySeverity, tierLabelKey } from './geoAnomaly'

function row(over: Partial<GeoAnomaly>): GeoAnomaly {
  return {
    user_id: 1,
    state: 'clean',
    reason: '',
    tier: '',
    flagged: false,
    places: [],
    live_ips: 0,
    concurrent_ips: 0,
    excluded_ips: 0,
    complete: true,
    over_streak: 0,
    under_streak: 0,
    ban_streak: 0,
    evidence: {
      v: 1, spots: [], excluded: { shared: 0, listed: 0, infra: 0, internal: 0 }, stale: 0,
      coverage: { placed: 0, unplaced: 0, region_known: 0, city_known: 0 }, networks: 0,
      spread: { countries: 0, regions: 0, region_country: '', cities: 0, city_country: '' },
    },
    updated_at_ms: 0,
    ...over,
  }
}

describe('tierLabelKey', () => {
  it('names each tier under the geo tab namespace and nothing for no tier', () => {
    expect(tierLabelKey('country')).toBe('admin:geo_anomalies.tier_country')
    expect(tierLabelKey('region')).toBe('admin:geo_anomalies.tier_region')
    expect(tierLabelKey('city')).toBe('admin:geo_anomalies.tier_city')
    // A clean or never-over row has no tier; a chip reading "tier_" would be
    // a claim about nothing.
    expect(tierLabelKey('')).toBeNull()
  })
})

describe('groupSpots', () => {
  const spots: GeoSpot[] = [
    { cc: 'CN', region: 'Guangdong', city: 'Shenzhen', n: 2 },
    { cc: 'CN', region: 'Hunan', city: 'Yueyang', n: 1 },
    { cc: 'JP', region: 'Tokyo', city: '', n: 1 },
    { cc: 'CN', region: 'Guangdong', city: 'Guangzhou', n: 1 },
    { cc: 'CN', region: '', city: '', n: 3 },
    { cc: 'CN', region: 'Hunan', city: 'Changsha', n: 1 },
  ]

  it('sums each tier from the spots below it', () => {
    const tree = groupSpots(spots)
    expect(tree.map(c => [c.cc, c.n])).toEqual([['CN', 8], ['JP', 1]])
    expect(tree[0].regions.map(r => [r.region, r.n])).toEqual([['Guangdong', 3], ['Hunan', 2], ['', 3]])
    expect(tree[1]).toEqual({ cc: 'JP', n: 1, regions: [{ region: 'Tokyo', n: 1, cities: [{ city: '', n: 1 }] }] })
  })

  it('orders by count, then name, with unresolved names last', () => {
    const regions = groupSpots(spots).flatMap(c => c.regions)
    expect(regions.map(r => r.cities)).toEqual([
      [{ city: 'Shenzhen', n: 2 }, { city: 'Guangzhou', n: 1 }],
      // A tie on count falls back to the name, so the order is stable.
      [{ city: 'Changsha', n: 1 }, { city: 'Yueyang', n: 1 }],
      // The country-only bucket carries 3, as many as Guangdong, and is still
      // last: it is "resolved no further than the country", not a region
      // competing with the real ones.
      [{ city: '', n: 3 }],
      [{ city: '', n: 1 }],
    ])
  })

  it('returns nothing for no spots', () => {
    expect(groupSpots([])).toEqual([])
  })
})

describe('sortBySeverity', () => {
  it('puts what needs a decision first, and a latched idle flag above the ramp', () => {
    const rows = [
      row({ user_id: 1, state: 'clean', updated_at_ms: 50 }),
      row({ user_id: 2, state: 'suspect', updated_at_ms: 40 }),
      // Flagged, then disconnected: the streak froze with the latch on. It is
      // still flagged, and sorting it by its "idle" state would bury it under
      // every clean row — the easiest evasion there is.
      row({ user_id: 3, state: 'idle', flagged: true, updated_at_ms: 10 }),
      row({ user_id: 4, state: 'flagged', flagged: true, updated_at_ms: 20 }),
      row({ user_id: 5, state: 'unknown', updated_at_ms: 30 }),
      row({ user_id: 6, state: 'exempt', updated_at_ms: 60 }),
      row({ user_id: 7, state: 'unknown', flagged: true, updated_at_ms: 5 }),
      row({ user_id: 8, state: 'idle', updated_at_ms: 70 }),
    ]
    expect(sortBySeverity(rows).map(r => r.user_id)).toEqual([4, 3, 7, 2, 5, 1, 8, 6])
  })

  it('breaks ties newest first and leaves the input alone', () => {
    const rows = [
      row({ user_id: 1, state: 'suspect', updated_at_ms: 1 }),
      row({ user_id: 2, state: 'suspect', updated_at_ms: 7 }),
    ]
    const sorted = sortBySeverity(rows)
    expect(sorted.map(r => r.user_id)).toEqual([2, 1])
    // A new array: the input is the query cache's, shared with every reader.
    expect(sorted).not.toBe(rows)
    expect(rows.map(r => r.user_id)).toEqual([1, 2])
  })
})

describe('activeDbIsCountryOnly', () => {
  const db = (granularity: string, active: boolean) =>
    ({ file: `${granularity}.mmdb`, type: '', granularity, build_epoch: 0, active })
  const status = (available: ReturnType<typeof db>[]) =>
    ({ enabled: true, dir: '', active: '', available, update: { updating: false } }) as GeoIPStatus

  it('judges only the ACTIVE database', () => {
    expect(activeDbIsCountryOnly(status([db('country', true)]))).toBe(true)
    // A country-only file lying next to an active city one changes nothing.
    expect(activeDbIsCountryOnly(status([db('country', false), db('city', true)]))).toBe(false)
    expect(activeDbIsCountryOnly(status([]))).toBe(false)
  })

  it('says nothing when the status is unknown', () => {
    // A failed or missing read is no evidence of a coarse database; claiming
    // one would tell the admin two tiers are dead when they may be fine.
    expect(activeDbIsCountryOnly(undefined)).toBe(false)
    expect(activeDbIsCountryOnly({} as GeoIPStatus)).toBe(false)
  })
})

describe('geoTolerances', () => {
  const s = (over: Partial<UISettings>) => over as UISettings

  it('reads unset (0 or missing) as the shipped defaults, never as zero tolerance', () => {
    expect(geoTolerances(s({}))).toEqual({
      flag: { countries: 1, regions: 1, cities: 2 },
      ban: { countries: 1, regions: 2, cities: 3 },
      banAfterPolls: 6,
      banMinutes: 60,
    })
    expect(geoTolerances(s({
      geo_anomaly_max_places: 0, geo_anomaly_max_regions: -1, geo_anomaly_max_cities: 0,
      geo_anomaly_ban_max_countries: 0, geo_anomaly_ban_max_regions: 0, geo_anomaly_ban_max_cities: -3,
      geo_anomaly_ban_after_polls: 0, geo_anomaly_ban_duration_minutes: -5,
    }))).toEqual({
      flag: { countries: 1, regions: 1, cities: 2 },
      ban: { countries: 1, regions: 2, cities: 3 },
      banAfterPolls: 6,
      banMinutes: 60,
    })
  })

  it('raises each ban tolerance to at least its flag tolerance', () => {
    // The server does the same (sanitized), so ban-over always implies
    // flag-over; the caption must say what will actually happen.
    const got = geoTolerances(s({
      geo_anomaly_max_places: 3, geo_anomaly_max_regions: 4, geo_anomaly_max_cities: 5,
      geo_anomaly_ban_max_countries: 2, geo_anomaly_ban_max_regions: 9, geo_anomaly_ban_max_cities: 1,
    }))
    expect(got.flag).toEqual({ countries: 3, regions: 4, cities: 5 })
    expect(got.ban).toEqual({ countries: 3, regions: 9, cities: 5 })
  })

  it('clamps the suspension length to seven days', () => {
    expect(geoTolerances(s({ geo_anomaly_ban_duration_minutes: 99999 })).banMinutes).toBe(10080)
    expect(geoTolerances(s({ geo_anomaly_ban_duration_minutes: 10080 })).banMinutes).toBe(10080)
    expect(geoTolerances(s({ geo_anomaly_ban_duration_minutes: 1 })).banMinutes).toBe(1)
    expect(geoTolerances(s({ geo_anomaly_ban_after_polls: 2 })).banAfterPolls).toBe(2)
  })
})
