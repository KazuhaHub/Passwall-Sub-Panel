import type { Cert, DNSProviderField, DNSProviderInfo } from '@/api/certs'
import type { Node } from '@/api/types'
import type { Tone } from '@/components/common/tones'

const LE_PROD = 'https://acme-v02.api.letsencrypt.org/directory'
const LE_STAGING = 'https://acme-staging-v02.api.letsencrypt.org/directory'

/**
 * CA directory presets for the ACME-account form. ZeroSSL and Google Public CA require External Account
 * Binding (eab), so the form says so when one of them is picked.
 */
export const CA_PRESETS: { label: string; url: string; eab?: boolean }[] = [
  { label: "Let's Encrypt", url: LE_PROD },
  { label: 'LE Staging', url: LE_STAGING },
  { label: 'ZeroSSL', url: 'https://acme.zerossl.com/v2/DV90', eab: true },
  { label: 'Google', url: 'https://dv.acme-v02.api.pki.goog/directory', eab: true },
]
export const DEFAULT_DIRECTORY = LE_PROD
export const DEFAULT_KEY_TYPES = ['EC256', 'EC384', 'RSA2048', 'RSA4096']

/** A directory URL as a short CA name; an unrecognised URL shows its host so a custom CA still reads well. */
export function caLabel(directory: string): string {
  const preset = CA_PRESETS.find(p => p.url === directory)
  if (preset) return preset.label
  try { return new URL(directory).hostname } catch { return directory }
}

/** One domain per line or comma-separated, as the form has always accepted. */
export function parseDomains(text: string): string[] {
  return text.split(/[\s,]+/).map(s => s.trim()).filter(Boolean)
}

/** Same set of names, in any order: the backend re-issues only when the set changes. */
export function sameDomains(a: readonly string[], b: readonly string[]): boolean {
  if (a.length !== b.length) return false
  const left = [...a].sort()
  const right = [...b].sort()
  return left.every((d, i) => d === right[i])
}

// ---- health ----

export type CertHealthId = 'failed' | 'expired' | 'expiring' | 'issuing' | 'valid'

export interface CertHealth {
  id: CertHealthId
  tone: Tone
  /** Whole days until expiry (negative once past); null while no certificate was ever issued. */
  days: number | null
  /** Problems sort first. */
  rank: number
}

const DAY = 86_400_000
/** Until the settings load, the renewal threshold the backend itself defaults to. */
export const DEFAULT_RENEW_BEFORE_DAYS = 30

/**
 * What state a certificate is in, in the words an admin acts on. The backend's `expired` is derived (an
 * active certificate past its end), and a certificate can also be past its end while still stored as active
 * between two list reads, so both count.
 */
export function certHealth(c: Pick<Cert, 'status' | 'not_after'>, now: number, renewBeforeDays: number): CertHealth {
  const end = c.not_after ? Date.parse(c.not_after) : NaN
  const days = Number.isNaN(end) ? null : Math.floor((end - now) / DAY)
  if (c.status === 'failed') return { id: 'failed', tone: 'danger', days, rank: 0 }
  if (c.status === 'expired' || (c.status === 'active' && days !== null && end <= now)) return { id: 'expired', tone: 'danger', days, rank: 0 }
  if (c.status === 'pending') return { id: 'issuing', tone: 'info', days, rank: 2 }
  if (days !== null && days <= renewBeforeDays) return { id: 'expiring', tone: 'warning', days, rank: 1 }
  return { id: 'valid', tone: 'success', days, rank: 3 }
}

/** Problems first, then the soonest to expire, then by name; the list reads as a to-do list. */
export function sortCerts(list: readonly Cert[], now: number, renewBeforeDays: number): Cert[] {
  const key = (c: Cert) => ({ h: certHealth(c, now, renewBeforeDays), end: c.not_after ? Date.parse(c.not_after) : Infinity })
  return list.map(c => ({ c, ...key(c) })).sort((a, b) =>
    a.h.rank - b.h.rank
    || (a.end === b.end ? 0 : a.end < b.end ? -1 : 1)
    || a.c.name.localeCompare(b.c.name)).map(x => x.c)
}

// ---- where a certificate is used ----

/** The nodes that deploy each managed certificate. The backend refuses to delete a certificate any node still references. */
export function nodesByCert(nodes: readonly Node[]): Map<number, Node[]> {
  const out = new Map<number, Node[]>()
  for (const n of nodes) {
    if (n.cert_source !== 'psp_managed' || !n.cert_id) continue
    const list = out.get(n.cert_id)
    if (list) list.push(n)
    else out.set(n.cert_id, [n])
  }
  return out
}

// ---- DNS credentials ----

/** No fixed schema (exec, httpreq, or a name the catalogue does not know): free-form KEY / VALUE pairs. */
export function isCustomProvider(info: DNSProviderInfo | null | undefined): boolean {
  return !info || info.custom
}

export type CredentialsResult = { ok: true; credentials: Record<string, string> } | { ok: false }

/**
 * The `credentials` body of a create or update, exactly as the form has always built it. Editing, a blank
 * value means "keep the stored secret" (the backend merges it), so every field is sent; creating, every
 * required value must be filled and an optional one left blank is simply left out.
 */
export function buildCredentials(opts: {
  editing: boolean
  custom: boolean
  fields: readonly DNSProviderField[]
  values: Record<string, string>
  pairs: readonly { k: string; v: string }[]
}): CredentialsResult {
  const credentials: Record<string, string> = {}
  if (opts.custom) {
    for (const p of opts.pairs) if (p.k.trim()) credentials[p.k.trim()] = p.v
    if (!opts.editing && Object.values(credentials).some(v => !v.trim())) return { ok: false }
    return { ok: true, credentials }
  }
  for (const f of opts.fields) {
    const v = opts.values[f.key] ?? ''
    if (opts.editing) credentials[f.key] = v
    else if (v.trim()) credentials[f.key] = v
    else if (!f.optional) return { ok: false }
  }
  return { ok: true, credentials }
}
