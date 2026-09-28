import type { AccountStatus, ServiceStatus, User } from '@/api/types'

/**
 * Compatibility readers for the backend-derived access snapshot. New servers
 * always send access; the flat status fallback keeps rolling upgrades safe
 * without re-implementing expiry or quota decisions in the browser.
 *
 * They take only the fields they read, so the risk center's account (which
 * carries `access` but not the whole user row) reads its states through the
 * same code as the Users page, and the two can never name one state two ways.
 */
export function accountStateOf(user: Pick<User, 'access' | 'account_status' | 'enabled'>): AccountStatus {
  return user.access?.account_state ?? user.account_status ?? (user.enabled ? 'active' : 'disabled')
}

export function serviceStateOf(user: Pick<User, 'access' | 'service_status'>): ServiceStatus {
  return user.access?.service_state ?? user.service_status ?? 'active'
}

export function accountEnabledForEdit(user: User): boolean {
  return user.access?.can_login ?? accountStateOf(user) === 'active'
}

export function proxyEnabled(user: User): boolean {
  return user.access?.proxy_enabled ?? ['active', 'emergency_active'].includes(serviceStateOf(user))
}
