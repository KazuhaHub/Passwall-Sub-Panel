import { client } from './client'

/**
 * Node host resource telemetry.
 *
 * EVERY VALUE IS EITHER A NUMBER OR NULL, AND NULL IS NOT ZERO. The API omits
 * nothing: a metric the node could not read arrives as null so the chart can
 * break its line rather than draw a healthy-looking zero through an outage. Any
 * component that renders `?? 0` here is wrong, and the tests say so.
 */

/** The resource-health verdict for one node. Never collapsed with connectivity. */
export type NodeResourceHealth =
  | 'healthy'
  | 'warming_up'
  | 'warning'
  | 'critical'
  | 'stale'
  | 'unavailable'
  | 'unsupported'

/** How old the newest sample is, relative to the node's own cadence. */
export type NodeMetricsFreshness = 'fresh' | 'stale' | 'missing' | 'unsupported'

/** Which limit the CPU percentage is measured against. */
export type CPUScope = 'system' | 'cgroup_quota' | 'cgroup_cpuset'

/** Which total the memory percentage is measured against. */
export type MemoryScope = 'system' | 'cgroup'

export interface NodeMetricsScope {
  deployment: string
  /** `host`, `container`, `mixed` or `unknown`. A mixed scope must not be shown as host. */
  resource_scope: string
  cgroup_version: number
  data_filesystem_scope: string
}

export interface NodeMetricsSummary {
  cpu_percent: number | null
  cpu_scope: CPUScope | null
  system_cpu_percent: number | null
  cgroup_cpu_cores_percent: number | null
  cgroup_cpu_quota_percent: number | null
  cgroup_cpu_capacity_percent: number | null
  cgroup_cpu_throttled_period_percent: number | null
  memory_used_percent: number | null
  memory_scope: MemoryScope | null
  disk_used_percent: number | null
  rx_bps: number | null
  tx_bps: number | null
  tcp_retrans_percent: number | null
  core_rss_bytes: number | null
}

export interface NodeHealthFinding {
  code: string
  severity: 'warning' | 'critical'
  started_at: string
  current_value: number
  threshold: number
  unit: string
}

export interface NodeMetricsCurrent {
  available: boolean
  freshness: NodeMetricsFreshness
  received_at?: string
  collected_at?: string
  scope?: NodeMetricsScope
  summary: NodeMetricsSummary
  health?: { status: NodeResourceHealth; findings: NodeHealthFinding[] }
}

export interface NodeMetricsSeriesPoint extends Partial<NodeMetricsSummary> {
  at: string
  coverage_seconds: number
  disk_available_bytes?: number | null
  link_utilization_percent?: number | null
}

export interface NodeMetricsHistory {
  resolution: 'minute' | 'hour'
  from: string
  to: string
  series: NodeMetricsSeriesPoint[]
}

export interface NodeInterfaceSeriesPoint {
  at: string
  coverage_seconds: number
  rx_bps: number | null
  tx_bps: number | null
  link_utilization_percent: number | null
  error_ratio: number | null
}

export interface NodeInterfaceHistory {
  resolution: 'minute' | 'hour'
  interface: string
  from: string
  to: string
  series: NodeInterfaceSeriesPoint[]
}

export interface NodeHealthDetail {
  resource_health: NodeResourceHealth
  freshness: NodeMetricsFreshness
  findings: NodeHealthFinding[]
}

export type HistoryResolution = 'auto' | 'minute' | 'hour'

export async function getNodeMetricsCurrent(serverId: number, signal?: AbortSignal): Promise<NodeMetricsCurrent> {
  const { data } = await client.get(`/admin/servers/${serverId}/node-metrics/current`, { signal })
  return data
}

export async function getNodeMetricsHistory(
  serverId: number,
  from: string,
  to: string,
  resolution: HistoryResolution = 'auto',
  signal?: AbortSignal,
): Promise<NodeMetricsHistory> {
  const { data } = await client.get(`/admin/servers/${serverId}/node-metrics/history`, {
    params: { from, to, resolution },
    signal,
  })
  return data
}

export async function getNodeInterfaceHistory(
  serverId: number,
  iface: string,
  from: string,
  to: string,
  resolution: HistoryResolution = 'auto',
  signal?: AbortSignal,
): Promise<NodeInterfaceHistory> {
  const { data } = await client.get(`/admin/servers/${serverId}/node-metrics/interfaces`, {
    params: { from, to, resolution, interface: iface },
    signal,
  })
  return data
}

/** Opens the on-demand refresh window. Resolves immediately; the sample arrives on the next sync. */
export async function requestNodeMetricsRefresh(serverId: number): Promise<void> {
  await client.post(`/admin/servers/${serverId}/node-metrics/refresh`)
}

export async function getNodeHealth(serverId: number, signal?: AbortSignal): Promise<NodeHealthDetail> {
  const { data } = await client.get(`/admin/servers/${serverId}/node-health`, { signal })
  return data
}
