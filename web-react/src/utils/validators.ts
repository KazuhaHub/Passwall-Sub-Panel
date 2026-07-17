// Shared form validators. Pure functions returning either an empty string
// (valid) or a translation key for the error. Translation lookup happens at
// the call site so the same validator can be reused across views.
//
// Convention: each validator takes the raw value, returns "" or
// "validation.<key>" — views pass that to t() with the admin namespace.
// Some validators take options (min/max, required) and return the most
// relevant error first (required > format > range).

export type Validator = (value: unknown) => string

// --- Primitive validators ---------------------------------------------------

export function isNonEmpty(v: unknown): boolean {
  if (v === null || v === undefined) return false
  if (typeof v === 'string') return v.trim().length > 0
  if (typeof v === 'number') return Number.isFinite(v)
  return true
}

// Matches the RFC-5321 mailbox shape that 99% of real systems accept. We
// don't try to enforce full RFC-5322 since the backend already round-trips
// these through Go's net/mail and we just want to catch typos at the form.
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

export function isEmail(v: string): boolean {
  return EMAIL_RE.test(v.trim())
}

// Accept http/https URLs only (and require a host). Path/query/fragment are
// optional. Used for 3X-UI panel URLs, OIDC issuer URLs, subscription URLs,
// portal logos, etc.
//
// `new URL()` is too permissive on its own — it happily parses junk like
// `http://127.0.0.1rgwerger` because `127.0.0.1rgwerger` is technically a
// legal DNS label. We layer an extra rule on top: if the FIRST hostname
// label is pure digits we treat the whole hostname as an IPv4 candidate
// and require strict octet form. Anything else (real DNS names, IPv6
// literals in brackets, localhost) goes through the loose HOSTNAME_RE.
export function isHttpUrl(v: string): boolean {
  try {
    const raw = v.trim()
    const u = new URL(raw)
    if (u.protocol !== 'http:' && u.protocol !== 'https:') return false
    // Validate the hostname as typed as well as URL's canonicalized value.
    // WHATWG URL expands short numeric hosts (127.0.0 -> 127.0.0.0), which
    // would otherwise defeat the strict IPv4 rule below.
    const rawHost = rawHostname(raw)
    return rawHost !== null && isValidHostname(rawHost) && isValidHostname(u.hostname)
  } catch {
    return false
  }
}

