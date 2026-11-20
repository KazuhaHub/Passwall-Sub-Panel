// @vitest-environment jsdom
import { MemoryRouter, Route, Routes } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useAppearanceStore } from '@/stores/appearance'
import { useAuthStore } from '@/stores/auth'
import { useSiteStore } from '@/stores/site'
import UserShellRoute from './UserShellRoute'

vi.mock('react-i18next', async () => (await import('@/test/i18nMock')).reactI18nextMock())
vi.mock('@/components/ConsentDialog', () => ({ default: () => <div data-testid="consent" /> }))
vi.mock('@/api/client', () => ({ client: { get: vi.fn(() => new Promise(() => {})), post: vi.fn(), put: vi.fn(), delete: vi.fn() } }))
vi.mock('@/i18n', () => ({
  default: { t: (key: string) => key, language: 'zh-CN' },
  setLanguage: vi.fn(), currentLanguage: () => 'zh-CN',
  SUPPORTED_LANGUAGES: ['zh-CN', 'en-US'], isBuiltinLanguage: () => true, serverLanguageMeta: () => undefined,
}))

const siteLoad = vi.fn(async () => {})

beforeEach(() => {
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: false, media: query, addEventListener: vi.fn(), removeEventListener: vi.fn(),
    addListener: vi.fn(), removeListener: vi.fn(), onchange: null, dispatchEvent: vi.fn(),
  }))
  localStorage.clear()
  siteLoad.mockClear()
  useAuthStore.setState({ role: 'user', userId: 5, displayName: 'Alice', upn: 'alice', hasToken: true })
  useSiteStore.setState({ loaded: false, load: siteLoad, themeColor: '#336699', appTitle: 'Panel Name', siteTitle: 'Site', footerText: 'Footer text', legalEnabled: true })
  useAppearanceStore.setState({ systemColor: '#0061A4' })
})
afterEach(cleanup)

function mount() {
  return render(
    <MemoryRouter initialEntries={['/user/me']}>
      <Routes>
        <Route path="/user" element={<UserShellRoute />}>
          <Route path="me" element={<div data-testid="page">me</div>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  )
}

describe('UserShellRoute', () => {
  it('draws the page with no padding of its own around it, and no navigation', () => {
    mount()
    const content = document.getElementById('main-content')!
    expect(content.querySelector('[data-testid="page"]')).not.toBeNull()
    // MeView centres itself at 1200px with its own padding, as under the old layout.
    expect(content.className).toContain('p-0')
    expect(content.className).toContain('max-w-none')
    expect(content.className).toContain('sm:p-0')
    expect(screen.queryByRole('navigation')).toBeNull()
  })

  it('still renders the consent dialog and the site footer with its legal links', () => {
    mount()
    expect(screen.getByTestId('consent')).toBeTruthy()
    const footer = document.querySelector('footer')!
    expect(footer.textContent).toContain('Footer text')
    expect(footer.querySelectorAll('a')).toHaveLength(2)
  })

  it('loads the site branding and follows its theme colour', async () => {
    mount()
    expect(siteLoad).toHaveBeenCalledTimes(1)
    useSiteStore.setState({ loaded: true })
    await waitFor(() => expect(useAppearanceStore.getState().systemColor).toBe('#336699'))
  })

  it('has the log, language and appearance buttons but no bell or density toggle of its own', () => {
    mount()
    expect(screen.getByRole('button', { name: '提示记录' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '语言' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '外观' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: /^通知/ })).toBeNull()
  })

  it('names the panel and signs out through the auth store from the account button', async () => {
    const logout = vi.fn()
    useAuthStore.setState({ logout })
    mount()
    expect(screen.getByText('Panel Name')).toBeTruthy()
    const account = document.querySelector('[data-psp-account-menu]') as HTMLElement
    fireEvent.pointerDown(account, { button: 0, pointerType: 'mouse' })
    fireEvent.mouseDown(account, { button: 0 })
    fireEvent.click(account)
    fireEvent.click(await screen.findByRole('menuitem', { name: '退出登录' }))
    await waitFor(() => expect(logout).toHaveBeenCalled())
  })
})
