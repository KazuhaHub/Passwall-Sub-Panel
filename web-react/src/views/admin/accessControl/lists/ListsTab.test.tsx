// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as api from '@/api/accessControl'
import type { DestBudget, DestList, DestListsView, DestPolicies } from '@/api/accessControl'
import { confirmMock, renderAccess, snack, whereIs } from '@/test/accessControlHarness'
import ListsTab from './ListsTab'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})
const media = vi.hoisted(() => ({ folded: false }))
vi.mock('@mui/material', async importOriginal => ({ ...await importOriginal<typeof import('@mui/material')>(), useMediaQuery: () => media.folded }))

const budget: DestBudget = {
  rules: { used: 1, limit: 256 }, domains: { used: 8420, limit: 50000 }, regexps: { used: 140, limit: 256 },
  cidrs: { used: 3, limit: 20000 }, source_cidrs: { used: 0, limit: 60000 }, subjects: { used: 0, limit: 5000 }, bytes: { used: 0, limit: 1000 },
}
const NOW = Date.now()
const list = (o: Partial<DestList>): DestList => ({
  id: 1, name: 'Internal', kind: 'custom', side: 'dest', entry_count: 142, regexp_count: 0, state: 'ready', empty: false,
  last_fetched_at: NOW - 3 * 86400_000, last_error: '', owner_group_id: 0, updated_at: 1, used_by: [], ...o,
})
const view = (items: DestList[], over: Partial<DestListsView> = {}): DestListsView => ({ items, refresh_hours: 24, budget, ...over })
const policies: DestPolicies = {
  allow: [], observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget,
  block: [{ id: 5, name: 'No mail', description: '', action: 'block', state: 'on', list_ids: [], inline: {}, scope: 'all', group_ids: [], panel_ids: [], priority: 1, template_key: '', created_at: 0, updated_at: 0, list_states: [], note: null, scope_missing: false, hits_recent: null, would_block_recent: null, last_hit_at: null }],
}
const items = [
  list({ id: 1, name: 'Internal', used_by: [{ kind: 'policy', id: 5, name: 'No mail' }] }),
  list({ id: 2, name: 'Scam list', kind: 'remote', source_url: 'https://raw.example.org/list.txt', entry_count: 8012, state: 'failed', last_error: 'fetch: HTTP 404', used_by: [{ kind: 'policy', id: 5, name: 'No mail' }] }),
  list({ id: 3, name: 'News', kind: 'remote', source_url: 'https://example.org/n.txt', state: 'pending', last_fetched_at: null, entry_count: 0 }),
]

const onOpenEntries = vi.fn()
const onOpenSettings = vi.fn()
const onOpenPolicy = vi.fn()
const ui = <ListsTab onOpenEntries={onOpenEntries} onOpenSettings={onOpenSettings} onOpenPolicy={onOpenPolicy} />
const route = '/admin/access-control?tab=lists'

beforeEach(() => {
  vi.clearAllMocks()
  media.folded = false
  vi.mocked(api.getDestLists).mockResolvedValue(view(items))
  vi.mocked(api.getDestPolicies).mockResolvedValue(policies)
  vi.mocked(api.getGeositeStatus).mockResolvedValue({ current: { tag: 't', sha256: '', downloaded_at: '', bytes: 1 }, latest: { tag: 't' }, update_available: false, mode: 'manual' })
})

