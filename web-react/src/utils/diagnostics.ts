import type {
  CounterSnapshot,
  DiagnosticsSnapshot,
  GaugeSnapshot,
  HistogramSnapshot,
  MetricsSnapshot,
} from '@/api/diagnostics'
import {
  CARD_ORDER,
  FAMILY_CATALOG,
  STAGE_GROUP,
  STAGE_GROUP_ORDER,
  WRITE_REASON_GROUP,
  WRITE_REASON_GROUP_ORDER,
  familyOf,
  type CardId,
  type FindingLink,
  type MetricType,
  type StageGroup,
  type Translate,
  type WriteReasonGroup,
} from './diagnosticsCatalog'

export type { FindingLink } from './diagnosticsCatalog'

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
  /**
   * Children of the labelled families that explain the count, largest first,
   * keyed by the copy's own placeholder (see BREAKDOWN_LABELS).
   */
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

/** What a histogram can honestly be said to show. */
export type QuantileReading =
  | { kind: 'none' }
  /** Under MIN_QUANTILE_SAMPLES: the longest sample is the only true figure. */
  | { kind: 'few'; count: number; max: number }
  /**
   * over: the p95 lies beyond the last finite bucket, where interpolation has
   * no upper bound but the observed max, so the estimate is pulled toward it.
   * The number is then withheld and only the ceiling is stated (for the
   * latency buckets that ceiling is 10 s).
   */
  | { kind: 'ok'; p50: number; p95: number; over: boolean; ceiling: number }

