import type {
  CounterSnapshot,
  DiagnosticsSnapshot,
  GaugeSnapshot,
  HistogramSnapshot,
  MetricsSnapshot,
} from '@/api/diagnostics'

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

export type Severity = 'critical' | 'error' | 'warn'

export interface Finding {
  id: string
  severity: Severity
  /** i18n key suffix under admin:diagnostics.findings */
  key: string
  values?: Record<string, string | number>
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

/** Value or 0. Use only where absence and zero are genuinely the same. */
function val(m: MetricsSnapshot, name: string): number {
  return counter(m, name)?.value ?? 0
}

// ---------------------------------------------------------------------------
// window
// ---------------------------------------------------------------------------

/** Windows shorter than this many intervals leave zeros inconclusive. */
export const SETTLED_INTERVALS = 3

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

const SEVERITY_ORDER: Record<Severity, number> = { critical: 0, error: 1, warn: 2 }

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
 *  - Geo verdicts (psp_geo_verdict_total). No response to a flagged account has
 *    been built yet, so a finding here would carry no remedy — and an all-idle
 *    fleet at 04:00 is the healthiest reading the detector can produce, not a
 *    blind one.
 */
export function deriveFindings(
  snap: DiagnosticsSnapshot,
  pollIntervalMs: number,
): Finding[] {
  const m = snap.metrics
  const mode = windowMode(m.window_ms, pollIntervalMs)
  const out: Finding[] = []

  const capacity = gauge(m, 'psp_push_sem_capacity')
  if (capacity !== undefined && capacity.value <= 0) {
    out.push({ id: 'push_capacity_zero', severity: 'critical', key: 'push_capacity_zero' })
  }

  // Zero cycles is only a statement once the window could have held several.
  // Young window: expected and silent. Past the gate: the strongest single
  // thing this page can say, because nothing downstream runs without it.
  const polls = val(m, 'psp_poll_total')
  if (polls === 0 && mode === 'settled') {
    out.push({
      id: 'poll_dead',
      severity: 'critical',
      key: 'poll_dead',
      values: { minutes: Math.round(m.window_ms / 60_000) },
    })
  }

  // --- observed non-zeros: never gated by the window ---

  const pushErrors = val(m, 'psp_push_client_config_error_total')
  if (pushErrors > 0) {
    out.push({
      id: 'push_errors',
      severity: 'error',
      key: 'push_errors',
      values: { errors: pushErrors, attempted: val(m, 'psp_push_client_config_total') },
    })
  }

  const pollErrors = val(m, 'psp_poll_error_total')
  if (pollErrors > 0) {
    out.push({ id: 'poll_errors', severity: 'error', key: 'poll_errors', values: { errors: pollErrors } })
  }

  const lifecycleErrors = val(m, 'psp_lifecycle_sync_error_total')
  if (lifecycleErrors > 0) {
    out.push({
      id: 'lifecycle_errors',
      severity: 'error',
      key: 'lifecycle_errors',
      values: { errors: lifecycleErrors },
    })
  }

  const suppressed = val(m, 'psp_push_suppressed_total')
  if (suppressed > 0) {
    out.push({ id: 'push_suppressed', severity: 'warn', key: 'push_suppressed', values: { suppressed } })
  }

  const carryover = val(m, 'psp_push_sem_carryover_total')
  if (carryover > 0) {
    out.push({ id: 'push_carryover', severity: 'warn', key: 'push_carryover', values: { carryover } })
  }

  const gaps = counterFamilyTotal(m, 'psp_capability_gap_total')
  if (gaps > 0) {
    out.push({ id: 'capability_gaps', severity: 'warn', key: 'capability_gaps', values: { gaps } })
  }

  const incomplete = val(m, 'psp_live_ip_users_incomplete_total')
  if (incomplete > 0) {
    out.push({ id: 'liveip_incomplete', severity: 'warn', key: 'liveip_incomplete', values: { incomplete } })
  }

  out.push(...deriveInvariantViolations(m))

  return out.sort((a, b) => SEVERITY_ORDER[a.severity] - SEVERITY_ORDER[b.severity])
}

/**
 * Cross-metric identities that must hold. Rendered ONLY when violated: a
 * passing invariant is noise, and a page that lists its own self-checks
 * teaches the reader to skim past the section that matters.
 *
 * Take() is not atomic across metrics, so a cycle in flight can split a pair by
 * one. A disagreement of 1 is therefore noise, not a violation.
 */
export function deriveInvariantViolations(m: MetricsSnapshot): Finding[] {
  const out: Finding[] = []
  const polls = val(m, 'psp_poll_total')
  const pollMs = histogram(m, 'psp_poll_ms')
  if (pollMs && polls > 0 && Math.abs(pollMs.count - polls) > 1) {
    out.push({
      id: 'invariant_poll_ms',
      severity: 'warn',
      key: 'invariant_poll_ms',
      values: { observed: pollMs.count, expected: polls },
    })
  }

  // Every started push was enqueued first, so the enqueue count can never trail
  // the execution count.
  const enqueued = val(m, 'psp_poll_floor_push_enqueued_total')
  const started = val(m, 'psp_push_client_config_total')
  if (started - enqueued > 1) {
    out.push({
      id: 'invariant_push_enqueue',
      severity: 'warn',
      key: 'invariant_push_enqueue',
      values: { started, enqueued },
    })
  }
  return out
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
