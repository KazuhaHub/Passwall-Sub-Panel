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
  })
})
