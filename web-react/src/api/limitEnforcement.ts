import { client } from './client'

/**
 * What one of a user's panels does with the connection caps PSP pushes there.
 *
 * Mirrors userLimitPanelDTO in internal/transport/http/handler/admin_user_limits.go;
 * field names are the Go json tags verbatim.
 */
export interface LimitPanel {
  panel_id: number
  name?: string
  kind?: string
  /** Panel-side client rows this user has HERE. Each carries its own copy of the caps. */
  clients: number
  can_store_ip: boolean
  can_store_device: boolean
  /** Registered but the pool would not hand over an adapter. */
  panel_unreachable?: boolean
  /** The client exists but its panel row is gone. */
  panel_missing?: boolean
  /**
   * The node's fail2ban verdict. Always present, "unknown" included — an
   * unread precondition must never render as a working cap.
   */
  ip_limit_enforcement: string
  ip_limit_probed_at?: string
}

export interface LimitEnforcement {
  ip_limit: number
  device_limit: number
  /**
   * False on every panel version today. Sent as a field rather than assumed
   * so the day it becomes true is a server change, not a UI hunt.
   */
  device_limit_enforced_anywhere: boolean
  panels: LimitPanel[]
}

export async function getLimitEnforcement(userID: number): Promise<LimitEnforcement> {
  const { data } = await client.get<LimitEnforcement>(`/admin/users/${userID}/limit-enforcement`)
  return data
}
