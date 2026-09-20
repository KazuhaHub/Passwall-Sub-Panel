// Shared per-group scope-override metadata + load/save logic (v3.8.0 §6.3).
//
// Extracted from GroupsView so the same editor can drive both the group detail
// "Policies" tab and the Settings scope rail. The SCOPE_KEYS table is the
// frontend mirror of the backend allowlist (ports.OverridableScopeKeys /
// admin_scope_settings.go) — a key the backend stops advertising is filtered
// out at render time via scope.overridable, so a stale entry here degrades
// silently rather than letting a non-overridable write through.
import { deleteGroupScopeOverride, getGroupScopeSettings, setGroupScopeOverride } from '@/api/scopeSettings'
import { getUISettings, type UISettings } from '@/api/settings'

export type ScopeKind = 'bool' | 'int' | 'float' | 'str'

export interface ScopeCategoryMeta {
  id: string
  /** i18n key suffix under `admin:groups.scope.` */
  labelKey: string
  def: string
}

export interface ScopeKeyMeta {
  cat: string
  /** "type.name" — the backend override key. */
  key: string
  type: string
  name: string
  kind: ScopeKind
  /** Matching global UISettings field, used to read the inherited baseline. */
  field: keyof UISettings
  /** i18n key suffix under `admin:groups.scope.` */
  labelKey: string
  def: string
}

export const SCOPE_CATEGORIES: ScopeCategoryMeta[] = [
 { id: 'brand', labelKey: 'cat_brand', def: '页面展示' },
  { id: '2fa', labelKey: 'cat_2fa', def: '两步验证 (2FA) 方式' },
  { id: 'notify', labelKey: 'cat_notify', def: '通知阈值' },
  { id: 'emergency', labelKey: 'cat_emergency', def: '紧急访问（超额救急）' },
  { id: 'login', labelKey: 'cat_login', def: '登录与自助策略' },
  { id: 'sub', labelKey: 'cat_sub', def: '订阅策略' },
]

export const SCOPE_KEYS: ScopeKeyMeta[] = [
 { cat: 'brand', key: 'site.version_display', type: 'site', name: 'version_display', kind: 'str', field: 'version_display', labelKey: 'version_display', def: '版本号显示位置' },
  { cat: '2fa', key: 'security.totp_enabled', type: 'security', name: 'totp_enabled', kind: 'bool', field: 'totp_enabled', labelKey: 'totp', def: '验证器 App (TOTP)' },
  { cat: '2fa', key: 'security.passkey_enabled', type: 'security', name: 'passkey_enabled', kind: 'bool', field: 'passkey_enabled', labelKey: 'passkey', def: '通行密钥' },
  { cat: '2fa', key: 'security.twofa_allow_email', type: 'security', name: 'twofa_allow_email', kind: 'bool', field: 'twofa_allow_email', labelKey: 'email', def: '邮箱验证码' },
  { cat: 'notify', key: 'notify.expire_before_days', type: 'notify', name: 'expire_before_days', kind: 'int', field: 'expire_before_days', labelKey: 'expire_before', def: '到期前提醒（天）' },
  { cat: 'notify', key: 'notify.traffic_remain_percent', type: 'notify', name: 'traffic_remain_percent', kind: 'int', field: 'traffic_remain_percent', labelKey: 'traffic_remain', def: '剩余流量提醒（%）' },
  { cat: 'emergency', key: 'security.emergency_access_enabled', type: 'security', name: 'emergency_access_enabled', kind: 'bool', field: 'emergency_access_enabled', labelKey: 'em_enabled', def: '启用紧急访问' },
  { cat: 'emergency', key: 'security.emergency_access_hours', type: 'security', name: 'emergency_access_hours', kind: 'int', field: 'emergency_access_hours', labelKey: 'em_hours', def: '单次时长（小时）' },
  { cat: 'emergency', key: 'security.emergency_access_max_count', type: 'security', name: 'emergency_access_max_count', kind: 'int', field: 'emergency_access_max_count', labelKey: 'em_max_count', def: '可用次数' },
  { cat: 'emergency', key: 'security.emergency_access_quota_gb', type: 'security', name: 'emergency_access_quota_gb', kind: 'float', field: 'emergency_access_quota_gb', labelKey: 'em_quota_gb', def: '额外流量额度（GB）' },
  { cat: 'login', key: 'auth.disallow_user_password_change', type: 'auth', name: 'disallow_user_password_change', kind: 'bool', field: 'disallow_user_password_change', labelKey: 'disallow_pwd_change', def: '禁止用户自助改密码' },
  { cat: 'login', key: 'runtime.allow_user_personal_rules', type: 'runtime', name: 'allow_user_personal_rules', kind: 'bool', field: 'allow_user_personal_rules', labelKey: 'allow_personal_rules', def: '允许用户自定义规则' },
  { cat: 'sub', key: 'sub.sub_update_interval_hours', type: 'sub', name: 'sub_update_interval_hours', kind: 'int', field: 'sub_update_interval_hours', labelKey: 'sub_update_interval', def: '订阅更新间隔（小时）' },
  { cat: 'sub', key: 'sub.sub_profile_name_template', type: 'sub', name: 'sub_profile_name_template', kind: 'str', field: 'sub_profile_name_template', labelKey: 'sub_profile_name', def: '配置名模板' },
  { cat: 'sub', key: 'sub.sub_region_flag_prefix', type: 'sub', name: 'sub_region_flag_prefix', kind: 'bool', field: 'sub_region_flag_prefix', labelKey: 'sub_region_flag', def: '节点名加地区旗帜' },
  { cat: 'sub', key: 'sub.sub_block_auto_disable', type: 'sub', name: 'sub_block_auto_disable', kind: 'bool', field: 'sub_block_auto_disable', labelKey: 'sub_block_auto', def: '违规客户端自动停用' },
  { cat: 'sub', key: 'sub.sub_block_auto_disable_count', type: 'sub', name: 'sub_block_auto_disable_count', kind: 'int', field: 'sub_block_auto_disable_count', labelKey: 'sub_block_count', def: '自动停用阈值（次）' },
  { cat: 'sub', key: 'sub.sub_block_notify_user', type: 'sub', name: 'sub_block_notify_user', kind: 'bool', field: 'sub_block_notify_user', labelKey: 'sub_block_notify', def: '违规时通知用户' },
  { cat: 'sub', key: 'sub.sub_block_notify_max_per_day', type: 'sub', name: 'sub_block_notify_max_per_day', kind: 'int', field: 'sub_block_notify_max_per_day', labelKey: 'sub_block_notify_max', def: '每日通知上限（封）' },
]

