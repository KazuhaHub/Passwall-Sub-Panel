// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as authApi from '@/api/auth'
import type { AuthMethods } from '@/api/types'
import { renderAccess, setTestLang } from '@/test/accessControlHarness'
import { useSiteStore } from '@/stores/site'
import RegisterView from './RegisterView'

vi.mock('@/api/auth', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/auth')>()
  return { ...real, getAuthMethods: vi.fn(), registerUser: vi.fn() }
})

const methods = (legal?: { enabled: boolean; consent_version: number }): AuthMethods => ({ registration_enabled: true, legal } as AuthMethods)
const axiosErr = (status: number, data: unknown) => Object.assign(new Error(String(status)), { response: { status, data } })

function mount() { return renderAccess(<RegisterView />, { route: '/register', path: '/register', role: 'user' }) }

function fill() {
  fireEvent.change(screen.getByLabelText('邮箱地址'), { target: { value: 'a@example.test' } })
  fireEvent.change(screen.getByLabelText('密码'), { target: { value: 'abcdefg1' } })
  fireEvent.change(screen.getByLabelText('确认密码'), { target: { value: 'abcdefg1' } })
}
const submit = () => screen.getByRole('button', { name: '创建账户' }) as HTMLButtonElement
const box = () => screen.queryByRole('checkbox')
const ticked = () => box()!.getAttribute('aria-checked') === 'true'

beforeEach(() => {
  vi.mocked(authApi.getAuthMethods).mockReset()
  vi.mocked(authApi.registerUser).mockReset().mockResolvedValue({ ok: true, requires_verification: false })
  useSiteStore.setState({ loaded: false, legalEnabled: false, footerText: '© Example' })
})

describe('the register form with the terms on (S23)', () => {
  it('has no checkbox and sends no consent version while the feature is off', async () => {
    vi.mocked(authApi.getAuthMethods).mockResolvedValue(methods({ enabled: false, consent_version: 0 }))
    mount()
    await waitFor(() => expect(authApi.getAuthMethods).toHaveBeenCalled())
    expect(box()).toBeNull()
    fill()
    fireEvent.click(submit())
    await waitFor(() => expect(authApi.registerUser).toHaveBeenCalled())
    expect(vi.mocked(authApi.registerUser).mock.calls[0][0]).not.toHaveProperty('accepted_consent_version')
  })

  it('requires the box to be ticked, and sends the version the form showed', async () => {
    vi.mocked(authApi.getAuthMethods).mockResolvedValue(methods({ enabled: true, consent_version: 3 }))
    mount()
    const cb = await screen.findByRole('checkbox')
    fill()
    expect(submit().disabled).toBe(true)
    fireEvent.click(cb)
    expect(submit().disabled).toBe(false)
    fireEvent.click(submit())
    await waitFor(() => expect(authApi.registerUser).toHaveBeenCalledWith(expect.objectContaining({ email: 'a@example.test', accepted_consent_version: 3 })))
  })

  it('links the two documents from the label without ticking the box', async () => {
    vi.mocked(authApi.getAuthMethods).mockResolvedValue(methods({ enabled: true, consent_version: 3 }))
    mount()
    const cb = await screen.findByRole('checkbox')
    const label = cb.closest('label')!
    expect(label.textContent).toBe('我已阅读并同意《服务条款》和《隐私政策》')
    const terms = screen.getByRole('link', { name: '《服务条款》' })
    expect(terms.getAttribute('href')).toBe('/legal/terms')
    expect(terms.getAttribute('target')).toBe('_blank')
    expect(screen.getByRole('link', { name: '《隐私政策》' }).getAttribute('href')).toBe('/legal/privacy')
    fireEvent.click(terms)
    expect(ticked()).toBe(false)
  })

  it('a refused version says the terms changed, unticks the box, and takes the new version', async () => {
    vi.mocked(authApi.getAuthMethods)
      .mockResolvedValueOnce(methods({ enabled: true, consent_version: 3 }))
      .mockResolvedValue(methods({ enabled: true, consent_version: 4 }))
    vi.mocked(authApi.registerUser).mockRejectedValueOnce(axiosErr(409, { code: 'legal_consent_outdated', consent_version: 4 }))
    mount()
    fireEvent.click(await screen.findByRole('checkbox'))
    fill()
    fireEvent.click(submit())
    expect(await screen.findByText('条款刚刚更新，请重新阅读后再提交')).toBeTruthy()
    expect(screen.queryByText('该邮箱已被注册')).toBeNull()
    expect(ticked()).toBe(false)
    fireEvent.click(box()!)
    fireEvent.click(submit())
    await waitFor(() => expect(authApi.registerUser).toHaveBeenLastCalledWith(expect.objectContaining({ accepted_consent_version: 4 })))
  })

  it('another 409 is still an existing account', async () => {
    vi.mocked(authApi.getAuthMethods).mockResolvedValue(methods({ enabled: true, consent_version: 3 }))
    vi.mocked(authApi.registerUser).mockRejectedValueOnce(axiosErr(409, { error: 'exists' }))
    mount()
    fireEvent.click(await screen.findByRole('checkbox'))
    fill()
    fireEvent.click(submit())
    expect(await screen.findByText('该邮箱已被注册')).toBeTruthy()
  })

  it('carries the footer links, which the page had no footer for', async () => {
    vi.mocked(authApi.getAuthMethods).mockResolvedValue(methods({ enabled: true, consent_version: 3 }))
    mount()
    expect(await screen.findByRole('link', { name: '服务条款' })).toBeTruthy()
    expect(screen.getByRole('link', { name: '隐私政策' })).toBeTruthy()
  })

  it('in English', async () => {
    setTestLang('en-US')
    vi.mocked(authApi.getAuthMethods).mockResolvedValue(methods({ enabled: true, consent_version: 3 }))
    mount()
    const cb = await screen.findByRole('checkbox')
    expect(cb.closest('label')!.textContent).toBe('I have read and agree to the Terms of Service and the Privacy Policy.')
  })
})
