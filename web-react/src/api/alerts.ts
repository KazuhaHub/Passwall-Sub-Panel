import { client } from './client'

export type AlertSeverity = 'error' | 'warning' | 'info'
export type AlertType =
  | 'node_health'
  | 'cert_failed'
  | 'cert_expiring'
  | 'panel_upgrade'
  | 'psp_upgrade'
  | 'login_security'

export interface Alert {
  key: string
  type: AlertType
  severity: AlertSeverity
  target_id?: number
  target_name?: string
  health_state?: string
  panel_name?: string
  last_error?: string
  current_version?: string
  latest_version?: string
  expire_at?: string
  count?: number
  since?: string
}

export interface AlertCounts {
  error: number
  warning: number
  info: number
}

export interface AlertsResponse {
  alerts: Alert[]
  counts: AlertCounts
}

// getAlerts fetches the unified notification feed. Skips the shared error toast
// — the bell polls quietly in the background and shouldn't pop a toast on a
// transient blip.
export async function getAlerts(): Promise<AlertsResponse> {
  const { data } = await client.get<AlertsResponse>('/admin/alerts', { _skipErrorToast: true })
  return data
}