// edit[key].on distinguishes "overridden" (sparse row exists) from "inherit"
// (no row → falls back to the global value). value is the raw KV string
// ("1"/"0" for bools, a number string for ints/floats).
export interface ScopeState {
  /** "type.name" keys the backend currently allows this scope to override. */
  overridable: string[]
  /** "type.name" -> raw global (inherited) KV value, the baseline. */
  global: Record<string, string>
  /** "type.name" -> raw value of overrides as loaded (for diff-on-save). */
  orig: Record<string, string>
  /** "type.name" -> editor state. */
  edit: Record<string, { on: boolean; value: string }>
}

export function kvFromGlobal(kind: ScopeKind, v: unknown): string {
  return kind === 'bool' ? (v ? '1' : '0') : String(v ?? '')
}

// loadScopeState fetches a group's sparse overrides + the global baseline and
// merges them into an editable ScopeState. Throws on API failure; callers
// decide whether to degrade (hide the section) or surface the error.
export async function loadScopeState(groupId: number, signal?: AbortSignal): Promise<ScopeState> {
  const [ss, gs] = await Promise.all([getGroupScopeSettings(groupId, signal), getUISettings()])
  const global: Record<string, string> = {}
  const edit: Record<string, { on: boolean; value: string }> = {}
  for (const k of SCOPE_KEYS) {
    global[k.key] = kvFromGlobal(k.kind, gs[k.field])
    const ov = ss.overrides[k.key]
    edit[k.key] = ov !== undefined ? { on: true, value: ov } : { on: false, value: global[k.key] }
  }
  return { overridable: ss.overridable, global, orig: ss.overrides, edit }
}

// saveScopeState diffs the editor state against the originally loaded overrides
// and persists the delta: PUT changed/new, DELETE those flipped back to
// inherit. Keys outside the backend's overridable allowlist are skipped
// (defense in depth). Throws on the first failed write so callers can warn;
// backend writes are idempotent, so reopening re-syncs the real state.
export async function saveScopeState(groupId: number, scope: ScopeState): Promise<void> {
  for (const k of SCOPE_KEYS) {
    if (!scope.overridable.includes(k.key)) continue
    const st = scope.edit[k.key]
    const wasOverridden = scope.orig[k.key] !== undefined
    if (st.on) {
      if (!wasOverridden || scope.orig[k.key] !== st.value) {
        await setGroupScopeOverride(groupId, k.type, k.name, st.value)
      }
    } else if (wasOverridden) {
      await deleteGroupScopeOverride(groupId, k.type, k.name)
    }
  }
}
