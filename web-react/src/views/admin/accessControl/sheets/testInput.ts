// What the test sheet does to what an admin types, as pure functions.

/** The host of whatever was pasted: a URL loses its scheme, credentials, path, query and fragment. */
export function normalizeTarget(raw: string): { host: string; port: number | null; stripped: boolean } {
  let s = raw.trim()
  let stripped = false
  const scheme = /^[a-z][a-z0-9+.-]*:\/\//i.exec(s)
  if (scheme) { s = s.slice(scheme[0].length); stripped = true }
  const cut = s.search(/[/?#]/)
  if (cut >= 0) { s = s.slice(0, cut); stripped = true }
  const at = s.lastIndexOf('@')
  if (at >= 0) { s = s.slice(at + 1); stripped = true }
  let port: number | null = null
  const hp = /^(\[[0-9a-f:.]+\]|[^:]+):(\d{1,5})$/i.exec(s)
  if (hp && (scheme || cut >= 0 || stripped)) { s = hp[1]; port = Number(hp[2]) }
  if (s.startsWith('[') && s.endsWith(']')) s = s.slice(1, -1)
  return { host: s, port, stripped }
}

export function validIp(raw: string): boolean {
  const s = raw.trim()
  if (s === '') return true
  const v4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(s)
  if (v4) return v4.slice(1).every(o => Number(o) <= 255)
  return s.includes(':') && /^[0-9a-f:.]+$/i.test(s) && s.length <= 45
}

/** An empty port is "unknown" (rules with ports are then untestable); otherwise 1–65535. */
export function validPort(raw: string): boolean {
  if (raw.trim() === '') return true
  return /^\d{1,5}$/.test(raw.trim()) && Number(raw) >= 1 && Number(raw) <= 65535
}