export function quantileReading(h: HistogramSnapshot | undefined): QuantileReading {
  if (!h || h.count === 0) return { kind: 'none' }
  if (!quantileUsable(h)) return { kind: 'few', count: h.count, max: h.max }
  const ceiling = h.buckets.reduce((top, b) => (b.inf ? top : Math.max(top, b.le)), 0)
  return { kind: 'ok', p50: h.p50, p95: h.p95, over: ceiling > 0 && h.p95 > ceiling, ceiling }
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
 * The sentences each finding may show between its impact and its action, in
 * order. Two kinds: conditional ones apply to a reading only when the finding
 * carries them (origin and sui in `variants`, breakdown when it has one); the
 * rest always show. The copy test reads this table, so a part added here
 * without copy in both bundles fails.
 */
export const FINDING_PARTS: Record<FindingId, readonly string[]> = {
  push_capacity_zero: [],
  poll_dead: [],
  poll_errors: [],
  lifecycle_errors: ['origin', 'breakdown', 'retry'],
  poll_behind: [],
  push_errors: ['retry'],
  push_backlog: [],
  liveip_incomplete: ['sui'],
  history_write_errors: [],
  flag_write_errors: [],
  geo_auto_errors: [],
  node_sync_refused: [],
  sso_claim_silent: [],
  capability_gaps: ['note'],
}

const CONDITIONAL_PARTS = new Set(['origin', 'sui', 'breakdown'])

/** The log keywords each finding offers, and whether it explains them. */
export const FINDING_LOGS: Record<FindingId, readonly ('log' | 'log_note')[]> = {
  push_capacity_zero: [],
  poll_dead: ['log'],
  poll_errors: ['log'],
  lifecycle_errors: ['log', 'log_note'],
  poll_behind: [],
  push_errors: ['log'],
  push_backlog: [],
  liveip_incomplete: ['log'],
  history_write_errors: ['log'],
  flag_write_errors: ['log'],
  geo_auto_errors: ['log'],
  node_sync_refused: ['log'],
  sso_claim_silent: [],
  capability_gaps: ['log'],
}

/** The parts of FINDING_PARTS that apply to this reading of the finding. */
export function findingParts(f: Finding): string[] {
  const parts = FINDING_PARTS[f.id as FindingId] ?? []
  return parts.filter(part => {
    if (!CONDITIONAL_PARTS.has(part)) return true
    if (part === 'breakdown') return f.breakdown !== undefined
    return f.variants?.includes(part) ?? false
  })
}

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
    const breakdown = lifecycleErrorBreakdown(m)
    add({
      id: 'lifecycle_errors', severity: 'error', card: 'lifecycle',
      series: ['psp_lifecycle_sync_error_total'], link: 'sync_tasks',
      values: { errors: lifecycleErrors, checks },
      ratio: { n: lifecycleErrors, d: checks },
      // With no refresh failure in the window, none of these came from the
      // refresh path. A refresh failure that DID happen is a write failure
      // too, so the sentence is offered only when it is true.
      variants: pushErrors === 0 ? ['origin'] : [],
      // Both or nothing: the sentence names both lists, and the registry reads
      // each counter on its own, so a reading taken between a failure's step
      // child and its kind child being counted can hold one without the other.
      // The next refresh has both.
      ...(breakdown.stages.length > 0 && breakdown.kinds.length > 0 ? { breakdown } : {}),
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
  // their access here. Counted PER KIND: one sign-in with both rule sets and
  // neither attribute adds to role and to group (user.reconcileSSOUser), so
  // `count` is the family total for the export and the copy never calls it a
  // number of sign-ins.
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
  // is a disagreement; fewer is a refresh still waiting for a slot. A clear
  // taken while refreshes were queued splits each of them across it (queued
  // before, started after, traffic.go then user.PushClientConfig), so a
  // window a clear opened can carry that excess for its whole life; the
  // copy says so rather than calling it a bug.
  const enqueued = val(m, 'psp_poll_floor_push_enqueued_total')
  const started = val(m, 'psp_push_client_config_total')

  return [
    { id: 'poll_ms', pass: pollPass, values: { observed, expected: polls } },
    { id: 'push_enqueue', pass: started - enqueued <= 1, values: { started, enqueued } },
  ]
}

// ---------------------------------------------------------------------------
// cards — one state per area, from a closed vocabulary
// ---------------------------------------------------------------------------

/**
 * The closed vocabulary every card badge renders through, so no card can draw
 * "cannot tell" as "fine". `ok` claims only that the card's driver moved; it
 * never claims health, because a finding can accuse but nothing here can
 * acquit.
 */
export type CardState =
  /** Worst finding on the card is critical or error. */
  | 'failing'
  /** Worst finding on the card is a warning. */
  | 'attention'
  | 'ok'
  /** Settled window, nothing found, and the driver never moved. */
  | 'idle'
  /** Blackout, or a young window in which the driver has not moved yet. */
  | 'measuring'
  /** Its upstream (the traffic poll) is not running, so a zero says nothing. */
  | 'inhibited'
  /** Proven from a complete server list. Never inferred from a zero. */
  | 'not_applicable'
  /** Single sign-on only: no refusal and no finding in the window. */
  | 'none'
  /** Single sign-on only: SAML refusals were recorded but none is a finding. */
  | 'recorded'

export interface CardSummary {
  id: CardId
  state: CardState
  /** The card also has notice-level findings: one extra line, no colour. */
  notices: boolean
  /** The count whose movement separates ok from idle or measuring. */
  driver: number
}

/** Activity on any psp_node_* series: a counter that moved or a histogram
 *  with samples. The unlabelled node histograms are registered at start, so
 *  their mere presence is not activity. */
function nodeActivity(m: MetricsSnapshot): number {
  const counters = m.counters
    .filter(c => c.name.startsWith('psp_node_'))
    .reduce((sum, c) => sum + c.value, 0)
  const samples = m.histograms
    .filter(h => h.name.startsWith('psp_node_'))
    .reduce((sum, h) => sum + h.count, 0)
  return counters + samples
}

function cardDriver(m: MetricsSnapshot, id: CardId): number {
  switch (id) {
    case 'poll': return val(m, 'psp_poll_total')
    case 'lifecycle': return val(m, 'psp_lifecycle_sync_total')
    case 'floor': return val(m, 'psp_push_client_config_total')
    case 'panel_api': return counterFamilyTotal(m, 'psp_panel_op_total')
    case 'liveip': return histogram(m, 'psp_user_live_ips')?.count ?? 0
    case 'node':
      return counterFamilyTotal(m, 'psp_node_host_report_total') + counterFamilyTotal(m, 'psp_node_sync_refused_total')
    case 'sso': return counterFamilyTotal(m, 'psp_saml_acs_failure_total')
  }
}

/**
 * Not in use, proven: the server list is complete, holds no panel of the
 * kind the card measures, AND the card has seen no activity. Activity always
 * wins over the list, which may be stale by a minute.
 */
function provenNotApplicable(m: MetricsSnapshot, id: CardId, facts: PanelFacts): boolean {
  if (!facts.complete) return false
  if (id === 'panel_api') return !facts.types.has('3xui') && counterFamilyTotal(m, 'psp_panel_op_total') === 0
  if (id === 'node') return !facts.types.has('psp') && nodeActivity(m) === 0
  return false
}

/** Cards whose only input is the traffic poll's work. The status sync is not
 *  one: admin actions, sync tasks and the periodic repair also drive it. */
const POLL_FED_CARDS: ReadonlySet<CardId> = new Set(['floor', 'liveip'])

/**
 * One state per card, in this precedence: the card's own findings; proven not
 * in use; undecidable because the poll is not running; then the window.
 * Findings come first because an observed failure is a fact even on a card
 * whose upstream has stopped.
 */
export function deriveCards(
  snap: DiagnosticsSnapshot,
  pollIntervalMs: number,
  findings: Finding[],
  facts: PanelFacts = UNKNOWN_FACTS,
): CardSummary[] {
  const m = snap.metrics
  const mode = windowMode(m.window_ms, pollIntervalMs)
  const upstreamDown = findings.some(f => f.id === 'poll_dead' || f.id === 'push_capacity_zero')

  return CARD_ORDER.map(id => {
    const own = findings.filter(f => f.card === id)
    const notices = own.some(f => f.severity === 'notice')
    const driver = cardDriver(m, id)
    const state = ((): CardState => {
      if (own.some(f => f.severity === 'critical' || f.severity === 'error')) return 'failing'
      if (own.some(f => f.severity === 'warn')) return 'attention'
      // A refusal is a record, not a rate: the window cannot make one more or
      // less meaningful, so this card skips every gate below.
      if (id === 'sso') return driver > 0 ? 'recorded' : 'none'
      if (provenNotApplicable(m, id, facts)) return 'not_applicable'
      if (upstreamDown && POLL_FED_CARDS.has(id)) return 'inhibited'
      if (mode === 'blackout') return 'measuring'
      if (mode === 'measuring') return driver > 0 ? 'ok' : 'measuring'
      return driver > 0 ? 'ok' : 'idle'
    })()
    return { id, state, notices, driver }
  })
}

/** The sentence a card shows under its title, as a key under admin:diagnostics. */
export function cardSentenceKey(card: CardSummary, mode: WindowMode, intervalKnown: boolean): string {
  switch (card.state) {
    case 'failing':
    case 'attention':
    case 'inhibited':
      return `cards.common.${card.state}`
    case 'measuring':
      if (mode === 'blackout') return 'cards.common.blackout'
      return intervalKnown ? 'cards.common.measuring' : 'cards.common.measuring_unknown'
    case 'none':
    case 'recorded':
      return `cards.sso.${card.state}`
    default:
      return `cards.${card.id}.${card.state}`
  }
}

// ---------------------------------------------------------------------------
// page verdict — one line, coloured by the worst finding
// ---------------------------------------------------------------------------

export type VerdictTone = 'critical' | 'action' | 'attention' | 'blackout' | 'measuring' | 'ok'

export interface PageVerdict {
  tone: VerdictTone
  /** Key suffix under admin:diagnostics.verdict. */
  key: 'critical' | 'action' | 'attention' | 'blackout' | 'measuring' | 'measuring_unknown' | 'ok' | 'ok_notices'
  /** The cards the verdict names, in page order. */
  cards: CardId[]
  /** measuring only: time until the window settles. */
  remainingMs?: number
  notices: number
}

/**
 * Findings outrank the window, and the colour is the WORST finding's: a page
 * with only warnings is amber, never red. A short window means a zero proves
 * nothing; it does not unprove an error that already happened, and letting it
 * outrank one would hide findings during exactly the incident the page is for.
 * Only a settled window with nothing found reads "no problems found", which is
 * all the page can say: findings accuse, nothing here acquits.
 */
export function pageVerdict(
  findings: Finding[],
  cards: CardSummary[],
  windowMs: number,
  pollIntervalMs: number,
): PageVerdict {
  const notices = findings.filter(f => f.severity === 'notice').length
  const cardsWhere = (pred: (c: CardSummary) => boolean) => cards.filter(pred).map(c => c.id)

  if (findings.some(f => f.severity === 'critical')) {
    const named = new Set(findings.filter(f => f.severity === 'critical').map(f => f.card))
    return { tone: 'critical', key: 'critical', cards: CARD_ORDER.filter(id => named.has(id)), notices }
  }
  if (findings.some(f => f.severity === 'error')) {
    return { tone: 'action', key: 'action', cards: cardsWhere(c => c.state === 'failing'), notices }
  }
  if (findings.some(f => f.severity === 'warn')) {
    return { tone: 'attention', key: 'attention', cards: cardsWhere(c => c.state === 'attention'), notices }
  }
  const mode = windowMode(windowMs, pollIntervalMs)
  if (mode === 'blackout') return { tone: 'blackout', key: 'blackout', cards: [], notices }
  if (mode === 'measuring') {
    return pollIntervalMs > 0
      ? {
          tone: 'measuring', key: 'measuring', cards: [], notices,
          remainingMs: Math.max(0, SETTLED_INTERVALS * pollIntervalMs - windowMs),
        }
      : { tone: 'measuring', key: 'measuring_unknown', cards: [], notices }
  }
  return { tone: 'ok', key: notices > 0 ? 'ok_notices' : 'ok', cards: [], notices }
}

// ---------------------------------------------------------------------------
// session delta — "is it still happening?", within what this page has seen
// ---------------------------------------------------------------------------

export type SessionDelta =
  /** No second reading yet, or too soon after the first to say "none". */
  | { kind: 'wait' }
  /** The window was reopened (restart or reset): start a new baseline. */
  | { kind: 'rebuild' }
  | { kind: 'grew'; count: number; minutes: number }
  | { kind: 'flat'; minutes: number }

/**
 * Growth of a finding's series since the page took its baseline. A hint, not
 * a verdict: it never changes a severity, because two people opening the page
 * at different times must see the same colours. Elapsed time is taken from the
 * server's own window, not the browser clock, and a shrinking total (a reset
 * by someone else that kept since_unix_ms aside) is read as a new baseline
 * rather than shown as a negative.
 */
export function sessionDelta(
  base: MetricsSnapshot | undefined,
  cur: MetricsSnapshot,
  series: readonly string[],
): SessionDelta {
  if (!base) return { kind: 'wait' }
  if (base.since_unix_ms !== cur.since_unix_ms) return { kind: 'rebuild' }
  const elapsed = cur.window_ms - base.window_ms
  if (elapsed <= 0) return { kind: 'wait' }
  const sum = (m: MetricsSnapshot) => series.reduce((total, name) => total + counterFamilyTotal(m, name), 0)
  const count = sum(cur) - sum(base)
  if (count < 0) return { kind: 'rebuild' }
  const minutes = Math.max(1, Math.round(elapsed / 60_000))
  if (count > 0) return { kind: 'grew', count, minutes }
  // "No new ones" needs at least a refresh interval behind it to mean anything.
  return elapsed < 60_000 ? { kind: 'wait' } : { kind: 'flat', minutes }
}

// ---------------------------------------------------------------------------
// formatting
// ---------------------------------------------------------------------------

const FMT = 'admin:diagnostics.fmt'

/** A count with grouping, as the reader's locale writes it. */
export function formatCount(n: number, lang: string): string {
  return new Intl.NumberFormat(lang).format(n)
}

/** An API value as delivered: grouped, never rounded. For the raw area. */
export function formatExact(n: number, lang: string): string {
  return new Intl.NumberFormat(lang, { maximumFractionDigits: 20 }).format(n)
}

/**
 * A share. Zero attempts is not 0%: there is no rate, and saying so is the
 * difference between "nothing failed" and "nothing was tried". Bands are cut
 * after rounding, so a value never prints in the next band's format
 * ("10.0%", "1.00%").
 */
export function formatPct(n: number, d: number, t: Translate): string {
  if (!(d > 0)) return t(`${FMT}.no_denominator`)
  const p = (n / d) * 100
  if (p === 0) return '0%'
  if (p >= 9.95) return `${Math.round(p)}%`
  if (p >= 0.995) return `${p.toFixed(1)}%`
  if (p >= 0.01) return `${p.toFixed(2)}%`
  return t(`${FMT}.pct_tiny`)
}

/** Latency: whole ms under a second, then seconds to two places, then one.
 *  SI symbols in both languages, one space before the unit. */
export function formatLatency(ms: number): string {
  if (Math.round(ms) < 1000) return `${Math.round(ms)} ms`
  const s = ms / 1000
  return Number(s.toFixed(2)) < 10 ? `${s.toFixed(2)} s` : `${s.toFixed(1)} s`
}

/**
 * A span of time in the reader's language. Each band is chosen after
 * rounding into it, so 59.6 s reads "1 min" rather than "60 s".
 */
export function formatDuration(ms: number, t: Translate): string {
  const safe = Number.isFinite(ms) && ms > 0 ? ms : 0
  const s = Math.round(safe / 1000)
  if (s < 60) return t(`${FMT}.duration_s`, { s })
  const min = Math.round(safe / 60_000)
  if (min < 60) return t(`${FMT}.duration_m`, { m: min })
  if (min < 24 * 60) return t(`${FMT}.duration_hm`, { h: Math.floor(min / 60), m: min % 60 })
  const hours = Math.floor(min / 60)
  return t(`${FMT}.duration_dh`, { d: Math.floor(hours / 24), h: hours % 24 })
}

/** Bytes in 1024 steps, three significant figures above a KiB. */
export function formatBytes(n: number): string {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let v = Math.max(0, n)
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  if (i === 0) return `${Math.round(v)} B`
  const digits = v < 10 ? 2 : v < 100 ? 1 : 0
  return `${v.toFixed(digits)} ${units[i]}`
}

/** A rate (per hour, per poll): whole above 10, finer below. */
export function formatRate(r: number, lang: string): string {
  const digits = r >= 10 ? 0 : r >= 0.1 ? 1 : 2
  return new Intl.NumberFormat(lang, { maximumFractionDigits: digits }).format(r)
}

/** value per hour of window, or null when the window is empty. */
export function ratePerHour(value: number, windowMs: number): number | null {
  return windowMs > 0 ? value / (windowMs / 3_600_000) : null
}

/** value per poll, or null when no poll has run. */
export function ratePerPoll(value: number, polls: number): number | null {
  return polls > 0 ? value / polls : null
}

/** The label group (admin:diagnostics.labels.<group>) for each breakdown
 *  placeholder a finding's copy uses. */
export const BREAKDOWN_LABELS: Record<string, string> = {
  stages: 'lifecycle_stage',
  kinds: 'panel_kind',
}

/**
 * One breakdown as text: each item as "label count", joined with the same
 * middle dot the cards use between figures. `label` resolves a value in a
 * label group, falling back to the raw value (diagnosticsCatalog.labelFor).
 * The card lines and the finding sentence both go through here, so the two
 * can never write the same reading two ways.
 */
export function formatBreakdownList(
  items: readonly LabelCount[],
  group: string,
  label: (group: string, value: string) => string,
  count: (n: number) => string,
): string {
  return items.map(item => `${label(group, item.value)} ${count(item.count)}`).join(' · ')
}

/** The breakdown sentence's values, one formatted list per placeholder. */
export function breakdownInterpolation(
  f: Finding,
  label: (group: string, value: string) => string,
  count: (n: number) => string,
): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [key, items] of Object.entries(f.breakdown ?? {})) {
    out[key] = formatBreakdownList(items, BREAKDOWN_LABELS[key] ?? key, label, count)
  }
  return out
}

