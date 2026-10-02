import type { DiagnosticsSnapshot, MetricsSnapshot } from '@/api/diagnostics'
import type { CardId } from '@/utils/diagnosticsCatalog'
import type { CardState, SelfCheck, Severity, WindowMode } from '@/utils/diagnostics'

// What the "copy / download all data" actions hand to support, and the file
// a reset leaves behind. The API response travels exactly as it arrived; the
// page's own reading of it sits beside it under `page`, so a reader can tell
// what the server said from what the page concluded. Neither carries a
// hostname, a user name or a secret: series labels are operation, stage and
// reason names.

export interface PageReading {
  read_at: string
  settings_interval_ms: number
  effective_interval_ms: number
  window_mode: WindowMode
  verdict: string
  expected_polls_min: number | null
  findings: Array<{ id: string; severity: Severity; values?: Record<string, number> }>
  self_checks: Array<Pick<SelfCheck, 'id' | 'pass' | 'values'>>
  cards: Array<{ id: CardId; state: CardState }>
  panel_types: { complete: boolean; types: string[] }
}

export interface DiagnosticsExport {
  kind: 'psp-diagnostics'
  exported_at: string
  page: PageReading
  api: DiagnosticsSnapshot
}

export function buildExport(api: DiagnosticsSnapshot, page: PageReading, now: number): DiagnosticsExport {
  return {
    kind: 'psp-diagnostics',
    exported_at: new Date(now).toISOString(),
    // Sorted so two exports of one fleet compare equal.
    page: { ...page, panel_types: { ...page.panel_types, types: [...page.panel_types.types].sort() } },
    api,
  }
}

export interface PreviousExport {
  kind: 'psp-diagnostics-pre-reset'
  exported_at: string
  closed_at: string
  version: string
  commit: string
  previous: MetricsSnapshot
}

/** The window a reset closed, as the reset returned it, with the build that
 *  measured it: after the clear, the page holds it nowhere else. */
export function buildPreviousExport(
  previous: MetricsSnapshot,
  closedAt: number,
  snap: Pick<DiagnosticsSnapshot, 'version' | 'commit'>,
  now: number,
): PreviousExport {
  return {
    kind: 'psp-diagnostics-pre-reset',
    exported_at: new Date(now).toISOString(),
    closed_at: new Date(closedAt).toISOString(),
    version: snap.version,
    commit: snap.commit ?? '',
    previous,
  }
}

/**
 * yyyyMMdd-HHmm on the panel's clock, the one every other date on the panel
 * is reported against. An unset or unknown zone falls back to the browser's
 * rather than failing the download.
 */
export function exportStamp(ms: number, tz: string): string {
  const parts = (zone: string | undefined) => new Intl.DateTimeFormat('en-GB', {
    timeZone: zone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
  }).formatToParts(new Date(ms))
  let p: Intl.DateTimeFormatPart[]
  try {
    p = parts(tz || undefined)
  } catch {
    p = parts(undefined)
  }
  const get = (type: Intl.DateTimeFormatPartTypes) => p.find(x => x.type === type)?.value ?? '00'
  return `${get('year')}${get('month')}${get('day')}-${get('hour')}${get('minute')}`
}

export function exportFileName(prefix: string, version: string, ms: number, tz: string): string {
  const safe = (version || 'unknown').replace(/[^A-Za-z0-9._-]/g, '_')
  return `${prefix}-${safe}-${exportStamp(ms, tz)}.json`
}

/** Hand the reader a JSON file. */
export function downloadJson(name: string, data: unknown) {
  const url = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' }))
  try {
    const link = document.createElement('a')
    link.href = url
    link.download = name
    document.body.appendChild(link)
    link.click()
    link.remove()
  } finally {
    URL.revokeObjectURL(url)
  }
}
