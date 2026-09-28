// formatDualTz renders a timestamp in the panel timezone first (the "system
// view" the panel reports against — traffic resets, expiry, etc.) with the
// browser-local rendering in parentheses. Falls back to a single value when the
// two timezones are identical or the panel tz is unset. Shared by the Logs and
// Certificates pages so every date renders the same way.
export function formatDualTz(s: string | undefined | null, panelTz: string): string {
  if (!s) return '-'
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return '-'
  let bz = ''
  try {
    bz = Intl.DateTimeFormat().resolvedOptions().timeZone
  } catch {
    bz = ''
  }
  const panelStr = panelTz ? d.toLocaleString(undefined, { timeZone: panelTz }) : d.toLocaleString()
  if (!panelTz || panelTz === bz) return panelStr
  const browserStr = d.toLocaleString()
  return `${panelStr} (${browserStr})`
}

// formatMsDualTz is formatDualTz for epoch milliseconds, the shape every
// risk-center time travels in. 0 and absent read '—' (the risk center's
// "never"), not '-' and not 1970: a zero there means nothing was recorded.
// A value outside the Date range is guarded here because toISOString throws
// on it rather than returning something formatDualTz could reject.
export function formatMsDualTz(ms: number | null | undefined, panelTz: string): string {
  if (!ms) return '—'
  const d = new Date(ms)
  if (Number.isNaN(d.getTime())) return '—'
  return formatDualTz(d.toISOString(), panelTz)
}

// formatDualDate is formatDualTz's DATE-ONLY sibling: panel-tz calendar day
// first, the browser-local day in parens, only when they differ. For fields
// shown as a date without a time (expiry, build date, cert expiry) — using
// formatDualTz there would wrongly tack on a "00:00" time. Takes an RFC3339
// instant (or any Date-parseable string); '-' for empty/invalid.
export function formatDualDate(s: string | undefined | null, panelTz: string): string {
  if (!s) return '-'
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return '-'
  let bz = ''
  try {
    bz = Intl.DateTimeFormat().resolvedOptions().timeZone
  } catch {
    bz = ''
  }
  const panelStr = panelTz ? d.toLocaleDateString(undefined, { timeZone: panelTz }) : d.toLocaleDateString()
  if (!panelTz || panelTz === bz) return panelStr
  const browserStr = d.toLocaleDateString()
  return `${panelStr} (${browserStr})`
}

// panelDayStr returns YYYY-MM-DD for "panel-local today + offsetDays" in the
// panel's configured IANA timezone, falling back to the browser-local calendar
// day when tz is empty/invalid. UTC carries the day arithmetic so adding days
// never drifts across a tz boundary. Used to window the traffic-trend charts on
// the SAME day boundaries as the rest of the panel (traffic resets, expiry — all
// panel-tz) instead of the viewing user's browser tz. Pass the same tz to the
// history API so the backend parses these dates AND buckets snapshots in it.
//
// `now` is injectable for tests; defaults to the current instant.
export function panelDayStr(tz: string | undefined, offsetDays: number, now: Date = new Date()): string {
  let y: number, m: number, d: number
  try {
    const parts = new Intl.DateTimeFormat('en-CA', {
      timeZone: tz || undefined,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    }).formatToParts(now)
    y = Number(parts.find(p => p.type === 'year')!.value)
    m = Number(parts.find(p => p.type === 'month')!.value)
    d = Number(parts.find(p => p.type === 'day')!.value)
  } catch {
    // Invalid tz string — degrade to the browser-local calendar day.
    y = now.getFullYear()
    m = now.getMonth() + 1
    d = now.getDate()
  }
  const base = new Date(Date.UTC(y, m - 1, d))
  base.setUTCDate(base.getUTCDate() + offsetDays)
  return base.toISOString().slice(0, 10)
}
