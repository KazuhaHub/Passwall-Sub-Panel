// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as api from '@/api/accessControl'
import { confirmMock, renderAccess, snack, whereIs } from '@/test/accessControlHarness'
import { detail, group, node, preview, status } from '@/test/allowlistFixtures'
import AllowlistTab from './AllowlistTab'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})

const onOpenEntries = vi.fn()
const onOpenNodes = vi.fn()
const onOpenSettings = vi.fn()
const onTestDestination = vi.fn()
const ui = <AllowlistTab onOpenEntries={onOpenEntries} onOpenNodes={onOpenNodes} onOpenSettings={onOpenSettings} onTestDestination={onTestDestination} />
const route = '/admin/access-control?tab=allowlist'

const on = group({
  group_id: 2, group_name: 'Dev', members: 24, state: 'on', list_ids: [20], lists: [{ id: 20, name: 'Dev list', entry_count: 312, state: 'ready' }],
  base_list_id: 21, extra_list_id: 22, eligible_nodes: 8, total_nodes: 12, ineligible: [], applied_nodes: 8, pending_nodes: [], would_deny_recent: null, denied_24h: 1203,
})
const none = group({
  group_id: 3, group_name: 'Vendors', members: 6, state: 'on', list_ids: [], lists: [], eligible_nodes: 0, total_nodes: 4, ineligible: [], pending_nodes: [],
  would_deny_recent: null, denied_24h: 0,
})
const off = group({ group_id: 4, group_name: 'Staff', members: 9, state: 'off', updated_at: 7, would_deny_recent: null, ineligible: [], pending_nodes: [], eligible_nodes: 12 })
const fresh = group({ group_id: 5, group_name: 'Fresh', members: 2, state: 'off', updated_at: null, list_ids: [], lists: [], base_list_id: 0, extra_list_id: 0, ineligible: [], pending_nodes: [] })

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestGroups).mockResolvedValue([group(), on, none, off, fresh])
  vi.mocked(api.getDestStatus).mockResolvedValue(status())
  vi.mocked(api.getDestSettings).mockResolvedValue({ settings: {}, defaults: {}, effective: { dest_policy_apply_min_seconds: 60, dest_list_refresh_hours: 24, dest_rules_update_mode: 'manual', dest_hit_retention_days: 30, dest_trial_retention_days: 7 } } as unknown as api.DestSettingsView)
  vi.mocked(api.getDestLists).mockResolvedValue({ items: [], refresh_hours: 24, budget: {} as api.DestBudget })
  vi.mocked(api.getDestPolicies).mockResolvedValue({ allow: [], block: [], observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget: {} as api.DestBudget })
  vi.mocked(api.getDestGroup).mockResolvedValue(detail())
  vi.mocked(api.previewDestGroup).mockResolvedValue(preview())
})

