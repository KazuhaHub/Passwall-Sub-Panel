import { describe, expect, it } from 'vitest'
import zh from '@/locales/zh-CN/admin.json'
import en from '@/locales/en-US/admin.json'
import type { DiagnosticsSnapshot, MetricsSnapshot } from '@/api/diagnostics'
import {
  SETTLED_INTERVALS,
  deriveFindings,
  derivePreconditions,
  quantileUsable,
  verdict,
  wasReset,
  windowMode,
} from './diagnostics'

const INTERVAL = 5 * 60_000

function metrics(over: Partial<MetricsSnapshot> = {}): MetricsSnapshot {
  return {
    since_unix_ms: 0,
    window_ms: INTERVAL * 10,
    counters: [],
    gauges: [],
    histograms: [],
    ...over,
  }
}

function snap(m: Partial<MetricsSnapshot> = {}, uptimeMs?: number): DiagnosticsSnapshot {
  const mm = metrics(m)
  return {
    version: 'v3.9.2-beta.18',
    uptime_ms: uptimeMs ?? mm.window_ms,
    goroutines: 42,
    metrics: mm,
  }
}

const c = (name: string, value: number) => ({ name, help: '', value })
const g = (name: string, value: number, peak = value) => ({ name, help: '', value, peak })
const h = (name: string, count: number) => ({
  name, help: '', unit: 'ms', count, sum: 0, mean: 0, max: 0, p50: 0, p90: 0, p95: 0, p99: 0, buckets: [],
})

// A fleet that is genuinely fine: cycles ran, pushes executed, nothing errored.
const healthy = () => metrics({
  counters: [
    c('psp_poll_total', 40),
    c('psp_push_client_config_total', 12),
    c('psp_poll_floor_push_enqueued_total', 12),
    c('psp_lifecycle_sync_total', 30),
  ],
  gauges: [g('psp_push_sem_capacity', 8)],
  histograms: [h('psp_poll_ms', 40)],
})

describe('windowMode', () => {
  it('withholds numbers entirely below one poll interval', () => {
    expect(windowMode(INTERVAL - 1, INTERVAL)).toBe('blackout')
  })
  it('leaves zeros inconclusive until several intervals have passed', () => {
    expect(windowMode(INTERVAL, INTERVAL)).toBe('measuring')
    expect(windowMode(INTERVAL * SETTLED_INTERVALS - 1, INTERVAL)).toBe('measuring')
  })
  it('settles once the window could have held several cycles', () => {
    expect(windowMode(INTERVAL * SETTLED_INTERVALS, INTERVAL)).toBe('settled')
  })
  // An unknown interval must never license a confident reading of a zero.
  it('never claims settled when the interval is unknown', () => {
    expect(windowMode(INTERVAL * 100, 0)).toBe('measuring')
  })
})

// THE rule the whole page rests on. Getting it backwards hides findings during
// exactly the incident the page exists for.
describe('the asymmetry rule: zeros are gated by the window, non-zeros are not', () => {
  it('reports an observed push error even in the blackout window', () => {
    const s = snap({
      window_ms: 1_000,
      counters: [c('psp_push_client_config_error_total', 3), c('psp_poll_total', 1)],
      gauges: [g('psp_push_sem_capacity', 8)],
    })
    const found = deriveFindings(s, INTERVAL)
    expect(found.map(f => f.id)).toContain('push_errors')
    expect(verdict(found, windowMode(s.metrics.window_ms, INTERVAL))).toBe('problems')
  })

  it('does NOT call a zero-cycle poll dead while the window is young', () => {
    const s = snap({ window_ms: INTERVAL, counters: [], gauges: [g('psp_push_sem_capacity', 8)] })
    expect(deriveFindings(s, INTERVAL).map(f => f.id)).not.toContain('poll_dead')
  })

  it('does call it dead once the window is settled', () => {
    const s = snap({
      window_ms: INTERVAL * SETTLED_INTERVALS,
      counters: [],
      gauges: [g('psp_push_sem_capacity', 8)],
    })
    expect(deriveFindings(s, INTERVAL).map(f => f.id)).toContain('poll_dead')
  })
})

