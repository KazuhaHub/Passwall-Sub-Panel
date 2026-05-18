import { client } from './client'
import type { AuthLoginResponse, AuthMethods } from './types'

export async function getAuthMethods() {
  const { data } = await client.get<AuthMethods>('/auth/methods')
  return data
}

export async function localLogin(upn: string, password: string) {
  const { data } = await client.post<AuthLoginResponse>('/auth/local/login', { upn, password })
  return data
}

// refreshTokens trades a refresh JWT for a fresh (access, refresh) pair.
// Skips the shared axios interceptor's 401 handling via _skipRefresh so a
// refresh-token rejection cleanly falls through to the logout path
// instead of recursing back through itself.
export async function refreshTokens(refreshToken: string): Promise<AuthLoginResponse> {
  const { data } = await client.post<AuthLoginResponse>(
    '/auth/refresh',
    { refresh_token: refreshToken },
    { _skipRefresh: true, _skipErrorToast: true },
  )
  return data
}

export async function ssoComplete(): Promise<AuthLoginResponse> {
  const { data } = await client.get<AuthLoginResponse>('/auth/sso-complete')
  return data
}

export function samlLoginURL(returnTo: string = '/user/me'): string {
  return `/api/auth/saml/login?return_to=${encodeURIComponent(returnTo)}`
}

export function oidcLoginURL(returnTo: string = '/user/me'): string {
  return `/api/auth/oidc/login?return_to=${encodeURIComponent(returnTo)}`
}
