import { client } from './client'

// Mirrors internal/pkg/metrics/registry.go. Field names are the Go JSON tags
// verbatim — a rename there silently produces `undefined` here, which would
// render as a missing metric rather than an error, so keep them in lockstep.

export interface CounterSnapshot {
  name: string
  help: string
  value: number
}

export interface GaugeSnapshot {
  name: string
  help: string
  value: number
  // Peak is the high-water mark since the window opened. It survives the
  // value falling back to 0, which is the whole reason a gauge is worth
  // reading after the fact: "queue is empty now" and "the queue was never
  // deep" are different claims.
  peak: number
}

export interface BucketSnapshot {
  le: number
  inf?: boolean
  count: number
}

export interface HistogramSnapshot {
  name: string
  help: string
  unit: string
  count: number
  sum: number
  mean: number
  max: number
  p50: number
  p90: number
  p95: number
  p99: number
  buckets: BucketSnapshot[]
}

export interface MetricsSnapshot {
  // since_unix_ms is when the measurement window opened: process start, or
  // the last explicit reset. Every counter is "since that moment".
  since_unix_ms: number
  window_ms: number
  counters: CounterSnapshot[]
  gauges: GaugeSnapshot[]
  histograms: HistogramSnapshot[]
}

export interface DiagnosticsSnapshot {
  version: string
  commit?: string
  // uptime_ms is PROCESS uptime and is NOT the same as metrics.window_ms:
  // an admin can reset the counters without restarting, after which the
  // window is shorter than the uptime. The gap is how the UI can tell a
  // fresh boot from a deliberate re-measurement.
  uptime_ms: number
  goroutines: number
  metrics: MetricsSnapshot
}

export interface ResetResult {
  reset: boolean
  // The snapshot as of just before the reset, so closing a window never
  // discards the measurement it closes.
  previous: MetricsSnapshot
}

export async function getDiagnostics(): Promise<DiagnosticsSnapshot> {
  const { data } = await client.get<DiagnosticsSnapshot>('/admin/diagnostics/metrics')
  return data
}

export async function resetDiagnostics(): Promise<ResetResult> {
  const { data } = await client.post<ResetResult>('/admin/diagnostics/metrics/reset')
  return data
}
