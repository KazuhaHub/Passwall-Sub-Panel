// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('react-i18next', async () => (await import('@/test/i18nMock')).reactI18nextMock())
// The shell's sync-pending link imports the i18n bootstrap, which fetches over HTTP.
vi.mock('@/i18n', () => ({ default: { t: (k: string) => k } }))

import { ADMIN_NAV } from './adminNav'
import { AppShell, type AppShellProps } from './AppShell'
import { notifySyncPending } from '@/components/feedback/syncPendingToast'
import ToastHost from '@/components/feedback/ToastHost'

function Where() {
  return <div data-testid="where">{useLocation().pathname}</div>
}

function setViewport(mobile: boolean, narrow = false) {
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    // Two width queries exist: the sidebar's phone breakpoint, and the top bar's "too narrow for the log button".
    matches: (mobile && query.includes('max-width: 767px')) || (narrow && query.includes('max-width: 359px')), media: query,
    addEventListener: vi.fn(), removeEventListener: vi.fn(), addListener: vi.fn(), removeListener: vi.fn(),
    onchange: null, dispatchEvent: vi.fn(),
  }))
}

beforeEach(() => {
  setViewport(false)
  localStorage.clear()
  globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} } as never
  Element.prototype.scrollIntoView = vi.fn()
})
afterEach(cleanup)

const baseProps = (over: Partial<AppShellProps> = {}): AppShellProps => ({
  nav: ADMIN_NAV,
  role: 'admin',
  brand: { name: 'Passwall' },
  user: { label: 'kazuha', role: 'admin', onLogout: vi.fn() },
  ...over,
})

