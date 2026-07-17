import { beforeEach, describe, expect, it, vi } from 'vitest'

const http = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('./client', () => ({ client: http }))
vi.mock('@/panelPath', () => ({ panelAPIBase: '/panel/api' }))

import * as auth from './auth'

beforeEach(() => {
  http.get.mockReset().mockResolvedValue({ data: { source: 'get' } })
  http.post.mockReset().mockResolvedValue({ data: { source: 'post' } })
})

describe('authentication API contracts', () => {
  it('maps public login and refresh operations with safe interceptor flags', async () => {
    await auth.getAuthMethods()
    await auth.getCaptcha()
    await auth.localLogin('user@example.com', 'secret', { captcha_token: 'captcha' })
    await auth.verify2FA('pending', '123456')
    await auth.send2FAEmail('pending')
    await auth.refreshTokens('refresh')
    await auth.ssoComplete()

    expect(http.get).toHaveBeenCalledWith('/auth/captcha', { _skipErrorToast: true })
    expect(http.post).toHaveBeenCalledWith('/auth/local/login', {
      upn: 'user@example.com', password: 'secret', captcha_token: 'captcha',
    })
    expect(http.post).toHaveBeenCalledWith('/auth/2fa/verify',
      { pending_token: 'pending', code: '123456' },
      { _skipErrorToast: true, _skipRefresh: true })
    expect(http.post).toHaveBeenCalledWith('/auth/refresh',
      { refresh_token: 'refresh' },
      { _skipRefresh: true, _skipErrorToast: true })
  })

  it('encodes passkey session identifiers and keeps pre-session 401s caller-managed', async () => {
    const assertion = { id: 'credential' } as never
    await auth.passkey2FABegin('pending token')
    await auth.passkey2FAFinish('pending token', 'session/id', assertion)
    await auth.passkeyLoginBegin()
    await auth.passkeyLoginFinish('session/id', assertion)

    expect(http.post).toHaveBeenCalledWith('/auth/2fa/passkey/begin',
      { pending_token: 'pending token' },
      { _skipErrorToast: true, _skipRefresh: true })
    expect(http.post).toHaveBeenCalledWith(
      '/auth/2fa/passkey/finish?pending_token=pending%20token&session=session%2Fid',
      assertion,
      { _skipErrorToast: true, _skipRefresh: true },
    )
    expect(http.post).toHaveBeenCalledWith('/auth/passkey/finish?session=session%2Fid',
      assertion,
      { _skipErrorToast: true, _skipRefresh: true })
  })

  it('maps account recovery and registration and builds prefixed SSO URLs', async () => {
    await auth.requestPasswordReset('user@example.com', { captcha_id: 'id', captcha_answer: 'answer' })
    await auth.resetPassword({ token: 'token', new_password: 'new-password' })
    await auth.registerUser({ email: 'user@example.com', password: 'password' })
    await auth.verifyEmail({ ident: 'user@example.com', code: '123456' })
    await auth.resendVerification({ email: 'user@example.com' })

    expect(http.post).toHaveBeenCalledWith('/auth/forgot-password', {
      ident: 'user@example.com', captcha_id: 'id', captcha_answer: 'answer',
    }, { _skipErrorToast: true })
    expect(http.post).toHaveBeenCalledWith('/auth/register', {
      email: 'user@example.com', password: 'password',
    }, { _skipErrorToast: true })
    expect(auth.samlLoginURL('/user/me?tab=rules')).toBe(
      '/panel/api/auth/saml/login?return_to=%2Fuser%2Fme%3Ftab%3Drules',
    )
    expect(auth.oidcLoginURL()).toBe('/panel/api/auth/oidc/login?return_to=%2Fuser%2Fme')
  })
})
