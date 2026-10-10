import { panelDayStr } from '@/utils/datetime'

export function hitTime(locale: string, timezone: string) {
  let zone: string | undefined = timezone || undefined
  try { new Intl.DateTimeFormat(locale, { timeZone: zone }).format(0) } catch { zone = undefined }
  const clock = new Intl.DateTimeFormat(locale, { timeZone: zone, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
  const date = new Intl.DateTimeFormat(locale, { timeZone: zone, dateStyle: 'medium' })
  const full = new Intl.DateTimeFormat(locale, { timeZone: zone, year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZoneName: 'shortOffset' })
  return { range: (hour: number) => `${clock.format(hour)}–${clock.format(hour + 3600000)}`,
    day: (hour: number) => panelDayStr(zone, 0, new Date(hour)), date: (hour: number) => date.format(hour), full: (at: number) => full.format(at) }
}
