// THE CLOCK OF THE HIT RECORDS: every time on the records tab is in the PANEL's
// time zone (plan S12), including the hour bucket a row covers. A zone whose
// offset is not a whole hour is shown as it is (14:30–15:30): the row is the
// UTC hour, and rounding it to the zone's hour would put its hits in an hour
// they did not happen in. Pure; an empty zone is the browser's, a bad one UTC.

const HOUR = 3_600_000

function zoneOf(tz: string): string | undefined {
  if (!tz) return undefined
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: tz })
    return tz
  } catch {
    return 'UTC'
  }
}

/** `HH:mm` of an instant in the zone, 24-hour, whatever the UI language. */
export function formatClock(ms: number, tz: string): string {
  const parts = new Intl.DateTimeFormat('en-GB', { timeZone: zoneOf(tz), hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts(new Date(ms))
  const get = (type: string) => parts.find(p => p.type === type)?.value ?? '00'
  return `${get('hour')}:${get('minute')}`
}

/** `14:00–15:00`: the bucket that starts at `hourMs` and runs one hour. */
export function formatHourRange(hourMs: number, tz: string): string {
  return `${formatClock(hourMs, tz)}–${formatClock(hourMs + HOUR, tz)}`
}

/** `YYYY-MM-DD`: the calendar day an instant falls on in the zone. */
export function dayKey(ms: number, tz: string): string {
  const parts = new Intl.DateTimeFormat('en-GB', { timeZone: zoneOf(tz), year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(new Date(ms))
  const get = (type: string) => parts.find(p => p.type === type)?.value ?? ''
  return `${get('year')}-${get('month')}-${get('day')}`
}

/** Whether a day is today, yesterday (in the zone, as of `now`) or neither. */
export function dayRelation(ms: number, now: number, tz: string): 'today' | 'yesterday' | 'date' {
  const day = dayKey(ms, tz)
  if (day === dayKey(now, tz)) return 'today'
  // The zone's calendar day one day earlier (24 hours back: a DST step moves it by an hour, never by a day).
  if (day === dayKey(now - 24 * HOUR, tz)) return 'yesterday'
  return 'date'
}