// ---------------------------------------------------------------------------
// lifecycle failure breakdown — where the status write failures happened
// ---------------------------------------------------------------------------

// A type rather than an interface so it fits Finding.breakdown's record.
export type LifecycleErrorBreakdown = {
  /** By the step that failed (labels.lifecycle_stage). */
  stages: LabelCount[]
  /** By the kind of panel the client is on (labels.panel_kind). */
  kinds: LabelCount[]
}

/**
 * The two breakdowns of psp_lifecycle_sync_error_total, non-zero children
 * largest first. The server counts every failure in exactly one child of each
 * (sharedclient.countLifecycleFailure), so either list sums to the total;
 * both are empty on a server that predates the breakdown and right after a
 * clear. The finding and the user status sync card both read it from here,
 * so they can never list different steps for one reading.
 */
export function lifecycleErrorBreakdown(m: MetricsSnapshot): LifecycleErrorBreakdown {
  return {
    stages: counterChildren(m, 'psp_lifecycle_sync_error_stage_total'),
    kinds: counterChildren(m, 'psp_lifecycle_sync_error_panel_kind_total'),
  }
}

export interface BreakdownLine {
  /** Key under admin:diagnostics; the copy takes the list as {{list}}. */
  key: string
  /** Label group (admin:diagnostics.labels.<group>) naming the items. */
  group: string
  items: LabelCount[]
}

