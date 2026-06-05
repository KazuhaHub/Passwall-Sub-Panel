import { client } from './client'
import type { AuthLoginResponse, AuthMethods, CaptchaChallenge, LoginCaptcha } from './types'

export async function getAuthMethods() {
  const { data } = await client.get<AuthMethods>('/auth/methods')
  return data
}

// getCaptcha fetches a fresh image-captcha challenge. Returns {enabled:false}
// when captcha is off or the active provider renders client-side (token
// providers). Skips the shared error toast — the login form handles failures.
export async function getCaptcha(): Promise<CaptchaChallenge> {
  const { data } = await client.get<CaptchaChallenge>('/auth/captcha', { _skipErrorToast: true })
  return data
}

export async function localLogin(upn: string, password: string, captcha?: LoginCaptcha) {
  const { data } = await client.post<AuthLoginResponse>('/auth/local/login', { upn, password, ...captcha })
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

// requestPasswordReset asks the panel to email a reset to the named account.
// Always resolves on a 2xx regardless of whether the account exists (the
// backend deliberately doesn't reveal that). Skips the shared error toast.
export async function requestPasswordReset(ident: string) {
  const { data } = await client.post('/auth/forgot-password', { ident }, { _skipErrorToast: true })
  return data
}

// resetPassword applies a new password. Link delivery passes token; OTP delivery
// passes ident + code. Errors propagate so the reset page can show the reason.
export async function resetPassword(input: {
  token?: string
  ident?: string
  code?: string
  new_password: string
}) {
  const { data } = await client.post('/auth/reset-password', input, { _skipErrorToast: true })
  return data
}

export function samlLoginURL(returnTo: string = '/user/me'): string {
  return `/api/auth/saml/login?return_to=${encodeURIComponent(returnTo)}`
}

export function oidcLoginURL(returnTo: string = '/user/me'): string {
  return `/api/auth/oidc/login?return_to=${encodeURIComponent(returnTo)}`
}
