import type { AccountStatus, ServiceStatus, User } from '@/api/types'

/**
 * Compatibility readers for the backend-derived access snapshot. New servers
 * always send access; the flat status fallback keeps rolling upgrades safe
 * without re-implementing expiry or quota decisions in the browser.
 */
export function accountStateOf(user: User): AccountStatus {
  return user.access?.account_state ?? user.account_status ?? (user.enabled ? 'active' : 'disabled')
}

export function serviceStateOf(user: User): ServiceStatus {
  return user.access?.service_state ?? user.service_status ?? 'active'
}

export function accountEnabledForEdit(user: User): boolean {
  return user.access?.can_login ?? accountStateOf(user) === 'active'
}

export function proxyEnabled(user: User): boolean {
  return user.access?.proxy_enabled ?? ['active', 'emergency_active'].includes(serviceStateOf(user))
}