/**
 * The user status sync card's breakdown lines, the step first because it is
 * what says where to look. Unlike the finding's one sentence, each line stands
 * alone, so an empty list is simply left out rather than printed as "by step:"
 * with nothing after it.
 */
export function lifecycleCardBreakdown(m: MetricsSnapshot): BreakdownLine[] {
  const { stages, kinds } = lifecycleErrorBreakdown(m)
  const lines: BreakdownLine[] = [
    { key: 'cards.lifecycle.errors_by_stage', group: BREAKDOWN_LABELS.stages, items: stages },
    { key: 'cards.lifecycle.errors_by_kind', group: BREAKDOWN_LABELS.kinds, items: kinds },
  ]
  return lines.filter(line => line.items.length > 0)
}

export interface FindingFormatters {
  count: (n: number) => string
  duration: (ms: number) => string
  pct: (n: number, d: number) => string
}

/**
 * The interpolation values for a finding's copy. Every value is a count
 * except `window` (a duration); `pct` is derived from the ratio, never stored,
 * so the export carries the raw numerator and denominator.
 */
export function findingInterpolation(f: Finding, fmt: FindingFormatters): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(f.values ?? {})) {
    out[k] = k === 'window' ? fmt.duration(v) : fmt.count(v)
  }
  if (f.ratio) out.pct = fmt.pct(f.ratio.n, f.ratio.d)
  return out
}

