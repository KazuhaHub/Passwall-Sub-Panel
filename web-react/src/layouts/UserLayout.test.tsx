// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter, Route, Routes } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { flatten, type Nested } from '@/i18n/options'
import auth from '@/locales/zh-CN/auth.json'
import user from '@/locales/zh-CN/user.json'
import UserLayout from './UserLayout'

const mocks = vi.hoisted(() => ({
  auth: { userId: 7, role: 'user', authEpoch: 1, hasToken: true, logout: vi.fn() },
  site: { siteTitle: 'Site', appTitle: 'App', footerText: '', legalEnabled: true, loaded: false, themeColor: '', load: vi.fn() },
  profile: vi.fn(), refresh: vi.fn(), accept: vi.fn(), authCopy: {} as Record<string, string>, userCopy: {} as Record<string, string>,
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => mocks.auth, selectLabel: () => 'User' }))
vi.mock('@/stores/site', () => ({ useSiteStore: () => mocks.site }))
vi.mock('@/stores/appearance', () => ({ useAppearanceStore: () => ({ mode: 'light', systemColor: '', userColor: null }) }))
vi.mock('@/i18n', () => ({ setLanguage: vi.fn(), currentLanguage: () => 'zh-CN' }))
vi.mock('@/components/AppearanceMenu', () => ({ default: () => null }))
vi.mock('@/components/LanguageMenu', () => ({ default: () => null }))
vi.mock('@/components/BrandLogo', () => ({ default: () => null }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn() }))
vi.mock('@/query/me', () => ({ useMyProfile: mocks.profile }))
vi.mock('@/api/legal', () => ({ acceptLegalConsent: mocks.accept }))
vi.mock('react-i18next', () => ({ useTranslation: (namespace: string) => ({
  t: (key: string) => (namespace === 'user' ? mocks.userCopy : mocks.authCopy)[key] ?? key,
  i18n: { language: 'zh-CN', resolvedLanguage: 'zh-CN' },
}) }))
mocks.authCopy = flatten(auth as Nested)
mocks.userCopy = flatten(user as Nested)

function view() {
  return <MemoryRouter initialEntries={['/user/me']}><ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'zh-CN' })}>
    <Routes><Route path="/user" element={<UserLayout />}><Route path="me" element={<p>Portal remains mounted</p>} /></Route></Routes>
  </ThemeProvider></MemoryRouter>
}
beforeEach(() => {
  vi.clearAllMocks()
  sessionStorage.clear()
  mocks.auth.role = 'user'
  mocks.auth.authEpoch = 1
  mocks.site.legalEnabled = true
  mocks.profile.mockReturnValue({ data: { legal_pending: true, legal_consent_version: 2 }, refetch: mocks.refresh })
  mocks.refresh.mockResolvedValue(undefined)
})
afterEach(cleanup)

describe('UserLayout consent wiring', () => {
  it.each(['admin', 'operator'])('does not fetch or mount the ordinary-user prompt for %s', role => {
    mocks.auth.role = role
    render(view())
    expect(mocks.profile).not.toHaveBeenCalled()
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.getByRole('link', { name: '服务条款' })).toBeTruthy()
  })

  it('keeps the portal mounted and scopes later to the current account login', async () => {
    const { rerender } = render(view())
    expect(mocks.profile).toHaveBeenCalledWith(expect.objectContaining({ userId: 7, authEpoch: 1, role: 'user' }))
    expect(screen.getByRole('dialog')).toBeTruthy()
    expect(screen.getByText('Portal remains mounted')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '稍后' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(sessionStorage.getItem('psp-legal-later-7-1')).toBe('later')
    mocks.auth.authEpoch = 2
    rerender(view())
    expect(screen.getByRole('dialog')).toBeTruthy()
    expect(mocks.profile).toHaveBeenLastCalledWith(expect.objectContaining({ authEpoch: 2 }))
  })

  it('does not prompt or render legal links for disabled nonpending legal state', () => {
    mocks.site.legalEnabled = false
    mocks.profile.mockReturnValue({ data: { legal_pending: false, legal_consent_version: 2 }, refetch: mocks.refresh })
    render(view())
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.queryByRole('link', { name: '服务条款' })).toBeNull()
    expect(screen.getByText('Portal remains mounted')).toBeTruthy()
  })
})
