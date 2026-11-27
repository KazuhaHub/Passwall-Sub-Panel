import type { MeProfile, SubImportClient } from '@/api/me'

/** Bytes as "1.50 GB". Zero is a bare "0" (as it always was here), so an empty meter does not read "0.00 B". */
export function bytesToHuman(n: number): string {
  if (n === 0) return '0'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n, u = 0
  while (v >= 1024 && u < units.length - 1) { v /= 1024; u++ }
  return `${v.toFixed(2)} ${units[u]}`
}

/**
 * Coarse user-agent platform: one of the values used in SubImportClient.recommended_for, so the portal can pick the
 * right recommended client. Order matters: iPad/iPhone before "mac" (some iPads claim macOS in their UA), Android
 * before "linux" (Android UAs contain "Linux"). Null when nothing matches (curl, exotic platforms): then no client
 * is recommended and the client tab lists them all.
 */
export function detectPlatform(): string | null {
  const ua = (navigator.userAgent || '').toLowerCase()
  if (/iphone|ipad|ipod/.test(ua)) return 'ios'
  if (/android/.test(ua)) return 'android'
  if (/windows/.test(ua)) return 'windows'
  if (/mac os x|macintosh/.test(ua)) return 'macos'
  if (/linux/.test(ua)) return 'linux'
  return null
}

/**
 * The link with its secret part hidden: the protocol and host stay (so the person can tell it is theirs), everything
 * after the host is dots. The dots are always the same count, so the length of the token is not leaked either.
 */
export function maskUrl(url: string): string {
  try {
    const u = new URL(url)
    return `${u.protocol}//${u.host}/${'•'.repeat(16)}`
  } catch {
    return '•'.repeat(Math.min(40, url.length))
  }
}

/** The clients the admin switched on, in their configured order. */
export function enabledSorted<T extends { enabled: boolean; sort: number }>(list: readonly T[] | undefined): T[] {
  return (list ?? []).filter(c => c.enabled).slice().sort((a, b) => (a.sort || 0) - (b.sort || 0))
}

/** The client recommended for this platform: the first enabled one whose recommended_for names it. */
export function recommendedClient(clients: readonly SubImportClient[], platform: string | null): SubImportClient | undefined {
  return platform ? clients.find(c => c.recommended_for?.includes(platform)) : undefined
}

/**
 * Fills a client's import-URL template. Every placeholder form is kept exactly as the old page produced it:
 * the server resolves the profile name (SubProfileNameTemplate), the base64 forms exist for V2rayNG / Shadowrocket
 * deep links, and CMfA reads `update-interval` from the URI in MINUTES while the HTTP header is in hours.
 */
export function buildImportURL(profile: Pick<MeProfile, 'sub_url' | 'profile_name' | 'display_name' | 'upn' | 'sub_update_interval_hours'>, c: { import_url_template: string }): string {
  const subUrl = profile.sub_url || ''
  const profileName = profile.profile_name || profile.display_name || profile.upn || 'Passwall'
  // btoa requires latin-1; the link is ASCII, so unescape(encodeURIComponent()) pre-encodes it safely.
  let subUrlB64 = ''
  try { subUrlB64 = btoa(unescape(encodeURIComponent(subUrl))) } catch { /* leave empty */ }
  // URL-safe base64, padding stripped: the form Shadowrocket's `sub://<b64>?remark=` parser accepts reliably.
  const subUrlB64UrlSafe = subUrlB64.replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
  const intervalHours = profile.sub_update_interval_hours && profile.sub_update_interval_hours > 0 ? profile.sub_update_interval_hours : 24
  return c.import_url_template
    .replaceAll('{{ sub_url_encoded }}', encodeURIComponent(subUrl))
    .replaceAll('{{ sub_url_b64_url_safe }}', subUrlB64UrlSafe)
    .replaceAll('{{ sub_url_b64 }}', subUrlB64)
    .replaceAll('{{ sub_url }}', subUrl)
    .replaceAll('{{ profile_name_encoded }}', encodeURIComponent(profileName))
    .replaceAll('{{ profile_name }}', profileName)
    .replaceAll('{{ sub_update_interval_minutes }}', String(intervalHours * 60))
    .replaceAll('{{ sub_update_interval_hours }}', String(intervalHours))
}

/** Schemes that run code or read local data when navigated to; an import link or quick link may never use them. */
const DANGEROUS_SCHEMES = ['javascript', 'data', 'vbscript', 'blob', 'file']

/** The scheme of a link, lower-cased; '' when it has none. */
export function schemeOf(url: string): string {
  return (url.match(/^([a-z][a-z0-9+.-]*):/i)?.[1] ?? '').toLowerCase()
}

export const isHttpUrl = (url: string) => /^https?:\/\//i.test(url)

/** An app-scheme import link (clash://, karing://) is allowed; a script-capable or scheme-less one is not. */
export function isSafeAppScheme(url: string): boolean {
  const scheme = schemeOf(url)
  return scheme !== '' && !DANGEROUS_SCHEMES.includes(scheme)
}

export interface Expiry {
  /** The calendar day, in the viewer's own time zone (the page says so when it differs from the panel's). */
  dateStr: string
  /** Whole days left, rounded up; negative once past. */
  days: number
  expired: boolean
}

/** Remaining days and the printed date of an absolute expiry instant; null for an account that never expires. */
export function expiryOf(expireAt: string | null | undefined, now = Date.now()): Expiry | null {
  if (!expireAt) return null
  const d = new Date(expireAt)
  const days = Math.ceil((d.getTime() - now) / 86400000)
  return { dateStr: d.toLocaleDateString(), days, expired: days < 0 }
}

/** The state both the status card and the notice read: the access snapshot's service axis, else the flat field. */
export function serviceStateOf(p: Pick<MeProfile, 'access' | 'service_status'>): string | undefined {
  return p.access?.service_state ?? p.service_status
}

/** No state at all (an older backend) or "active": the only states in which the service is known to be working. */
export const isServiceActive = (state: string | undefined) => !state || state === 'active'