describe('verdict', () => {
  it('is ok only when the window settled AND nothing was found', () => {
    expect(verdict([], 'settled')).toBe('ok')
  })
  // "Cannot tell" is never green.
  it('is never ok on a young window', () => {
    expect(verdict([], 'blackout')).toBe('blackout')
    expect(verdict([], 'measuring')).toBe('measuring')
  })
  it('lets findings outrank the window', () => {
    const f = [{ id: 'x', severity: 'error' as const, key: 'x' }]
    expect(verdict(f, 'blackout')).toBe('problems')
  })
})

describe('findings', () => {
  it('finds nothing on a healthy fleet', () => {
    expect(deriveFindings(snap(healthy()), INTERVAL)).toEqual([])
  })

  // Set once at construction, so 0 is not "a small pool" — the traffic service
  // was never built and every sibling metric is describing nothing.
  it('treats a zero push-semaphore capacity as broken, not low', () => {
    const s = snap({ ...healthy(), gauges: [g('psp_push_sem_capacity', 0)] })
    const f = deriveFindings(s, INTERVAL).find(x => x.id === 'push_capacity_zero')
    expect(f?.severity).toBe('critical')
  })

  it('ranks critical above error above warn', () => {
    const s = snap({
      window_ms: INTERVAL * 10,
      counters: [
        c('psp_poll_total', 5),
        c('psp_push_suppressed_total', 2),
        c('psp_push_client_config_error_total', 1),
      ],
      gauges: [g('psp_push_sem_capacity', 0)],
    })
    expect(deriveFindings(s, INTERVAL).map(f => f.severity)).toEqual(['critical', 'error', 'warn'])
  })

  // A cap nobody set cannot be un-enforced. Without a DB fact proving a cap
  // exists, this must not open a red banner on a fresh install.
  it('never raises an IP-cap enforcement finding from metrics alone', () => {
    const s = snap({
      ...healthy(),
      counters: [
        ...healthy().counters,
        c('psp_ip_limit_enforcement_total{state=not_installed}', 12),
      ],
    })
    expect(deriveFindings(s, INTERVAL).map(f => f.id)).not.toContain('ip_limit')
  })

  // All-idle with nobody online is the healthiest reading the detector can
  // produce, not a blind one, and no response to a flag has been built yet.
  it('never raises a geo finding', () => {
    const s = snap({
      ...healthy(),
      counters: [...healthy().counters, c('psp_geo_verdict_total{state=idle}', 25)],
    })
    expect(deriveFindings(s, INTERVAL).map(f => f.id)).not.toContain('geo_blind')
  })

  // Take() is not atomic across metrics, so a cycle in flight can split a pair.
  it('treats an invariant disagreement of one as noise', () => {
    const base = healthy()
    const s = snap({ ...base, histograms: [h('psp_poll_ms', 39)] })
    expect(deriveFindings(s, INTERVAL).map(f => f.id)).not.toContain('invariant_poll_ms')
  })

  it('reports a real invariant violation', () => {
    const base = healthy()
    const s = snap({ ...base, histograms: [h('psp_poll_ms', 12)] })
    expect(deriveFindings(s, INTERVAL).map(f => f.id)).toContain('invariant_poll_ms')
  })
})

describe('preconditions', () => {
  it('marks a zero-capacity semaphore broken regardless of the window', () => {
    const s = snap({ window_ms: 1_000, gauges: [g('psp_push_sem_capacity', 0)] })
    const p = derivePreconditions(s, INTERVAL).find(x => x.id === 'push_capacity')
    expect(p?.state).toBe('broken')
  })

  // Nothing attempted is not 0% — it is an absent denominator, and rendering it
  // as a rate invents a success rate out of no samples.
  it('says no_data rather than ok when nothing was attempted', () => {
    const s = snap({ window_ms: INTERVAL * 10, counters: [c('psp_poll_total', 9)], gauges: [g('psp_push_sem_capacity', 8)] })
    const p = derivePreconditions(s, INTERVAL).find(x => x.id === 'push_reaching')
    expect(p?.state).toBe('no_data')
  })

  it('holds judgement on a young window instead of reporting no_data', () => {
    const s = snap({ window_ms: 1_000, gauges: [g('psp_push_sem_capacity', 8)] })
    const p = derivePreconditions(s, INTERVAL).find(x => x.id === 'push_reaching')
    expect(p?.state).toBe('measuring')
  })

  it('marks live-IP coverage broken when any read was incomplete', () => {
    const s = snap({ ...healthy(), counters: [...healthy().counters, c('psp_live_ip_users_incomplete_total', 4)] })
    const p = derivePreconditions(s, INTERVAL).find(x => x.id === 'liveip_complete')
    expect(p?.state).toBe('broken')
  })
})