// ---------------------------------------------------------------------------
// page models — what the cards and the raw area render, kept pure
// ---------------------------------------------------------------------------

export interface PanelOpRow {
  op: string
  requests: number
  errors: number
  rtt?: HistogramSnapshot
}

/**
 * The three 3X-UI request families joined by operation, busiest first and
 * ties by name, so a refresh does not reshuffle equal rows. An operation
 * that shows up in only one family (a failure with no success yet, a
 * latency recorded before its count was read) is still a row: the registry
 * reads each series on its own, and dropping the row would hide a request
 * the panel was asked for.
 */
export function panelOpRows(m: MetricsSnapshot): PanelOpRow[] {
  const rows = new Map<string, PanelOpRow>()
  const row = (op: string) => {
    let r = rows.get(op)
    if (!r) {
      r = { op, requests: 0, errors: 0 }
      rows.set(op, r)
    }
    return r
  }
  for (const c of m.counters) {
    const { family, value } = familyOf(c.name)
    if (value === undefined) continue
    if (family === 'psp_panel_op_total') row(value).requests += c.value
    else if (family === 'psp_panel_op_error_total') row(value).errors += c.value
  }
  for (const h of m.histograms) {
    const { family, value } = familyOf(h.name)
    if (family === 'psp_panel_rtt_ms' && value !== undefined) row(value).rtt = h
  }
  return [...rows.values()].sort((a, b) => b.requests - a.requests || a.op.localeCompare(b.op))
}

