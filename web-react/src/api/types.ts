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
  /** RFC3339 timestamp of the most recent moment any owned 3X-UI client
   *  reported activity (max(clientStats.lastOnline) per traffic poll).
   *  Absent / null = never seen, or every panel is still on 3X-UI < 3.1.0
   *  (where the lastOnline field doesn't exist). UI renders missing as
   *  "—" rather than a 1970 date. */
  last_online_at?: string | null
  /** Whether the account has TOTP 2FA enabled. Drives the admin table badge
   *  and the break-glass "reset 2FA" action's visibility. */
  totp_enabled?: boolean
  /** Per-user "force second-factor enrollment" override (on top of the group /
   *  staff-wide flags). Toggled from the Account Security drawer. */
  require_2fa?: boolean
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
  /** Managed-certificate binding. "" / undefined = unmanaged (manual /
   *  historical). 'psp_managed' means cert_id points to a PSP-managed cert
   *  that the renewal worker keeps deployed. Never carries any PEM. */
  cert_source?: '' | 'manual' | 'from_panel' | 'psp_managed'
  cert_id?: number
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
  // page + page_size land in every paged response from v3.6.1; legacy
  // callers that don't pass page params still get back these fields
  // (defaulting to page=1, the backend-clamped page_size). Optional
  // because not every caller cares to thread them through.
  page?: number
  page_size?: number
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
  /** Force every local-password member of this group to enroll a second factor. */
  require_2fa?: boolean
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

// TwoFAMethod identifies an alternative verification method offered at the login
// 2FA challenge. The server returns the allowed set; the UI renders the "use
// another method" picker from it.
export type TwoFAMethod = 'totp' | 'recovery' | 'passkey' | 'email'

export interface TwoFAChallenge {
  status: '2fa_required'
  pending_token: string
  // The verification methods the server will accept for THIS challenge (always
  // includes totp + recovery; passkey/email are admin opt-in and context-gated).
  methods?: TwoFAMethod[]
}

// AuthLoginResult is what /auth/local/login returns: either a full session
// (AuthLoginResponse) or a 2FA challenge that must be completed via /auth/2fa/*.
export type AuthLoginResult = AuthLoginResponse | TwoFAChallenge

export function isTwoFAChallenge(r: AuthLoginResult): r is TwoFAChallenge {
  return (r as { status?: string }).status === '2fa_required'
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
  // Login captcha (v3.7.0). captcha_required is the upfront requirement
  // (always mode); after_failures mode flips it on via a captcha_required flag
  // returned on a failed login. site_key is public.
  captcha_enabled?: boolean
  captcha_provider?: CaptchaProvider
  captcha_site_key?: string
  captcha_required?: boolean
  // Per-context captcha (v3.7.0): the register / forgot forms gate their widget
  // on these (always-on when the admin enables that context).
  captcha_register_required?: boolean
  captcha_forgot_required?: boolean
  // Self-service password recovery (v3.7.0). When enabled, the login page shows
  // a "Forgot password?" link; delivery decides whether the reset page expects
  // a token (from the email link) or an OTP code the user types.
  password_recovery_enabled?: boolean
  password_recovery_delivery?: 'link' | 'otp'
  // Self-service registration (v3.7.0). The email-domain allow-list is NOT
  // exposed (server-side only). require_email_verification drives whether the
  // register page shows a "check your email" step.
  registration_enabled?: boolean
  registration_require_email_verification?: boolean
  registration_delivery?: 'link' | 'otp'
  // Passkeys (v3.7.0). passkey_passwordless gates the login page's "Sign in with
  // a passkey" button (usernameless login); passkey_enabled alone only allows
  // enrollment as a second factor from the profile page.
  passkey_enabled?: boolean
  passkey_passwordless?: boolean
}

// PasskeyCredential is the sanitized view of a registered passkey shown in the
// profile management dialog — never the raw credential record or public key.
export interface PasskeyCredential {
  id: number
  name: string
  created_at: string
  last_used_at?: string | null
}

export type CaptchaProvider = 'image' | 'turnstile' | 'recaptcha' | 'hcaptcha'

// CaptchaChallenge is the image-provider challenge issued by GET /auth/captcha.
export interface CaptchaChallenge {
  enabled: boolean
  captcha_id?: string
  image?: string // data:image/...;base64 URL
}

// LoginCaptcha is the captcha response carried on a login attempt. Image
// provider fills id+answer; token providers fill token.
export interface LoginCaptcha {
  captcha_id?: string
  captcha_answer?: string
  captcha_token?: string
}
