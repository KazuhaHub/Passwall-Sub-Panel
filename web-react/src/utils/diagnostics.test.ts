import { describe, expect, it } from 'vitest'
import zh from '@/locales/zh-CN/admin.json'
import en from '@/locales/en-US/admin.json'
import zhNav from '@/locales/zh-CN/nav.json'
import enNav from '@/locales/en-US/nav.json'
import type { DiagnosticsSnapshot, HistogramSnapshot, MetricsSnapshot } from '@/api/diagnostics'
import { flatten, type Nested } from '@/i18n/options'
import {
  FINDING_LOGS,
  FINDING_PARTS,
  SETTLED_INTERVALS,
  cardSentenceKey,
  deriveCards,
  deriveFindings,
  deriveSelfChecks,
  effectivePollIntervalMs,
  expectedPollsMin,
  derivePreconditions,
  FINDING_ORDER,
  breakdownInterpolation,
  findingInterpolation,
  findingParts,
  formatBreakdownList,
  formatBound,
  formatBytes,
  formatDuration,
  formatLatency,
  formatPct,
  formatRate,
  lifecycleCardBreakdown,
  lifecycleErrorBreakdown,
  pageVerdict,
  panelFacts,
  quantileReading,
  quantileUsable,
  nextSessionBase,
  sessionDelta,
  sessionLineKey,
  wasReset,
  windowMode,
  bucketRows,
  filterRawFamilies,
  nodeSaveFailures,
  panelOpRows,
  rawFamilies,
  rawSummary,
  stageGroups,
  writeReasonGroups,
  type CardState,
  type Finding,
  type PanelFacts,
} from './diagnostics'
import {
  CARD_ORDER,
  CARD_LINK,
  FAMILY_CATALOG,
  LIFECYCLE_ERROR_KINDS,
  LIFECYCLE_ERROR_STAGES,
  LINK_TARGET,
  labelKey,
  type CardId,
} from './diagnosticsCatalog'

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
    const cards = deriveCards(s, INTERVAL, found)
    expect(pageVerdict(found, cards, s.metrics.window_ms, INTERVAL).tone).toBe('attention')
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
      stages: [{ value: 'confirm_read', count: 4 }, { value: 'update', count: 1 }],
      kinds: [{ value: 'psp', count: 4 }, { value: '3xui', count: 1 }],
    })
  })

  it('carries no breakdown when the server recorded none', () => {
    const f = deriveFindings(snap({
      ...healthy(), counters: [...healthy().counters, c('psp_lifecycle_sync_error_total', 2)],
    }), INTERVAL).find(x => x.id === 'lifecycle_errors')
    expect(f?.breakdown).toBeUndefined()
  })

  // The registry reads each counter on its own, so a reading taken in the
  // instant between a failure's step child and its kind child being counted
  // can hold one without the other. The sentence names both, and printing
  // "by panel type: ." for a minute says something false.
  it('carries no breakdown while one of the two is still empty', () => {
    const f = deriveFindings(snap({
      ...healthy(),
      counters: [
        ...healthy().counters,
        c('psp_lifecycle_sync_error_total', 1),
        c('psp_lifecycle_sync_error_stage_total{stage=update}', 1),
      ],
    }), INTERVAL).find(x => x.id === 'lifecycle_errors')
    expect(f?.breakdown).toBeUndefined()
  })
})

// The finding and the user status sync card read the breakdowns through one
// function, so the two can never list different steps for the same reading.
describe('lifecycleErrorBreakdown', () => {
  it('reads both breakdowns, largest first, without the zeroed children a clear leaves', () => {
    const m = metrics({
      counters: [
        c('psp_lifecycle_sync_error_total', 6),
        c('psp_lifecycle_sync_error_stage_total{stage=confirm_mismatch}', 0),
        c('psp_lifecycle_sync_error_stage_total{stage=pool_get}', 2),
        c('psp_lifecycle_sync_error_stage_total{stage=confirm_read}', 4),
        c('psp_lifecycle_sync_error_panel_kind_total{kind=unknown}', 2),
        c('psp_lifecycle_sync_error_panel_kind_total{kind=psp}', 4),
        c('psp_lifecycle_sync_error_panel_kind_total{kind=sui}', 0),
      ],
    })
    expect(lifecycleErrorBreakdown(m)).toEqual({
      stages: [{ value: 'confirm_read', count: 4 }, { value: 'pool_get', count: 2 }],
      kinds: [{ value: 'psp', count: 4 }, { value: 'unknown', count: 2 }],
    })
  })

  it('is empty for a server that predates the breakdown', () => {
    expect(lifecycleErrorBreakdown(metrics({ counters: [c('psp_lifecycle_sync_error_total', 3)] })))
      .toEqual({ stages: [], kinds: [] })
  })
})

// The user status sync card shows the same two breakdowns as the finding, one
// line each, so where the failures happen is visible on the card without
// opening the raw metrics. Each line stands alone: an empty list is left out
// rather than printed as "by step: ".
describe('lifecycleCardBreakdown', () => {
  const failures = (...extra: ReturnType<typeof c>[]) => metrics({
    counters: [c('psp_lifecycle_sync_error_total', 5), ...extra],
  })

  it('gives one line per breakdown, the step first, each with its label group', () => {
    const m = failures(
      c('psp_lifecycle_sync_error_stage_total{stage=update}', 1),
      c('psp_lifecycle_sync_error_stage_total{stage=confirm_read}', 4),
      c('psp_lifecycle_sync_error_panel_kind_total{kind=psp}', 4),
      c('psp_lifecycle_sync_error_panel_kind_total{kind=3xui}', 1),
    )
    expect(lifecycleCardBreakdown(m)).toEqual([
      {
        key: 'cards.lifecycle.errors_by_stage', group: 'lifecycle_stage',
        items: [{ value: 'confirm_read', count: 4 }, { value: 'update', count: 1 }],
      },
      {
        key: 'cards.lifecycle.errors_by_kind', group: 'panel_kind',
        items: [{ value: 'psp', count: 4 }, { value: '3xui', count: 1 }],
      },
    ])
  })

  it('leaves out a breakdown with nothing in it', () => {
    const m = failures(c('psp_lifecycle_sync_error_stage_total{stage=update}', 5))
    expect(lifecycleCardBreakdown(m).map(line => line.key)).toEqual(['cards.lifecycle.errors_by_stage'])
  })

  it('has nothing to show after a clear, or from a server without the breakdown', () => {
    expect(lifecycleCardBreakdown(failures())).toEqual([])
    expect(lifecycleCardBreakdown(metrics({
      counters: [
        c('psp_lifecycle_sync_error_stage_total{stage=update}', 0),
        c('psp_lifecycle_sync_error_panel_kind_total{kind=sui}', 0),
      ],
    }))).toEqual([])
  })
})

