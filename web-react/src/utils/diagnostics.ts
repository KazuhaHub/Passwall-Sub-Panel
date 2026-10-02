import type {
  CounterSnapshot,
  DiagnosticsSnapshot,
  GaugeSnapshot,
  HistogramSnapshot,
  MetricsSnapshot,
} from '@/api/diagnostics'
import { familyOf, type CardId } from './diagnosticsCatalog'

// Derivation for the admin diagnostics page. Pure, so the rules that decide
// what an operator is told can be tested without rendering anything.
//
// One rule governs the whole page, and it is stated once here rather than
// re-decided per row:
//
//   ZEROS ARE GATED BY THE WINDOW. NON-ZEROS ARE NOT.
//
// Every counter is "since the window opened" (process start, or the last
// reset). A zero read three minutes after a restart is not evidence of health,
// so zero-derived conclusions are withheld until the window is long enough to
// have produced a non-zero. An OBSERVED error, suppression or capability gap is
// a fact that a short window cannot invalidate — it already happened — so it is
// never withheld. Getting this backwards would hide findings during exactly the
// incident the page exists for.

/** How much of a verdict the window can support. */
export type WindowMode =
  /** Under one poll interval: not one cycle has necessarily run. Numbers are
   *  withheld entirely rather than greyed — a greyed zero is still a zero on
   *  the screen, and it gets read. */
  | 'blackout'
  /** Past one interval but still short: zeros stay inconclusive. */
  | 'measuring'
  /** Long enough that a zero means something. */
  | 'settled'

/**
 * The vocabulary every evidence row renders through, so "cannot tell" can never
 * be drawn as "fine" by a row that forgot to distinguish them.
 */
export type EvidenceState =
  | 'ok'
  | 'measuring'
  /** Nothing was attempted, so there is no ratio — not 0%. */
  | 'no_data'
  /** This code path has not run in this window. */
  | 'never'
  /** Proven impossible from a fact. Never inferred from a zero. */
  | 'not_applicable'
  | 'unknown'
  /** The number is a lower bound: coverage was incomplete. */
  | 'floor'

/**
 * How loudly a finding speaks. critical/error colour the page red and warn
 * amber; a notice is a standing condition rather than an incident and never
 * colours anything.
 */
export type Severity = 'critical' | 'error' | 'warn' | 'notice'

/** Where a finding sends the operator next. */
export type FindingLink = 'sync_tasks' | 'settings' | 'servers' | 'risk'

export interface LabelCount {
  value: string
  count: number
}

export interface Finding {
  id: string
  severity: Severity
  /** i18n key suffix under admin:diagnostics.findings (always the id). */
  key: string
  /** The area card the finding belongs to. */
  card: CardId
  /**
   * Counter series (or whole labelled families) whose growth since the page
   * was opened is shown beside the finding. Empty when growth means nothing:
   * a capacity of zero or a dead poll does not "grow".
   */
  series: string[]
  link?: FindingLink
  /** Raw numbers only; the page formats them. */
  values?: Record<string, number>
  /** A count with its denominator, for the percentage in the title. */
  ratio?: { n: number; d: number }
  /** Optional sentences that apply to THIS reading, by i18n key suffix. */
  variants?: string[]
  /** Children of the labelled families that explain the count, largest first. */
  breakdown?: Record<string, LabelCount[]>
}

export interface Precondition {
  id: string
  state: 'ok' | 'broken' | 'measuring' | 'no_data'
  values?: Record<string, string | number>
}

// ---------------------------------------------------------------------------
// snapshot lookup
// ---------------------------------------------------------------------------

/** Exact-name counter lookup. Absent means the metric was never registered. */
export function counter(m: MetricsSnapshot, name: string): CounterSnapshot | undefined {
  return m.counters.find(c => c.name === name)
}

export function gauge(m: MetricsSnapshot, name: string): GaugeSnapshot | undefined {
  return m.gauges.find(g => g.name === name)
}

export function histogram(m: MetricsSnapshot, name: string): HistogramSnapshot | undefined {
  return m.histograms.find(h => h.name === name)
}

/** Sum every series of a labelled counter family (`name{label=...}`). */
export function counterFamilyTotal(m: MetricsSnapshot, name: string): number {
  return m.counters
    .filter(c => c.name === name || c.name.startsWith(`${name}{`))
    .reduce((sum, c) => sum + c.value, 0)
}

