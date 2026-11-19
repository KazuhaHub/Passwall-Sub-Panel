// @vitest-environment jsdom
import { MemoryRouter, useLocation } from 'react-router'
import { fireEvent, cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { riskCenterKeys } from '@/query/keys'
import { sessionScope } from '@/query/session'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
// t over the REAL zh-CN bundles, so assertions read as the copy a user sees and a key
// the bell asks for but a bundle lacks shows up as its raw key.
vi.mock('react-i18next', async () => (await import('@/test/i18nMock')).reactI18nextMock())

import { NotificationBell } from './NotificationBell'

// Ported from components/NotificationBell.test.tsx: same cases, same copy, same routes.
// What changed is the query for the button (it is named by what it does now, not by a
// literal English aria-label) and for the icon (a lucide class, not a MUI test id).

function Location() {
  const loc = useLocation()
  return <div data-testid="location">{loc.pathname + loc.search}</div>
}

function mount() {
  const client = makeTestQueryClient()
  render(
    <MemoryRouter>
      <NotificationBell />
      <Location />
    </MemoryRouter>,
    { wrapper: queryWrapper(client) },
  )
  return client
}

function feed(...alerts: object[]) {
  return { data: { alerts, counts: { error: 0, warning: alerts.length, info: 0 } } }
}

const bell = () => screen.findByRole('button', { name: /^通知/ })
// Base UI menus open on pointer down.
function open(button: HTMLElement) {
  fireEvent.pointerDown(button, { button: 0, pointerType: 'mouse' })
  fireEvent.mouseDown(button, { button: 0 })
  fireEvent.click(button)
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
})
afterEach(cleanup)

describe('NotificationBell', () => {
  it('says notifications are unavailable when the first load fails, not that there are none', async () => {
    // "No notifications" is a claim about the server; a failed request is not
    // evidence for it. Reporting an empty success here would hide an outage.
    api.get.mockRejectedValue(new Error('network down'))
    mount()
    open(await bell())
    await waitFor(() => expect(screen.getByText('通知暂时不可用')).toBeTruthy())
    expect(screen.queryByText('暂无需要处理的通知')).toBeNull()
  })

  it('reports an empty feed as empty when the request succeeds', async () => {
    api.get.mockResolvedValue({ data: { alerts: [], counts: { error: 0, warning: 0, info: 0 } } })
    mount()
    open(await bell())
    await waitFor(() => expect(screen.getByText('暂无需要处理的通知')).toBeTruthy())
    expect(screen.queryByText('通知暂时不可用')).toBeNull()
  })

  it('renders the risk_queue title with its count and the shield', async () => {
    api.get.mockResolvedValue(feed({ key: 'risk_queue', type: 'risk_queue', severity: 'warning', count: 3 }))
    mount()
    open(await bell())
    // One entry for every account that needs action now, not a row each; the words
    // say "needs action now" because suspect-only accounts do not ring, so the
    // number is not the size of the whole queue.
    await waitFor(() => expect(screen.queryByText('3 个账号需立即处理')).not.toBeNull())
    expect(screen.getByRole('menuitem').querySelector('.lucide-shield')).not.toBeNull()
  })

  it('opens the queue filtered to the accounts that need action now, and refreshes it', async () => {
    api.get.mockResolvedValue(feed({ key: 'risk_queue', type: 'risk_queue', severity: 'warning', count: 1 }))
    const client = mount()
    // A queue page already in the cache (the bell lives in the top bar, so the risk
    // center may have been open a minute ago): clicking the entry must not land on
    // that stale page.
    const scope = sessionScope({ userId: null, role: '', authEpoch: 0 })
    const cached = riskCenterKeys.queue(scope, { urgent: true })
    client.setQueryData(cached, { items: [], total: 0 })

    open(await bell())
    fireEvent.click(await screen.findByRole('menuitem'))

    await waitFor(() => expect(screen.getByTestId('location').textContent).toBe('/admin/risk?tab=queue&urgent=1'))
    expect(client.getQueryState(cached)?.isInvalidated).toBe(true)
  })

  // The entries the risk center's one entry replaced have no deep link any more: a
  // feed that still carried one (a cached response across the upgrade) falls back to
  // the dashboard rather than to a retired tab.
  it.each(['geo_anomaly', 'risk_signals'])('has no route for the retired %s entry', async type => {
    api.get.mockResolvedValue(feed({ key: type, type, severity: 'warning', count: 1 }))
    mount()
    open(await bell())
    fireEvent.click(await screen.findByRole('menuitem'))
    await waitFor(() => expect(screen.getByTestId('location').textContent).toBe('/admin/dashboard'))
  })

  it.each([
    ['node_health', '/admin/nodes'],
    ['cert_failed', '/admin/certs'],
    ['cert_expiring', '/admin/certs'],
    ['panel_upgrade', '/admin/servers'],
    ['login_security', '/admin/logs'],
  ])('sends a %s alert to %s', async (type, route) => {
    api.get.mockResolvedValue(feed({ key: type, type, severity: 'warning', count: 2, target_name: 'X' }))
    mount()
    open(await bell())
    fireEvent.click(await screen.findByRole('menuitem'))
    await waitFor(() => expect(screen.getByTestId('location').textContent).toBe(route))
  })

  it('opens the releases page, not a route, for a PSP update', async () => {
    const opener = vi.spyOn(window, 'open').mockImplementation(() => null)
    api.get.mockResolvedValue(feed({ key: 'p', type: 'psp_upgrade', severity: 'info', latest_version: '9.9.9' }))
    mount()
    open(await bell())
    fireEvent.click(await screen.findByRole('menuitem'))
    expect(opener).toHaveBeenCalledWith(expect.stringContaining('/releases'), '_blank', 'noopener,noreferrer')
    expect(screen.getByTestId('location').textContent).toBe('/')
    opener.mockRestore()
  })

  it('names the bell with how many alerts need attention, and lists the worst first', async () => {
    api.get.mockResolvedValue(feed(
      { key: 'a', type: 'node_health', severity: 'warning', target_name: 'W' },
      { key: 'b', type: 'cert_failed', severity: 'error', target_name: 'E' },
    ))
    mount()
    const button = await screen.findByRole('button', { name: '通知, 2 项待处理' })
    open(button)
    const items = await screen.findAllByRole('menuitem')
    expect(items[0].textContent).toContain('E')
  })
})
