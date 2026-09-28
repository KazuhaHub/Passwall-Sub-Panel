// formatRelativeTimeShort renders a "X 分钟前" / "X 小时前" / "X 天前" style
// label. Chunked rather than using Intl.RelativeTimeFormat directly because we
// want a single integer pick per call (no auto-pluralization in EN that adds
// "(s)"), and i18n keys give translators full control of the phrase. Buckets:
//   < 1m  : just now
//   < 1h  : minutes_ago
//   < 1d  : hours_ago
//   < 30d : days_ago
//   ≥ 30d : long_ago_date (fall back to YYYY-MM-DD so the tooltip still has
//           the exact timestamp but the column doesn't shout "9999天前")
//
// Shared by the Users list's "last online" and the risk center queue's "last
// change", so one moment reads in one set of words on both pages.
export function formatRelativeTimeShort(diffMs: number, t: (k: string, opts?: Record<string, unknown>) => string): string {
  if (diffMs < 0) diffMs = 0
  const sec = Math.floor(diffMs / 1000)
  if (sec < 60) return t('admin:users.relative_time.just_now', { defaultValue: '刚刚' })
  const min = Math.floor(sec / 60)
  if (min < 60) return t('admin:users.relative_time.minutes_ago', { count: min, defaultValue: '{{count}} 分钟前' })
  const hr = Math.floor(min / 60)
  if (hr < 24) return t('admin:users.relative_time.hours_ago', { count: hr, defaultValue: '{{count}} 小时前' })
  const day = Math.floor(hr / 24)
  if (day < 30) return t('admin:users.relative_time.days_ago', { count: day, defaultValue: '{{count}} 天前' })
  // Long ago — emit YYYY-MM-DD instead of a relative label.
  const d = new Date(Date.now() - diffMs)
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const dd = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${dd}`
}