/**
 * The non-zero children of a labelled counter family, largest first, ties in
 * name order so a refresh does not reshuffle equal rows. Zero children are
 * dropped: a reset zeroes a child but the registry never removes it.
 */
export function counterChildren(m: MetricsSnapshot, family: string): LabelCount[] {
  const out: LabelCount[] = []
  for (const c of m.counters) {
    const parsed = familyOf(c.name)
    if (parsed.family === family && parsed.value !== undefined && c.value > 0) {
      out.push({ value: parsed.value, count: c.value })
    }
  }
  return out.sort((a, b) => b.count - a.count || a.value.localeCompare(b.value))
}

/** Value or 0. Use only where absence and zero are genuinely the same. */
function val(m: MetricsSnapshot, name: string): number {
  return counter(m, name)?.value ?? 0
}

// ---------------------------------------------------------------------------
// facts — what the server list proves, as opposed to what a zero suggests
// ---------------------------------------------------------------------------

/**
 * Which kinds of panel are connected, read from the server list. A fact may
 * only ever ADD information ("an S-UI panel exists, so this count keeps
 * growing by design"; "no 3X-UI panel exists, so this card does not apply").
 * It is trusted only when the list read covered every panel: a partial or
 * failed read leaves the page saying less, never hiding a problem.
 */
export interface PanelFacts {
  complete: boolean
  types: ReadonlySet<string>
}

export const UNKNOWN_FACTS: PanelFacts = { complete: false, types: new Set() }

export function panelFacts(
  items: ReadonlyArray<{ panel_type?: string }> | undefined,
  total: number | undefined,
): PanelFacts {
  if (!items || total === undefined) return UNKNOWN_FACTS
  // A row without a kind predates the adapter layer and is 3X-UI everywhere
  // else in PSP (domain.NormalizePanelKind).
  return {
    complete: items.length >= total,
    types: new Set(items.map(i => i.panel_type || '3xui')),
  }
}

/** True only when a complete server list proves a panel of this kind exists. */
function hasPanelKind(facts: PanelFacts, kind: string): boolean {
  return facts.complete && facts.types.has(kind)
}

// ---------------------------------------------------------------------------
// window
// ---------------------------------------------------------------------------

/** Windows shorter than this many intervals leave zeros inconclusive. */
export const SETTLED_INTERVALS = 3

/**
 * The poll interval to judge a window against.
 *
 * Prefer what the traffic loop reports it is actually running on. The settings
 * row is only what has been REQUESTED: the loop picks a change up on its next
 * tick, so between the two the row says one minute while the ticker is still
 * sleeping out five. Judging against the row there marks a window settled three
 * minutes in and reports a healthy poll as dead — the exact false alarm every
 * gate on this page exists to avoid, arriving through the gate's own input.
 *
 * Falls back to the settings value only when the gauge is absent, which means a
 * server older than the gauge.
 */
export function effectivePollIntervalMs(
  m: MetricsSnapshot,
  settingsIntervalMs: number,
): number {
  const g = gauge(m, 'psp_poll_interval_ms')
  return g && g.value > 0 ? g.value : settingsIntervalMs
}

export function windowMode(windowMs: number, pollIntervalMs: number): WindowMode {
  if (!(pollIntervalMs > 0)) return 'measuring' // interval unknown: never claim settled
  if (windowMs < pollIntervalMs) return 'blackout'
  if (windowMs < pollIntervalMs * SETTLED_INTERVALS) return 'measuring'
  return 'settled'
}

/**
 * True when the counters were reset without a restart. The gap between process
 * uptime and the measurement window is the only way to tell a fresh boot from a
 * deliberate re-measurement, and they warrant different readings of a zero.
 */
export function wasReset(snap: DiagnosticsSnapshot): boolean {
  // One interval of slack: the window opens during boot, not at t=0.
  return snap.uptime_ms - snap.metrics.window_ms > 60_000
}

/** Slack granted to the traffic loop's first tick, on top of start-up time. */
const POLL_START_SLACK_MS = 60_000

