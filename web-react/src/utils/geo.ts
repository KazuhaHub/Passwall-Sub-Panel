import type { GeoLocation } from '../api/subLogs'

/** countryFlag turns a 2-letter ISO country code into its flag emoji
 * (regional-indicator pair). Returns '' for anything that isn't 2 letters. */
export function countryFlag(cc?: string): string {
  if (!cc || cc.length !== 2 || !/^[A-Za-z]{2}$/.test(cc)) return ''
  const base = 0x1f1e6
  const up = cc.toUpperCase()
  return String.fromCodePoint(base + up.charCodeAt(0) - 65, base + up.charCodeAt(1) - 65)
}

/** countryName returns a localized country name from the code when the backend
 * didn't supply one (ipinfo gives a name; MaxMind gives en; ipinfo Lite via
 * mmdb gives only the code → derive via Intl). Falls back to the raw code. */
export function countryName(g?: GeoLocation): string {
  if (!g) return ''
  if (g.country) return g.country
  if (!g.country_code) return ''
  try {
    const dn = new Intl.DisplayNames([navigator.language, 'en'], { type: 'region' })
    return dn.of(g.country_code.toUpperCase()) || g.country_code
  } catch {
    return g.country_code
  }
}

/** formatRegion renders a "🇨🇳 China · Guangdong · Shenzhen" label —
 * country · state/province (region) · city, each shown when present. Region
 * and city often coincide in free DBs, so a city equal to the region is
 * dropped to avoid "Shanghai · Shanghai". City/region are the least-reliable
 * parts for free DBs — callers should present them as approximate. Returns ''
 * when there's nothing to show. */
export function formatRegion(g?: GeoLocation): string {
  if (!g || (!g.country_code && !g.country)) return ''
  const flag = countryFlag(g.country_code)
  const name = countryName(g)
  const tail: string[] = []
  if (g.region) tail.push(g.region)
  if (g.city && g.city !== g.region) tail.push(g.city)
  const head = [flag, name].filter(Boolean).join(' ')
  return tail.length ? `${head} · ${tail.join(' · ')}` : head
}
