import { describe, expect, it } from 'vitest'
import type { DiagnosticsSnapshot, MetricsSnapshot } from '@/api/diagnostics'
import {
  SETTLED_INTERVALS,
  deriveFindings,
  deriveSelfChecks,
  effectivePollIntervalMs,
  expectedPollsMin,
  derivePreconditions,
  FINDING_ORDER,
  panelFacts,
  quantileUsable,
  verdict,
  wasReset,
  windowMode,
  type Finding,
  type PanelFacts,
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
    const f: Finding[] = [{ id: 'x', severity: 'error', key: 'x', card: 'poll', series: [] }]
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

  it('ranks critical above error above warn above notice', () => {
    const s = snap({
      window_ms: INTERVAL * 10,
      counters: [
        c('psp_poll_total', 5),
        c('psp_capability_gap_total{capability=client.iplimit}', 4),
        c('psp_push_suppressed_total', 2),
        c('psp_poll_error_total', 1),
      ],
      gauges: [g('psp_push_sem_capacity', 0)],
    })
    expect(deriveFindings(s, INTERVAL).map(f => f.severity)).toEqual(['critical', 'error', 'warn', 'notice'])
  })

  // Within one severity the order is the catalogue's, so the page reads the
  // same way on every refresh instead of reshuffling with the counts.
  it('keeps a fixed order within one severity', () => {
    const s = snap({
      ...healthy(),
      counters: [
        ...healthy().counters,
        c('psp_live_ip_users_incomplete_total', 9),
        c('psp_push_sem_carryover_total', 1),
        c('psp_push_client_config_error_total', 1),
        c('psp_lifecycle_sync_error_total', 1),
        c('psp_poll_error_total', 1),
      ],
    })
    expect(deriveFindings(s, INTERVAL).map(f => f.id)).toEqual([
      'poll_errors', 'lifecycle_errors', 'push_errors', 'push_backlog', 'liveip_incomplete',
    ])
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

})

// The operator-facing severities. A sustained condition is a notice and never
// colours the page; a quota safety refresh failure is amber because PSP still
// suspends at the next poll while it is running; a status write failure stays
// red because enable, expiry and quota all travel that path.
describe('severity of each finding', () => {
  const sev = (counters: ReturnType<typeof c>[]) =>
    Object.fromEntries(deriveFindings(snap({ ...healthy(), counters: [...healthy().counters, ...counters] }), INTERVAL)
      .map(f => [f.id, f.severity]))

  it('makes a capability gap a notice', () => {
    expect(sev([c('psp_capability_gap_total{capability=client.iplimit}', 3)]).capability_gaps).toBe('notice')
  })
  it('makes a quota safety refresh failure a warning', () => {
    expect(sev([c('psp_push_client_config_error_total', 3)]).push_errors).toBe('warn')
  })
  it('keeps a status write failure an error', () => {
    expect(sev([c('psp_lifecycle_sync_error_total', 3)]).lifecycle_errors).toBe('error')
  })
  it('keeps an incomplete live-IP read a warning', () => {
    expect(sev([c('psp_live_ip_users_incomplete_total', 3)]).liveip_incomplete).toBe('warn')
  })
})

// Carryover counts polls that skipped their refreshes; suppressed counts the
// refreshes skipped. They describe one event, and listing both made one
// backlog read as two problems.
describe('push_backlog', () => {
  const backlog = (carryover: number, suppressed: number) => {
    const counters = [...healthy().counters]
    if (carryover) counters.push(c('psp_push_sem_carryover_total', carryover))
    if (suppressed) counters.push(c('psp_push_suppressed_total', suppressed))
    return deriveFindings(snap({ ...healthy(), counters }), INTERVAL)
  }

  it.each([
    ['only carryover', 2, 0],
    ['only suppressed', 0, 7],
    ['both', 2, 7],
  ])('raises exactly one finding with %s', (_label, carryover, suppressed) => {
    const found = backlog(carryover, suppressed)
    expect(found.filter(f => f.id === 'push_backlog')).toHaveLength(1)
    expect(found.find(f => f.id === 'push_backlog')?.values).toEqual({ cycles: carryover, suppressed })
    expect(found.map(f => f.id)).not.toContain('push_suppressed')
    expect(found.map(f => f.id)).not.toContain('push_carryover')
  })
})

// Self-checks are about the statistics, not the fleet: a mismatch says the
// counters disagree, not that anything is broken, so they no longer appear as
// findings and are listed whether they pass or not.
describe('deriveSelfChecks', () => {
  it('keeps self-checks out of the findings', () => {
    const s = snap({ ...healthy(), histograms: [h('psp_poll_ms', 12)] })
    expect(deriveFindings(s, INTERVAL).map(f => f.id).filter(id => id.startsWith('invariant'))).toEqual([])
  })

  it('always returns both checks, each with a verdict', () => {
    const checks = deriveSelfChecks(healthy())
    expect(checks.map(x => x.id)).toEqual(['poll_ms', 'push_enqueue'])
    expect(checks.every(x => typeof x.pass === 'boolean')).toBe(true)
  })

  // Take() is not atomic across metrics, so a cycle in flight can split a pair
  // by one; two is a real disagreement.
  it.each([
    [39, true], [41, true], [38, false], [42, false],
  ])('poll_ms: %i records against 40 polls passes=%s', (records, pass) => {
    const check = deriveSelfChecks({ ...healthy(), histograms: [h('psp_poll_ms', records)] })[0]
    expect(check).toEqual({ id: 'poll_ms', pass, values: { observed: records, expected: 40 } })
  })

  // Every started refresh was queued first, so only MORE started than queued
  // is a disagreement; fewer is a refresh still waiting.
  it.each([
    [13, true], [14, false], [5, true],
  ])('push_enqueue: %i started against 12 queued passes=%s', (started, pass) => {
    const m = metrics({
      counters: [c('psp_poll_total', 40), c('psp_push_client_config_total', started), c('psp_poll_floor_push_enqueued_total', 12)],
    })
    expect(deriveSelfChecks(m)[1]).toEqual({ id: 'push_enqueue', pass, values: { started, enqueued: 12 } })
  })
})

// Every error count comes with what it is a count OF.
describe('denominators', () => {
  const find = (counters: ReturnType<typeof c>[], id: string) =>
    deriveFindings(snap({ ...healthy(), counters: [...healthy().counters, ...counters] }), INTERVAL)
      .find(f => f.id === id)

  it('gives poll errors the number of polls', () => {
    expect(find([c('psp_poll_error_total', 2)], 'poll_errors')?.values).toEqual({ errors: 2, polls: 40 })
  })

  it('gives status write failures the number of checks and a ratio', () => {
    const f = find([c('psp_lifecycle_sync_error_total', 3)], 'lifecycle_errors')
    expect(f?.values).toEqual({ errors: 3, checks: 30 })
    expect(f?.ratio).toEqual({ n: 3, d: 30 })
  })

  it('gives quota safety refresh failures the number attempted and a ratio', () => {
    const f = find([c('psp_push_client_config_error_total', 3)], 'push_errors')
    expect(f?.values).toEqual({ errors: 3, attempted: 12 })
    expect(f?.ratio).toEqual({ n: 3, d: 12 })
  })

  // push_errors = 0 rules out only the refresh path. It does not rule out
  // anything else, so the explanation is offered only when it is true.
  it('explains that status failures are not refresh failures only when no refresh failed', () => {
    expect(find([c('psp_lifecycle_sync_error_total', 3)], 'lifecycle_errors')?.variants).toEqual(['origin'])
    expect(find([
      c('psp_lifecycle_sync_error_total', 3), c('psp_push_client_config_error_total', 1),
    ], 'lifecycle_errors')?.variants ?? []).toEqual([])
  })
})

describe('lifecycle_errors breakdown', () => {
  it('lists each step and each panel kind that failed, largest first', () => {
    const f = deriveFindings(snap({
      ...healthy(),
      counters: [
        ...healthy().counters,
        c('psp_lifecycle_sync_error_total', 5),
        c('psp_lifecycle_sync_error_stage_total{stage=update}', 1),
        c('psp_lifecycle_sync_error_stage_total{stage=confirm_read}', 4),
        // A reset zeroes a child but never removes it.
        c('psp_lifecycle_sync_error_stage_total{stage=pool_get}', 0),
        c('psp_lifecycle_sync_error_panel_kind_total{kind=psp}', 4),
        c('psp_lifecycle_sync_error_panel_kind_total{kind=3xui}', 1),
      ],
    }), INTERVAL).find(x => x.id === 'lifecycle_errors')
    expect(f?.breakdown).toEqual({
      stage: [{ value: 'confirm_read', count: 4 }, { value: 'update', count: 1 }],
      kind: [{ value: 'psp', count: 4 }, { value: '3xui', count: 1 }],
    })
  })

  it('carries no breakdown when the server recorded none', () => {
    const f = deriveFindings(snap({
      ...healthy(), counters: [...healthy().counters, c('psp_lifecycle_sync_error_total', 2)],
    }), INTERVAL).find(x => x.id === 'lifecycle_errors')
    expect(f?.breakdown).toBeUndefined()
  })
})

// The live-IP read is incomplete for two different reasons that one counter
// cannot separate: an S-UI panel never reports live IPs, and a panel that
// should have answered did not. The S-UI explanation is added only when the
// server list proves an S-UI panel exists, and the finding is never softened
// for it, because the same count may also hold a real outage.
describe('liveip_incomplete and the panel facts', () => {
  const incomplete = (facts?: PanelFacts) => deriveFindings(snap({
    ...healthy(), counters: [...healthy().counters, c('psp_live_ip_users_incomplete_total', 50)],
  }), INTERVAL, facts).find(f => f.id === 'liveip_incomplete')

  it('stays a warning with the S-UI note when an S-UI panel is connected', () => {
    const f = incomplete(panelFacts([{ panel_type: 'sui' }, { panel_type: '3xui' }], 2))
    expect(f?.severity).toBe('warn')
    expect(f?.variants).toEqual(['sui'])
  })

  it('adds no note when the server list was incomplete', () => {
    expect(incomplete(panelFacts([{ panel_type: 'sui' }], 3))?.variants ?? []).toEqual([])
  })

  it('adds no note without an S-UI panel, or without any facts at all', () => {
    expect(incomplete(panelFacts([{ panel_type: '3xui' }], 1))?.variants ?? []).toEqual([])
    expect(incomplete()?.variants ?? []).toEqual([])
  })
})

describe('panelFacts', () => {
  it('is complete only when the page held every panel', () => {
    expect(panelFacts([{ panel_type: 'psp' }], 1).complete).toBe(true)
    expect(panelFacts([{ panel_type: 'psp' }], 2).complete).toBe(false)
    expect(panelFacts(undefined, undefined).complete).toBe(false)
  })

  // A row without a kind predates the adapter layer and is 3X-UI everywhere
  // else in PSP.
  it('reads a missing kind as 3X-UI', () => {
    expect([...panelFacts([{}], 1).types]).toEqual(['3xui'])
  })
})

// The fixture the old page carried used {field=limitHwid}, a label the server
// never emits; the real children are keyed by capability.
describe('capability_gaps', () => {
  it('counts the IP and the device limit separately from the real labels', () => {
    const f = deriveFindings(snap({
      ...healthy(),
      counters: [
        ...healthy().counters,
        c('psp_capability_gap_total{capability=client.iplimit}', 3),
        c('psp_capability_gap_total{capability=client.devicelimit}', 40),
      ],
    }), INTERVAL).find(x => x.id === 'capability_gaps')
    expect(f?.values).toEqual({ ip: 3, device: 40 })
  })
})

// What a finding carries beyond its text: the card it belongs to, the series
// the page watches for growth while it is open, and where the operator goes
// next. Pinned as a table so a finding cannot lose its link silently.
describe('finding metadata', () => {
  const all = (): Finding[] => deriveFindings(snap({
    ...healthy(),
    window_ms: INTERVAL * 20,
    counters: [
      ...healthy().counters,
      c('psp_poll_error_total', 1),
      c('psp_lifecycle_sync_error_total', 1),
      c('psp_push_client_config_error_total', 1),
      c('psp_push_sem_carryover_total', 1),
      c('psp_live_ip_users_incomplete_total', 1),
      c('psp_capability_gap_total{capability=client.iplimit}', 1),
      c('psp_connection_history_write_errors_total', 1),
      c('psp_flag_record_write_errors_total', 1),
      c('psp_geo_auto_suspension_total{outcome=lift_error}', 1),
      c('psp_node_sync_refused_total{reason=report_invalid}', 1),
      c('psp_sso_claim_silent_total{kind=role}', 1),
    ],
    gauges: [g('psp_push_sem_capacity', 0)],
  }), INTERVAL)

  it.each([
    ['push_capacity_zero', 'poll', [], undefined],
    ['poll_errors', 'poll', ['psp_poll_error_total'], undefined],
    ['lifecycle_errors', 'lifecycle', ['psp_lifecycle_sync_error_total'], 'sync_tasks'],
    ['push_errors', 'floor', ['psp_push_client_config_error_total'], 'servers'],
    ['push_backlog', 'floor', ['psp_push_sem_carryover_total'], 'servers'],
    ['liveip_incomplete', 'liveip', ['psp_live_ip_users_incomplete_total'], 'servers'],
    ['capability_gaps', 'lifecycle', ['psp_capability_gap_total'], 'servers'],
    ['history_write_errors', 'liveip', ['psp_connection_history_write_errors_total'], undefined],
    ['flag_write_errors', 'liveip', ['psp_flag_record_write_errors_total'], undefined],
    ['geo_auto_errors', 'liveip', [
      'psp_geo_auto_suspension_total{outcome=suspend_error}',
      'psp_geo_auto_suspension_total{outcome=lift_error}',
    ], 'risk'],
    ['node_sync_refused', 'node', ['psp_node_sync_refused_total'], 'servers'],
    ['sso_claim_silent', 'sso', ['psp_sso_claim_silent_total'], 'settings'],
  ])('%s sits on %s, watches %j and links to %s', (id, card, series, link) => {
    const f = all().find(x => x.id === id)
    expect(f, id).toBeDefined()
    expect({ card: f!.card, series: f!.series, link: f!.link }).toEqual({ card, series, link })
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

// poll_behind: the poll ran, but fewer times than the interval allows. Built
// to never fire on a healthy loop, so every input is read conservatively.
describe('poll_behind', () => {
  const POLL = 120_000
  // A window that holds exactly `expected` polls once the 60 s start-up slack
  // is taken off, with the uptime equal to it (a fresh boot, no reset).
  const windowFor = (expected: number) => expected * POLL + 60_000
  const behind = (polls: number, over: { window?: number; uptime?: number; gauge?: ReturnType<typeof g> | null } = {}) => {
    const window = over.window ?? windowFor(100)
    const s = snap({
      window_ms: window,
      counters: [c('psp_poll_total', polls)],
      gauges: [
        g('psp_push_sem_capacity', 8),
        ...(over.gauge === null ? [] : [over.gauge ?? g('psp_poll_interval_ms', POLL)]),
      ],
    }, over.uptime ?? window)
    return deriveFindings(s, POLL).find(f => f.id === 'poll_behind')
  }

  it('stays silent until the window is settled', () => {
    expect(behind(0, { window: POLL * 2 })).toBeUndefined()
    expect(behind(1, { window: POLL * 2 })).toBeUndefined()
  })

  it('leaves zero polls to poll_dead', () => {
    const s = snap({
      window_ms: windowFor(100), counters: [c('psp_poll_total', 0)],
      gauges: [g('psp_push_sem_capacity', 8), g('psp_poll_interval_ms', POLL)],
    })
    const ids = deriveFindings(s, POLL).map(f => f.id)
    expect(ids).toContain('poll_dead')
    expect(ids).not.toContain('poll_behind')
  })

  // Tolerance is max(2, 5%): 5 of 100 expected, 2 of 20.
  it('fires one poll past the 5% tolerance and not at it', () => {
    expect(behind(95)).toBeUndefined()
    expect(behind(94)?.values).toEqual({ polls: 94, expected: 100, missing: 6 })
  })

  it('never tolerates fewer than two missing polls', () => {
    expect(behind(18, { window: windowFor(20) })).toBeUndefined()
    expect(behind(17, { window: windowFor(20) })?.values).toEqual({ polls: 17, expected: 20, missing: 3 })
  })

  it('carries severity, card and a link to the settings', () => {
    const f = behind(50)
    expect(f).toMatchObject({ severity: 'warn', card: 'poll', link: 'settings', series: [] })
  })

  // The peak is the longest interval used in the window, so a shortened
  // interval cannot make the polls that ran at the old, longer one look
  // missing — and a lengthened one only lowers the expectation.
  it('judges against the longest interval used, so changing it never misfires', () => {
    const window = 2 * 3600_000
    const shortened = g('psp_poll_interval_ms', 60_000, 300_000)
    expect(behind(24, { window, gauge: shortened })).toBeUndefined()
    const lengthened = g('psp_poll_interval_ms', 300_000, 300_000)
    expect(behind(60, { window, gauge: lengthened })).toBeUndefined()
  })

  it('stays silent without the interval gauge', () => {
    expect(behind(10, { gauge: null })).toBeUndefined()
    expect(behind(10, { gauge: g('psp_poll_interval_ms', 0) })).toBeUndefined()
  })

  // The registry opens its window at process start; the loop starts after the
  // schema migration. That gap (window − uptime) is added to the slack.
  it('allows for the time the process spent starting up', () => {
    const window = 12_360_000 // 100 polls + 60 s + 300 s of start-up
    expect(behind(95, { window, uptime: window - 300_000 })).toBeUndefined()
    expect(behind(95, { window, uptime: window })?.values).toEqual({ polls: 95, expected: 102, missing: 7 })
  })

  // After a reset the window is shorter than the uptime; the loop was already
  // running, so only the 60 s slack applies.
  it('uses only the fixed slack after a reset', () => {
    expect(behind(94, { window: windowFor(100), uptime: 10 * 3600_000 })?.values)
      .toEqual({ polls: 94, expected: 100, missing: 6 })
  })
})

describe('expectedPollsMin', () => {
  it('is the conservative lower bound the poll card shows', () => {
    const s = snap({ window_ms: 100 * 120_000 + 60_000, gauges: [g('psp_poll_interval_ms', 120_000)] })
    expect(expectedPollsMin(s)).toBe(100)
  })
  it('is unknown without a usable interval gauge', () => {
    expect(expectedPollsMin(snap({ gauges: [] }))).toBeNull()
    expect(expectedPollsMin(snap({ gauges: [g('psp_poll_interval_ms', 0)] }))).toBeNull()
  })
})

// Failures that nothing else on PSP's admin surface reports: each has only a
// Warn log behind it otherwise.
describe('findings nothing else surfaces', () => {
  const find = (counters: ReturnType<typeof c>[], id: string) =>
    deriveFindings(snap({ ...healthy(), counters: [...healthy().counters, ...counters] }), INTERVAL)
      .find(f => f.id === id)

  it('reports connection history write failures', () => {
    expect(find([c('psp_connection_history_write_errors_total', 3)], 'history_write_errors'))
      .toMatchObject({ severity: 'warn', values: { errors: 3 } })
  })

  it('reports flag record write failures', () => {
    expect(find([c('psp_flag_record_write_errors_total', 2)], 'flag_write_errors'))
      .toMatchObject({ severity: 'warn', values: { errors: 2 } })
  })

  it('reports automatic location suspensions and lifts that failed', () => {
    expect(find([
      c('psp_geo_auto_suspension_total{outcome=suspend_error}', 2),
      c('psp_geo_auto_suspension_total{outcome=lift_error}', 1),
    ], 'geo_auto_errors')).toMatchObject({ severity: 'warn', values: { suspend: 2, lift: 1 } })
  })

  // The bell already counts what was suspended; only what failed is new here.
  it('ignores every outcome of the automatic suspension that is not a failure', () => {
    expect(find([
      c('psp_geo_auto_suspension_total{outcome=suspended}', 9),
      c('psp_geo_auto_suspension_total{outcome=lifted_admin}', 3),
      c('psp_geo_auto_suspension_total{outcome=deferred}', 4),
    ], 'geo_auto_errors')).toBeUndefined()
  })

  it('splits refused native-node syncs into version and content refusals', () => {
    expect(find([
      c('psp_node_sync_refused_total{reason=protocol_generation}', 5),
      c('psp_node_sync_refused_total{reason=report_invalid}', 2),
    ], 'node_sync_refused')).toMatchObject({ severity: 'warn', values: { count: 7, proto: 5, invalid: 2 } })
  })

  it('splits single sign-ins without claims into role and group', () => {
    expect(find([
      c('psp_sso_claim_silent_total{kind=role}', 1),
      c('psp_sso_claim_silent_total{kind=group}', 4),
    ], 'sso_claim_silent')).toMatchObject({ severity: 'warn', values: { count: 5, role: 1, group: 4 } })
  })

  it('stays silent while each of them is zero', () => {
    const ids = deriveFindings(snap({
      ...healthy(),
      counters: [
        ...healthy().counters,
        c('psp_connection_history_write_errors_total', 0),
        c('psp_flag_record_write_errors_total', 0),
        c('psp_node_sync_refused_total{reason=report_invalid}', 0),
      ],
    }), INTERVAL).map(f => f.id)
    expect(ids).toEqual([])
  })
})

// Every finding the page can raise, from one fixture, so a new finding cannot
// be added without this list noticing. The locale half of this check (every
// part of every finding in both bundles) lands with the copy.
describe('finding coverage', () => {
  const everything = snap({
    window_ms: INTERVAL * 20,
    counters: [
      c('psp_push_client_config_error_total', 1),
      c('psp_poll_error_total', 1),
      c('psp_lifecycle_sync_error_total', 1),
      c('psp_push_suppressed_total', 1),
      c('psp_push_sem_carryover_total', 1),
      c('psp_capability_gap_total{capability=client.devicelimit}', 1),
      c('psp_live_ip_users_incomplete_total', 1),
      c('psp_push_client_config_total', 9),
      c('psp_poll_floor_push_enqueued_total', 0),
      c('psp_connection_history_write_errors_total', 1),
      c('psp_flag_record_write_errors_total', 1),
      c('psp_geo_auto_suspension_total{outcome=suspend_error}', 1),
      c('psp_node_sync_refused_total{reason=protocol_generation}', 1),
      c('psp_sso_claim_silent_total{kind=group}', 1),
    ],
    gauges: [g('psp_push_sem_capacity', 0)],
    histograms: [],
  })
  // poll_dead and poll_behind cannot fire on one reading (zero polls versus
  // too few), so the second one comes from its own snapshot.
  const behind = snap({
    window_ms: 100 * 120_000 + 60_000,
    counters: [c('psp_poll_total', 10)],
    gauges: [g('psp_push_sem_capacity', 8), g('psp_poll_interval_ms', 120_000)],
  })
  const allFindings = () => [...deriveFindings(everything, INTERVAL), ...deriveFindings(behind, 120_000)]

  it('emits every finding id across the fixtures', () => {
    const ids = [...new Set(allFindings().map(f => f.id))].sort()
    expect(ids).toEqual([...FINDING_ORDER].sort())
  })

  it.todo('has a title, impact and action for every finding in both bundles')
})

// The live check that this test was written from: the settings row said one
// minute while the traffic loop's ticker was still on five, so a four-minute
// window looked settled, psp_poll_total was legitimately 0, and the page
// reported a critical "the traffic poll is dead" about a healthy process. The
// gate was right; its input was a value that had not taken effect yet.
describe('effectivePollIntervalMs', () => {
  it('believes the loop over the settings row when they disagree', () => {
    const m = metrics({ gauges: [g('psp_poll_interval_ms', 5 * 60_000)] })
    expect(effectivePollIntervalMs(m, 60_000)).toBe(5 * 60_000)
  })

  it('does not call a window settled while the loop is still on the old cadence', () => {
    // 4 min elapsed, settings say 1 min (=> settled, poll must be dead),
    // loop says 5 min (=> not even one cycle is due yet).
    const m = metrics({
      window_ms: 4 * 60_000,
      counters: [c('psp_poll_total', 0)],
      gauges: [g('psp_push_sem_capacity', 8), g('psp_poll_interval_ms', 5 * 60_000)],
    })
    expect(windowMode(m.window_ms, 60_000)).toBe('settled')
    expect(windowMode(m.window_ms, effectivePollIntervalMs(m, 60_000))).toBe('blackout')

    const s: DiagnosticsSnapshot = { ...snap(), metrics: m }
    expect(deriveFindings(s, 60_000).map(f => f.id)).toContain('poll_dead')
    expect(deriveFindings(s, effectivePollIntervalMs(m, 60_000)).map(f => f.id))
      .not.toContain('poll_dead')
  })

  it('falls back to the settings row when the server is too old to publish the gauge', () => {
    expect(effectivePollIntervalMs(metrics(), 60_000)).toBe(60_000)
  })

  it('treats a zero or missing gauge as no answer rather than as zero', () => {
    // A zero interval would make windowMode refuse to settle forever, which
    // hides real findings instead of merely delaying them.
    const m = metrics({ gauges: [g('psp_poll_interval_ms', 0)] })
    expect(effectivePollIntervalMs(m, 60_000)).toBe(60_000)
  })
})
