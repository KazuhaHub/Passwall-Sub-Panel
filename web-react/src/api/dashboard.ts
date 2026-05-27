import { client } from './client'

export interface DashboardExpiringRow {
  id: number
  upn: string
  display_name?: string
  // Always present — backend filters out users with nil ExpireAt
  // before appending to this list. Declared non-optional so the
  // DashboardView call sites don't need non-null assertions.
  expire_at: string
}

export interface DashboardNodeAlert {
  id: number
  display_name: string
  panel_name: string
  health_state: string
}

export interface DashboardSummary {
  user_total: number
  user_enabled: number
  user_disabled: number
  user_emergency: number
  node_total: number
  node_enabled: number
  node_healthy: number
  group_count: number
  expiring_users: DashboardExpiringRow[]
  node_alerts: DashboardNodeAlert[]
}

export async function dashboardSummary(): Promise<DashboardSummary> {
  const { data } = await client.get<DashboardSummary>('/admin/dashboard/summary')
  return data
}
