import { coerce, gte } from 'semver'

const MLKEM_FIRST_VERSION = '26.9.8'
const REALITY_SITES = ['www.asus.com', 'www.samsung.com', 'www.nvidia.com', 'www.amd.com', 'www.intel.com']

export function randomRealitySite(exclude: string[] = []): string {
  const excluded = new Set(exclude.map(site => site.toLowerCase()))
  const candidates = REALITY_SITES.filter(site => !excluded.has(site))
  const sites = candidates.length ? candidates : REALITY_SITES
  return sites[Math.floor(Math.random() * sites.length)]
}

/** Whether the observed Xray core requires X25519MLKEM768 as the first key share. */
export function xrayRequiresMLKEMFirst(version?: string): boolean {
  if (!version?.trim()) return false
  const parsed = coerce(version)
  return parsed !== null && gte(parsed, MLKEM_FIRST_VERSION)
}
