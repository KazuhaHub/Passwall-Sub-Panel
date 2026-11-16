// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as api from '@/api/accessControl'
import { renderAccess, snack, whereIs } from '@/test/accessControlHarness'
import { group, report, status } from '@/test/allowlistFixtures'
import AllowlistReport from './AllowlistReport'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})

const onBack = vi.fn()
const onOpenEntries = vi.fn()
const onTestDestination = vi.fn()
const onOpenSettings = vi.fn()
const onTurnOn = vi.fn()
const onViewDenied = vi.fn()
const ui = <AllowlistReport groupId={1} onBack={onBack} onOpenEntries={onOpenEntries} onTestDestination={onTestDestination} onOpenSettings={onOpenSettings}
  onTurnOn={onTurnOn} onViewDenied={onViewDenied} />
const route = '/admin/access-control?tab=allowlist&al_group=1&al_view=report'
const settings = (trial: number) => ({ settings: {}, defaults: {}, effective: { dest_policy_apply_min_seconds: 60, dest_list_refresh_hours: 24, dest_rules_update_mode: 'manual', dest_hit_retention_days: 30, dest_trial_retention_days: trial } }) as unknown as api.DestSettingsView

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestGroups).mockResolvedValue([group()])
  vi.mocked(api.getDestGroupReport).mockResolvedValue(report())
  vi.mocked(api.getDestSettings).mockResolvedValue(settings(7))
  vi.mocked(api.getDestStatus).mockResolvedValue(status())
  vi.mocked(api.getDestLists).mockResolvedValue({ items: [], refresh_hours: 24, budget: {} as api.DestBudget })
  vi.mocked(api.editDestListEntries).mockResolvedValue({ list: {} as api.DestList, report: null })
})