/**
 * The fewest polls the window can have held, or null when the loop's own
 * interval is not published. Every input is read in the direction that LOWERS
 * the expectation, because its one use that colours anything (poll_behind)
 * must never fire on a healthy loop:
 *
 *  - the interval is the gauge's PEAK, the longest one used in the window, so
 *    shortening the interval cannot make polls run at the old one look missing,
 *    and lengthening it only lowers the count;
 *  - the registry opens its window at process start, before the schema
 *    migration and Build, while the loop starts after; that gap is
 *    window − uptime on a fresh boot (zero after a reset, when the loop was
 *    already running) and it is taken off along with a fixed slack;
 *  - the result is floored.
 */
export function expectedPollsMin(snap: DiagnosticsSnapshot): number | null {
  const interval = gauge(snap.metrics, 'psp_poll_interval_ms')?.peak ?? 0
  if (!(interval > 0)) return null
  const window = snap.metrics.window_ms
  const slack = POLL_START_SLACK_MS + Math.max(0, window - snap.uptime_ms)
  return Math.max(0, Math.floor((window - slack) / interval))
}

// ---------------------------------------------------------------------------
// histograms
// ---------------------------------------------------------------------------

/** Below this, a bucket-interpolated quantile says more about the buckets. */
export const MIN_QUANTILE_SAMPLES = 10

/**
 * Quantiles are interpolated from cumulative buckets and clamped to the
 * observed max, so at two samples a "p95" is the max wearing a percentile's
 * name. Under the threshold, report what was actually seen instead.
 */
export function quantileUsable(h: HistogramSnapshot | undefined): boolean {
  return !!h && h.count >= MIN_QUANTILE_SAMPLES
}

// ---------------------------------------------------------------------------
// preconditions — does a whole downstream area have any input at all
// ---------------------------------------------------------------------------

export function derivePreconditions(
  snap: DiagnosticsSnapshot,
  pollIntervalMs: number,
): Precondition[] {
  const m = snap.metrics
  const mode = windowMode(m.window_ms, pollIntervalMs)
  const polls = val(m, 'psp_poll_total')
  const capacity = gauge(m, 'psp_push_sem_capacity')

  const out: Precondition[] = []

  // The poll drives everything below it, so its absence explains every other
  // zero on the page and must be read before them.
  out.push({
    id: 'poll_running',
    state: polls > 0 ? 'ok' : mode === 'settled' ? 'broken' : 'measuring',
    values: { polls },
  })

  // Set once at construction, so zero does not mean "a small pool" — it means
  // the traffic service was never built and every metric in that family is
  // describing nothing. Ungated: it is a fact, not a tally.
  out.push({
    id: 'push_capacity',
    state: capacity === undefined ? 'no_data' : capacity.value > 0 ? 'ok' : 'broken',
    values: { capacity: capacity?.value ?? 0 },
  })

  const attempted = val(m, 'psp_push_client_config_total')
  out.push({
    id: 'push_reaching',
    state: attempted > 0 ? 'ok' : mode === 'settled' ? 'no_data' : 'measuring',
    values: { attempted },
  })

  const lifecycle = val(m, 'psp_lifecycle_sync_total')
  out.push({
    id: 'lifecycle_running',
    state: lifecycle > 0 ? 'ok' : mode === 'settled' ? 'no_data' : 'measuring',
    values: { lifecycle },
  })

  // A live-IP read that covered every panel is what makes the geo verdicts
  // countable at all; an incomplete one makes every downstream figure a floor.
  const incomplete = val(m, 'psp_live_ip_users_incomplete_total')
  const liveIPs = histogram(m, 'psp_user_live_ips')
  out.push({
    id: 'liveip_complete',
    state: incomplete > 0
      ? 'broken'
      : liveIPs && liveIPs.count > 0
        ? 'ok'
        : mode === 'settled' ? 'no_data' : 'measuring',
    values: { incomplete },
  })

  return out
}

// ---------------------------------------------------------------------------
// findings — things that can only ever accuse, never reassure
// ---------------------------------------------------------------------------

const SEVERITY_ORDER: Record<Severity, number> = { critical: 0, error: 1, warn: 2, notice: 3 }

/**
 * The order findings are listed in within one severity, and the whole set the
 * page can raise. Fixed rather than by count, so the list reads the same way
 * on every refresh instead of reshuffling as numbers move.
 */