function mount(props: Partial<AppShellProps> = {}, at = '/admin/dashboard') {
  return render(
    <MemoryRouter initialEntries={[at]}>
      <Routes>
        <Route element={<AppShell {...baseProps(props)} />}>
          <Route path="/admin/*" element={<><Where /><div data-testid="page">page body</div></>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  )
}

const nav = () => screen.getByRole('navigation')
const linkNames = () => within(nav()).getAllByRole('link').map(a => a.textContent)

describe('navigation', () => {
  it('scrolls the lit item into view, clear of the list fades', () => {
    const spy = vi.fn()
    Element.prototype.scrollIntoView = spy
    mount({}, '/admin/users')
    expect(spy).toHaveBeenCalledWith({ block: 'nearest' })
    expect((spy.mock.contexts[0] as HTMLElement).getAttribute('aria-current')).toBe('page')
  })

  it('draws the sections with their headers, and the page in the outlet', () => {
    mount()
    expect(screen.getByTestId('page')).toBeTruthy()
    for (const header of ['目录', '基础设施', '订阅', '报表']) expect(within(nav()).getByText(header)).toBeTruthy()
    expect(linkNames()).toHaveLength(18)
    expect(within(nav()).getByRole('link', { name: '用户管理' }).getAttribute('href')).toBe('/admin/users')
  })

  it('shows an operator only what the old layout showed, headers included', () => {
    mount({ role: 'operator', user: { label: 'op', role: 'operator', onLogout: vi.fn() } })
    expect(linkNames()).toHaveLength(10)
    for (const hidden of ['服务器', '证书', '订阅客户端', '运行诊断', '风控中心', '访问控制', '语言包', '系统设置']) {
      expect(within(nav()).queryByText(hidden)).toBeNull()
    }
    expect(within(nav()).getByText('目录')).toBeTruthy()
  })

  it('marks the active item by route prefix, and only that one', () => {
    mount({}, '/admin/users/12')
    const current = within(nav()).getAllByRole('link').filter(a => a.getAttribute('aria-current') === 'page')
    expect(current.map(a => a.textContent)).toEqual(['用户管理'])
    expect(current[0].getAttribute('data-active')).not.toBeNull()
  })

  it('tells the model’s owner when the pointer, focus or a touch reaches an item', () => {
    const onNavIntent = vi.fn()
    mount({ onNavIntent })
    const link = within(nav()).getByRole('link', { name: '节点管理' })
    fireEvent.pointerEnter(link)
    fireEvent.focus(link)
    fireEvent.touchStart(link)
    expect(onNavIntent).toHaveBeenCalledTimes(3)
    expect(onNavIntent).toHaveBeenCalledWith('/admin/nodes')
  })

  it('names the navigation landmark', () => {
    mount()
    expect(nav().getAttribute('aria-label')).toBe('主导航')
  })

  // THE PRODUCTION SERVER INJECTS <base href="<panel path>/">, which makes a "#main-content" link
  // resolve against the base: following it navigated away from the page. The skip control must
  // move focus itself and never touch the location.
  it('skips to the content by moving focus, not by navigating, even under a <base href>', () => {
    const base = document.createElement('base')
    base.href = 'http://localhost/x/'
    document.head.appendChild(base)
    try {
      mount({}, '/admin/users')
      const before = window.location.href
      const skip = screen.getByRole('button', { name: '跳到主要内容' })
      expect(skip.getAttribute('href')).toBeNull()
      fireEvent.click(skip)
      expect(window.location.href).toBe(before)
      expect(screen.getByTestId('where').textContent).toBe('/admin/users')
      expect(document.activeElement).toBe(screen.getByRole('main'))
      expect(Element.prototype.scrollIntoView).toHaveBeenCalled()
    } finally {
      base.remove()
    }
  })

  it('has one banner, one main and a navigation, with the top-bar controls in the banner and not in main', () => {
    mount({ topBarActions: <button>slot-action</button> })
    expect(screen.getAllByRole('banner')).toHaveLength(1)
    expect(screen.getAllByRole('main')).toHaveLength(1)
    const banner = screen.getByRole('banner')
    const main = screen.getByRole('main')
    expect(main.id).toBe('main-content')
    expect(within(banner).getByText('slot-action')).toBeTruthy()
    expect(within(banner).getByRole('button', { name: /账号菜单/ })).toBeTruthy()
    expect(main.contains(banner)).toBe(false)
    expect(within(main).getByTestId('page')).toBeTruthy()
    expect(nav()).toBeTruthy()
  })
})

describe('footer version', () => {
  it('shows the version string, and nothing when there is none', () => {
    const { unmount } = mount({ version: { version: 'v4.3.0-beta.1', channel: 'beta', commit: 'abc1234', build_date: '2026-11-19' } })
    expect(screen.getByText('v4.3.0-beta.1')).toBeTruthy()
    unmount()
    mount({ version: null })
    expect(screen.queryByText('v4.3.0-beta.1')).toBeNull()
  })

  it('falls back to dev for a build with no version', () => {
    mount({ version: { version: '', commit: '', build_date: '' } })
    expect(screen.getByText('dev')).toBeTruthy()
  })
})

describe('collapsing to the icon rail', () => {
  const state = () => document.querySelector('[data-slot="sidebar"]')?.getAttribute('data-state')

  it('toggles from the button in the sidebar and remembers it', () => {
    mount()
    expect(state()).toBe('expanded')
    const button = screen.getByRole('button', { name: '展开或收起侧栏' })
    expect(button.closest('[data-slot="sidebar-footer"]')).not.toBeNull()
    fireEvent.click(button)
    expect(state()).toBe('collapsed')
    expect(localStorage.getItem('psp-sidebar-collapsed')).toBe('1')
  })

  it('starts collapsed when the old layout’s stored choice says so', () => {
    localStorage.setItem('psp-sidebar-collapsed', '1')
    mount()
    expect(state()).toBe('collapsed')
  })

  it('toggles on Ctrl+B, but not while typing in a field', () => {
    mount()
    const search = document.createElement('input')
    document.body.appendChild(search)
    fireEvent.keyDown(search, { key: 'b', ctrlKey: true })
    expect(state()).toBe('expanded')
    fireEvent.keyDown(document.body, { key: 'b', ctrlKey: true })
    expect(state()).toBe('collapsed')
    search.remove()
  })

  it('keeps every item reachable by keyboard when collapsed, labelled by tooltip on focus', async () => {
    localStorage.setItem('psp-sidebar-collapsed', '1')
    mount()
    const link = within(nav()).getByRole('link', { name: '用户管理' })
    fireEvent.focus(link)
    await waitFor(() => expect(document.querySelector('[data-slot="tooltip-content"]')?.textContent).toBe('用户管理'))
  })
})

describe('phone width', () => {
  beforeEach(() => setViewport(true))

  it('has no desktop rail; the menu is a sheet that closes when a link is followed', async () => {
    mount()
    expect(document.querySelector('[data-slot="sidebar"][data-state]')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: '打开菜单' }))
    const sheet = await screen.findByRole('dialog', { name: '导航菜单' })
    fireEvent.click(within(sheet).getByRole('link', { name: '分组' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '导航菜单' })).toBeNull())
  })

  it('shows the current page’s name in the top bar', () => {
    mount({}, '/admin/groups')
    expect(screen.getByRole('heading', { level: 2 }).textContent).toBe('分组')
  })
})

