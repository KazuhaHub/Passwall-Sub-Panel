// @vitest-environment jsdom
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react'
import { useAuthStore } from '@/stores/auth'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as acApi from '@/api/accessControl'
import * as riskApi from '@/api/riskCenter'
import { confirmMock, renderAccess } from '@/test/accessControlHarness'
import RiskUserDrawer from './RiskUserDrawer'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})
vi.mock('@/api/riskCenter', async importOriginal => ({ ...await importOriginal<typeof import('@/api/riskCenter')>(), getRiskUser: vi.fn() }))
// The other four tabs have their own tests; here they are inert.
for (const t of ['Overview', 'Connections', 'Devices', 'Timeline']) {
  vi.doMock(`./${t}Tab`, () => ({ default: () => <p data-testid={`${t}-tab`} /> }))
}
vi.mock('./DrawerHeader', () => ({ default: () => <p data-testid="risk-drawer-header" /> }))
vi.mock('../../accessControl/sheets/AddExemptionDialog', () => ({
  default: ({ open, lockedUserId }: { open: boolean; lockedUserId?: number }) => open ? <p data-testid="add-exemption">{lockedUserId}</p> : null,
}))

const summary = {
  user: { id: 7, upn: 'alice', display_name: 'Alice', role: 'user', group_id: 2, group_name: 'Team A', enabled: true, traffic_limit_bytes: 0,
    access: { account_state: 'active', service_state: 'active', can_login: true, can_use_portal: true, can_subscribe: true, proxy_enabled: true } },
  attention: [], review: { dismissed: false, trusted: false, levels: {}, escalated: [] }, geo: null, signals: [], live: null, devices: [],
  device_window_hours: 24, devices_unavailable: false,
} as unknown as riskApi.RiskUserSummary

const view = (over: Partial<acApi.DestUserView> = {}): acApi.DestUserView => ({
  user_id: 7, upn: 'alice', exemption: null, group: { id: 2, name: 'Team A', mode: 'open', stage: null },
  hits_available: null, recent_hits: null, usage_available: null, usage_nodes: null, ...over,
})

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(riskApi.getRiskUser).mockResolvedValue(summary)
  vi.mocked(acApi.getDestUser).mockResolvedValue(view())
  vi.mocked(acApi.getDestStatus).mockResolvedValue({ apply_eta_ms: 0, nodes: [], generation: 0, published_generation: 0, paused: false, publish_error: null, policy_notes: {}, totals: {} } as unknown as acApi.DestStatus)
})

const tabs = () => screen.getAllByRole('tab').map(t => t.textContent)

describe('the access tab', () => {
  it.each(['risk', 'users', 'access'] as const)('is the fifth tab for an admin on host %s', async host => {
    renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host={host} />)
    await screen.findByTestId('risk-drawer-header')
    expect(tabs()).toHaveLength(5)
    expect(tabs()[4]).toBe('访问')
  })

  it('is absent for an operator', async () => {
    renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="users" />, { role: 'operator' })
    await screen.findByTestId('risk-drawer-header')
    expect(tabs()).toHaveLength(4)
  })

  it('reads the account only after the tab is chosen', async () => {
    renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="risk" />)
    await screen.findByTestId('risk-drawer-header')
    expect(acApi.getDestUser).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('tab', { name: '访问' }))
    expect(await screen.findByText('Team A')).toBeTruthy()
    expect(acApi.getDestUser).toHaveBeenCalledTimes(1)
    expect(screen.getByText('不限')).toBeTruthy()
    expect(screen.getByText('未豁免')).toBeTruthy()
  })

  it('host access opens on it', async () => {
    renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="access" initialTab="access" />)
    expect(await screen.findByText('Team A')).toBeTruthy()
    expect(screen.getByRole('tab', { name: '访问' }).getAttribute('aria-selected')).toBe('true')
  })

  it('opens the exemption dialog locked to this account', async () => {
    renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="access" initialTab="access" />)
    fireEvent.click(await screen.findByRole('button', { name: '豁免此账号…' }))
    expect((await screen.findByTestId('add-exemption')).textContent).toBe('7')
  })

  it('lifts an exemption after asking, through the same mutation as the sheet', async () => {
    vi.mocked(acApi.getDestUser).mockResolvedValue(view({ exemption: {
      user_id: 7, upn: 'alice', reason: 'debugging', created_by: 1, created_by_upn: 'root', created_at: Date.now() - 3_600_000,
      expires_at: null, expired: false,
    } }))
    vi.mocked(acApi.deleteDestExemption).mockResolvedValue()
    renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="access" initialTab="access" />)
    expect(await screen.findByText(/豁免：debugging · 由 root/)).toBeTruthy()
    expect(screen.getByText('访问策略不对这个账号生效')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '取消豁免' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '取消 alice 的豁免？' })))
    await waitFor(() => expect(acApi.deleteDestExemption).toHaveBeenCalledWith(7))
  })

  it('shows a tab-local error with a retry', async () => {
    vi.mocked(acApi.getDestUser).mockRejectedValueOnce(new Error('x'))
    renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="access" initialTab="access" />)
    expect(await screen.findByText('读取这个账号的访问信息失败')).toBeTruthy()
    expect(screen.getAllByRole('tab')).toHaveLength(5)
  })

  it('scrolls the tabs on a phone only when all five show', async () => {
    const mm = window.matchMedia
    window.matchMedia = ((q: string) => ({ matches: true, media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, onchange: null, dispatchEvent: () => false })) as typeof window.matchMedia
    try {
      renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="users" />, { role: 'operator' })
      await screen.findByTestId('risk-drawer-header')
      expect(document.querySelector('.MuiTabs-scrollableX')).toBeNull()
      cleanup()
      renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="users" />)
      await screen.findByTestId('risk-drawer-header')
      expect(document.querySelector('.MuiTabs-scrollableX')).not.toBeNull()
    } finally { window.matchMedia = mm }
  })

  it('falls back to the overview when the permission to see the access tab goes while it is selected', async () => {
    renderAccess(<RiskUserDrawer userId={7} onClose={vi.fn()} host="access" initialTab="access" />)
    await screen.findByText('Team A')
    act(() => { useAuthStore.setState({ role: 'operator' }) })
    await waitFor(() => expect(tabs()).toHaveLength(4))
    expect(screen.getByRole('tab', { name: '概览' }).getAttribute('aria-selected')).toBe('true')
  })
})
