import { client } from './client'
import type { LoginMode } from './auth'

export interface UISettings {
  login_mode: LoginMode
  site_title: string
  logo_url: string
  logo_url_dark: string
  email_domain: string
  audit_retention_days: number
  sub_base_url: string
  // Runtime tuning — changes take effect on next panel restart.
  cron_traffic_pull_minutes: number
  cron_reconcile_minutes: number
  jwt_access_ttl_minutes: number
  jwt_refresh_ttl_minutes: number
  jwt_issuer: string
  sub_per_ip_per_min: number
  login_per_ip_per_min: number
  sync_task_retention_days: number
  disallow_user_local_login: boolean
  disallow_user_password_change: boolean
  emergency_access_enabled: boolean
  emergency_access_hours: number
  emergency_access_max_count: number
}

export async function getUISettings() {
  const { data } = await client.get<UISettings>('/admin/settings/ui')
  return data
}

export async function putUISettings(s: UISettings) {
  const { data } = await client.put<UISettings>('/admin/settings/ui', s)
  return data
}

// ---- SAML ----

export type SAMLMode = 'auto' | 'manual'

export interface SAMLConfig {
  enabled: boolean
  mode: SAMLMode
  sp: {
    entity_id: string
    acs_url: string
    cert_pem: string
    has_key_pem: boolean
  }
  idp: {
    metadata_url: string
    metadata_refresh_hours: number
  }
  attribute_mapping: {
    upn: string
    email: string
    display_name: string
    groups: string
  }
  admin_group_ids: string[]
  default_group_slug: string
  new_user_defaults: {
    expire_days: number
    traffic_limit_bytes: number
    traffic_reset_period: string
  }
}

export interface SAMLUpdateRequest extends Omit<SAMLConfig, 'sp'> {
  sp: {
    entity_id: string
    acs_url: string
    cert_pem: string
    key_pem: string
  }
}

export async function getSAML() {
  const { data } = await client.get<SAMLConfig>('/admin/settings/saml')
  return data
}

export async function putSAML(req: SAMLUpdateRequest) {
  const { data } = await client.put<{ saved: boolean; reload_error?: string; config: SAMLConfig }>(
    '/admin/settings/saml', req)
  return data
}

// ---- OIDC ----

export interface OIDCConfig {
  enabled: boolean
  issuer_url: string
  client_id: string
  has_client_secret: boolean
  redirect_url: string
  scopes: string[]
  attribute_mapping: {
    username: string
    email: string
    display_name: string
    groups: string
  }
  admin_group_ids: string[]
  default_group_slug: string
  new_user_defaults: {
    expire_days: number
    traffic_limit_bytes: number
    traffic_reset_period: string
  }
}

export interface OIDCUpdateRequest extends Omit<OIDCConfig, 'has_client_secret'> {
  client_secret: string
}

export async function getOIDC() {
  const { data } = await client.get<OIDCConfig>('/admin/settings/oidc')
  return data
}

export async function putOIDC(req: OIDCUpdateRequest) {
  const { data } = await client.put<{ saved: boolean; reload_error?: string; config: OIDCConfig }>(
    '/admin/settings/oidc', req)
  return data
}
