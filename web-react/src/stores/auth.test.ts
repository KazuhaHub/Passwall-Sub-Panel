import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  localLogin: vi.fn(),
  passkey2FABegin: vi.fn(),
  passkey2FAFinish: vi.fn(),
  passkeyLoginBegin: vi.fn(),
  passkeyLoginFinish: vi.fn(),
  ssoComplete: vi.fn(),
  verify2FA: vi.fn(),
  startAuthentication: vi.fn(),
}))

vi.mock('@/api/auth', () => ({
  localLogin: mocks.localLogin,
  passkey2FABegin: mocks.passkey2FABegin,
  passkey2FAFinish: mocks.passkey2FAFinish,
  passkeyLoginBegin: mocks.passkeyLoginBegin,
  passkeyLoginFinish: mocks.passkeyLoginFinish,
  ssoComplete: mocks.ssoComplete,
  verify2FA: mocks.verify2FA,
}))
vi.mock('@simplewebauthn/browser', () => ({ startAuthentication: mocks.startAuthentication }))
vi.mock('@/panelPath', () => ({ panelURL: (route = '/') => `/panel${route}` }))

class MemoryStorage implements Storage {
  private values = new Map<string, string>()
  get length() { return this.values.size }
  clear() { this.values.clear() }
  getItem(key: string) { return this.values.get(key) ?? null }
  key(index: number) { return [...this.values.keys()][index] ?? null }
  removeItem(key: string) { this.values.delete(key) }
  setItem(key: string, value: string) { this.values.set(key, String(value)) }
}

const windowStub = {
  addEventListener: vi.fn(),
  location: { replace: vi.fn() },
}

type AuthModule = typeof import('./auth')
let auth: AuthModule

const session = (id = 7) => ({
  access_token: `access-${id}`,
  refresh_token: `refresh-${id}`,
  user: { id, upn: `user${id}@example.com`, display_name: `User ${id}`, role: 'admin' as const },
})

beforeAll(async () => {
  vi.stubGlobal('localStorage', new MemoryStorage())
  vi.stubGlobal('window', windowStub)
  auth = await import('./auth')
})

beforeEach(() => {
  localStorage.clear()
  Object.values(mocks).forEach(mock => mock.mockReset())
  windowStub.location.replace.mockReset()
  auth.useAuthStore.setState({
    userId: null,
    upn: '',
    displayName: '',
    role: '',
    hasToken: false,
  })
})

describe('auth store', () => {
  it('persists a successful password login', async () => {
    mocks.localLogin.mockResolvedValue(session())

    await expect(auth.useAuthStore.getState().login('user7@example.com', 'secret')).resolves.toEqual({ twoFA: false })
    expect(mocks.localLogin).toHaveBeenCalledWith('user7@example.com', 'secret', undefined)
    expect(localStorage.getItem('psp_access')).toBe('access-7')
    expect(localStorage.getItem('psp_refresh')).toBe('refresh-7')
    expect(JSON.parse(localStorage.getItem('psp_user') || '{}')).toMatchObject({ userId: 7, role: 'admin' })
    expect(auth.useAuthStore.getState()).toMatchObject({ userId: 7, displayName: 'User 7', hasToken: true })
  })

  it('returns a 2FA challenge without mutating the current session', async () => {
    mocks.localLogin.mockResolvedValue({ status: '2fa_required', pending_token: 'pending' })

    await expect(auth.useAuthStore.getState().login('u', 'p')).resolves.toEqual({
      twoFA: true,
      pendingToken: 'pending',
      methods: ['totp', 'recovery'],
    })
    expect(localStorage.getItem('psp_access')).toBeNull()
    expect(auth.useAuthStore.getState().hasToken).toBe(false)
  })

  it('applies the session returned after code-based 2FA', async () => {
    mocks.verify2FA.mockResolvedValue(session(8))
    await auth.useAuthStore.getState().complete2FA('pending', '123456')

    expect(mocks.verify2FA).toHaveBeenCalledWith('pending', '123456')
    expect(auth.useAuthStore.getState()).toMatchObject({ userId: 8, upn: 'user8@example.com', hasToken: true })
  })

  it('runs the passkey login ceremony and preserves offered 2FA methods', async () => {
    const publicKey = { challenge: 'challenge' }
    const assertion = { id: 'credential' }
    mocks.passkeyLoginBegin.mockResolvedValue({ session_id: 'session', publicKey })
    mocks.startAuthentication.mockResolvedValue(assertion)
    mocks.passkeyLoginFinish.mockResolvedValue({
      status: '2fa_required', pending_token: 'pending', methods: ['email', 'recovery'],
    })

    await expect(auth.useAuthStore.getState().loginPasskey()).resolves.toEqual({
      twoFA: true, pendingToken: 'pending', methods: ['email', 'recovery'],
    })
    expect(mocks.startAuthentication).toHaveBeenCalledWith({ optionsJSON: publicKey })
    expect(mocks.passkeyLoginFinish).toHaveBeenCalledWith('session', assertion)
  })

  it('runs passkey 2FA and SSO through the shared session persistence path', async () => {
    mocks.passkey2FABegin.mockResolvedValue({ session_id: 'second', publicKey: { challenge: '2fa' } })
    mocks.startAuthentication.mockResolvedValue({ id: 'assertion' })
    mocks.passkey2FAFinish.mockResolvedValue(session(9))
    await auth.useAuthStore.getState().complete2FAPasskey('pending')
    expect(mocks.passkey2FAFinish).toHaveBeenCalledWith('pending', 'second', { id: 'assertion' })
    expect(auth.useAuthStore.getState().userId).toBe(9)

    mocks.ssoComplete.mockResolvedValue(session(10))
    await auth.useAuthStore.getState().loginSSO()
    expect(auth.useAuthStore.getState()).toMatchObject({ userId: 10, hasToken: true })
    expect(localStorage.getItem('psp_access')).toBe('access-10')
  })

  it('updates display name and synchronizes external storage changes', () => {
    auth.useAuthStore.setState({ userId: 4, upn: 'old@example.com', role: 'operator', displayName: '', hasToken: true })
    auth.useAuthStore.getState().setDisplayName('New Name')
    expect(JSON.parse(localStorage.getItem('psp_user') || '{}')).toMatchObject({ displayName: 'New Name' })

    localStorage.setItem('psp_user', JSON.stringify({
      userId: 11, upn: 'other@example.com', displayName: 'Other', role: 'user',
    }))
    localStorage.setItem('psp_access', 'external-token')
    auth.useAuthStore.getState().syncFromStorage()
    expect(auth.useAuthStore.getState()).toMatchObject({ userId: 11, role: 'user', hasToken: true })
  })

  it('clears storage on logout and exposes role/label selectors', () => {
    localStorage.setItem('psp_access', 'token')
    localStorage.setItem('psp_refresh', 'refresh')
    localStorage.setItem('psp_user', '{}')
    auth.useAuthStore.setState({ userId: 1, upn: 'admin@example.com', displayName: '', role: 'admin', hasToken: true })

    const current = auth.useAuthStore.getState()
    expect(auth.selectIsLoggedIn(current)).toBe(true)
    expect(auth.selectIsAdmin(current)).toBe(true)
    expect(auth.selectIsStaff(current)).toBe(true)
    expect(auth.selectLabel(current)).toBe('admin@example.com')

    current.logout()
    expect(auth.useAuthStore.getState()).toMatchObject({ userId: null, role: '', hasToken: false })
    expect(localStorage.length).toBe(0)
    expect(windowStub.location.replace).toHaveBeenCalledWith('/panel/logged-out')
  })
})
