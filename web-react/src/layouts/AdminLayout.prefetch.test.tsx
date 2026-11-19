// @vitest-environment jsdom
import type { ReactElement } from 'react'
import { ThemeProvider } from '@mui/material/styles'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { useAuthStore } from '@/stores/auth'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import AdminLayout from './AdminLayout'

const mq = vi.hoisted(() => ({ roomForLog: true }))
// useLogInBar's one query: whether the bar has room for the log button.
vi.mock('@/hooks/useMediaQuery', () => ({ useMediaQuery: () => mq.roomForLog }))

const prefetch = vi.hoisted(() => ({
  prefetchView: vi.fn(),
  prefetchViewsWhenIdle: vi.fn((_paths: readonly string[]) => () => {}),
}))
vi.mock('@/router/prefetch', () => prefetch)
// The layout's own reads (version badge, notifications) are not under test; a
// read that never answers keeps them quiet.
vi.mock('@/api/client', () => ({ client: { get: vi.fn(() => new Promise(() => {})), post: vi.fn(), put: vi.fn(), delete: vi.fn() } }))
vi.mock('@/i18n', () => ({
  default: { t: (key: string) => key, language: 'en-US' },
  setLanguage: vi.fn(), currentLanguage: () => 'en-US',
  SUPPORTED_LANGUAGES: ['zh-CN', 'en-US'], isBuiltinLanguage: () => true, serverLanguageMeta: () => undefined,
}))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en-US' } }),
  Trans: ({ children }: { children: ReactElement }) => children,
}))

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

function mountLayout() {
  return render(
    <MemoryRouter initialEntries={['/admin/dashboard']}>
      <ThemeProvider theme={theme}>
        <QueryClientProvider client={makeTestQueryClient()}><AdminLayout /></QueryClientProvider>
      </ThemeProvider>
    </MemoryRouter>,
  )
}

function navItem(labelKey: string) {
  return screen.getByText(labelKey).closest('[role="button"]') as HTMLElement
}

describe('AdminLayout view prefetching', () => {
  beforeEach(() => {
    prefetch.prefetchView.mockClear()
    prefetch.prefetchViewsWhenIdle.mockClear()
    mq.roomForLog = true
    useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
  })
  afterEach(cleanup)

  // THE CLICK IS PRECEDED BY A HOVER, A FOCUS OR A TOUCH, and each is a head start
  // on the chunk the click will need — the difference between a page that opens
  // and one that spins on a slow link.
  it.each([
    ['pointer', (el: HTMLElement) => fireEvent.mouseEnter(el)],
    ['keyboard focus', (el: HTMLElement) => fireEvent.focus(el)],
    ['touch', (el: HTMLElement) => fireEvent.touchStart(el)],
  ])('warms a view on %s intent', async (_, intent) => {
    mountLayout()
    const users = await waitFor(() => navItem('nav:admin.users'))
    intent(users)
    expect(prefetch.prefetchView).toHaveBeenCalledWith('/admin/users')
  })

  it('queues every view the role can reach for idle loading, and none it cannot', async () => {
    useAuthStore.setState({ role: 'operator' })
    mountLayout()
    await waitFor(() => expect(prefetch.prefetchViewsWhenIdle).toHaveBeenCalled())
    const paths = prefetch.prefetchViewsWhenIdle.mock.calls.at(-1)![0]
    expect(paths).toContain('/admin/users')
    expect(paths).toContain('/admin/nodes')
    // Operators never see the admin-only items, so their chunks are not fetched.
    expect(paths).not.toContain('/admin/servers')
    expect(paths).not.toContain('/admin/settings')
    expect(paths).not.toContain('/admin/risk')
    expect(paths).not.toContain('/admin/access-control')
  })

  // The risk center is a nav item like any other for an admin, so its chunk is
  // warmed with the rest; the operator case above proves the adminOnly flag
  // keeps it out of an operator's queue.
  it("queues the risk center for an admin's idle loading", async () => {
    mountLayout()
    await waitFor(() => expect(prefetch.prefetchViewsWhenIdle).toHaveBeenCalled())
    const paths = prefetch.prefetchViewsWhenIdle.mock.calls.at(-1)![0]
    expect(paths).toContain('/admin/risk')
    expect(paths).toContain('/admin/settings')
  })

  // Gate 3 of 3: the nav item is adminOnly, so an admin is offered it (and its
  // chunk is warmed) and an operator is not (the operator case above).
  it("queues access control for an admin's idle loading", async () => {
    mountLayout()
    await waitFor(() => expect(prefetch.prefetchViewsWhenIdle).toHaveBeenCalled())
    const paths = prefetch.prefetchViewsWhenIdle.mock.calls.at(-1)![0]
    expect(paths).toContain('/admin/access-control')
  })

  // Until the new shell replaces this layout, the message log (errors that came too fast to
  // be shown as toasts) is reachable from this app bar: as a button where the bar has room,
  // as an account-menu item where it has not, and never both.
  const openAccountMenu = async () => {
    const avatar = await waitFor(() => document.querySelector('.MuiAvatar-root')!.closest('button') as HTMLElement)
    fireEvent.click(avatar)
  }

  it('renders the log BUTTON where the bar has room, and no menu item', async () => {
    mq.roomForLog = true
    mountLayout()
    const log = await screen.findByRole('button', { name: 'ui.feedback.log_title' })
    expect(log.closest('header')).not.toBeNull()
    expect(log.className).toContain('size-10')
    await openAccountMenu()
    await screen.findByRole('menuitem', { name: 'common:auth.logout' })
    expect(screen.queryByRole('menuitem', { name: 'ui.feedback.log_title' })).toBeNull()
    expect(document.querySelector('[data-slot="toast-log-menu-count"]')).toBeNull()
  })

  it('renders the log MENU ITEM where it has not, and no button', async () => {
    mq.roomForLog = false
    mountLayout()
    await waitFor(() => expect(document.querySelector('.MuiAvatar-root')).not.toBeNull())
    expect(screen.queryByRole('button', { name: 'ui.feedback.log_title' })).toBeNull()
    await openAccountMenu()
    expect(await screen.findByRole('menuitem', { name: 'ui.feedback.log_title' })).toBeTruthy()
  })

  it('leaves the account menu keyboard-operable when the item is absent: the first real item takes focus and Enter signs out', async () => {
    mq.roomForLog = true
    const logout = vi.fn()
    useAuthStore.setState({ logout })
    mountLayout()
    await openAccountMenu()
    const signOut = await screen.findByRole('menuitem', { name: 'common:auth.logout' })
    // MUI focuses the first item it can: with no hidden item in the way, that is "log out".
    await waitFor(() => expect(document.activeElement).toBe(signOut))
    fireEvent.keyDown(signOut, { key: 'ArrowDown' })
    fireEvent.keyDown(signOut, { key: 'Home' })
    expect(document.activeElement).toBe(signOut)
    fireEvent.keyDown(signOut, { key: 'Enter' })
    fireEvent.click(signOut)
    expect(logout).toHaveBeenCalled()
  })
})
