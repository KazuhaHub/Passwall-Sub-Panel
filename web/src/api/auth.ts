import { client } from './client'
import type { AuthLoginResponse } from './types'

export async function ssoComplete(): Promise<AuthLoginResponse> {
  const { data } = await client.get<AuthLoginResponse>('/auth/sso-complete')
  return data
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
}

export async function getAuthMethods() {
  const { data } = await client.get<AuthMethods>('/auth/methods')
  return data
}

export async function localLogin(upn: string, password: string) {
  const { data } = await client.post<AuthLoginResponse>('/auth/local/login', {
    upn,
    password,
  })
  return data
}

// samlLoginURL — preserved as the SSO default when both SAML and OIDC are
// enabled. New callers should use samlLoginURL / oidcLoginURL explicitly.
export function ssoLoginURL(returnTo: string = '/user/me'): string {
  return samlLoginURL(returnTo)
}

export function samlLoginURL(returnTo: string = '/user/me'): string {
  return `/api/auth/saml/login?return_to=${encodeURIComponent(returnTo)}`
}

export function oidcLoginURL(returnTo: string = '/user/me'): string {
  return `/api/auth/oidc/login?return_to=${encodeURIComponent(returnTo)}`
}
