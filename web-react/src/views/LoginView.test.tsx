// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as authApi from '@/api/auth'
import type { AuthMethods } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { renderAccess, whereIs } from '@/test/accessControlHarness'
import LoginView from './LoginView'

vi.mock('@/api/auth', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/auth')>()
  return { ...real, getAuthMethods: vi.fn() }
})

// The session actions are the store's; they are replaced here so the test sees exactly what the
// form hands them (the request each one makes is the store's and api/auth's to test).
const login = vi.fn()
const complete2FA = vi.fn()
const methods = (over: Partial<AuthMethods> = {}): AuthMethods => ({ local: true, login_mode: 'local_only', ...over } as AuthMethods)

function mount() { return renderAccess(<LoginView />, { route: '/login', path: '/login', role: 'user' }) }
async function fillAndSubmit() {
  fireEvent.change(await screen.findByLabelText('账号'), { target: { value: 'ann' } })
  fireEvent.change(screen.getByLabelText('密码'), { target: { value: 'pw-1234x' } })
  fireEvent.click(screen.getByRole('button', { name: '登录' }))
}

beforeEach(() => {
  vi.mocked(authApi.getAuthMethods).mockReset().mockResolvedValue(methods())
  login.mockReset().mockResolvedValue({ twoFA: false })
  complete2FA.mockReset().mockResolvedValue(undefined)
  useAuthStore.setState({ login, complete2FA })
})

describe('the sign-in form', () => {
  it('tells a password manager which box is which, and keeps the password masked', async () => {
    mount()
    const user = await screen.findByLabelText('账号')
    const pw = screen.getByLabelText('密码')
    expect(user.getAttribute('autocomplete')).toBe('username')
    expect(pw.getAttribute('autocomplete')).toBe('current-password')
    expect(pw.getAttribute('type')).toBe('password')
  })

  it('sends the typed credentials, with no captcha while none is asked for, and goes home', async () => {
    const { router } = mount()
    await fillAndSubmit()
    await waitFor(() => expect(login).toHaveBeenCalledWith('ann', 'pw-1234x', undefined))
    await waitFor(() => expect(whereIs(router)).toBe('/user/me'))
  })

  it('says how long to wait after a lockout, without saying anything about the account', async () => {
    login.mockRejectedValue(Object.assign(new Error('429'), { response: { status: 429, data: { locked: true, retry_after: 600 } } }))
    mount()
    await fillAndSubmit()
    expect(await screen.findByText('失败次数过多，请约 10 分钟后再试。')).toBeTruthy()
  })

  it('moves to the code step for a 2FA account, and sends the trimmed code with the pending token', async () => {
    login.mockResolvedValue({ twoFA: true, pendingToken: 'tok', methods: ['totp', 'recovery'] })
    const { router } = mount()
    await fillAndSubmit()
    const code = await screen.findByLabelText('验证码')
    expect(code.getAttribute('autocomplete')).toBe('one-time-code')
    fireEvent.change(code, { target: { value: ' 123456 ' } })
    fireEvent.click(screen.getByRole('button', { name: '验证并登录' }))
    await waitFor(() => expect(complete2FA).toHaveBeenCalledWith('tok', '123456'))
    await waitFor(() => expect(whereIs(router)).toBe('/user/me'))
  })

  it('a wrong code is shown next to the field and leaves it empty for another try', async () => {
    login.mockResolvedValue({ twoFA: true, pendingToken: 'tok', methods: ['totp'] })
    complete2FA.mockRejectedValue(Object.assign(new Error('401'), { response: { status: 401, data: { error: 'bad code' } } }))
    mount()
    await fillAndSubmit()
    const code = await screen.findByLabelText('验证码') as HTMLInputElement
    fireEvent.change(code, { target: { value: '000000' } })
    fireEvent.click(screen.getByRole('button', { name: '验证并登录' }))
    expect(await screen.findByText('验证码无效，请重试。')).toBeTruthy()
    expect(code.value).toBe('')
  })
})
