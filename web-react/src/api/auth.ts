import { client } from './client'
import type {
  AuthLoginResponse,
  AuthLoginResult,
  AuthMethods,
  CaptchaChallenge,
  LoginCaptcha,
} from './types'

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
  const { data } = await client.post<AuthLoginResult>('/auth/local/login', { upn, password, ...captcha })
  return data
}

// verify2FA completes a 2FA login: it exchanges the pending token (from a
// 2fa_required login response) plus a TOTP or recovery code for a real session.
// _skipRefresh is essential: this is a PRE-session exchange, so a 401 (wrong/
// expired code) must propagate straight to the caller — without it the shared
// 401-refresh interceptor would hijack the response, wipe localStorage, and even
// replay the request with a stale token.
export async function verify2FA(pendingToken: string, code: string): Promise<AuthLoginResponse> {
  const { data } = await client.post<AuthLoginResponse>(
    '/auth/2fa/verify',
    { pending_token: pendingToken, code },
    { _skipErrorToast: true, _skipRefresh: true },
  )
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

// registerUser creates a new self-service account. The email is the username.
// Returns { requires_verification } so the page knows whether to show the
// "check your email" step or send the user straight to login.
export async function registerUser(input: {
  email: string
  password: string
  display_name?: string
}): Promise<{ ok: boolean; requires_verification: boolean }> {
  const { data } = await client.post('/auth/register', input, { _skipErrorToast: true })
  return data
}

// verifyEmail confirms a registration. Link delivery passes token; OTP delivery
// passes ident (the email) + code.
export async function verifyEmail(input: { token?: string; ident?: string; code?: string }) {
  const { data } = await client.post('/auth/verify-email', input, { _skipErrorToast: true })
  return data
}

export function samlLoginURL(returnTo: string = '/user/me'): string {
  return `/api/auth/saml/login?return_to=${encodeURIComponent(returnTo)}`
}

export function oidcLoginURL(returnTo: string = '/user/me'): string {
  return `/api/auth/oidc/login?return_to=${encodeURIComponent(returnTo)}`
}
