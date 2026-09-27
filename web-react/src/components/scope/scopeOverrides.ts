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

// 'enum' is purely a rendering concept: the backend stores the raw string
// (geo_anomaly.scope is a plain string setting) and repairs a bad value when
// it judges, so the select just keeps a typo from being typed.
export type ScopeKind = 'bool' | 'int' | 'float' | 'str' | 'enum'

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
  /** 'enum' only: the values offered; labelKey is under `admin:groups.scope.`. */
  options?: { value: string; labelKey: string; def: string }[]
  /** 'enum' only: the value the server uses for an unset (''). Shown for an
   *  inherited '' and seeded when an override is switched on from one. */
  enumDefault?: string
  /**
   * The shipped default a stored '' or '0' stands for. Some settings read 0 as
   * "never configured", not as zero (the geo tolerances do), and showing a
   * group admin "global: 0" would tell them the fleet tolerates nothing.
   */
  unsetValue?: string
  /** 'str' only: a multi-line value (one entry per line). */
  multiline?: boolean
}

export const SCOPE_CATEGORIES: ScopeCategoryMeta[] = [
  { id: '2fa', labelKey: 'cat_2fa', def: '两步验证 (2FA) 方式' },
  { id: 'notify', labelKey: 'cat_notify', def: '通知阈值' },
  { id: 'emergency', labelKey: 'cat_emergency', def: '紧急访问（超额救急）' },
  { id: 'login', labelKey: 'cat_login', def: '登录与自助策略' },
  { id: 'sub', labelKey: 'cat_sub', def: '订阅策略' },
  // Detection and the optional auto-suspension are separate categories so the
  // settings rail can show them as the two decisions they are: how strict to
  // judge, and whether the panel may act on a judgement by itself.
  { id: 'geo', labelKey: 'cat_geo', def: '异地并发检测' },
  { id: 'geo_ban', labelKey: 'cat_geo_ban', def: '异地并发 · 自动临时暂停' },
]

const GEO_SCOPE_OPTIONS: NonNullable<ScopeKeyMeta['options']> = [
  { value: 'city', labelKey: 'geo_scope_city', def: '城市·分级' },
  { value: 'region', labelKey: 'geo_scope_region', def: '省 / 州' },
  { value: 'country', labelKey: 'geo_scope_country', def: '仅国家' },
  { value: 'off', labelKey: 'geo_scope_off', def: '关闭' },
]

export const SCOPE_KEYS: ScopeKeyMeta[] = [
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
  // Concurrent-location detection. The unsetValues are domain.DefaultGeoPolicy:
  // a stored 0 means "shipped default" there, never zero tolerance.
  // geo_anomaly.ignore_addresses is deliberately absent — it names fleet
  // infrastructure and is global only (the backend refuses a group override).
  // min_placed_ratio stays API-only: a database-quality guard, not a
  // population's tolerance.
  { cat: 'geo', key: 'geo_anomaly.scope', type: 'geo_anomaly', name: 'scope', kind: 'enum', field: 'geo_anomaly_scope', labelKey: 'geo_scope', def: '判定粒度', options: GEO_SCOPE_OPTIONS, enumDefault: 'city' },
  { cat: 'geo', key: 'geo_anomaly.max_places', type: 'geo_anomaly', name: 'max_places', kind: 'int', field: 'geo_anomaly_max_places', labelKey: 'geo_max_countries', def: '国家容错', unsetValue: '1' },
  { cat: 'geo', key: 'geo_anomaly.max_regions', type: 'geo_anomaly', name: 'max_regions', kind: 'int', field: 'geo_anomaly_max_regions', labelKey: 'geo_max_regions', def: '省级容错', unsetValue: '1' },
  { cat: 'geo', key: 'geo_anomaly.max_cities', type: 'geo_anomaly', name: 'max_cities', kind: 'int', field: 'geo_anomaly_max_cities', labelKey: 'geo_max_cities', def: '城市容错', unsetValue: '2' },
  { cat: 'geo', key: 'geo_anomaly.flag_after_polls', type: 'geo_anomaly', name: 'flag_after_polls', kind: 'int', field: 'geo_anomaly_flag_after_polls', labelKey: 'geo_flag_after', def: '连续几次才标记', unsetValue: '3' },
  { cat: 'geo', key: 'geo_anomaly.clear_after_polls', type: 'geo_anomaly', name: 'clear_after_polls', kind: 'int', field: 'geo_anomaly_clear_after_polls', labelKey: 'geo_clear_after', def: '连续几次才解除', unsetValue: '6' },
  { cat: 'geo', key: 'geo_anomaly.co_travel', type: 'geo_anomaly', name: 'co_travel', kind: 'str', field: 'geo_anomaly_co_travel', labelKey: 'geo_co_travel', def: '视为同一地点的国家组合', multiline: true },
  { cat: 'geo', key: 'geo_anomaly.allow_anywhere', type: 'geo_anomaly', name: 'allow_anywhere', kind: 'bool', field: 'geo_anomaly_allow_anywhere', labelKey: 'geo_allow_anywhere', def: '允许任何地方（不检测）' },
  { cat: 'geo_ban', key: 'geo_anomaly.ban_enabled', type: 'geo_anomaly', name: 'ban_enabled', kind: 'bool', field: 'geo_anomaly_ban_enabled', labelKey: 'geo_ban_enabled', def: '启用自动临时暂停' },
  { cat: 'geo_ban', key: 'geo_anomaly.ban_max_countries', type: 'geo_anomaly', name: 'ban_max_countries', kind: 'int', field: 'geo_anomaly_ban_max_countries', labelKey: 'geo_ban_max_countries', def: '暂停阈值：国家', unsetValue: '1' },
  { cat: 'geo_ban', key: 'geo_anomaly.ban_max_regions', type: 'geo_anomaly', name: 'ban_max_regions', kind: 'int', field: 'geo_anomaly_ban_max_regions', labelKey: 'geo_ban_max_regions', def: '暂停阈值：省', unsetValue: '2' },
  { cat: 'geo_ban', key: 'geo_anomaly.ban_max_cities', type: 'geo_anomaly', name: 'ban_max_cities', kind: 'int', field: 'geo_anomaly_ban_max_cities', labelKey: 'geo_ban_max_cities', def: '暂停阈值：城市', unsetValue: '3' },
  { cat: 'geo_ban', key: 'geo_anomaly.ban_after_polls', type: 'geo_anomaly', name: 'ban_after_polls', kind: 'int', field: 'geo_anomaly_ban_after_polls', labelKey: 'geo_ban_after', def: '连续几次才暂停', unsetValue: '6' },
  { cat: 'geo_ban', key: 'geo_anomaly.ban_duration_minutes', type: 'geo_anomaly', name: 'ban_duration_minutes', kind: 'int', field: 'geo_anomaly_ban_duration_minutes', labelKey: 'geo_ban_duration', def: '暂停时长（分钟）', unsetValue: '60' },
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
