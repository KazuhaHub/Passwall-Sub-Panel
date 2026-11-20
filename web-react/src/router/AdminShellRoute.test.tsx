// @vitest-environment jsdom
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useAppearanceStore } from '@/stores/appearance'
import { useAuthStore } from '@/stores/auth'
import { useSiteStore } from '@/stores/site'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { DEFAULT_PRESET_HEX } from '@/theme'
import AdminShellRoute from './AdminShellRoute'

vi.mock('react-i18next', async () => (await import('@/test/i18nMock')).reactI18nextMock())

const prefetch = vi.hoisted(() => ({
  prefetchView: vi.fn(),
  prefetchViewsWhenIdle: vi.fn((_paths: readonly string[]) => () => {}),
}))
vi.mock('@/router/prefetch', () => prefetch)

const version = vi.hoisted(() => ({ getVersion: vi.fn() }))
vi.mock('@/api/version', () => version)

// The bell's alert poll is not under test; a read that never answers keeps it quiet.
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
  globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} } as never
  localStorage.clear()
  prefetch.prefetchView.mockClear()
  prefetch.prefetchViewsWhenIdle.mockClear()
  version.getVersion.mockReset()
  version.getVersion.mockResolvedValue({ version: 'v9.8.7', channel: 'stable', commit: '', build_date: '' })
  siteLoad.mockClear()
  useAuthStore.setState({ role: 'admin', userId: 1, displayName: 'Kazuha', upn: 'kazuha', hasToken: true })
  useSiteStore.setState({
    loaded: false, load: siteLoad, themeColor: '#336699', siteTitle: 'Site', appTitle: 'Panel Name', logoUrl: '/l.png', logoUrlDark: '/d.png', iconUrl: '/i.png',
  })
  useAppearanceStore.setState({ systemColor: '#112233' })
})
afterEach(cleanup)

function mount() {
  return render(
    <MemoryRouter initialEntries={['/admin/dashboard']}>
      <QueryClientProvider client={makeTestQueryClient()}>
        <Routes>
          <Route path="/admin" element={<AdminShellRoute />}>
            <Route path="dashboard" element={<div data-testid="page">page</div>} />
            <Route path="*" element={<div data-testid="other">other</div>} />
          </Route>
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  )
}

const nav = () => screen.getByRole('navigation')
const link = (name: string) => within(nav()).getByRole('link', { name })

describe('AdminShellRoute navigation', () => {
  // THE CLICK IS PRECEDED BY A HOVER, A FOCUS OR A TOUCH, and each is a head start on the chunk
  // the click will need: the difference between a page that opens and one that spins on a slow link.
  it.each([
    ['pointer', (el: HTMLElement) => fireEvent.pointerEnter(el)],
    ['keyboard focus', (el: HTMLElement) => fireEvent.focus(el)],
    ['touch', (el: HTMLElement) => fireEvent.touchStart(el)],
  ])('warms a view on %s intent', (_, intent) => {
    mount()
    intent(link('用户管理'))
    expect(prefetch.prefetchView).toHaveBeenCalledWith('/admin/users')
  })

  it('queues every view an admin can reach for idle loading, in menu order', () => {
    mount()
    const paths = prefetch.prefetchViewsWhenIdle.mock.calls.at(-1)![0]
    expect(paths).toHaveLength(18)
    expect(paths.slice(0, 3)).toEqual(['/admin/dashboard', '/admin/users', '/admin/groups'])
    for (const p of ['/admin/risk', '/admin/access-control', '/admin/settings', '/admin/servers']) expect(paths).toContain(p)
  })

  it('queues for an operator none of the admin-only views, and shows none of them', () => {
    useAuthStore.setState({ role: 'operator' })
    mount()
    const paths = prefetch.prefetchViewsWhenIdle.mock.calls.at(-1)![0]
    expect(paths).toHaveLength(10)
    for (const p of ['/admin/servers', '/admin/settings', '/admin/risk', '/admin/access-control', '/admin/certs']) expect(paths).not.toContain(p)
    expect(within(nav()).queryByRole('link', { name: '系统设置' })).toBeNull()
  })

  it('shows the page in the content area with no padding of its own around it', () => {
    mount()
    const content = document.getElementById('main-content')!
    expect(within(content).getByTestId('page')).toBeTruthy()
    // The MUI pages carry their own padding and width, as they did under the old layout.
    expect(content.className).toContain('p-0')
    expect(content.className).toContain('max-w-none')
    // The shell's own responsive padding is switched off at every width, not only the base one.
    expect(content.className).toContain('sm:p-0')
    expect(content.className).not.toContain('sm:p-6')
  })
})

describe('AdminShellRoute chrome', () => {
  it('loads the site branding once, and follows the site theme colour once it is loaded', () => {
    const { unmount } = mount()
    expect(siteLoad).toHaveBeenCalledTimes(1)
    expect(useAppearanceStore.getState().systemColor).toBe('#112233')
    unmount()
    cleanup()
    useSiteStore.setState({ loaded: true, themeColor: '#336699' })
    mount()
    expect(useAppearanceStore.getState().systemColor).toBe('#336699')
  })

  it('falls back to the compiled default colour when the site has none', () => {
    useSiteStore.setState({ loaded: true, themeColor: undefined })
    mount()
    expect(useAppearanceStore.getState().systemColor).toBe(DEFAULT_PRESET_HEX)
  })

  it('names the panel from the site title and shows the build version in the footer', async () => {
    mount()
    expect(within(nav().closest('[data-slot="sidebar"]') as HTMLElement).getByText('Panel Name')).toBeTruthy()
    expect(await screen.findByText('v9.8.7')).toBeTruthy()
  })

  it('has the bell, language, appearance and log buttons, and the account button the focus fallback finds', () => {
    mount()
    expect(screen.getByRole('button', { name: /^通知/ })).toBeTruthy()
    expect(screen.getByRole('button', { name: '语言' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '外观' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '提示记录' })).toBeTruthy()
    expect(document.querySelectorAll('[data-psp-account-menu]')).toHaveLength(1)
  })

  it('signs out from the account menu through the auth store', async () => {
    const logout = vi.fn()
    useAuthStore.setState({ logout })
    mount()
    const account = document.querySelector('[data-psp-account-menu]') as HTMLElement
    fireEvent.pointerDown(account, { button: 0, pointerType: 'mouse' })
    fireEvent.mouseDown(account, { button: 0 })
    fireEvent.click(account)
    expect(await screen.findByText('Kazuha')).toBeTruthy()
    fireEvent.click(await screen.findByRole('menuitem', { name: '退出登录' }))
    await waitFor(() => expect(logout).toHaveBeenCalled())
  })

  it('does not put the message log in the account menu as well', async () => {
    mount()
    const account = document.querySelector('[data-psp-account-menu]') as HTMLElement
    fireEvent.pointerDown(account, { button: 0, pointerType: 'mouse' })
    fireEvent.mouseDown(account, { button: 0 })
    fireEvent.click(account)
    await screen.findByRole('menuitem', { name: '退出登录' })
    expect(screen.queryByRole('menuitem', { name: '提示记录' })).toBeNull()
  })

  it('keeps focus on the nav link after following it (the sidebar is not remounted by a route change)', () => {
    mount()
    const users = link('用户管理')
    users.focus()
    fireEvent.click(users)
    expect(screen.getByTestId('other')).toBeTruthy()
    expect(document.activeElement).toBe(users)
  })
})
