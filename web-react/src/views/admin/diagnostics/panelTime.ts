/** A panel-time instant, as the rest of the panel writes one. */
export function panelTime(ms: number, tz: string, lang: string, timeOnly = false): string {
  const d = new Date(ms)
  const opts: Intl.DateTimeFormatOptions = timeOnly
    ? { hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }
    : { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }
  try {
    return d.toLocaleString(lang, { ...opts, timeZone: tz || undefined })
  } catch {
    return d.toLocaleString(lang, opts)
  }
}