export const FINDING_ORDER = [
  'push_capacity_zero',
  'poll_dead',
  'poll_errors',
  'lifecycle_errors',
  'poll_behind',
  'push_errors',
  'push_backlog',
  'liveip_incomplete',
  'history_write_errors',
  'flag_write_errors',
  'geo_auto_errors',
  'node_sync_refused',
  'sso_claim_silent',
  'capability_gaps',
] as const

export type FindingId = (typeof FINDING_ORDER)[number]

/**
 * Findings are one-way: each may APPEAR, and its absence is never evidence of
 * health. That is what keeps the page from turning an unmeasured fleet green.
 *
 * Deliberately NOT findings here, each for a stated reason:
 *
 *  - IP-cap enforcement (psp_ip_limit_enforcement_total). Whether a node cannot
 *    enforce a cap only matters if a cap is set, and that is a DB fact this
 *    endpoint does not carry. Inferring it from the metric alone opens a red
 *    banner on every fresh install that has never typed an IP limit — an
 *    "enforcement outage" with nothing to enforce. It stays on the servers page,
 *    which has the per-node badge and the tooltip that says which gate is shut.
 *  - Geo verdicts (psp_geo_verdict_total) and the non-error outcomes of the
 *    automatic suspension (psp_geo_auto_suspension_total). A response exists
 *    now — the bell counts flagged and auto-suspended accounts, and the Geo tab
 *    lists them with the evidence — so a finding here would only repeat the bell
 *    without naming anyone. And the one reading this page could add is not a
 *    fault: an all-idle fleet at 04:00 is the healthiest thing the detector can
 *    report, not a blind one. A detector that has stopped judging shows up as
 *    poll_dead (no poll, no verdicts), which is already a finding. Only the
 *    suspension's *_error outcomes are raised (geo_auto_errors), because the
 *    bell counts what was suspended, never what failed to be.
 *  - Panel request failures (psp_panel_op_error_total). One transient error
 *    would hold a card amber for the whole window; the card shows the rate.
 */