describe('nav tooltips', () => {
  const tooltip = () => document.querySelector('[data-slot="tooltip-content"]')

  it('are for the icon rail only: none on an expanded desktop sidebar', async () => {
    mount()
    fireEvent.focus(within(nav()).getByRole('link', { name: '用户管理' }))
    await new Promise(r => setTimeout(r, 50))
    expect(tooltip()).toBeNull()
  })

  it('are not opened in the phone sheet, so one Escape closes the sheet', async () => {
    setViewport(true)
    mount()
    fireEvent.click(screen.getByRole('button', { name: '打开菜单' }))
    const sheet = await screen.findByRole('dialog', { name: '导航菜单' })
    const link = within(sheet).getByRole('link', { name: '分组' })
    link.focus()
    fireEvent.focus(link)
    await new Promise(r => setTimeout(r, 50))
    expect(tooltip()).toBeNull()
    fireEvent.keyDown(link, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '导航菜单' })).toBeNull())
  })
})

describe('top bar at 320px', () => {
  const logButton = () => screen.queryByRole('button', { name: '提示记录' })
  const openAccount = async () => {
    const account = screen.getByRole('button', { name: /账号菜单/ })
    fireEvent.pointerDown(account, { button: 0, pointerType: 'mouse' })
    fireEvent.mouseDown(account, { button: 0 })
    fireEvent.click(account)
    await screen.findByRole('menuitem', { name: '退出登录' })
  }

  it('keeps the log button in the bar where there is room, and out of the account menu', async () => {
    mount()
    expect(logButton()).toBeTruthy()
    await openAccount()
    expect(screen.queryByRole('menuitem', { name: '提示记录' })).toBeNull()
  })

  // Below 360px the bar's seven controls do not fit and the log button overlapped the search
  // button, the phone's only way to the command palette. The log moves into the account menu.
  it('moves the log into the account menu where the bar has no room, never in both places', async () => {
    setViewport(false, true)
    mount()
    expect(logButton()).toBeNull()
    expect(screen.getAllByRole('button', { name: /搜索|打开命令面板/ }).length).toBeGreaterThan(0)
    await openAccount()
    expect(screen.getByRole('menuitem', { name: '提示记录' })).toBeTruthy()
  })
})

describe('top bar', () => {
  it('renders the action slot and the account menu, and signing out calls back', async () => {
    const onLogout = vi.fn()
    mount({ topBarActions: <button>slot-action</button>, user: { label: 'kazuha', role: 'admin', onLogout } })
    expect(screen.getByText('slot-action')).toBeTruthy()
    const account = screen.getByRole('button', { name: /账号菜单/ })
    fireEvent.pointerDown(account, { button: 0, pointerType: 'mouse' })
    fireEvent.mouseDown(account, { button: 0 })
    fireEvent.click(account)
    expect(await screen.findByText('管理员')).toBeTruthy()
    fireEvent.click(await screen.findByRole('menuitem', { name: '退出登录' }))
    expect(onLogout).toHaveBeenCalled()
  })

  it('opens the command palette from the search button and from Ctrl+K, listing the same pages', async () => {
    mount({ role: 'operator', user: { label: 'op', role: 'operator', onLogout: vi.fn() } })
    fireEvent.click(screen.getAllByRole('button', { name: /搜索|打开命令面板/ })[0])
    const dialog = await screen.findByRole('dialog', { name: '命令面板' })
    expect(within(dialog).getAllByRole('option')).toHaveLength(10)
    expect(within(dialog).queryByText('系统设置')).toBeNull()
  })

  it('takes extra palette providers without the shell knowing about them', async () => {
    mount({ commandProviders: [{ id: 'x', heading: '操作', search: () => [{ id: 'e', label: '导出日志', to: '/admin/logs' }] }] })
    act(() => { window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true, bubbles: true })) })
    expect(await screen.findByText('导出日志')).toBeTruthy()
  })
})

describe('sync-pending link', () => {
  it('lets the toast open the sync tasks page through the router, and stops when the shell goes', async () => {
    const { unmount } = render(
      <MemoryRouter initialEntries={['/admin/dashboard']}>
        <Routes>
          <Route element={<AppShell {...baseProps()} />}>
            <Route path="/admin/*" element={<Where />} />
          </Route>
        </Routes>
        <ToastHost />
      </MemoryRouter>,
    )
    act(() => notifySyncPending())
    fireEvent.click(await screen.findByRole('button', { name: 'common:ui.feedback.sync_pending_action' }))
    await waitFor(() => expect(screen.getByTestId('where').textContent).toBe('/admin/sync-tasks'))
    unmount()
    // With the shell gone nothing can honour the link, so the next toast has none.
    cleanup()
    render(<ToastHost />)
    act(() => notifySyncPending())
    expect(screen.queryByRole('button', { name: 'common:ui.feedback.sync_pending_action' })).toBeNull()
  })
})
