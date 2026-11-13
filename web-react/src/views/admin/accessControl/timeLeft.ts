// The words for a span that is still ahead ("22 hours" in "expires in 22
// hours"). formatRelativeTimeShort is for the past ("3 days ago") and would
// read "ago" in a sentence about the future.

const HOUR = 3_600_000
const K = 'admin:access_control.exemptions.'

/** Minutes under an hour, hours under two days, else days. */
export function timeLeft(t: (key: string, opts?: Record<string, unknown>) => string, ms: number): string {
  if (ms < HOUR) return t(`${K}left_minutes`, { count: Math.max(1, Math.ceil(ms / 60000)) })
  if (ms < 48 * HOUR) return t(`${K}left_hours`, { count: Math.floor(ms / HOUR) })
  return t(`${K}left_days`, { count: Math.floor(ms / (24 * HOUR)) })
}