export function deriveFindings(
  snap: DiagnosticsSnapshot,
  pollIntervalMs: number,
  facts: PanelFacts = UNKNOWN_FACTS,
): Finding[] {
  const m = snap.metrics
  const mode = windowMode(m.window_ms, pollIntervalMs)
  const out: Finding[] = []
  const add = (f: Omit<Finding, 'key'>) => out.push({ ...f, key: f.id })

  const capacity = gauge(m, 'psp_push_sem_capacity')
  if (capacity !== undefined && capacity.value <= 0) {
    add({ id: 'push_capacity_zero', severity: 'critical', card: 'poll', series: [] })
  }

  // Zero cycles is only a statement once the window could have held several.
  // Young window: expected and silent. Past the gate: the strongest single
  // thing this page can say, because nothing downstream runs without it.
  const polls = val(m, 'psp_poll_total')
  if (polls === 0 && mode === 'settled') {
    add({ id: 'poll_dead', severity: 'critical', card: 'poll', series: [], values: { window: m.window_ms } })
  }

  // --- observed non-zeros: never gated by the window ---

  const pollErrors = val(m, 'psp_poll_error_total')
  if (pollErrors > 0) {
    add({
      id: 'poll_errors', severity: 'error', card: 'poll', series: ['psp_poll_error_total'],
      values: { errors: pollErrors, polls },
    })
  }

  // Red, not amber: enable, expiry and the quota cap all travel this path, so
  // a failure can leave a user connected who should not be.
  const pushErrors = val(m, 'psp_push_client_config_error_total')
  const lifecycleErrors = val(m, 'psp_lifecycle_sync_error_total')
  if (lifecycleErrors > 0) {
    // The error count can never exceed the checks: the pool failure, the
    // earliest counted one, happens after the total is incremented
    // (sharedclient.pushLifecycle), so errors/checks is a real ratio.
    const checks = val(m, 'psp_lifecycle_sync_total')
    const stage = counterChildren(m, 'psp_lifecycle_sync_error_stage_total')
    const kind = counterChildren(m, 'psp_lifecycle_sync_error_panel_kind_total')
    add({
      id: 'lifecycle_errors', severity: 'error', card: 'lifecycle',
      series: ['psp_lifecycle_sync_error_total'], link: 'sync_tasks',
      values: { errors: lifecycleErrors, checks },
      ratio: { n: lifecycleErrors, d: checks },
      // With no refresh failure in the window, none of these came from the
      // refresh path. A refresh failure that DID happen is a write failure
      // too, so the sentence is offered only when it is true.
      variants: pushErrors === 0 ? ['origin'] : [],
      ...(stage.length > 0 || kind.length > 0 ? { breakdown: { stage, kind } } : {}),
    })
  }

  // Fewer polls than the interval allows. The traffic loop is a sequential
  // ticker that runs the poll and the rollup after it in one tick and drops
  // the ticks it overran (app.go runTrafficLoop), so a run of long polls loses
  // ticks rather than queueing them. Zero polls is poll_dead's; a manual
  // "poll now" only adds polls, so it can hide a shortfall but never invent
  // one. The tolerance, max(2, 5%), is on top of an already floored bound.
  const expected = expectedPollsMin(snap)
  if (expected !== null && mode === 'settled' && polls > 0 &&
      polls < expected - Math.max(2, Math.ceil(0.05 * expected))) {
    add({
      id: 'poll_behind', severity: 'warn', card: 'poll', series: [], link: 'settings',
      values: { polls, expected, missing: expected - polls },
    })
  }

  // Amber: while PSP runs, the next poll still suspends an over-quota user from
  // the real usage; the stale allowance only matters while PSP is offline.
  if (pushErrors > 0) {
    const attempted = val(m, 'psp_push_client_config_total')
    add({
      id: 'push_errors', severity: 'warn', card: 'floor',
      series: ['psp_push_client_config_error_total'], link: 'servers',
      values: { errors: pushErrors, attempted },
      ratio: { n: pushErrors, d: attempted },
    })
  }

  // One event, two counters: the polls that skipped their refreshes and the
  // refreshes skipped. Listed once so one backlog does not read as two faults.
  const carryover = val(m, 'psp_push_sem_carryover_total')
  const suppressed = val(m, 'psp_push_suppressed_total')
  if (carryover > 0 || suppressed > 0) {
    add({
      id: 'push_backlog', severity: 'warn', card: 'floor',
      series: ['psp_push_sem_carryover_total'], link: 'servers',
      values: { cycles: carryover, suppressed },
    })
  }

  // Never softened for S-UI: the same count also holds panels that should
  // have answered and did not. The S-UI sentence only explains the growth.
  const incomplete = val(m, 'psp_live_ip_users_incomplete_total')
  if (incomplete > 0) {
    add({
      id: 'liveip_incomplete', severity: 'warn', card: 'liveip',
      series: ['psp_live_ip_users_incomplete_total'], link: 'servers',
      values: { incomplete },
      variants: hasPanelKind(facts, 'sui') ? ['sui'] : [],
    })
  }

  // The next four are failures that otherwise exist only as a Warn log: the
  // poll and the request that hit them carry on, so nothing else on the admin
  // surface shows them.

  // A poll whose judged connections were not written leaves a gap in the
  // connection history that is never backfilled.
  const historyErrors = val(m, 'psp_connection_history_write_errors_total')
  if (historyErrors > 0) {
    add({
      id: 'history_write_errors', severity: 'warn', card: 'liveip',
      series: ['psp_connection_history_write_errors_total'],
      values: { errors: historyErrors },
    })
  }

  const flagErrors = val(m, 'psp_flag_record_write_errors_total')
  if (flagErrors > 0) {
    add({
      id: 'flag_write_errors', severity: 'warn', card: 'liveip',
      series: ['psp_flag_record_write_errors_total'],
      values: { errors: flagErrors },
    })
  }

  // Only the two failure outcomes: everything else the suspension does is
  // already counted by the bell.
  const suspendError = 'psp_geo_auto_suspension_total{outcome=suspend_error}'
  const liftError = 'psp_geo_auto_suspension_total{outcome=lift_error}'
  const suspendFailed = val(m, suspendError)
  const liftFailed = val(m, liftError)
  if (suspendFailed + liftFailed > 0) {
    add({
      id: 'geo_auto_errors', severity: 'warn', card: 'liveip',
      series: [suspendError, liftError], link: 'risk',
      values: { suspend: suspendFailed, lift: liftFailed },
    })
  }

  // An incompatible protocol generation gets every sync from that node
  // refused, which makes it offline in all but name.
  const refused = counterFamilyTotal(m, 'psp_node_sync_refused_total')
  if (refused > 0) {
    add({
      id: 'node_sync_refused', severity: 'warn', card: 'node',
      series: ['psp_node_sync_refused_total'], link: 'servers',
      values: {
        count: refused,
        proto: val(m, 'psp_node_sync_refused_total{reason=protocol_generation}'),
        invalid: val(m, 'psp_node_sync_refused_total{reason=report_invalid}'),
      },
    })
  }

  // A deliberate fail-open in the SSO rules: with no claim, the stored role
  // and group are kept, so someone the directory has already demoted keeps
  // their access here.
  const silent = counterFamilyTotal(m, 'psp_sso_claim_silent_total')
  if (silent > 0) {
    add({
      id: 'sso_claim_silent', severity: 'warn', card: 'sso',
      series: ['psp_sso_claim_silent_total'], link: 'settings',
      values: {
        count: silent,
        role: val(m, 'psp_sso_claim_silent_total{kind=role}'),
        group: val(m, 'psp_sso_claim_silent_total{kind=group}'),
      },
    })
  }

  // A standing condition, recounted at every check while it lasts: a notice,
  // which never colours the page.
  const gaps = counterFamilyTotal(m, 'psp_capability_gap_total')
  if (gaps > 0) {
    add({
      id: 'capability_gaps', severity: 'notice', card: 'lifecycle',
      series: ['psp_capability_gap_total'], link: 'servers',
      values: {
        ip: val(m, 'psp_capability_gap_total{capability=client.iplimit}'),
        device: val(m, 'psp_capability_gap_total{capability=client.devicelimit}'),
      },
    })
  }

  const rank = (id: string) => FINDING_ORDER.indexOf(id as FindingId)
  return out.sort((a, b) => SEVERITY_ORDER[a.severity] - SEVERITY_ORDER[b.severity] || rank(a.id) - rank(b.id))
}

