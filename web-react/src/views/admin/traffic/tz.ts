// The time zones offered on the trend toolbar. Read from the browser's IANA database
// (Intl.supportedValuesOf) so the list covers whatever Go's time.LoadLocation can resolve,
// with no upkeep; an engine without it gets a short hand-written list. The browser zone and
// the panel zone are pinned to the top by buildTzOptions.
const COMMON_CHART_TIMEZONES: string[] = (() => {
  try {
    const fn = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf
    if (typeof fn === 'function') return fn('timeZone')
  } catch { /* fall through */ }
  return [
    'UTC', 'America/Los_Angeles', 'America/Denver', 'America/Chicago',
    'America/New_York', 'Asia/Shanghai', 'Asia/Hong_Kong', 'Asia/Taipei',
    'Asia/Tokyo', 'Asia/Seoul', 'Asia/Singapore', 'Europe/London',
    'Europe/Paris', 'Europe/Berlin', 'Europe/Moscow', 'Australia/Sydney',
  ]
})()

export function browserTz(): string {
  try { return Intl.DateTimeFormat().resolvedOptions().timeZone } catch { return 'UTC' }
}

/** Browser zone first, the panel zone second when it differs, then the rest without those two repeated. */
export function buildTzOptions(panelTz: string): string[] {
  const bz = browserTz()
  const head = [bz]
  if (panelTz && panelTz !== bz) head.push(panelTz)
  return [...head, ...COMMON_CHART_TIMEZONES.filter(t => !head.includes(t))]
}

/** Whether the engine knows this zone name. Empty is valid: it means "the default". */
export function isKnownTz(name: string): boolean {
  if (name === '') return true
  try {
    new Intl.DateTimeFormat(undefined, { timeZone: name })
    return true
  } catch { return false }
}