/**
 * Native-node host metric saves that failed, and how many of those carried a
 * history sample. NOT a sum of the two storage_error children: one failed
 * persist counts once under psp_node_host_report_total and, when a history
 * sample was due, once more under psp_node_host_history_total, in the same
 * error branch (nodemetrics.Ingest). The history child is a subset of the
 * report child, so adding them shows one failed save as two.
 */
export function nodeSaveFailures(m: MetricsSnapshot): { failures: number; withHistory: number } {
  return {
    failures: val(m, 'psp_node_host_report_total{outcome=storage_error}'),
    withHistory: val(m, 'psp_node_host_history_total{outcome=storage_error}'),
  }
}

export interface StageGroupTime {
  group: StageGroup
  /** Mean milliseconds per poll spent in the group's stages. */
  avgMs: number
}

/**
 * Where an average poll's time goes, by the five groups an operator can act
 * on. Each stage histogram's SUM is the stage's total time, so a group's
 * share of an average poll is the sum of its stages' sums over the number of
 * polls timed. Never an average of quantiles: those do not add up. A stage
 * the catalogue does not group is left out here (diagnosticsCatalog.test.ts
 * fails until it is placed) and still shows in the raw area.
 */
export function stageGroups(m: MetricsSnapshot): StageGroupTime[] | null {
  const polls = histogram(m, 'psp_poll_ms')?.count ?? 0
  if (!(polls > 0)) return null
  const totals = new Map<StageGroup, number>(STAGE_GROUP_ORDER.map(g => [g, 0]))
  for (const h of m.histograms) {
    const { family, value } = familyOf(h.name)
    if (family !== 'psp_poll_stage_ms' || value === undefined) continue
    const group = STAGE_GROUP[value]
    if (group) totals.set(group, (totals.get(group) ?? 0) + h.sum)
  }
  return STAGE_GROUP_ORDER.map(group => ({ group, avgMs: (totals.get(group) ?? 0) / polls }))
}

