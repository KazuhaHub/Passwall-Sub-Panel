// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { flatten, type Nested } from '@/i18n/options'
import auth from '@/locales/zh-CN/auth.json'
import RegisterView from './RegisterView'

const mocks = vi.hoisted(() => ({ methods: vi.fn(), register: vi.fn(), dictionary: {} as Record<string, string>, load: vi.fn() }))
vi.mock('@/api/auth', () => ({ getAuthMethods: mocks.methods, registerUser: mocks.register }))
vi.mock('@/stores/site', () => ({ useSiteStore: () => ({ load: mocks.load, footerText: '', legalEnabled: false }) }))
vi.mock('@/components/BrandLogo', () => ({ default: () => null }))
vi.mock('@/components/CaptchaWidget', () => ({ default: () => null }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({
  t: (key: string) => mocks.dictionary[key.replace(/^auth:/, '')] ?? key,
  i18n: { language: 'zh-CN', resolvedLanguage: 'zh-CN' },
}) }))
mocks.dictionary = flatten(auth as Nested)
const methods = { legal: { enabled: true, consent_version: 3 }, registration_enabled: true }

function mount() {
  return render(<MemoryRouter><ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'zh-CN' })}><RegisterView /></ThemeProvider></MemoryRouter>)
}
function fill() {
  fireEvent.change(screen.getByLabelText(auth.register_email_label), { target: { value: 'user@example.test' } })
  fireEvent.change(screen.getByLabelText(auth.register_password_label), { target: { value: 'Password123' } })
  fireEvent.change(screen.getByLabelText(auth.register_password_confirm_label), { target: { value: 'Password123' } })
}
beforeEach(() => { vi.clearAllMocks(); mocks.load.mockResolvedValue(undefined); mocks.methods.mockResolvedValue(methods); mocks.register.mockResolvedValue({ ok: true, requires_verification: false }) })
afterEach(cleanup)

describe('RegisterView legal consent', () => {
  it('requires the checkbox and sends its exact displayed version', async () => {
    const { container } = mount()
    const checkbox = await screen.findByRole('checkbox', { name: auth.legal.checkbox_label })
    fill()
    const submit = screen.getByRole('button', { name: auth.register_submit })
    expect(submit.hasAttribute('disabled')).toBe(true)
    // Follow a document link without activating the containing checkbox label.
    const label = container.querySelector('.MuiFormControlLabel-root')!
    fireEvent.click(within(label as HTMLElement).getByRole('link', { name: '服务条款' }))
    expect((checkbox as HTMLInputElement).checked).toBe(false)
    fireEvent.click(checkbox)
    fireEvent.click(submit)
    await waitFor(() => expect(mocks.register).toHaveBeenCalledWith(expect.objectContaining({ email: 'user@example.test', accepted_consent_version: 3 })))
    expect(await screen.findByText(auth.register_success)).toBeTruthy()
  })

  it('distinguishes stale consent from duplicate email, refreshes and requires a new check', async () => {
    mocks.methods.mockResolvedValueOnce(methods).mockResolvedValueOnce({ ...methods, legal: { enabled: true, consent_version: 4 } })
    mocks.register.mockRejectedValueOnce({ response: { status: 409, data: { error: 'legal_consent_outdated' } } })
    mount()
    const checkbox = await screen.findByRole('checkbox', { name: auth.legal.checkbox_label })
    fill()
    fireEvent.click(checkbox)
    fireEvent.click(screen.getByRole('button', { name: auth.register_submit }))
    expect(await screen.findByText(auth.legal.register_outdated)).toBeTruthy()
    const fresh = await screen.findByRole('checkbox', { name: auth.legal.checkbox_label })
    expect((fresh as HTMLInputElement).checked).toBe(false)
    expect(screen.queryByText(auth.register_email_exists)).toBeNull()
    expect(screen.getByRole('button', { name: auth.register_submit }).hasAttribute('disabled')).toBe(true)
    fireEvent.click(fresh)
    fireEvent.click(screen.getByRole('button', { name: auth.register_submit }))
    await waitFor(() => expect(mocks.register).toHaveBeenLastCalledWith(expect.objectContaining({ accepted_consent_version: 4 })))
  })

  it.each([{ enabled: false, consent_version: 3 }, { enabled: true, consent_version: 0 }, undefined])('preserves legacy registration with legal state %j', async legal => {
    mocks.methods.mockResolvedValue({ legal })
    mount()
    await waitFor(() => expect(mocks.methods).toHaveBeenCalledTimes(1))
    fill()
    await waitFor(() => expect(screen.getByRole('button', { name: auth.register_submit }).hasAttribute('disabled')).toBe(false))
    expect(screen.queryByRole('checkbox')).toBeNull()
    if (!legal?.enabled) expect(screen.queryByRole('link', { name: '服务条款' })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: auth.register_submit }))
    await waitFor(() => expect(mocks.register).toHaveBeenCalledTimes(1))
    expect(mocks.register.mock.calls[0][0]).not.toHaveProperty('accepted_consent_version')
  })

  it('allows retrying a failed methods read instead of silently assuming consent is off', async () => {
    mocks.methods.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(methods)
    mount()
    expect(await screen.findByText(auth.legal.register_settings_failed)).toBeTruthy()
    expect(screen.getByRole('button', { name: auth.register_submit }).hasAttribute('disabled')).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByRole('checkbox', { name: auth.legal.checkbox_label })).toBeTruthy()
  })
})