describe('the report view', () => {
  it('names the group in the heading and asks for the retention-limited range', async () => {
    renderAccess(ui, { route })
    expect(await screen.findByText('本应拒绝的目的地')).toBeTruthy()
    expect(screen.getByText('白名单分组')).toBeTruthy()
    await waitFor(() => expect(api.getDestGroupReport).toHaveBeenCalledWith(1, 7, expect.anything()))
    expect(screen.getByText('受保留期限制，最多 7 天')).toBeTruthy()
  })

  it('counts the sites, the connections, those not yet added and those the nodes could not report', async () => {
    vi.mocked(api.getDestGroupReport).mockResolvedValue(report({ dropped_in_range: 12 }))
    renderAccess(ui, { route })
    const kpi = await screen.findByText('本应拒绝的主域名')
    expect(kpi.parentElement?.textContent).toContain('4')
    expect(screen.getByText('4,714')).toBeTruthy()
    // The (ip) row cannot be added by name, so it is not counted among what is left to handle.
    expect(screen.getByText('未处理').parentElement?.textContent).toContain('2')
    const lost = screen.getByText('未统计').parentElement as HTMLElement
    expect(lost.textContent).toContain('12')
    expect(screen.getByText('节点上报有上限，报告可能漏了一些主域名；这是所有分组的合计')).toBeTruthy()
  })

  it('shows a row per site: a bar and a count, an add button, and "added" for what is already in the list', async () => {
    renderAccess(ui, { route })
    const row = (await screen.findByText('gstatic.com')).closest('[data-testid="report-row"]') as HTMLElement
    expect(within(row).getByText('2,210')).toBeTruthy()
    expect(within(row).getByText('5 分钟前')).toBeTruthy()
    expect(within(row).getByRole('button', { name: '加入白名单' })).toBeTruthy()
    const added = screen.getByText('notion.so').closest('[data-testid="report-row"]') as HTMLElement
    expect(within(added).getByText('已加入 · 下次下发后生效')).toBeTruthy()
    expect(within(added).queryByRole('checkbox')).toBeNull()
    expect(within(added).queryByRole('button', { name: '加入白名单' })).toBeNull()
  })

  it('the (ip) row cannot be added by name: it explains, and opens the supplement list instead', async () => {
    renderAccess(ui, { route })
    const row = (await screen.findByText('(ip)')).closest('[data-testid="report-row"]') as HTMLElement
    expect(within(row).queryByRole('checkbox')).toBeNull()
    expect(within(row).queryByRole('button', { name: '加入白名单' })).toBeNull()
    expect(within(row).getByText('客户端直接用 IP 连接，节点只记录为 (ip)；要放行请在名单里写 IP 段')).toBeTruthy()
    fireEvent.click(within(row).getByRole('button', { name: '编辑补充列表' }))
    expect(onOpenEntries).toHaveBeenCalledWith(12)
  })

  it('one add is one entries request on the supplement list, never a rebuilt list', async () => {
    renderAccess(ui, { route })
    const row = (await screen.findByText('gstatic.com')).closest('[data-testid="report-row"]') as HTMLElement
    fireEvent.click(within(row).getByRole('button', { name: '加入白名单' }))
    await waitFor(() => expect(api.editDestListEntries).toHaveBeenCalledWith(12, { add: ['domain:gstatic.com'], remove: undefined }))
    expect(snack).toHaveBeenCalledWith('已把 1 个主域名加入「Guests · 补充」，约 2 分钟内生效', 'success')
    expect(await within(row).findByText('已加入 · 下次下发后生效')).toBeTruthy()
  })

  it('a selection adds together, in one request, from a bar that sticks to the bottom', async () => {
    renderAccess(ui, { route })
    await screen.findByText('gstatic.com')
    fireEvent.click(screen.getByRole('checkbox', { name: '选择 gstatic.com' }))
    fireEvent.click(screen.getByRole('checkbox', { name: '选择 figma.com' }))
    const bar = await screen.findByTestId('bulk-bar')
    expect(within(bar).getByText('已选 2 个')).toBeTruthy()
    fireEvent.click(within(bar).getByRole('button', { name: '加入白名单（2）' }))
    await waitFor(() => expect(api.editDestListEntries).toHaveBeenCalledTimes(1))
    expect(api.editDestListEntries).toHaveBeenCalledWith(12, { add: ['domain:gstatic.com', 'domain:figma.com'], remove: undefined })
  })

  it('an add to a list other than the supplement does not mark the site added: only the supplement and the base are what "added" means', async () => {
    vi.mocked(api.getDestGroups).mockResolvedValue([group({ lists: [{ id: 10, name: 'Guest list', entry_count: 40, state: 'ready' }] })])
    vi.mocked(api.getDestLists).mockResolvedValue({ items: [{ id: 10, name: 'Guest list', kind: 'custom', side: 'dest', entry_count: 40, regexp_count: 0, state: 'ready', empty: false, last_fetched_at: 1, last_error: '', owner_group_id: 0, updated_at: 1, used_by: [] }], refresh_hours: 24, budget: {} as api.DestBudget })
    renderAccess(ui, { route })
    const row = (await screen.findByText('gstatic.com')).closest('[data-testid="report-row"]') as HTMLElement
    fireEvent.click(screen.getByRole('checkbox', { name: '选择 gstatic.com' }))
    const bar = await screen.findByTestId('bulk-bar')
    fireEvent.mouseDown(within(bar).getByRole('combobox'))
    fireEvent.click(await screen.findByRole('option', { name: 'Guest list' }))
    fireEvent.click(within(bar).getByRole('button', { name: '加入白名单（1）' }))
    await waitFor(() => expect(api.editDestListEntries).toHaveBeenCalledWith(10, { add: ['domain:gstatic.com'], remove: undefined }))
    expect(within(row).queryByText('已加入 · 下次下发后生效')).toBeNull()
  })

  it('sorts by count or by recency', async () => {
    renderAccess(ui, { route })
    await screen.findByText('gstatic.com')
    expect(screen.getAllByTestId('report-row').map(r => r.getAttribute('data-site'))).toEqual(['gstatic.com', 'figma.com', 'notion.so', '(ip)'])
    fireEvent.click(screen.getByRole('button', { name: '最近' }))
    expect(screen.getAllByTestId('report-row')[0].getAttribute('data-site')).toBe('gstatic.com')
    fireEvent.click(screen.getByRole('button', { name: '次数' }))
    fireEvent.click(screen.getByRole('button', { name: '次数' }))
    expect(screen.getAllByTestId('report-row')[0].getAttribute('data-site')).toBe('(ip)')
  })

  it('opens the destination in the test sheet from its name', async () => {
    renderAccess(ui, { route })
    fireEvent.click(await screen.findByRole('button', { name: 'gstatic.com' }))
    expect(onTestDestination).toHaveBeenCalledWith('gstatic.com')
  })
})