export interface WriteReasonGroupCount {
  group: WriteReasonGroup
  count: number
  /** The raw reasons behind the count, largest first. */
  reasons: LabelCount[]
}

/** The lifecycle's write reasons folded into the five groups the card lists. */
export function writeReasonGroups(m: MetricsSnapshot): WriteReasonGroupCount[] {
  const children = counterChildren(m, 'psp_lifecycle_sync_write_reason_total')
  return WRITE_REASON_GROUP_ORDER.map(group => {
    const reasons = children.filter(r => WRITE_REASON_GROUP[r.value] === group)
    return { group, count: reasons.reduce((sum, r) => sum + r.count, 0), reasons }
  })
}

export interface BucketRow {
  le: number
  inf: boolean
  cumulative: number
  /** Samples in this bucket alone: the cumulative count minus the previous. */
  count: number
}

/** A histogram's cumulative buckets with each bucket's own count beside it. */
export function bucketRows(h: HistogramSnapshot): BucketRow[] {
  let prev = 0
  return h.buckets.map(b => {
    const out = { le: b.le, inf: !!b.inf, cumulative: b.count, count: b.count - prev }
    prev = b.count
    return out
  })
}

export interface RawSeries {
  /** The full series name, `family{label=value}` for a child. */
  name: string
  label?: string
  value?: string
  counter?: CounterSnapshot
  gauge?: GaugeSnapshot
  histogram?: HistogramSnapshot
}

export interface RawFamily {
  family: string
  /** 'other' for a family the catalogue does not know yet. */
  card: CardId | 'other'
  type: MetricType
  labelled: boolean
  /** The family's one series when unlabelled, its children when labelled. */
  series: RawSeries[]
  /** The server's help string, from the first series that carries one. */
  help: string
  /** Catalogued but missing from this reading. */
  absent: boolean
}