// A quantile interpolated from two samples is the max wearing a percentile's
// name; reporting it as p95 is a small lie the page exists to avoid.
describe('quantileUsable', () => {
  it('rejects too few samples', () => {
    expect(quantileUsable(h('x', 2))).toBe(false)
    expect(quantileUsable(undefined)).toBe(false)
  })
  it('accepts enough samples', () => {
    expect(quantileUsable(h('x', 10))).toBe(true)
  })
})

// The gap between uptime and window is the only way to tell a fresh boot from a
// deliberate re-measurement, and a zero reads differently under each.
describe('wasReset', () => {
  it('is false on a fresh boot', () => {
    expect(wasReset(snap({ window_ms: 90_000 }, 92_000))).toBe(false)
  })
  it('is true when the counters were zeroed without a restart', () => {
    expect(wasReset(snap({ window_ms: 60_000 }, 8 * 3600_000))).toBe(true)
  })
})

// Drift guard. Findings and preconditions are looked up by a key COMPUTED at
// runtime, so a new one added without locale entries renders as the raw key
// instead of failing to compile — and this repo has already shipped a missing
// en-US key that silently fell back to Chinese. Both bundles are checked, and
// the fixture below is built to emit every finding at once.
describe('locale coverage for computed keys', () => {
  // Every finding fires: capacity 0, no polls on a settled window, and one of
  // each observed non-zero, plus both invariant violations.
  const everything = snap({
    window_ms: INTERVAL * 20,
    counters: [
      c('psp_push_client_config_error_total', 1),
      c('psp_poll_error_total', 1),
      c('psp_lifecycle_sync_error_total', 1),
      c('psp_push_suppressed_total', 1),
      c('psp_push_sem_carryover_total', 1),
      c('psp_capability_gap_total{field=limitHwid}', 1),
      c('psp_live_ip_users_incomplete_total', 1),
      c('psp_push_client_config_total', 9),
      c('psp_poll_floor_push_enqueued_total', 0),
    ],
    gauges: [g('psp_push_sem_capacity', 0)],
    histograms: [],
  })

  it('emits every finding id from the fixture, so the check below is exhaustive', () => {
    const ids = deriveFindings(everything, INTERVAL).map(f => f.id).sort()
    expect(ids).toEqual([
      'capability_gaps', 'invariant_push_enqueue', 'lifecycle_errors', 'liveip_incomplete',
      'poll_dead', 'poll_errors', 'push_capacity_zero', 'push_carryover', 'push_errors',
      'push_suppressed',
    ])
  })

  for (const [lang, bundle] of Object.entries({ 'zh-CN': zh, 'en-US': en })) {
    it(`${lang} has a title and detail for every finding`, () => {
      const d = (bundle as Record<string, any>).diagnostics
      for (const f of deriveFindings(everything, INTERVAL)) {
        expect(d.findings[f.key]?.title, `${lang} findings.${f.key}.title`).toBeTruthy()
        expect(d.findings[f.key]?.detail, `${lang} findings.${f.key}.detail`).toBeTruthy()
      }
    })

    it(`${lang} has a label and help for every precondition`, () => {
      const d = (bundle as Record<string, any>).diagnostics
      for (const p of derivePreconditions(everything, INTERVAL)) {
        expect(d.precondition[p.id]?.label, `${lang} precondition.${p.id}.label`).toBeTruthy()
        expect(d.precondition[p.id]?.help, `${lang} precondition.${p.id}.help`).toBeTruthy()
      }
    })

    it(`${lang} has every verdict wording`, () => {
      const d = (bundle as Record<string, any>).diagnostics
      for (const v of ['problems', 'blackout', 'measuring', 'ok']) {
        expect(d.verdict[v], `${lang} verdict.${v}`).toBeTruthy()
      }
    })
  }
})