describe('the range', () => {
  it('offers only the days the retention keeps, and a choice is a replace of al_days', async () => {
    vi.mocked(api.getDestSettings).mockResolvedValue(settings(3))
    const { router } = renderAccess(ui, { route })
    await screen.findByText('gstatic.com')
    expect(screen.getAllByRole('button', { name: /^\d 天$/ }).map(b => b.textContent)).toEqual(['1 天', '3 天'])
    fireEvent.click(screen.getByRole('button', { name: '1 天' }))
    await waitFor(() => expect(whereIs(router)).toContain('al_days=1'))
    expect(router.state.historyAction).toBe('REPLACE')
    await waitFor(() => expect(api.getDestGroupReport).toHaveBeenLastCalledWith(1, 1, expect.anything()))
  })

  it('says why when the server answers with a shorter range than asked', async () => {
    vi.mocked(api.getDestGroupReport).mockResolvedValue(report({ effective_days: 2 }))
    renderAccess(ui, { route })
    expect(await screen.findByText('保留期只有 2 天，已按 2 天显示')).toBeTruthy()
  })
})

describe('what the report says of itself', () => {
  it('a report that began hours ago says the data is thin; an older one just says how long', async () => {
    vi.mocked(api.getDestGroupReport).mockResolvedValue(report({ since_at: Date.now() - 3 * 3_600_000 - 1000 }))
    const first = renderAccess(ui, { route })
    expect(await screen.findByText('仅报告刚开始 3 小时，数据还少')).toBeTruthy()
    first.unmount()
    vi.mocked(api.getDestGroupReport).mockResolvedValue(report())
    renderAccess(ui, { route })
    expect(await screen.findByText('仅报告第 4 天')).toBeTruthy()
  })

  it('an empty range says so, with the way to turn it on; one that never ran says that instead', async () => {
    vi.mocked(api.getDestGroupReport).mockResolvedValue(report({ items: [] }))
    const first = renderAccess(ui, { route })
    expect(await screen.findByText('过去 7 天没有本应被拒绝的目的地')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '开启…' }))
    expect(onTurnOn).toHaveBeenCalled()
    first.unmount()
    vi.mocked(api.getDestGroupReport).mockResolvedValue(report({ items: [], since_at: null }))
    renderAccess(ui, { route })
    expect(await screen.findByText('本组没有仅报告的数据：开启前看不到会拦到什么')).toBeTruthy()
  })

  it('a group that is on is titled for what it denies and links to the records', async () => {
    vi.mocked(api.getDestGroups).mockResolvedValue([group({ state: 'on' })])
    renderAccess(ui, { route })
    expect(await screen.findByText('Guests · 被拒绝的目的地')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '查看被拒记录' }))
    expect(onViewDenied).toHaveBeenCalled()
  })

  it('a failed read has a retry', async () => {
    vi.mocked(api.getDestGroupReport).mockRejectedValue(new Error('boom'))
    renderAccess(ui, { route })
    expect(await screen.findByText('读取报告失败')).toBeTruthy()
    vi.mocked(api.getDestGroupReport).mockResolvedValue(report())
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('gstatic.com')).toBeTruthy()
  })

  it('goes back', async () => {
    renderAccess(ui, { route })
    fireEvent.click(await screen.findByRole('button', { name: '白名单分组' }))
    expect(onBack).toHaveBeenCalled()
  })
})