// One list format for the card lines and the finding sentence alike.
describe('formatBreakdownList', () => {
  it('writes each item as label and count, joined by the middle dot the cards use', () => {
    const label = (group: string, value: string) => `${group}:${value}`
    expect(formatBreakdownList(
      [{ value: 'psp', count: 5 }, { value: 'sui', count: 2 }], 'panel_kind', label, n => `#${n}`,
    )).toBe('panel_kind:psp #5 · panel_kind:sui #2')
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

  // Findings are looked up by a key COMPUTED at runtime, so one added without
  // copy renders as a raw key path instead of failing to compile — and this
  // repo has already shipped an en-US key that silently fell back to Chinese.
  for (const [lang, bundle] of BUNDLES) {
    it(`${lang} has a title, impact and action, and every extra part, for every finding`, () => {
      for (const id of FINDING_ORDER) {
        for (const part of ['title', 'impact', 'action', ...FINDING_PARTS[id], ...FINDING_LOGS[id]]) {
          expect(bundle.has(`diagnostics.findings.${id}.${part}`), `${lang} findings.${id}.${part}`).toBe(true)
        }
      }
    })
  }
})

// --- copy lookup ---------------------------------------------------------

const BUNDLES: Array<[string, Set<string>]> = [
  ['zh-CN', new Set(Object.keys(flatten(zh as Nested)))],
  ['en-US', new Set(Object.keys(flatten(en as Nested)))],
]

/** A t() over one real bundle, enough to run the formatters on real copy. */
function tFor(bundle: unknown): (key: string, opts?: Record<string, unknown>) => string {
  const flat = flatten(bundle as Nested)
  return (key, opts = {}) => {
    const k = key.replace(/^admin:/, '')
    const raw = flat[k]
    if (typeof raw !== 'string') throw new Error(`missing ${key}`)
    return raw.replace(/\{\{(\w+)\}\}/g, (_, name: string) => String(opts[name]))
  }
}
const tZh = tFor(zh)
const tEn = tFor(en)

// --- cards ---------------------------------------------------------------

const cardStates = (s: DiagnosticsSnapshot, interval = INTERVAL, facts?: PanelFacts) =>
  Object.fromEntries(deriveCards(s, interval, deriveFindings(s, interval, facts), facts).map(x => [x.id, x.state]))

describe('deriveCards', () => {
  it('returns the seven cards in page order', () => {
    expect(deriveCards(snap(healthy()), INTERVAL, []).map(x => x.id)).toEqual([...CARD_ORDER])
  })

  // "Running" says the driver moved, nothing more; a card with nothing to do
  // on a settled window is idle, which needs its own sentence.
  it('reads each card from its own driver on a settled window', () => {
    const s = snap({
      ...healthy(),
      counters: [...healthy().counters, c('psp_panel_op_total{op=GetClient}', 5)],
    })
    expect(cardStates(s)).toEqual({
      poll: 'ok', lifecycle: 'ok', floor: 'ok', panel_api: 'ok', liveip: 'idle', node: 'idle', sso: 'none',
    })
  })

  it('colours a card by its worst finding, and a notice not at all', () => {
    const s = snap({
      ...healthy(),
      counters: [
        ...healthy().counters,
        c('psp_lifecycle_sync_error_total', 1),
        c('psp_push_client_config_error_total', 1),
        c('psp_capability_gap_total{capability=client.iplimit}', 1),
      ],
    })
    const cards = deriveCards(s, INTERVAL, deriveFindings(s, INTERVAL))
    expect(cards.find(x => x.id === 'lifecycle')).toMatchObject({ state: 'failing', notices: true })
    expect(cards.find(x => x.id === 'floor')).toMatchObject({ state: 'attention', notices: false })
  })

  it('leaves a card with only a notice in its activity state', () => {
    const s = snap({
      ...healthy(),
      counters: [...healthy().counters, c('psp_capability_gap_total{capability=client.iplimit}', 1)],
    })
    const card = deriveCards(s, INTERVAL, deriveFindings(s, INTERVAL)).find(x => x.id === 'lifecycle')
    expect(card).toMatchObject({ state: 'ok', notices: true })
  })

  // Nothing downstream of a dead poll can be judged, so those cards say so
  // rather than "idle" — but only while they have no fact of their own, and the
  // status sync, which admin actions and repairs also drive, is not affected.
  it.each([
    ['a dead poll', { counters: [c('psp_lifecycle_sync_total', 0)], gauges: [g('psp_push_sem_capacity', 8)] }],
    ['a zero capacity', { ...healthy(), gauges: [g('psp_push_sem_capacity', 0)] }],
  ])('marks the refresh and live-IP cards undecidable after %s', (_label, m) => {
    const states = cardStates(snap({ window_ms: INTERVAL * 10, ...m }))
    expect(states.floor).toBe('inhibited')
    expect(states.liveip).toBe('inhibited')
    expect(states.lifecycle).not.toBe('inhibited')
  })

  it('still shows an observed failure on an inhibited card', () => {
    const s = snap({
      window_ms: INTERVAL * 10,
      counters: [c('psp_live_ip_users_incomplete_total', 3)],
      gauges: [g('psp_push_sem_capacity', 8)],
    })
    expect(cardStates(s).liveip).toBe('attention')
    expect(cardStates(s).floor).toBe('inhibited')
  })

  // Not in use is proven from the server list, never inferred from a zero.
  it('says a card is not in use only from a complete list with no such panel and no activity', () => {
    const quiet = snap(healthy())
    const onlySui = panelFacts([{ panel_type: 'sui' }], 1)
    expect(cardStates(quiet, INTERVAL, onlySui)).toMatchObject({ panel_api: 'not_applicable', node: 'not_applicable' })
    expect(cardStates(quiet, INTERVAL, panelFacts([{ panel_type: 'sui' }], 2)))
      .toMatchObject({ panel_api: 'idle', node: 'idle' })
    expect(cardStates(quiet)).toMatchObject({ panel_api: 'idle', node: 'idle' })
  })

  it('never calls an active card not in use, whatever the list says', () => {
    const busy = snap({
      ...healthy(),
      counters: [
        ...healthy().counters,
        c('psp_panel_op_total{op=ListInboundsSlim}', 3),
        c('psp_node_host_report_total{outcome=accepted}', 2),
      ],
    })
    expect(cardStates(busy, INTERVAL, panelFacts([{ panel_type: 'sui' }], 1)))
      .toMatchObject({ panel_api: 'ok', node: 'ok' })
  })

  // An always-registered histogram with no samples is not activity.
  it('reads an empty node histogram as no activity', () => {
    const s = snap({ ...healthy(), histograms: [...healthy().histograms, h('psp_node_host_persist_ms', 0)] })
    expect(cardStates(s, INTERVAL, panelFacts([{ panel_type: '3xui' }], 1)).node).toBe('not_applicable')
  })

  it('withholds every verdict in the blackout', () => {
    const s = snap({ ...healthy(), window_ms: INTERVAL - 1 })
    const states = cardStates(s)
    for (const id of ['poll', 'lifecycle', 'floor', 'panel_api', 'liveip', 'node'] as CardId[]) {
      expect(states[id], id).toBe('measuring')
    }
  })

  it('calls a card running while measuring once its driver moved, and measuring otherwise', () => {
    const s = snap({ ...healthy(), window_ms: INTERVAL * 2 })
    expect(cardStates(s)).toMatchObject({ poll: 'ok', floor: 'ok', liveip: 'measuring', node: 'measuring' })
  })

  it('reads the single sign-on card as none, recorded or attention', () => {
    expect(cardStates(snap(healthy())).sso).toBe('none')
    // A reset zeroes a child but never removes it: that is still nothing.
    expect(cardStates(snap({
      ...healthy(), counters: [...healthy().counters, c('psp_saml_acs_failure_total{reason=saml_destination}', 0)],
    })).sso).toBe('none')
    expect(cardStates(snap({
      ...healthy(), counters: [...healthy().counters, c('psp_saml_acs_failure_total{reason=saml_destination}', 2)],
    })).sso).toBe('recorded')
    expect(cardStates(snap({
      ...healthy(), counters: [...healthy().counters, c('psp_sso_claim_silent_total{kind=role}', 1)],
    })).sso).toBe('attention')
  })
})

// --- page verdict ----------------------------------------------------------

describe('pageVerdict', () => {
  const verdictOf = (s: DiagnosticsSnapshot, interval = INTERVAL) => {
    const f = deriveFindings(s, interval)
    return pageVerdict(f, deriveCards(s, interval, f), s.metrics.window_ms, interval)
  }

  // Amber stays amber: a page with only warnings must not turn red.
  it('is attention, never red, when the worst finding is a warning', () => {
    const v = verdictOf(snap({ ...healthy(), counters: [...healthy().counters, c('psp_push_sem_carryover_total', 1)] }))
    expect(v).toMatchObject({ tone: 'attention', key: 'attention', cards: ['floor'] })
  })

  it('is action and lists the failing cards when an error is present', () => {
    const v = verdictOf(snap({
      ...healthy(),
      counters: [...healthy().counters, c('psp_lifecycle_sync_error_total', 1), c('psp_push_sem_carryover_total', 1)],
    }))
    expect(v).toMatchObject({ tone: 'action', key: 'action', cards: ['lifecycle'] })
  })

  it('is critical and lists the cards holding a critical finding', () => {
    const v = verdictOf(snap({ ...healthy(), gauges: [g('psp_push_sem_capacity', 0)] }))
    expect(v).toMatchObject({ tone: 'critical', key: 'critical', cards: ['poll'] })
  })

  it('says "no problems found" with the number of notices when only notices exist', () => {
    const v = verdictOf(snap({
      ...healthy(), counters: [...healthy().counters, c('psp_capability_gap_total{capability=client.iplimit}', 1)],
    }))
    expect(v).toMatchObject({ tone: 'ok', key: 'ok_notices', notices: 1 })
  })

  it('is ok only on a settled window', () => {
    expect(verdictOf(snap(healthy()))).toMatchObject({ tone: 'ok', key: 'ok' })
    expect(verdictOf(snap({ ...healthy(), window_ms: INTERVAL * 2 }))).toMatchObject({
      tone: 'measuring', key: 'measuring', remainingMs: INTERVAL,
    })
    expect(verdictOf(snap({ ...healthy(), window_ms: 1_000 }))).toMatchObject({ tone: 'blackout', key: 'blackout' })
  })

  it('does not promise a time when the interval is unknown', () => {
    expect(verdictOf(snap(healthy()), 0)).toMatchObject({ tone: 'measuring', key: 'measuring_unknown' })
  })

  // A warning that already happened outranks a short window.
  it('lets findings outrank the window', () => {
    const v = verdictOf(snap({
      ...healthy(), window_ms: 1_000, counters: [...healthy().counters, c('psp_push_client_config_error_total', 1)],
    }))
    expect(v.tone).toBe('attention')
  })
})

// --- session delta -----------------------------------------------------------

describe('sessionDelta', () => {
  const at = (windowMs: number, value: number, since = 0) =>
    metrics({ since_unix_ms: since, window_ms: windowMs, counters: [c('psp_lifecycle_sync_error_total', value)] })
  const series = ['psp_lifecycle_sync_error_total']

  it('waits for a second reading', () => {
    expect(sessionDelta(undefined, at(1_000, 3), series)).toEqual({ kind: 'wait' })
    expect(sessionDelta(at(1_000, 3), at(1_000, 3), series)).toEqual({ kind: 'wait' })
  })

  it('reports growth since the page was opened', () => {
    expect(sessionDelta(at(60_000, 3), at(12 * 60_000 + 60_000, 5), series)).toEqual({ kind: 'grew', count: 2, minutes: 12 })
  })

  it('reports no growth once a refresh has happened', () => {
    expect(sessionDelta(at(60_000, 3), at(121_000, 3), series)).toEqual({ kind: 'flat', minutes: 1 })
  })

  // A restart or someone else's reset opens a new window; the old baseline
  // means nothing in it.
  it('rebuilds when the window was reopened', () => {
    expect(sessionDelta(at(60_000, 3, 0), at(30_000, 0, 999), series)).toEqual({ kind: 'rebuild' })
  })

  it('never shows a negative growth', () => {
    expect(sessionDelta(at(60_000, 5), at(120_000, 2), series)).toEqual({ kind: 'rebuild' })
  })

  it('sums a whole labelled family', () => {
    const base = metrics({ window_ms: 60_000, counters: [c('psp_node_sync_refused_total{reason=report_invalid}', 1)] })
    const cur = metrics({
      window_ms: 180_000,
      counters: [
        c('psp_node_sync_refused_total{reason=report_invalid}', 2),
        c('psp_node_sync_refused_total{reason=protocol_generation}', 4),
      ],
    })
    expect(sessionDelta(base, cur, ['psp_node_sync_refused_total'])).toEqual({ kind: 'grew', count: 5, minutes: 2 })
  })
})

// The page's "since you opened this page" line, across a restart or a clear
// that happens while the page stays open.
describe('nextSessionBase', () => {
  const at = (windowMs: number, value: number, since = 0) =>
    metrics({ since_unix_ms: since, window_ms: windowMs, counters: [c('psp_lifecycle_sync_error_total', value)] })
  const series = [['psp_lifecycle_sync_error_total']]

  it('takes the first reading as the page-open baseline', () => {
    const first = at(60_000, 3)
    expect(nextSessionBase(undefined, first, series)).toEqual({ metrics: first, rebuilt: false })
  })

  it('keeps the same baseline while the window is the same', () => {
    const base = nextSessionBase(undefined, at(60_000, 3), series)
    expect(nextSessionBase(base, at(180_000, 5), series)).toBe(base)
  })

  // Everything in a window that opened after the page did is new since the
  // page opened, so the line counts the whole new window, from its start.
  it('counts from the start of a window that reopened after the page opened', () => {
    const base = nextSessionBase(undefined, at(60_000, 3, 0), series)
    const cur = at(10 * 60_000, 4, 999)
    const next = nextSessionBase(base, cur, series)!
    expect(next.rebuilt).toBe(true)
    expect(sessionDelta(next.metrics, cur, series[0])).toEqual({ kind: 'grew', count: 4, minutes: 10 })
    expect(nextSessionBase(next, cur, series)).toBe(next)
  })

  it('starts again from the reading when a total shrank in the same window', () => {
    const base = nextSessionBase(undefined, at(60_000, 5), series)
    const cur = at(120_000, 2)
    expect(nextSessionBase(base, cur, series)).toEqual({ metrics: cur, rebuilt: true })
  })
})

// --- formatting --------------------------------------------------------------

describe('formatPct', () => {
  it.each([
    [17, 0, 'no_denominator'],
    [0, 40, '0%'],
    [10, 100, '10%'],
    [123, 1000, '12%'],
    [1, 100, '1.0%'],
    [17, 1000, '1.7%'],
    [1, 10_000, '0.01%'],
    [17, 2312, '0.74%'],
    [1, 1_000_000, '<0.01%'],
  ])('%i of %i reads %s', (n, d, want) => {
    const t = (k: string) => k.endsWith('no_denominator') ? 'no_denominator' : tEn(k)
    expect(formatPct(n, d, t)).toBe(want)
  })

  // Rounding must not push a value into the next band's format.
  it('does not print "10.0%" or "1.00%" at the band edges', () => {
    expect(formatPct(9.97, 100, tEn)).toBe('10%')
    expect(formatPct(0.998, 100, tEn)).toBe('1.0%')
  })

  it('uses the localised denominator wording', () => {
    expect(formatPct(3, 0, tZh)).toBe(zh.diagnostics.fmt.no_denominator)
  })
})

describe('formatLatency', () => {
  it.each([
    [0.4, '0 ms'],
    [999, '999 ms'],
    [999.6, '1.00 s'],
    [1000, '1.00 s'],
    [3260, '3.26 s'],
    [9999, '10.0 s'],
    [10_000, '10.0 s'],
    [29_870, '29.9 s'],
  ])('%f ms reads %s', (ms, want) => {
    expect(formatLatency(ms)).toBe(want)
  })
})

describe('formatDuration', () => {
  it.each([
    [42_000, '42 秒', '42 s'],
    [125_000, '2 分钟', '2 min'],
    [59 * 60_000 + 40_000, '1 小时 0 分', '1 h 0 min'],
    [3 * 3600_000 + 7 * 60_000, '3 小时 7 分', '3 h 7 min'],
    [59 * 3600_000, '2 天 11 小时', '2 d 11 h'],
  ])('%i ms', (ms, zhWant, enWant) => {
    expect(formatDuration(ms, tZh)).toBe(zhWant)
    expect(formatDuration(ms, tEn)).toBe(enWant)
  })
})

describe('formatBytes', () => {
  it.each([
    [512, '512 B'],
    [1536, '1.50 KiB'],
    [50 * 1024 * 1024, '50.0 MiB'],
    [5 * 1024 ** 3, '5.00 GiB'],
    [300 * 1024 ** 4, '300 TiB'],
  ])('%i bytes reads %s', (n, want) => {
    expect(formatBytes(n)).toBe(want)
  })
})

// A bucket bound is a raw value: the sub-millisecond latency bounds exist so
// the stage breakdown can tell them apart (metrics.LatencyBucketsMS), and a
// latency format rounding to whole ms would print three "0 ms" rows.
describe('formatBound', () => {
  it('keeps every latency bound distinct and unrounded', () => {
    const labels = [0.05, 0.1, 0.25, 0.5, 1, 2, 10000].map(le => formatBound(le, 'ms', 'en-US'))
    expect(labels).toEqual(['0.05 ms', '0.1 ms', '0.25 ms', '0.5 ms', '1 ms', '2 ms', '10,000 ms'])
  })

  it('writes bytes in 1024 steps, kilometres and plain counts exactly', () => {
    expect(formatBound(1024, 'bytes', 'en-US')).toBe('1.00 KiB')
    expect(formatBound(20020, 'km', 'en-US')).toBe('20,020 km')
    expect(formatBound(12, 'ips', 'en-US')).toBe('12')
  })
})

describe('formatRate', () => {
  it.each([
    [0, '0'], [0.04, '0.04'], [1.94, '1.9'], [12.4, '12'], [1234.5, '1,235'],
  ])('%f reads %s', (r, want) => {
    expect(formatRate(r, 'en-US')).toBe(want)
  })
})

describe('quantileReading', () => {
  const hist = (over: Partial<HistogramSnapshot>): HistogramSnapshot => ({
    ...h('psp_poll_ms', 0),
    buckets: [{ le: 5000, count: 0 }, { le: 10000, count: 0 }, { le: 0, inf: true, count: 0 }],
    ...over,
  })

  it('has nothing to say without samples', () => {
    expect(quantileReading(hist({ count: 0 }))).toEqual({ kind: 'none' })
    expect(quantileReading(undefined)).toEqual({ kind: 'none' })
  })

  it('reports only the longest under ten samples', () => {
    expect(quantileReading(hist({ count: 9, max: 812 }))).toEqual({ kind: 'few', count: 9, max: 812 })
  })

  it('reports the typical and 95% figures with enough samples', () => {
    expect(quantileReading(hist({ count: 40, p50: 835, p95: 3260 }))).toEqual({
      kind: 'ok', p50: 835, p95: 3260, over: false, ceiling: 10000,
    })
  })

  // Past the last finite bucket the estimate is pulled toward the max, so the
  // number is withheld and only the ceiling is stated.
  it('flags a p95 beyond the last bucket', () => {
    expect(quantileReading(hist({ count: 40, p50: 900, p95: 14_000 }))).toMatchObject({ over: true, ceiling: 10000 })
  })
})

describe('findingInterpolation', () => {
  const fmt = {
    count: (n: number) => `#${n}`,
    duration: (ms: number) => `${ms / 60_000}min`,
    pct: (n: number, d: number) => `${n}/${d}`,
  }

  it('formats counts, the window, and the percentage from the ratio', () => {
    const f: Finding = {
      id: 'lifecycle_errors', key: 'lifecycle_errors', severity: 'error', card: 'lifecycle', series: [],
      values: { errors: 17, checks: 2312 }, ratio: { n: 17, d: 2312 },
    }
    expect(findingInterpolation(f, fmt)).toEqual({ errors: '#17', checks: '#2312', pct: '17/2312' })
    const dead: Finding = { id: 'poll_dead', key: 'poll_dead', severity: 'critical', card: 'poll', series: [], values: { window: 600_000 } }
    expect(findingInterpolation(dead, fmt)).toEqual({ window: '10min' })
  })
})

// The breakdown sentence names each step and panel kind with its count; the
// keys are the copy's own placeholders, so nothing renames them in between.
describe('breakdownInterpolation', () => {
  it('lists each item as label and count, joined, under the copy placeholder', () => {
    const f: Finding = {
      id: 'lifecycle_errors', key: 'lifecycle_errors', severity: 'error', card: 'lifecycle', series: [],
      breakdown: {
        stages: [{ value: 'confirm_read', count: 4 }, { value: 'update', count: 1 }],
        kinds: [{ value: 'psp', count: 5 }],
      },
    }
    const label = (group: string, value: string) => `${group}:${value}`
    expect(breakdownInterpolation(f, label, n => `#${n}`)).toEqual({
      stages: 'lifecycle_stage:confirm_read #4 · lifecycle_stage:update #1',
      kinds: 'panel_kind:psp #5',
    })
  })

  it('says nothing for a finding without a breakdown', () => {
    const f: Finding = { id: 'poll_errors', key: 'poll_errors', severity: 'error', card: 'poll', series: [] }
    expect(breakdownInterpolation(f, () => '', String)).toEqual({})
  })
})

describe('findingParts', () => {
  const base: Finding = { id: 'lifecycle_errors', key: 'lifecycle_errors', severity: 'error', card: 'lifecycle', series: [] }

  it('shows a conditional sentence only when the reading carries it', () => {
    expect(findingParts(base)).toEqual(['retry'])
    expect(findingParts({ ...base, variants: ['origin'] })).toEqual(['origin', 'retry'])
    expect(findingParts({ ...base, variants: ['origin'], breakdown: { stages: [], kinds: [] } }))
      .toEqual(['origin', 'breakdown', 'retry'])
  })
})

// --- computed key coverage -------------------------------------------------------

describe('copy for computed keys', () => {
  // Every (card, state) the derivation can produce, from fixtures that walk
  // each branch, mapped through the same key function the page uses.
  const sentenceKeys = () => {
    const keys = new Set<string>()
    const add = (s: DiagnosticsSnapshot, interval: number, facts?: PanelFacts) => {
      const mode = windowMode(s.metrics.window_ms, interval)
      for (const card of deriveCards(s, interval, deriveFindings(s, interval, facts), facts)) {
        keys.add(cardSentenceKey(card, mode, interval > 0))
      }
    }
    const busy = metrics({
      ...healthy(),
      counters: [
        ...healthy().counters,
        c('psp_panel_op_total{op=GetClient}', 1),
        c('psp_node_host_report_total{outcome=accepted}', 1),
        c('psp_saml_acs_failure_total{reason=saml_destination}', 1),
      ],
      histograms: [...healthy().histograms, { ...h('psp_user_live_ips', 4) }],
    })
    add(snap(busy), INTERVAL)
    add(snap(metrics({ gauges: [g('psp_push_sem_capacity', 8)] })), INTERVAL)
    add(snap(metrics({ counters: [c('psp_poll_total', 40)], gauges: [g('psp_push_sem_capacity', 8)] })), INTERVAL)
    add(snap(metrics({ window_ms: INTERVAL * 2 })), INTERVAL)
    add(snap(metrics({ window_ms: 1_000 })), INTERVAL)
    add(snap(metrics()), 0)
    add(snap(metrics({ gauges: [g('psp_push_sem_capacity', 0)] })), INTERVAL)
    add(snap(healthy()), INTERVAL, panelFacts([], 0))
    add(snap({
      ...healthy(),
      counters: [...healthy().counters, c('psp_lifecycle_sync_error_total', 1), c('psp_push_sem_carryover_total', 1)],
    }), INTERVAL)
    return keys
  }

  it('reaches every sentence a card can show', () => {
    expect([...sentenceKeys()].sort()).toEqual([
      'cards.common.attention', 'cards.common.blackout', 'cards.common.failing', 'cards.common.inhibited',
      'cards.common.measuring', 'cards.common.measuring_unknown',
      'cards.floor.idle', 'cards.floor.ok', 'cards.lifecycle.idle', 'cards.lifecycle.ok',
      'cards.liveip.idle', 'cards.liveip.ok', 'cards.node.idle', 'cards.node.not_applicable', 'cards.node.ok',
      'cards.panel_api.idle', 'cards.panel_api.not_applicable', 'cards.panel_api.ok', 'cards.poll.ok',
      'cards.sso.none', 'cards.sso.recorded',
    ])
  })

  const STATES: CardState[] = ['failing', 'attention', 'ok', 'idle', 'measuring', 'inhibited', 'not_applicable', 'none', 'recorded']
  const VERDICTS = ['critical', 'action', 'attention', 'ok', 'ok_notices', 'measuring', 'measuring_unknown', 'blackout']

  for (const [lang, bundle] of BUNDLES) {
    const has = (k: string) => expect(bundle.has(`diagnostics.${k}`), `${lang} diagnostics.${k}`).toBe(true)

    it(`${lang} has every card sentence, title and purpose`, () => {
      for (const k of sentenceKeys()) has(k)
      for (const id of CARD_ORDER) {
        has(`cards.${id}.title`)
        has(`cards.${id}.purpose`)
      }
    })

    it(`${lang} has every state, severity and verdict wording`, () => {
      for (const s of STATES) has(`state.${s}`)
      for (const s of ['critical', 'error', 'warn', 'notice']) has(`severity.${s}`)
      for (const v of VERDICTS) has(`verdict.${v}`)
    })

    it(`${lang} has every growth line, from the page opening and from a restart`, () => {
      for (const kind of ['wait', 'rebuild', 'grew', 'flat'] as const) {
        for (const rebuilt of [false, true]) has(`problems.${sessionLineKey(kind, rebuilt)}`)
      }
    })

    it(`${lang} has both self-check sentences for each check`, () => {
      for (const check of deriveSelfChecks(healthy())) {
        has(`self_check.${check.id}.pass`)
        has(`self_check.${check.id}.fail`)
      }
    })

    it(`${lang} has every duration and count format`, () => {
      for (const k of ['duration_s', 'duration_m', 'duration_hm', 'duration_dh', 'no_denominator', 'pct_tiny',
        'few_samples', 'no_samples', 'p95_over', 'typical', 'p95_within', 'per_hour', 'per_hour_times', 'per_poll']) has(`fmt.${k}`)
    })
  }

  // Every place a link can point has a page name in the navigation bundle,
  // because the link text reuses it.
  it('names every link target in both navigation bundles', () => {
    const navs = [flatten(zhNav as Nested), flatten(enNav as Nested)]
    const targets = new Set([...Object.values(CARD_LINK).filter(Boolean), ...Object.keys(LINK_TARGET)])
    for (const link of targets) {
      const nav = LINK_TARGET[link as keyof typeof LINK_TARGET].nav.replace(/^nav:/, '')
      for (const flat of navs) expect(flat[nav], nav).toBeTruthy()
    }
  })
})

// The lifecycle breakdowns reach the page as two card lines and one finding
// sentence, all looked up by computed keys and filled through placeholders,
// so a renamed placeholder would print "{{list}}" rather than fail to build.
// Rendering them through the real bundles, with every step and every panel
// kind failing, proves the keys, the placeholders and the label names at once.
describe('copy for the lifecycle failure breakdown', () => {
  const everyStepAndKind = metrics({
    counters: [
      c('psp_lifecycle_sync_error_total', LIFECYCLE_ERROR_STAGES.length),
      ...LIFECYCLE_ERROR_STAGES.map(s => c(`psp_lifecycle_sync_error_stage_total{stage=${s}}`, 1)),
      ...LIFECYCLE_ERROR_KINDS.map(k => c(`psp_lifecycle_sync_error_panel_kind_total{kind=${k}}`, 1)),
    ],
  })

  for (const [lang, bundle, t] of [['zh-CN', zh, tZh], ['en-US', en, tEn]] as const) {
    const flat = flatten(bundle as Nested)
    const label = (group: string, value: string) => {
      const name = flat[`diagnostics.labels.${group}.${labelKey(value)}`]
      if (typeof name !== 'string') throw new Error(`${lang} has no label for ${group}.${value}`)
      return name
    }

    it(`${lang} renders both card lines with every step and panel kind named`, () => {
      const lines = lifecycleCardBreakdown(everyStepAndKind)
      expect(lines).toHaveLength(2)
      for (const line of lines) {
        const list = formatBreakdownList(line.items, line.group, label, String)
        const text = t(`diagnostics.${line.key}`, { list })
        expect(text, line.key).toContain(list)
        expect(text, line.key).not.toContain('{{')
      }
    })

    // Not every counted failure missed the panel: record_credentials is
    // counted after the write was confirmed. The sentence beside the lists says
    // what each step means, quoting the step by the name the list uses.
    it(`${lang} explains every step the breakdown can name`, () => {
      const sentence = flat['diagnostics.findings.lifecycle_errors.breakdown'] as string
      for (const s of LIFECYCLE_ERROR_STAGES) {
        const name = label('lifecycle_stage', s)
        expect(sentence, s).toContain(lang === 'zh-CN' ? `「${name}」` : `"${name}"`)
      }
    })

    it(`${lang} renders the finding's breakdown sentence with both lists`, () => {
      const f = deriveFindings(snap(everyStepAndKind), INTERVAL).find(x => x.id === 'lifecycle_errors')
      const values = breakdownInterpolation(f!, label, String)
      const text = t('diagnostics.findings.lifecycle_errors.breakdown', values)
      expect(text).toContain(values.stages)
      expect(text).toContain(values.kinds)
      expect(text).not.toContain('undefined')
    })
  }
})

// One sign-in with both rule sets and neither attribute counts once per kind
// (user.reconcileSSOUser), so the family total is not a number of sign-ins and
// the title may only state the per-kind figures.
describe('copy for single sign-on without claims', () => {
  const both = snap(metrics({
    counters: [c('psp_sso_claim_silent_total{kind=role}', 1), c('psp_sso_claim_silent_total{kind=group}', 1)],
  }))
  for (const [lang, t] of [['zh-CN', tZh], ['en-US', tEn]] as const) {
    it(`${lang} never presents the per-kind total as a number of sign-ins`, () => {
      const f = deriveFindings(both, INTERVAL).find(x => x.id === 'sso_claim_silent')!
      const title = t('diagnostics.findings.sso_claim_silent.title',
        findingInterpolation(f, { count: String, duration: String, pct: () => '' }))
      expect(title).not.toContain('2')
      expect(title).toContain('1')
    })
  }
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

// --- page models -----------------------------------------------------------

// One failed persist is counted under report{storage_error} and, when a
// history sample was due, under history{storage_error} as well
// (nodemetrics.Ingest): the second is a subset of the first.
describe('nodeSaveFailures', () => {
  it('counts a failed save once, whether or not it carried history', () => {
    expect(nodeSaveFailures(metrics({
      counters: [
        c('psp_node_host_report_total{outcome=storage_error}', 3),
        c('psp_node_host_history_total{outcome=storage_error}', 2),
        c('psp_node_host_report_total{outcome=accepted}', 40),
      ],
    }))).toEqual({ failures: 3, withHistory: 2 })
  })

  it('is zero when nothing failed', () => {
    expect(nodeSaveFailures(metrics())).toEqual({ failures: 0, withHistory: 0 })
  })
})

describe('panelOpRows', () => {
  const rtt = (op: string, count: number) => ({ ...h(`psp_panel_rtt_ms{op=${op}}`, count), p50: 300 })

  it('joins the three panel request families by operation, busiest first', () => {
    const m = metrics({
      counters: [
        c('psp_panel_op_total{op=GetClient}', 40),
        c('psp_panel_op_total{op=ListInboundsSlim}', 1774),
        c('psp_panel_op_error_total{op=GetClient}', 3),
        c('psp_panel_op_total{op=other}', 2),
      ],
      histograms: [rtt('ListInboundsSlim', 1774), rtt('GetClient', 40)],
    })
    const rows = panelOpRows(m)
    expect(rows.map(r => r.op)).toEqual(['ListInboundsSlim', 'GetClient', 'other'])
    expect(rows[1]).toMatchObject({ op: 'GetClient', requests: 40, errors: 3 })
    expect(rows[1].rtt?.count).toBe(40)
    expect(rows[2]).toMatchObject({ op: 'other', requests: 2, errors: 0 })
    expect(rows[2].rtt).toBeUndefined()
  })

  // An operation seen only as a failure or only as a latency is still an
  // operation the panel was asked for; dropping it would hide the row.
  it('keeps an operation that appears in only one family', () => {
    const m = metrics({ counters: [c('psp_panel_op_error_total{op=UpdateClient}', 1)], histograms: [rtt('GetInbound', 1)] })
    expect(panelOpRows(m).map(r => r.op).sort()).toEqual(['GetInbound', 'UpdateClient'])
  })

  it('orders equal counts by name, so a refresh does not reshuffle them', () => {
    const m = metrics({ counters: [c('psp_panel_op_total{op=b}', 1), c('psp_panel_op_total{op=a}', 1)] })
    expect(panelOpRows(m).map(r => r.op)).toEqual(['a', 'b'])
  })
})

describe('stageGroups', () => {
  const stage = (s: string, sum: number) => ({ ...h(`psp_poll_stage_ms{stage=${s}}`, 10), sum })

  it('averages each group over the polls, summing its stages', () => {
    const m = metrics({
      histograms: [
        { ...h('psp_poll_ms', 10), sum: 10_000 },
        stage('list_users', 100), stage('latest_prefetch', 200),
        stage('panel_fetch', 6_550),
        stage('live_ips', 50), stage('user_loop', 150),
        stage('sink_flush', 300),
        stage('geo_enforce', 20),
      ],
    })
    expect(stageGroups(m)).toEqual([
      { group: 'db', avgMs: 30 },
      { group: 'panels', avgMs: 655 },
      { group: 'compute', avgMs: 20 },
      { group: 'write', avgMs: 30 },
      { group: 'geo', avgMs: 2 },
    ])
  })

  it('has nothing to show before the first poll has been timed', () => {
    expect(stageGroups(metrics())).toBeNull()
    expect(stageGroups(metrics({ histograms: [h('psp_poll_ms', 0)] }))).toBeNull()
  })
})

describe('writeReasonGroups', () => {
  it('folds the raw reasons into the five groups, keeping each raw count', () => {
    const m = metrics({
      counters: [
        c('psp_lifecycle_sync_write_reason_total{reason=total_gb}', 5),
        c('psp_lifecycle_sync_write_reason_total{reason=enable}', 2),
        c('psp_lifecycle_sync_write_reason_total{reason=expiry}', 1),
        c('psp_lifecycle_sync_write_reason_total{reason=panel_unread}', 4),
      ],
    })
    expect(writeReasonGroups(m)).toEqual([
      { group: 'quota', count: 5, reasons: [{ value: 'total_gb', count: 5 }] },
      { group: 'state', count: 3, reasons: [{ value: 'enable', count: 2 }, { value: 'expiry', count: 1 }] },
      { group: 'limits', count: 0, reasons: [] },
      { group: 'credentials', count: 0, reasons: [] },
      { group: 'unread', count: 4, reasons: [{ value: 'panel_unread', count: 4 }] },
    ])
  })
})

describe('bucketRows', () => {
  it('differences adjacent cumulative buckets into per-bucket counts', () => {
    const hist: HistogramSnapshot = {
      ...h('psp_poll_ms', 9),
      buckets: [{ le: 100, count: 2 }, { le: 500, count: 7 }, { le: 1000, count: 7 }, { le: 0, inf: true, count: 9 }],
    }
    expect(bucketRows(hist)).toEqual([
      { le: 100, inf: false, cumulative: 2, count: 2 },
      { le: 500, inf: false, cumulative: 7, count: 5 },
      { le: 1000, inf: false, cumulative: 7, count: 0 },
      { le: 0, inf: true, cumulative: 9, count: 2 },
    ])
  })
})

describe('rawFamilies', () => {
  // Every catalogued family, then whatever the server sent that the
  // catalogue does not know: nothing the API returns may be dropped.
  const m = metrics({
    counters: [
      c('psp_poll_total', 12),
      c('psp_panel_op_total{op=GetClient}', 3),
      c('psp_panel_op_total{op=other}', 0),
      c('psp_brand_new_total', 1),
      c('psp_brand_new_vec_total{kind=a}', 2),
    ],
    gauges: [g('psp_push_sem_capacity', 8)],
    histograms: [h('psp_node_host_snapshot_bytes', 0)],
  })
  const fams = rawFamilies(m)
  const byName = Object.fromEntries(fams.map(f => [f.family, f]))

  it('lists every catalogued family, in catalogue order, then the unknown ones', () => {
    const catalogued = Object.keys(FAMILY_CATALOG)
    expect(fams.slice(0, catalogued.length).map(f => f.family)).toEqual(catalogued)
    expect(fams.slice(catalogued.length).map(f => f.family)).toEqual(['psp_brand_new_total', 'psp_brand_new_vec_total'])
  })

  it('places an unknown family under "other", typed by where it arrived', () => {
    expect(byName.psp_brand_new_total).toMatchObject({ card: 'other', type: 'counter', labelled: false, absent: false })
    expect(byName.psp_brand_new_vec_total).toMatchObject({ card: 'other', type: 'counter', labelled: true })
    expect(byName.psp_brand_new_vec_total.series[0]).toMatchObject({ label: 'kind', value: 'a' })
  })

  it('carries every child of a labelled family, zero ones included', () => {
    expect(byName.psp_panel_op_total.series.map(s => s.name)).toEqual([
      'psp_panel_op_total{op=GetClient}', 'psp_panel_op_total{op=other}',
    ])
  })

  it('marks a catalogued family the reading does not have as absent', () => {
    expect(byName.psp_poll_error_total).toMatchObject({ absent: true, series: [] })
    expect(byName.psp_poll_total).toMatchObject({ absent: false })
    expect(byName.psp_node_host_snapshot_bytes).toMatchObject({ absent: false, type: 'histogram' })
  })

  it('counts the families present and every series', () => {
    expect(rawSummary(m)).toEqual({ families: 6, series: 7 })
  })
})

describe('filterRawFamilies', () => {
  const m = metrics({
    counters: [
      c('psp_poll_total', 12),
      c('psp_poll_error_total', 0),
      c('psp_panel_op_total{op=GetClient}', 3),
      c('psp_panel_op_total{op=ListInbounds}', 0),
    ],
    gauges: [g('psp_push_sem_waiting', 0, 2)],
    histograms: [h('psp_poll_ms', 0)],
  })
  const fams = rawFamilies(m)
  const words = (f: { family: string }, s?: { value?: string }) =>
    s ? (s.value === 'GetClient' ? ['Read client'] : []) : (f.family === 'psp_poll_total' ? ['Traffic polls'] : [])
  const names = (out: ReturnType<typeof filterRawFamilies>) =>
    out.flatMap(f => f.series.length ? f.series.map(s => s.name) : [`${f.family} (absent)`])

  it('keeps only what moved under "non-zero only", a peak counting as movement', () => {
    const out = filterRawFamilies(fams, { query: '', nonZero: true, words })
    expect(names(out)).toEqual(['psp_poll_total', 'psp_push_sem_waiting', 'psp_panel_op_total{op=GetClient}'])
  })

  it('matches the family name, its translated words, a label value or a child\'s translated name', () => {
    expect(names(filterRawFamilies(fams, { query: 'traffic polls', nonZero: false, words }))).toEqual(['psp_poll_total'])
    expect(names(filterRawFamilies(fams, { query: 'LISTINBOUNDS', nonZero: false, words }))).toEqual(['psp_panel_op_total{op=ListInbounds}'])
    expect(names(filterRawFamilies(fams, { query: 'read client', nonZero: false, words }))).toEqual(['psp_panel_op_total{op=GetClient}'])
  })

  it('keeps every child when the family itself matches', () => {
    expect(names(filterRawFamilies(fams, { query: 'psp_panel_op_total', nonZero: false, words }))).toEqual([
      'psp_panel_op_total{op=GetClient}', 'psp_panel_op_total{op=ListInbounds}',
    ])
  })

  it('matches the server\'s own help text', () => {
    const helped = rawFamilies(metrics({ counters: [{ name: 'psp_poll_total', help: 'Traffic poll cycles started.', value: 1 }] }))
    expect(names(filterRawFamilies(helped, { query: 'cycles started', nonZero: false, words }))).toEqual(['psp_poll_total'])
  })

  it('keeps an absent family only when nothing but the name is asked about', () => {
    expect(names(filterRawFamilies(fams, { query: 'psp_live_connections', nonZero: false, words })))
      .toEqual(['psp_live_connections (absent)'])
    expect(names(filterRawFamilies(fams, { query: 'psp_live_connections', nonZero: true, words }))).toEqual([])
  })
})
