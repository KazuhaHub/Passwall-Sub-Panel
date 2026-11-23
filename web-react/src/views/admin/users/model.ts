import type { User } from '@/api/types'
import { accountEnabledForEdit, accountStateOf, serviceStateOf } from '@/utils/userAccess'

/** The columns the server can order the list by (`sort_by`); anything else in the URL reads as the default. */
export const SORT_KEYS = ['id', 'upn', 'display_name', 'group_id', 'role', 'expire_at', 'enabled', 'last_online_at'] as const
export type SortKey = (typeof SORT_KEYS)[number]
export const DEFAULT_SORT = 'id-desc'

/** `?sort=upn-asc` → the column and direction; a column the server does not order by, or no direction, reads as the default. */
export function parseSort(raw: string): { by: SortKey; dir: 'asc' | 'desc' } {
  const [by, dir] = raw.split('-')
  const known = (SORT_KEYS as readonly string[]).includes(by)
  return known ? { by: by as SortKey, dir: dir === 'asc' ? 'asc' : 'desc' } : { by: 'id', dir: 'desc' }
}

export function bytesToGB(b: number): number { return Math.round((b / 1024 / 1024 / 1024) * 100) / 100 }

/**
 * "n days from now" as a local YYYY-MM-DD string, the create form's default expiry. The exact day is
 * resolved against the panel time zone server-side; this is only the date picker's pre-fill.
 */
export function dateInNDays(n: number): string {
  const d = new Date(Date.now() + n * 86400000)
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${d.getFullYear()}-${m}-${day}`
}

/**
 * Fingerprint of the fields the edit form seeds from a row. Comparing it lets the form notice that the STORED
 * row moved while it was open without diffing whole objects: the backend attaches fields (usage, access, poll
 * timestamps) that change constantly and mean nothing to this form.
 */
export function rowFingerprint(u: User): string {
  return JSON.stringify([
    u.upn, u.display_name ?? '', u.email ?? '', u.role, u.group_id,
    u.traffic_limit_bytes, u.expire_date ?? '', u.expire_at ?? '',
    u.ip_limit, u.device_limit, accountEnabledForEdit(u),
    !!u.inherits_traffic_limit, !!u.inherits_ip_limit, !!u.inherits_device_limit,
    u.traffic_reset_period, u.remark ?? '',
  ])
}

export function canQuickRenew(u: User): boolean { return !!u.expire_at && accountStateOf(u) !== 'pending_delete' }
/** A user waiting for deletion is out of every batch: the delete is already under way. */
export function canSelect(u: User): boolean { return accountStateOf(u) !== 'pending_delete' }

export function canResumeService(u: User): boolean {
  return ['blocked_client', 'manual_suspended'].includes(serviceStateOf(u))
    || ['blocked_client', 'service_manual'].includes(u.service_disabled_reason || '')
}
export function canSuspendService(u: User): boolean {
  return accountStateOf(u) === 'active' && serviceStateOf(u) === 'active'
}

/** The expiry a renewal lands on: `days` after the later of now and the current expiry. */
export function renewedExpireAt(u: User, days: number): string {
  const now = Date.now()
  const current = u.expire_at ? new Date(u.expire_at).getTime() : 0
  const base = Number.isFinite(current) && current > now ? current : now
  return new Date(base + days * 86400000).toISOString()
}

/**
 * Mirrors the backend's ensureOperatorAllowed: an operator may act on role=user accounts only. An
 * administrator may act on anyone.
 */
export function canManageUser(target: Pick<User, 'role'>, canElevate: boolean): boolean {
  return canElevate || target.role === 'user'
}

/** The people a batch names in its confirmation: the first five, then "+N". */
export function namesOf(rows: readonly User[]): { names: string; suffix: string } {
  return {
    names: rows.slice(0, 5).map(r => r.display_name || r.upn).join('、'),
    suffix: rows.length > 5 ? ` +${rows.length - 5}` : '',
  }
}
