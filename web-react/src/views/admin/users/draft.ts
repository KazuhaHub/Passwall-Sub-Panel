import type { CreateUserRequest, ResetPeriod, Role, User } from '@/api/types'
import type { UpdateUserRequest } from '@/api/users'
import { accountEnabledForEdit } from '@/utils/userAccess'
import { validateEmail, validateGroupId, validateName, validatePassword, validateRequired } from '@/utils/validators'
import { bytesToGB } from './model'

/**
 * What the create form and the edit form hold. The three limits are `number | undefined`: undefined is "follow
 * the group", a number is this user's own value, and 0 is a real answer (unlimited) that can never be written
 * by leaving a box empty. Create has no way to say "unlimited regardless of the group" (the backend reads 0 as
 * "follow"), which the form says next to the limits.
 */
export interface UserDraft {
  upn: string
  display_name: string
  email: string
  /** Create only: blank means the server generates one. */
  password: string
  group_id: number | ''
  role: Role
  /** A date is read as the end of that day in the panel's time zone (sent as `expire_date`); permanent sends no expiry. */
  expire_mode: 'date' | 'permanent'
  expire_date: string
  traffic: number | undefined
  ip: number | undefined
  device: number | undefined
  reset_period: ResetPeriod
  remark: string
  /** Edit only. Seeded from the usage leaderboard; moving it pushes a synthetic baseline, so the next poll's delta starts from it. */
  period_used: number
  period_used_initial: number
  /** Edit only: the account switch, written through its own endpoint and only when it changed. */
  enabled: boolean
}

export type DraftField = 'upn' | 'display_name' | 'email' | 'password' | 'group_id' | 'expire_date'

export function blankDraft(groupId: number | '', expireDate: string): UserDraft {
  return {
    upn: '', display_name: '', email: '', password: '', group_id: groupId, role: 'user',
    expire_mode: 'date', expire_date: expireDate,
    traffic: undefined, ip: undefined, device: undefined,
    reset_period: 'monthly', remark: '',
    period_used: 0, period_used_initial: 0, enabled: true,
  }
}

/** Seeds the edit form from a row. Shared by "open" and "reload saved values", so the two cannot drift. */
export function draftOfUser(u: User, usedGB: number): UserDraft {
  return {
    upn: u.upn, display_name: u.display_name ?? '', email: u.email ?? '', password: '',
    group_id: u.group_id, role: u.role,
    expire_mode: u.expire_at ? 'date' : 'permanent',
    // The panel-time-zone calendar day the backend computed, not the raw instant: slicing the UTC date off
    // expire_at would show the wrong day in zones west of UTC.
    expire_date: u.expire_date ?? '',
    traffic: u.inherits_traffic_limit ? undefined : bytesToGB(u.traffic_limit_bytes),
    ip: u.inherits_ip_limit ? undefined : u.ip_limit,
    device: u.inherits_device_limit ? undefined : u.device_limit,
    reset_period: u.traffic_reset_period, remark: u.remark ?? '',
    period_used: usedGB, period_used_initial: usedGB,
    enabled: accountEnabledForEdit(u),
  }
}

/** Sub-1 MB jitter is no edit: opening the form and saving must not synthesise a baseline snapshot. */
const USED_EPSILON = 0.001
export const periodUsedMoved = (d: UserDraft) => Math.abs(d.period_used - d.period_used_initial) > USED_EPSILON

/** Compared field by field; the period-used box is compared with what it started from, not with the row, which a late leaderboard may move. */
export function isDirty(d: UserDraft, base: UserDraft): boolean {
  const { period_used: _u, period_used_initial: _i, ...a } = d
  const { period_used: _bu, period_used_initial: _bi, ...b } = base
  void _u; void _i; void _bu; void _bi
  return JSON.stringify(a) !== JSON.stringify(b) || periodUsedMoved(d)
}

/** The problems with a draft, as keys of the admin namespace; a field that is fine is absent. */
export function draftProblems(d: UserDraft, mode: 'create' | 'edit'): Partial<Record<DraftField, string>> {
  const all: Record<DraftField, string> = {
    // The UPN is the login name and the subscription salt: at creation it must look like an email, so password-reset
    // and emergency-access mail reach someone.
    upn: mode === 'create' ? validateEmail(d.upn, { required: true }) : validateRequired(d.upn.trim()),
    display_name: validateName(d.display_name, { max: 64 }),
    email: validateEmail(d.email),
    // Optional at creation (the server generates one when empty), but a typed one is held to the floor.
    password: mode === 'create' && d.password ? validatePassword(d.password, { strong: true }) : '',
    group_id: validateGroupId(d.group_id, { required: true }),
    expire_date: d.expire_mode === 'date' ? validateRequired(d.expire_date) : '',
  }
  return Object.fromEntries(Object.entries(all).filter(([, v]) => v !== '')) as Partial<Record<DraftField, string>>
}

export function createRequest(d: UserDraft): CreateUserRequest {
  return {
    upn: d.upn,
    email: d.email || undefined,
    display_name: d.display_name || undefined,
    password: d.password || undefined,
    // validateGroupId proves this is a number; the draft keeps '' for the empty state only.
    group_id: d.group_id as number,
    // The picked calendar day; the backend resolves it to end-of-day in the panel time zone. Permanent sends nothing.
    expire_date: d.expire_mode === 'date' ? d.expire_date : undefined,
    traffic_limit_gb: d.traffic ?? 0,
    ip_limit: d.ip ?? 0,
    device_limit: d.device ?? 0,
    traffic_reset_period: d.reset_period,
    remark: d.remark || undefined,
  }
}

/**
 * The profile save. A limit is sent as its value OR as the inherit flag, never the value while inheriting: the
 * number then shown is the GROUP's, and echoing it back would detach the user from the policy unasked. The UPN
 * is sent only when it changed, so an unrelated edit cannot rewrite a legacy non-canonical one.
 */
export function updateRequest(d: UserDraft, user: Pick<User, 'upn'>): UpdateUserRequest {
  const req: UpdateUserRequest = {
    group_id: d.group_id as number, email: d.email,
    traffic_reset_period: d.reset_period,
    remark: d.remark, display_name: d.display_name,
    role: d.role,
    ...(d.traffic === undefined ? { inherit_traffic_limit: true } : { traffic_limit_gb: d.traffic }),
    ...(d.ip === undefined ? { inherit_ip_limit: true } : { ip_limit: d.ip }),
    ...(d.device === undefined ? { inherit_device_limit: true } : { device_limit: d.device }),
  }
  if (d.upn !== user.upn) req.upn = d.upn
  if (d.expire_mode === 'permanent') req.clear_expire = true
  // The bare YYYY-MM-DD; the backend anchors it to end-of-day in the panel time zone, so the day cannot drift with the browser's.
  else if (d.expire_date) req.expire_date = d.expire_date
  return req
}
