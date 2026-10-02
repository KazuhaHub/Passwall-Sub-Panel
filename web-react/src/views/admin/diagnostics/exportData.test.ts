import { describe, expect, it } from 'vitest'
import type { DiagnosticsSnapshot, MetricsSnapshot } from '@/api/diagnostics'
import { buildExport, buildPreviousExport, exportFileName, exportStamp } from './exportData'

const AT = Date.UTC(2026, 9, 1, 6, 2, 11)

const metrics: MetricsSnapshot = { since_unix_ms: AT - 3_600_000, window_ms: 3_600_000, counters: [], gauges: [], histograms: [] }
const snap: DiagnosticsSnapshot = { version: 'v4.0.1.18', commit: '53045c9c', uptime_ms: 3_600_000, goroutines: 87, metrics }

describe('exportStamp', () => {
  // The file is named for the panel's clock, the one the rest of the panel
  // reports against, not for whichever browser saved it.
  it('writes the instant in the panel timezone', () => {
    expect(exportStamp(AT, 'Asia/Shanghai')).toBe('20261001-1402')
    expect(exportStamp(AT, 'UTC')).toBe('20261001-0602')
  })

  it('falls back to the browser clock for an unset or unknown timezone instead of throwing', () => {
    expect(exportStamp(AT, '')).toMatch(/^\d{8}-\d{4}$/)
    expect(exportStamp(AT, 'Not/AZone')).toMatch(/^\d{8}-\d{4}$/)
  })
})

describe('exportFileName', () => {
  it('names the file after the version and the panel-time instant', () => {
    expect(exportFileName('psp-diagnostics', 'v4.0.1.18', AT, 'UTC')).toBe('psp-diagnostics-v4.0.1.18-20261001-0602.json')
  })

  it('keeps a version that is not filename-safe from breaking the name', () => {
    expect(exportFileName('psp-diagnostics', 'dev build/1', AT, 'UTC')).toBe('psp-diagnostics-dev_build_1-20261001-0602.json')
  })
})

describe('buildExport', () => {
  const page = {
    read_at: new Date(AT).toISOString(),
    settings_interval_ms: 120_000,
    effective_interval_ms: 120_000,
    window_mode: 'settled' as const,
    verdict: 'action',
    expected_polls_min: 1773,
    findings: [{ id: 'lifecycle_errors', severity: 'error' as const, values: { errors: 17, checks: 900 } }],
    self_checks: [{ id: 'poll_ms' as const, pass: true, values: { observed: 1774, expected: 1774 } }],
    cards: [{ id: 'poll' as const, state: 'ok' as const }],
    panel_types: { complete: true, types: ['psp', '3xui'] },
  }

  // The response is what support needs to reproduce the page, so it travels
  // exactly as it arrived: no field renamed, rounded or dropped.
  it('carries the API response untouched, beside what the page made of it', () => {
    const out = buildExport(snap, page, AT)
    expect(out.kind).toBe('psp-diagnostics')
    expect(out.exported_at).toBe('2026-10-01T06:02:11.000Z')
    expect(out.api).toBe(snap)
    expect(out.page.findings).toEqual(page.findings)
    expect(out.page.expected_polls_min).toBe(1773)
  })

  it('lists the panel types in a stable order', () => {
    expect(buildExport(snap, page, AT).page.panel_types.types).toEqual(['3xui', 'psp'])
  })
})

describe('buildPreviousExport', () => {
  it('keeps the closed window with when it closed and which build measured it', () => {
    expect(buildPreviousExport(metrics, AT, snap, AT + 5)).toEqual({
      kind: 'psp-diagnostics-pre-reset',
      exported_at: new Date(AT + 5).toISOString(),
      closed_at: '2026-10-01T06:02:11.000Z',
      version: 'v4.0.1.18',
      commit: '53045c9c',
      previous: metrics,
    })
  })
})