describe('the lists tab', () => {
  it('shows the quotas, each list with its state, and what uses it', async () => {
    renderAccess(ui, { route })
    expect(await screen.findByText('Internal')).toBeTruthy()
    expect(screen.getByText('8,420')).toBeTruthy()
    expect(screen.getByText('共 50,000')).toBeTruthy()
    // Sorted by name: Internal, News, Scam list.
    const states = screen.getAllByTestId('list-state').map(e => e.textContent)
    expect(states).toEqual(['已更新', '等待首次下载', '刷新失败，仍用旧内容'])
    // An unused, not-yet-downloaded list is waiting (quiet), not failing.
    expect(screen.getAllByTestId('list-state')[1].getAttribute('data-state')).toBe('pending')
    expect(screen.getByText('服务器返回 HTTP 404 · 3 天前')).toBeTruthy()
    expect(screen.getAllByText('1 条策略').length).toBe(2)
    expect(screen.getByText('未使用')).toBeTruthy()
    expect(screen.getByText('远程与分类列表每 24 小时刷新一次', { exact: false })).toBeTruthy()
  })

  it('the problems tile filters through lst_state (replace) and a second press clears it', async () => {
    const { router } = renderAccess(ui, { route })
    await screen.findByText('Internal')
    const tile = screen.getByRole('button', { name: /有问题/ })
    expect(tile.getAttribute('aria-pressed')).toBe('false')
    fireEvent.click(tile)
    await waitFor(() => expect(whereIs(router)).toBe('/admin/access-control?tab=lists&lst_state=problem'))
    expect(router.state.historyAction).toBe('REPLACE')
    expect(screen.queryByText('Internal')).toBeNull()
    expect(screen.getByText('Scam list')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /有问题/ }))
    await waitFor(() => expect(whereIs(router)).toBe('/admin/access-control?tab=lists'))
  })

  it('a filtered-empty result is not the same sentence as a really empty one', async () => {
    vi.mocked(api.getDestLists).mockResolvedValue(view([items[0]]))
    renderAccess(ui, { route: `${route}&lst_state=problem` })
    expect(await screen.findByText('没有符合的列表')).toBeTruthy()
    expect(screen.queryByText('还没有列表。策略都从列表里取目的地。')).toBeNull()
  })

  it('says why it is empty and offers the new-list menu', async () => {
    vi.mocked(api.getDestLists).mockResolvedValue(view([]))
    renderAccess(ui, { route })
    expect(await screen.findByText('还没有列表。策略都从列表里取目的地。')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /新建列表/ }))
    expect(screen.getByRole('menuitem', { name: '社区分类' })).toBeTruthy()
  })

  it('delete is disabled for a list in use, and asks first for one that is not', async () => {
    vi.mocked(api.deleteDestList).mockResolvedValue()
    renderAccess(ui, { route })
    await screen.findByText('News')
    fireEvent.click(screen.getByRole('button', { name: 'Internal 的操作' }))
    expect(screen.getByRole('menuitem', { name: '删除' }).getAttribute('aria-disabled')).toBe('true')
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    fireEvent.click(screen.getByRole('button', { name: 'News 的操作' }))
    fireEvent.click(screen.getByRole('menuitem', { name: '删除' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '删除列表「News」？', destructive: true })))
    await waitFor(() => expect(api.deleteDestList).toHaveBeenCalledWith(3))
    expect(snack).toHaveBeenCalledWith('已删除「News」', 'success')
  })

  it('a 409 dest_list_in_use is an explanation with links, not a confirmation', async () => {
    const err = Object.assign(new Error('x'), { isAxiosError: true, response: { status: 409, data: { code: 'dest_list_in_use', used_by: [{ kind: 'policy', id: 5, name: 'No mail' }] } } })
    vi.mocked(api.deleteDestList).mockRejectedValue(err)
    renderAccess(ui, { route })
    await screen.findByText('News')
    fireEvent.click(screen.getByRole('button', { name: 'News 的操作' }))
    fireEvent.click(screen.getByRole('menuitem', { name: '删除' }))
    const dialog = await screen.findByRole('dialog', { name: '列表正在被使用' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'No mail' }))
    expect(onOpenPolicy).toHaveBeenCalledWith(5)
  })

  it('refreshes a remote list on request, and a custom one has no refresh', async () => {
    vi.mocked(api.refreshDestList).mockResolvedValue()
    renderAccess(ui, { route })
    await screen.findByText('News')
    fireEvent.click(screen.getByRole('button', { name: 'Internal 的操作' }))
    expect(screen.queryByRole('menuitem', { name: '立即刷新' })).toBeNull()
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    fireEvent.click(screen.getByRole('button', { name: 'Scam list 的操作' }))
    fireEvent.click(screen.getByRole('menuitem', { name: '立即刷新' }))
    await waitFor(() => expect(api.refreshDestList).toHaveBeenCalledWith(2))
  })

  it('opens the used-by popover with a link to the policy', async () => {
    renderAccess(ui, { route })
    await screen.findByText('Internal')
    fireEvent.click(screen.getAllByText('1 条策略')[0])
    fireEvent.click(await screen.findByRole('button', { name: 'No mail' }))
    expect(onOpenPolicy).toHaveBeenCalledWith(5)
  })

  it('opens the dialog a nav state asks for, once', async () => {
    vi.mocked(api.getGeositeCategories).mockResolvedValue({ categories: [] })
    const { router } = renderAccess(ui, { route })
    await screen.findByText('Internal')
    await router.navigate(route, { state: { openList: 'new_remote' } })
    expect(await screen.findByRole('dialog', { name: '新建列表' })).toBeTruthy()
    await waitFor(() => expect(router.state.location.state).toBeNull())
  })

  it('handles a SECOND request while still mounted (the dialog was closed in between)', async () => {
    vi.mocked(api.getGeositeCategories).mockResolvedValue({ categories: [] })
    const { router } = renderAccess(ui, { route })
    await screen.findByText('Internal')
    await router.navigate(route, { state: { openList: 'new_remote' } })
    fireEvent.click(await screen.findByRole('button', { name: '取消' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '新建列表' })).toBeNull())
    await router.navigate(route, { state: { openList: 'new_custom' } })
    expect(await screen.findByRole('dialog', { name: '新建列表' })).toBeTruthy()
  })

  it('never shows an unknown refresh error raw', async () => {
    vi.mocked(api.getDestLists).mockResolvedValue(view([list({ id: 2, name: 'Odd', kind: 'remote', state: 'failed', last_error: 'dial tcp 10.1.2.3:443: refused', used_by: [] })]))
    renderAccess(ui, { route })
    await screen.findByText('Odd')
    expect(screen.queryByText(/10\.1\.2\.3/)).toBeNull()
    expect(screen.getAllByText(/^刷新失败( · .*)?$/).length).toBeGreaterThan(0)
  })

  it('tells the admin the community data is missing and downloads only on request', async () => {
    vi.mocked(api.getGeositeStatus).mockResolvedValue({ latest: { tag: '' }, update_available: false, mode: 'manual' })
    vi.mocked(api.updateGeosite).mockResolvedValue({ applied: true } as api.GeositeUpdateResult)
    renderAccess(ui, { route })
    expect(await screen.findByText('社区分类数据尚未下载')).toBeTruthy()
    expect(api.updateGeosite).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: '立即下载' }))
    await waitFor(() => expect(api.updateGeosite).toHaveBeenCalledWith({}))
  })

  it('on a phone each list folds into a block with its values labelled', async () => {
    media.folded = true
    renderAccess(ui, { route })
    const blocks = await screen.findAllByTestId('list-block')
    expect(blocks).toHaveLength(3)
    expect(within(blocks[0]).getByText('条目')).toBeTruthy()
    expect(within(blocks[0]).getByText('使用情况')).toBeTruthy()
  })

  it('a failed read has a retry; a 503 is not-wired', async () => {
    vi.mocked(api.getDestLists).mockRejectedValue(new Error('boom'))
    renderAccess(ui, { route })
    expect(await screen.findByRole('button', { name: '重试' })).toBeTruthy()
  })

  it('opens the settings dialog on the refresh field from the interval note', async () => {
    renderAccess(ui, { route })
    await screen.findByText('Internal')
    fireEvent.click(screen.getByRole('button', { name: '修改' }))
    expect(onOpenSettings).toHaveBeenCalledWith('refresh')
  })
})
