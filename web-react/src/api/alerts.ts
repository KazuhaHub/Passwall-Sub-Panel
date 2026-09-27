import { client } from './client'
import type { ReadOptions } from './requestOptions'

export type AlertSeverity = 'error' | 'warning' | 'info'
export type AlertType =
  | 'node_health'
  | 'cert_failed'
  | 'cert_expiring'
  | 'panel_upgrade'
  | 'psp_upgrade'
  | 'login_security'
  // The location detector's two admin-only singletons, each with a `count`:
  // accounts whose flag is latched, and accounts it has auto-suspended.
  | 'geo_anomaly'
  | 'geo_auto_suspended'

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
export async function getAlerts(opts: ReadOptions = {}): Promise<AlertsResponse> {
  const { data } = await client.get<AlertsResponse>('/admin/alerts', {
    _skipErrorToast: true,
    signal: opts.signal,
  })
  return data
}