// ---------------------------------------------------------------------------
// self-checks — about the statistics, not about the fleet
// ---------------------------------------------------------------------------

export interface SelfCheck {
  id: 'poll_ms' | 'push_enqueue'
  pass: boolean
  values: Record<string, number>
}

/**
 * Cross-metric identities that should hold. Listed whether they pass or not,
 * and never raised as findings: a mismatch says the counters disagree, which
 * is a question about the statistics, not evidence that the fleet is unwell.
 *
 * Take() is not atomic across metrics, so a cycle in flight can split a pair by
 * one. A disagreement of 1 is therefore noise, not a mismatch.
 */
export function deriveSelfChecks(m: MetricsSnapshot): SelfCheck[] {
  // The duration is recorded in a defer of PollOnce, so the two drift apart by
  // more than one only when more than one poll was running when this was read.
  const polls = val(m, 'psp_poll_total')
  const pollMs = histogram(m, 'psp_poll_ms')
  const observed = pollMs?.count ?? 0
  const pollPass = !(pollMs && polls > 0 && Math.abs(observed - polls) > 1)

  // Every started refresh was queued first, so only MORE started than queued
  // is a disagreement; fewer is a refresh still waiting for a slot.
  const enqueued = val(m, 'psp_poll_floor_push_enqueued_total')
  const started = val(m, 'psp_push_client_config_total')

  return [
    { id: 'poll_ms', pass: pollPass, values: { observed, expected: polls } },
    { id: 'push_enqueue', pass: started - enqueued <= 1, values: { started, enqueued } },
  ]
}

// ---------------------------------------------------------------------------
// verdict
// ---------------------------------------------------------------------------

export type Verdict = 'problems' | 'blackout' | 'measuring' | 'ok'

/**
 * Findings outrank the window. A short window means a zero proves nothing — it
 * does not mean an error that already happened is unproven, and letting
 * MEASURING outrank PROBLEMS would hide findings during exactly the incident
 * the page is for.
 */
export function verdict(findings: Finding[], mode: WindowMode): Verdict {
  if (findings.length > 0) return 'problems'
  if (mode === 'blackout') return 'blackout'
  if (mode === 'measuring') return 'measuring'
  return 'ok'
}