function rawHostname(value: string): string | null {
  const schemeEnd = value.indexOf('://')
  if (schemeEnd < 0) return null
  const authority = value.slice(schemeEnd + 3).split(/[/?#]/, 1)[0]
  const hostPort = authority.slice(authority.lastIndexOf('@') + 1)
  if (hostPort.startsWith('[')) {
    const end = hostPort.indexOf(']')
    return end > 1 ? hostPort.slice(1, end) : null
  }
  const colon = hostPort.lastIndexOf(':')
  return colon >= 0 ? hostPort.slice(0, colon) : hostPort
}

function isValidHostname(host: string): boolean {
  // URL.hostname includes brackets around IPv6 literals in some runtimes.
  if (host.startsWith('[') && host.endsWith(']')) host = host.slice(1, -1)
  if (!host) return false
  // IPv6 literal — URL.hostname strips brackets, leaving the raw address.
  if (host.includes(':')) return IPV6_RE.test(host)
  const firstLabel = host.split('.')[0]
  // First label all-digits => the user is typing an IP. Reject anything
  // that doesn't pass the strict 4-octet form (catches typos like the
  // example above and partial IPs like `127.0.0`).
  if (/^\d+$/.test(firstLabel)) return IPV4_RE.test(host)
  return HOSTNAME_RE.test(host)
}

// Port range 1-65535. 0 means "unset" in some forms — callers decide whether
// to treat 0 as valid via the required flag.
export function isPort(n: number): boolean {
  return Number.isInteger(n) && n >= 1 && n <= 65535
}

// Hostname OR IPv4/IPv6 literal. Used for server addresses where we accept
// either a DNS name or a raw IP. Brackets aren't expected — the inbound
// listen field uses plain hostnames.
const HOSTNAME_RE = /^(?=.{1,253}$)(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$/
const IPV4_RE = /^(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)$/
const IPV6_RE = /^[0-9a-fA-F:]+$/ // loose — full IPv6 grammar is overkill for a form check

export function isHostOrIP(v: string): boolean {
  const s = v.trim()
  if (!s) return false
  return HOSTNAME_RE.test(s) || IPV4_RE.test(s) || (s.includes(':') && IPV6_RE.test(s))
}

// Non-negative integer (allows 0 — used for "unlimited" semantics on traffic
// limits and expire days).
export function isNonNegativeInt(n: number): boolean {
  return Number.isInteger(n) && n >= 0
}

// Strict positive integer (1+). Used for emergency-access hours/count where
// 0 is meaningless.
export function isPositiveInt(n: number): boolean {
  return Number.isInteger(n) && n >= 1
}

export function inRange(n: number, min: number, max: number): boolean {
  return Number.isFinite(n) && n >= min && n <= max
}

// Minimum-strength password check used at form-submit time. Backend enforces
// the same floor — this is just for fast feedback. 8+ chars and at least one
// letter + digit covers the panel's "personal-use proxy" threat model
// without nagging users about symbols.
export function isStrongPassword(v: string): boolean {
  if (v.length < 8) return false
  if (!/[A-Za-z]/.test(v)) return false
  if (!/[0-9]/.test(v)) return false
  return true
}

// Group ID select widget uses '' to mean "none picked" so a numeric 0 is
// invalid in practice — keep the check explicit so callers don't have to
// remember.
export function isValidGroupId(v: number | ''): boolean {
  return typeof v === 'number' && Number.isInteger(v) && v > 0
}

// ISO date (YYYY-MM-DD). MUI date input emits this shape. Empty is valid —
// callers gate that with the required flag.
export function isIsoDate(v: string): boolean {
  if (!v) return true
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(v)
  if (!m) return false
  const d = new Date(v + 'T00:00:00Z')
  if (Number.isNaN(d.getTime())) return false
  // Date normalizes impossible calendar values instead of rejecting them
  // (for example 2024-02-30 becomes 2024-03-01), so compare the parsed
  // components with the normalized instant.
  return d.getUTCFullYear() === Number(m[1])
    && d.getUTCMonth() + 1 === Number(m[2])
    && d.getUTCDate() === Number(m[3])
}

// --- High-level field validators (return i18n key or empty) -----------------

interface Opts { required?: boolean }

export function validateRequired(v: unknown, key = 'validation.required'): string {
  return isNonEmpty(v) ? '' : key
}

export function validateEmail(v: string, opts: Opts = {}): string {
  if (!v) return opts.required ? 'validation.required' : ''
  return isEmail(v) ? '' : 'validation.email'
}

export function validateUrl(v: string, opts: Opts = {}): string {
  if (!v) return opts.required ? 'validation.required' : ''
  return isHttpUrl(v) ? '' : 'validation.url'
}

export function validatePort(n: number, opts: Opts = {}): string {
  if (n === 0 || n === null || n === undefined) return opts.required ? 'validation.required' : ''
  return isPort(n) ? '' : 'validation.port'
}

export function validateHost(v: string, opts: Opts = {}): string {
  if (!v) return opts.required ? 'validation.required' : ''
  return isHostOrIP(v) ? '' : 'validation.host'
}

export function validatePassword(v: string, opts: Opts & { strong?: boolean } = {}): string {
  if (!v) return opts.required ? 'validation.required' : ''
  if (opts.strong && !isStrongPassword(v)) return 'validation.password_strength'
  return ''
}

export function validateNonNegativeInt(n: number, opts: Opts = {}): string {
  if (n === null || n === undefined || (Number.isNaN(n))) {
    return opts.required ? 'validation.required' : ''
  }
  return isNonNegativeInt(n) ? '' : 'validation.non_negative_int'
}

// validateNonNegativeNumber is the decimal-friendly sibling. Use for
// fields whose underlying unit is finer than the displayed unit — e.g.
// "GB" inputs that ultimately persist as int64 bytes, where 1.23 GB
// is both valid and the natural way to show 1320 MB of usage.
export function validateNonNegativeNumber(n: number, opts: Opts = {}): string {
  if (n === null || n === undefined || (Number.isNaN(n))) {
    return opts.required ? 'validation.required' : ''
  }
  return Number.isFinite(n) && n >= 0 ? '' : 'validation.non_negative_number'
}

export function validatePositiveInt(n: number, opts: Opts = {}): string {
  if (n === null || n === undefined || (Number.isNaN(n))) {
    return opts.required ? 'validation.required' : ''
  }
  return isPositiveInt(n) ? '' : 'validation.positive_int'
}

export function validateRange(n: number, min: number, max: number, opts: Opts = {}): string {
  if (n === null || n === undefined || (Number.isNaN(n))) {
    return opts.required ? 'validation.required' : ''
  }
  return inRange(n, min, max) ? '' : 'validation.range'
}

export function validateGroupId(v: number | '', opts: Opts = {}): string {
  if (v === '' || v === null || v === undefined) return opts.required ? 'validation.required' : ''
  return isValidGroupId(v) ? '' : 'validation.required'
}

// Identifier-like fields (usernames, names, codes). Trims whitespace and
// optionally enforces length bounds. Default min=1, max=128.
export function validateName(v: string, opts: Opts & { min?: number; max?: number } = {}): string {
  const s = (v ?? '').trim()
  if (!s) return opts.required ? 'validation.required' : ''
  const min = opts.min ?? 1
  const max = opts.max ?? 128
  if (s.length < min) return 'validation.too_short'
  if (s.length > max) return 'validation.too_long'
  return ''
}

// --- Form-level helper ------------------------------------------------------

export type FieldErrors<T extends string> = Partial<Record<T, string>>

// Pick the first non-empty error from an object. Used in submit handlers to
// produce a single user-facing snack when the form has multiple bad fields.
export function firstError<T extends string>(errs: FieldErrors<T>): string {
  for (const k of Object.keys(errs) as T[]) {
    const v = errs[k]
    if (v) return v
  }
  return ''
}

export function hasErrors<T extends string>(errs: FieldErrors<T>): boolean {
  return firstError(errs) !== ''
}