describe('the allowlist tab', () => {
  it('shows a skeleton while it reads and an error with a retry when it fails', async () => {
    vi.mocked(api.getDestGroups).mockReturnValue(new Promise(() => {}))
    const { unmount } = renderAccess(ui, { route })
    expect((await screen.findAllByTestId('allowlist-skeleton')).length).toBe(2)
    unmount()
    vi.mocked(api.getDestGroups).mockRejectedValue(new Error('boom'))
    renderAccess(ui, { route })
    expect(await screen.findByText('读取白名单分组失败')).toBeTruthy()
    vi.mocked(api.getDestGroups).mockResolvedValue([group()])
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('Guests')).toBeTruthy()
  })

  it('says why it is empty and offers to enable one', async () => {
    vi.mocked(api.getDestGroups).mockResolvedValue([fresh])
    renderAccess(ui, { route })
    expect(await screen.findByText('还没有分组启用白名单')).toBeTruthy()
    expect(screen.getByText(/先选「仅报告」/)).toBeTruthy()
    expect(screen.getAllByRole('button', { name: '启用白名单…' }).length).toBeGreaterThan(0)
  })

  it('a report-only card: the day, the lists, the nodes, what is applying and what it would have denied', async () => {
    renderAccess(ui, { route })
    const card = (await screen.findByText('Guests')).closest('[data-testid="allowlist-card"]') as HTMLElement
    expect(within(card).getByTestId('allowlist-state').textContent).toBe('仅报告 · 第 3 天')
    expect(within(card).getByTestId('allowlist-state').getAttribute('data-state')).toBe('report')
    expect(within(card).getByText(/成员 51 · 名单：/)).toBeTruthy()
    expect(within(card).getByText(/基础放行 1 条/)).toBeTruthy()
    expect(within(card).getByRole('button', { name: /Guest list/ }).textContent).toBe('Guest list（40）')
    expect(within(card).getByText('可用节点 8 / 12')).toBeTruthy()
    expect(within(card).getByText('2 台节点正在下发本组规则')).toBeTruthy()
    expect(within(card).getByText('过去 7 天本应拒绝 128 个主域名，共 9,420 次')).toBeTruthy()
    fireEvent.click(within(card).getByRole('button', { name: /2 台节点不对本组开放/ }))
    expect(await screen.findByText(/US West A · 3X-UI/)).toBeTruthy()
    expect(screen.getByText(/Osaka 05 · 节点版本过旧/)).toBeTruthy()
  })

  it('an on card shows the denials of the last 24 hours and links to them', async () => {
    const { router } = renderAccess(ui, { route })
    const card = (await screen.findByText('Dev')).closest('[data-testid="allowlist-card"]') as HTMLElement
    expect(within(card).getByTestId('allowlist-state').textContent).toBe('开启')
    expect(within(card).getByText('过去 24 小时拒绝 1,203 次')).toBeTruthy()
    fireEvent.click(within(card).getByRole('button', { name: '查看被拒记录' }))
    await waitFor(() => expect(whereIs(router)).toBe('/admin/access-control?tab=records&rec_source=g2&rec_action=block'))
  })

  it('a group with no node that can enforce it says so in a band, not an alert', async () => {
    renderAccess(ui, { route })
    const card = (await screen.findByText('Vendors')).closest('[data-testid="allowlist-card"]') as HTMLElement
    expect(within(card).getByText('本组成员现在没有任何可用节点')).toBeTruthy()
    fireEvent.click(within(card).getByRole('button', { name: '查看原因' }))
    expect(onOpenNodes).toHaveBeenCalled()
  })

  it('a group that is off with its lists kept is a quiet card with one button; one never enabled has no card', async () => {
    renderAccess(ui, { route })
    const card = (await screen.findByText('Staff')).closest('[data-testid="allowlist-card"]') as HTMLElement
    expect(within(card).getByTestId('allowlist-state').textContent).toBe('已关闭 · 名单保留')
    expect(within(card).getByRole('button', { name: '重新开启…' })).toBeTruthy()
    expect(screen.queryByText('Fresh')).toBeNull()
  })

  it('names the groups a node left out for weak sniffing, and what that means', async () => {
    vi.mocked(api.getDestStatus).mockResolvedValue(status([node({ panel_name: 'Tokyo 01', sniffing_groups_dropped: [{ id: 1, name: 'Guests' }] })]))
    renderAccess(ui, { route })
    const card = (await screen.findByText('Guests')).closest('[data-testid="allowlist-card"]') as HTMLElement
    expect(await within(card).findByText(/Tokyo 01/)).toBeTruthy()
    expect(within(card).getByText(/所有入站上都不生效/)).toBeTruthy()
    expect(within(card).getByText(/成员会在下一次同步中失去这些节点/)).toBeTruthy()
  })

  it('opens a list in the entries sheet from its name', async () => {
    renderAccess(ui, { route })
    const card = (await screen.findByText('Guests')).closest('[data-testid="allowlist-card"]') as HTMLElement
    fireEvent.click(within(card).getByRole('button', { name: /Guest list/ }))
    expect(onOpenEntries).toHaveBeenCalledWith(10)
  })

  it('the report view is a push, so Back returns to the cards', async () => {
    const { router } = renderAccess(ui, { route })
    const card = (await screen.findByText('Guests')).closest('[data-testid="allowlist-card"]') as HTMLElement
    vi.mocked(api.getDestGroupReport).mockResolvedValue({ effective_days: 7, state: 'report', since_at: 1, items: [], dropped_in_range: 0 })
    fireEvent.click(within(card).getByRole('button', { name: '查看报告' }))
    await waitFor(() => expect(whereIs(router)).toBe('/admin/access-control?tab=allowlist&al_group=1&al_view=report'))
    expect(router.state.historyAction).toBe('PUSH')
  })

  it('a report-only group is turned on through the checklist dialog, which writes once', async () => {
    vi.mocked(api.putDestGroup).mockResolvedValue(group({ state: 'on' }))
    renderAccess(ui, { route })
    const card = (await screen.findByText('Guests')).closest('[data-testid="allowlist-card"]') as HTMLElement
    fireEvent.click(within(card).getByRole('button', { name: '开启…' }))
    const dialog = await screen.findByRole('dialog', { name: '让「Guests」开启白名单？' })
    expect(api.putDestGroup).not.toHaveBeenCalled()
    fireEvent.click(await within(dialog).findByRole('button', { name: '开启白名单' }))
    await waitFor(() => expect(api.putDestGroup).toHaveBeenCalledTimes(1))
    expect(api.putDestGroup).toHaveBeenCalledWith(1, { state: 'on', list_ids: [10], updated_at: 100 })
    await waitFor(() => expect(snack).toHaveBeenCalledWith('「Guests」已开启白名单，约 2 分钟内生效', 'success'))
  })

  it('back to report-only and off are plain confirmations that send the state with the updated_at that was read', async () => {
    vi.mocked(api.putDestGroup).mockResolvedValue(group())
    renderAccess(ui, { route })
    const card = (await screen.findByText('Dev')).closest('[data-testid="allowlist-card"]') as HTMLElement
    fireEvent.click(within(card).getByRole('button', { name: '改回仅报告…' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '让「Dev」回到仅报告？', confirmText: '改回仅报告' })))
    await waitFor(() => expect(api.putDestGroup).toHaveBeenLastCalledWith(2, { state: 'report', list_ids: [20], updated_at: 100 }))
    fireEvent.click(within(card).getByRole('button', { name: 'Dev 的操作' }))
    fireEvent.click(screen.getByRole('menuitem', { name: '关闭白名单…' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '关闭「Dev」的白名单？' })))
    await waitFor(() => expect(api.putDestGroup).toHaveBeenLastCalledWith(2, { state: 'off', list_ids: [20], updated_at: 100 }))
  })

  it('a stale write is explained and the cards are read again', async () => {
    const err = Object.assign(new Error('x'), { isAxiosError: true, response: { status: 409, data: { code: 'dest_group_stale' } } })
    vi.mocked(api.putDestGroup).mockRejectedValue(err)
    renderAccess(ui, { route })
    const card = (await screen.findByText('Dev')).closest('[data-testid="allowlist-card"]') as HTMLElement
    fireEvent.click(within(card).getByRole('button', { name: '改回仅报告…' }))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('状态已被其他人修改，已重新载入', 'warning'))
    await waitFor(() => expect(vi.mocked(api.getDestGroups).mock.calls.length).toBeGreaterThan(1))
  })

  it('al_enable opens the enable dialog on that group, once, and the param is dropped', async () => {
    vi.mocked(api.getDestGroups).mockResolvedValue([group(), fresh])
    const { router } = renderAccess(ui, { route: `${route}&al_enable=5` })
    expect(await screen.findByRole('dialog', { name: '启用白名单' })).toBeTruthy()
    await waitFor(() => expect(whereIs(router)).toBe('/admin/access-control?tab=allowlist'))
  })
})
