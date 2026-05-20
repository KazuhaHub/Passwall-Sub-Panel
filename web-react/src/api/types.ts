// Step 1.0 minimal types — only what the auth/login flow needs.
// Other DTOs are added per page when migrated. Mirrors backend handlers
// in internal/transport/http/handler/*.go.

/** Operator can manage day-to-day user records but is locked out of
 *  3X-UI panel credentials, system settings, mail SMTP, SAML/OIDC,
 *  rule sets, templates, audit clear. The intent: bring in an
 *  assistant without handing over break-glass keys. */
export type Role = 'admin' | 'operator' | 'user'
export type ResetPeriod = 'never' | 'monthly' | 'quarterly' | 'yearly'

export interface User {
  id: number
  upn: string
  display_name?: string
  email?: string
  /** SSO connection this account is bound to. "local" for password-only
   *  accounts; "saml:<name>" / "oidc:<name>" once linked. UI shows it as
   *  a badge and offers Unlink when not local. */
  sso_provider: string
  /** IdP-side stable identifier (SAML NameID / OIDC sub). Matches UPN
   *  for local rows. Read-only display. */
  sso_subject?: string
  role: Role
  group_id: number
  uuid: string
  sub_url: string
  /** Absolute expiry instant (RFC3339). Use for "is expired / days left" math. */
  expire_at?: string | null
  /** expire_at rendered as the YYYY-MM-DD calendar day in the *panel* timezone.
   *  Use this for the date picker and table so the shown day matches what was
   *  set, independent of the browser's timezone. Empty for permanent users. */
  expire_date?: string
  traffic_limit_bytes: number
  /** Lifetime counters (never reset by period rolls). Read-only detail. */
  lifetime_up_bytes?: number
  lifetime_down_bytes?: number
  lifetime_total_bytes?: number
  traffic_reset_period: ResetPeriod
  remark?: string
  enabled: boolean
  auto_disabled_reason?: string
  emergency_used_count: number
  /** RFC3339 timestamp; emergency window is active iff > now. */
  emergency_until?: string | null
  /** Bytes consumed during the currently-active emergency window. Always 0 when no window is active. */
  emergency_used_bytes?: number
  /** Per-window traffic cap in bytes (0 = unlimited). Comes from system settings. */
  emergency_quota_bytes?: number
  created_at: string
}

export interface CreateUserRequest {
  upn: string
  email?: string
  display_name?: string
  password?: string
  group_id: number
  expire_at?: string
  /** YYYY-MM-DD calendar date; interpreted as end-of-day in the panel
   *  timezone server-side. Preferred over expire_at for a picked date. */
  expire_date?: string
  traffic_limit_gb?: number
  traffic_reset_period?: ResetPeriod
  remark?: string
}

export interface CreateUserResponse {
  user: User
  initial_password: string
  synced_inbounds: number
}

export interface Node {
  id: number
  panel_id: number
  panel_name: string
  inbound_id: number
  display_name: string
  server_address: string
  flow?: string
  /** Cached upstream inbound protocol (vless / vmess / trojan /
   *  shadowsocks / hysteria2, lowercased). Empty for nodes imported
   *  before this field existed; used to gate protocol-specific UI like
   *  the VLESS-only Flow field. */
  protocol?: string
  region: string
  tags: string[]
  sort_order: number
  enabled: boolean
  /** "real" (3X-UI-backed, default for legacy rows) or "separator" (layout-only).
   *  Separator rows render as a DIRECT proxy in subscriptions and don't have
   *  server/inbound/health metadata. */
  kind?: 'real' | 'separator'
  /** Most recent health-probe outcome. Empty before the first tick has run. */
  health_state?: '' | 'ok' | 'panel_unreachable' | 'inbound_missing' | 'inbound_disabled'
  /** RFC3339 timestamp of the last probe (regardless of outcome). */
  health_checked_at?: string | null
  /** Error string for the most recent failed probe; empty when healthy. */
  health_detail?: string
}

export type SyncTaskStatus = 'pending' | 'running' | 'succeeded' | 'canceled'
export type SyncTaskType =
  | 'user_delete'
  | 'user_resync'
  | 'user_push_config'
  | 'node_create'
  | 'node_delete'
  | 'node_set_enabled'
  | 'node_update'

// Backend serializes both PascalCase (Go field names) and snake_case in some
// older paths; accept either. Helper getters below normalize.
export interface SyncTask {
  ID?: number
  id?: number
  Type?: SyncTaskType
  type?: SyncTaskType
  Status?: SyncTaskStatus
  status?: SyncTaskStatus
  TargetType?: string
  target_type?: string
  TargetID?: number
  target_id?: number
  Summary?: string
  summary?: string
  Payload?: string
  payload?: string
  LastError?: string
  last_error?: string
  Attempts?: number
  attempts?: number
  NextRunAt?: string
  next_run_at?: string
  CreatedAt?: string
  created_at?: string
  UpdatedAt?: string
  updated_at?: string
  FinishedAt?: string | null
  finished_at?: string | null
}

export interface UnmanagedInbound {
  PanelID: number
  PanelName: string
  InboundID: number
  Protocol: string
  Port: number
  Remark: string
  Enable: boolean
  ClientCount: number
}

export interface ListResponse<T> {
  items: T[]
  total: number
}

export interface TagFilter {
  all: boolean
  tags: string[]
  // Conjunction over tags. "" / "all" → AND, "any" → OR. Optional in the
  // wire shape: empty / missing serializes as omitted on legacy rows.
  mode?: 'all' | 'any'
}

export interface Layout {
  separators: { position: number; name: string }[]
  sort: { node_id: number; weight: number }[]
  default_sort_strategy: string
}

export interface Group {
  id: number
  slug: string
  name: string
  tag_filter: TagFilter
  layout: Layout
  remark?: string
  members: number
}

export interface AuthLoginResponse {
  access_token: string
  refresh_token: string
  user: {
    id: number
    upn: string
    display_name?: string
    role: Role
  }
}

export type LoginMode = 'sso_redirect' | 'sso_first' | 'dual' | 'local_only'

export interface AuthMethods {
  local: boolean
  sso: boolean
  saml: boolean
  oidc: boolean
  login_mode: LoginMode
  site_title: string
  app_title: string
  icon_url: string
  logo_url: string
  logo_url_dark: string
  footer_text: string
  // Step 1.0 expects these fields once the backend gains theme settings.
  // Until then, the panel returns undefined and the frontend falls back.
  theme_color?: string
  theme_default_mode?: 'light' | 'dark'
  // IANA timezone for system-level calculations (traffic resets, expire_at,
  // default for the admin traffic chart). Empty falls back to server local.
  timezone?: string
}