/**
 * Every metric family the raw area shows: the catalogue's, in its order, and
 * then every family the server sent that the catalogue does not know, by
 * name. The union is the point. Walking only the catalogue would drop a
 * family Go added after this page was built; walking only the reading would
 * drop the explanation of an absence. A catalogued family the reading lacks
 * is kept, marked absent: for an unlabelled family that means an older
 * server, for a labelled one that nothing has happened yet.
 */
export function rawFamilies(m: MetricsSnapshot): RawFamily[] {
  const seen = new Map<string, RawFamily>()
  const add = (name: string, type: MetricType, s: Omit<RawSeries, 'name' | 'label' | 'value'>, help: string) => {
    const parsed = familyOf(name)
    let f = seen.get(parsed.family)
    if (!f) {
      const info = FAMILY_CATALOG[parsed.family]
      f = {
        family: parsed.family,
        card: info?.card ?? 'other',
        type: info?.type ?? type,
        labelled: info?.labelled ?? parsed.value !== undefined,
        series: [],
        help: '',
        absent: false,
      }
      seen.set(parsed.family, f)
    }
    f.series.push({ name, label: parsed.label, value: parsed.value, ...s })
    if (!f.help && help) f.help = help
  }
  for (const c of m.counters) add(c.name, 'counter', { counter: c }, c.help)
  for (const g of m.gauges) add(g.name, 'gauge', { gauge: g }, g.help)
  for (const h of m.histograms) add(h.name, 'histogram', { histogram: h }, h.help)

  const catalogued = Object.entries(FAMILY_CATALOG).map(([family, info]): RawFamily =>
    seen.get(family) ?? { family, card: info.card, type: info.type, labelled: info.labelled, series: [], help: '', absent: true })
  const unknown = [...seen.values()]
    .filter(f => !(f.family in FAMILY_CATALOG))
    .sort((a, b) => a.family.localeCompare(b.family))
  return [...catalogued, ...unknown]
}

/** The families present in a reading and the series they hold. */
export function rawSummary(m: MetricsSnapshot): { families: number; series: number } {
  const families = new Set<string>()
  for (const s of [...m.counters, ...m.gauges, ...m.histograms]) families.add(familyOf(s.name).family)
  return { families: families.size, series: m.counters.length + m.gauges.length + m.histograms.length }
}

/** Whether a series has recorded anything. A gauge's peak counts: a queue
 *  that is empty now but was eight deep is not "zero". */
export function seriesNonZero(s: RawSeries): boolean {
  if (s.counter) return s.counter.value !== 0
  if (s.gauge) return s.gauge.value !== 0 || s.gauge.peak !== 0
  if (s.histogram) return s.histogram.count > 0
  return false
}

export interface RawFilter {
  /** Matched case-insensitively; empty matches everything. */
  query: string
  nonZero: boolean
  /**
   * The words a reader sees for a family, or for one of its children: the
   * translated names and descriptions in the current language. The filter
   * matches them too, so a search for what the page calls a row finds it.
   */
  words: (f: RawFamily, s?: RawSeries) => string[]
}

/**
 * The raw area's search and non-zero filter. A family that matches by its
 * own name, help or words keeps every (remaining) child; otherwise only the
 * children that match are kept. An absent family has nothing non-zero, so
 * it survives only an all-values search that names it.
 */
export function filterRawFamilies(families: RawFamily[], filter: RawFilter): RawFamily[] {
  const q = filter.query.trim().toLowerCase()
  const hit = (texts: Array<string | undefined>) => texts.some(t => !!t && t.toLowerCase().includes(q))
  const out: RawFamily[] = []
  for (const f of families) {
    if (f.absent) {
      if (!filter.nonZero && (q === '' || hit([f.family, ...filter.words(f)]))) out.push(f)
      continue
    }
    let series = filter.nonZero ? f.series.filter(seriesNonZero) : f.series
    if (q !== '' && !hit([f.family, f.help, ...filter.words(f)])) {
      series = series.filter(s => hit([s.name, s.value, ...filter.words(f, s)]))
    }
    if (series.length > 0) out.push({ ...f, series })
  }
  return out
}
