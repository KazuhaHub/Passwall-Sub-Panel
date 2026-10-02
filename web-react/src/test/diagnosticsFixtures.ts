import type {
  BucketSnapshot,
  CounterSnapshot,
  DiagnosticsSnapshot,
  GaugeSnapshot,
  HistogramSnapshot,
  MetricsSnapshot,
} from '@/api/diagnostics'

// Registry readings for the diagnostics page's view tests, shaped exactly as
// GET /api/admin/diagnostics/metrics returns them.

export const c = (name: string, value: number, help = ''): CounterSnapshot => ({ name, help, value })
export const g = (name: string, value: number, peak = value): GaugeSnapshot => ({ name, help: '', value, peak })

export function hist(name: string, over: Partial<HistogramSnapshot> = {}): HistogramSnapshot {
  return {
    name, help: '', unit: 'ms', count: 0, sum: 0, mean: 0, max: 0, p50: 0, p90: 0, p95: 0, p99: 0, buckets: [],
    ...over,
  }
}

export const POLL_INTERVAL_MS = 120_000

/**
 * The production reading the redesign was drawn from: two and a half days of
 * a two-minute poll, 17 status writes failed and no quota refresh did. The
 * window is 59 h 8 min rather than a round 59 h so that the conservative
 * lower bound on polls, floor((window − 60 s) / interval), is the 1,773 the
 * poll card's caption quotes.
 */
export const PROD_WINDOW_MS = 212_880_000
export const PROD_SINCE_MS = Date.UTC(2026, 8, 28, 22, 52, 0)

export const POLL_BUCKETS: BucketSnapshot[] = [
  { le: 500, count: 400 },
  { le: 1000, count: 1500 },
  { le: 5000, count: 1770 },
  { le: 10000, count: 1774 },
  { le: 0, inf: true, count: 1774 },
]

export function productionMetrics(over: Partial<MetricsSnapshot> = {}): MetricsSnapshot {
  return {
    since_unix_ms: PROD_SINCE_MS,
    window_ms: PROD_WINDOW_MS,
    counters: [
      c('psp_poll_total', 1774, 'Traffic poll cycles started.'),
      c('psp_poll_error_total', 0),
      c('psp_lifecycle_sync_total', 900),
      c('psp_lifecycle_sync_skipped_total', 860),
      c('psp_lifecycle_sync_write_total', 40),
      c('psp_lifecycle_sync_not_provisioned_total', 0),
      c('psp_lifecycle_sync_error_total', 17),
      c('psp_lifecycle_sync_write_reason_total{reason=total_gb}', 30),
      c('psp_lifecycle_sync_write_reason_total{reason=enable}', 10),
      c('psp_push_client_config_total', 3334),
      c('psp_push_client_config_error_total', 0),
      c('psp_poll_floor_push_enqueued_total', 3334),
      c('psp_push_sem_carryover_total', 0),
      c('psp_push_suppressed_total', 0),
      c('psp_live_ip_users_incomplete_total', 0),
      c('psp_panel_op_total{op=ListInboundsSlim}', 1774),
      c('psp_panel_op_total{op=GetClient}', 120),
      c('psp_panel_op_error_total{op=GetClient}', 4),
    ],
    gauges: [
      g('psp_poll_interval_ms', POLL_INTERVAL_MS),
      g('psp_push_sem_capacity', 8),
      g('psp_push_sem_inflight', 0, 8),
      g('psp_push_sem_waiting', 0, 1),
    ],
    histograms: [
      hist('psp_poll_ms', {
        count: 1774, sum: 1774 * 900, mean: 900, max: 9100, p50: 835, p90: 2100, p95: 3260, p99: 7800,
        buckets: POLL_BUCKETS,
      }),
      hist('psp_poll_stage_ms{stage=panel_fetch}', { count: 1774, sum: 1774 * 655, mean: 655, p50: 600, p95: 2000 }),
      hist('psp_user_live_ips', { unit: 'ips', count: 1774 * 6, sum: 1774 * 6 * 2, mean: 2, max: 9, p50: 2, p95: 4 }),
      hist('psp_node_host_snapshot_bytes', { unit: 'bytes', count: 0 }),
    ],
    ...over,
  }
}

export function productionSnapshot(over: Partial<MetricsSnapshot> = {}): DiagnosticsSnapshot {
  const metrics = productionMetrics(over)
  return { version: 'v4.0.1.18', commit: '53045c9c1f2e', uptime_ms: metrics.window_ms, goroutines: 87, metrics }
}

export interface ServerRow { id: number; name: string; panel_type: string }

export function serverList(items: ServerRow[], total = items.length) {
  return { items, total, page: 1, page_size: 200 }
}

export const THREE_XUI: ServerRow = { id: 1, name: 'a', panel_type: '3xui' }
export const NATIVE: ServerRow = { id: 2, name: 'b', panel_type: 'psp' }
export const SUI: ServerRow = { id: 3, name: 'c', panel_type: 'sui' }
