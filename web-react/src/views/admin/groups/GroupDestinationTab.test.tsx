// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as api from '@/api/accessControl'
import { renderAccess, whereIs } from '@/test/accessControlHarness'
import { group } from '@/test/allowlistFixtures'
import GroupDestinationTab from './GroupDestinationTab'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})

const ui = (id: number | null) => <GroupDestinationTab groupId={id} />
const route = '/admin/groups'
const off = group({ group_id: 7, group_name: 'Staff', state: 'off', updated_at: null, list_ids: [], lists: [], base_list_id: 0, extra_list_id: 0, base_count: 0, ineligible: [], pending_nodes: [] })

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestGroups).mockResolvedValue([group(), off])
})

describe('the group sheet destinations tab', () => {
  it('a group with no allowlist is unrestricted, and offers to enable one on the access-control page', async () => {
    const { router } = renderAccess(ui(7), { route })
    expect(await screen.findByText('不限')).toBeTruthy()
    expect(screen.getByText('本组成员可以访问所有目的地，仍受全局访问策略约束。')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '为本组启用白名单… ›' }))
    await waitFor(() => expect(whereIs(router)).toBe('/admin/access-control?tab=allowlist&al_enable=7'))
  })

  it('a report-only group shows its mode, its lists and its nodes as a summary, and the way to edit them', async () => {
    const { router } = renderAccess(ui(1), { route })
    expect(await screen.findByText('白名单 · 仅报告 · 第 3 天')).toBeTruthy()
    expect(screen.getByText('Guest list（40） · 基础放行 1 条')).toBeTruthy()
    expect(screen.getByText('8 / 12')).toBeTruthy()
    expect(screen.getByText('2 台节点不对本组开放')).toBeTruthy()
    expect(screen.getByText('这里只显示摘要；名单和状态在访问控制页修改。')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '在访问控制中编辑 ›' }))
    await waitFor(() => expect(whereIs(router)).toBe('/admin/access-control?tab=allowlist&al_group=1'))
  })

  it('lists the nodes that are not open to the group, with the reason, behind the count', async () => {
    renderAccess(ui(1), { route })
    fireEvent.click(await screen.findByRole('button', { name: /2 台节点不对本组开放/ }))
    expect(await screen.findByText(/US West A · 3X-UI/)).toBeTruthy()
  })

  it('an allowlist that is on says so', async () => {
    vi.mocked(api.getDestGroups).mockResolvedValue([group({ state: 'on' })])
    renderAccess(ui(1), { route })
    expect(await screen.findByText('白名单 · 开启')).toBeTruthy()
  })

  it('a group that was switched off keeps its lists and says so', async () => {
    vi.mocked(api.getDestGroups).mockResolvedValue([group({ state: 'off' })])
    renderAccess(ui(1), { route })
    expect(await screen.findByText('名单保留，可以再次开启。')).toBeTruthy()
  })

  it('a group not saved yet has nothing to set', () => {
    renderAccess(ui(null), { route })
    expect(screen.getByText('保存分组后即可设置访问模式')).toBeTruthy()
    expect(api.getDestGroups).not.toHaveBeenCalled()
  })

  it('reads with skeletons, and a failed read is one line with a retry that leaves the sheet alone', async () => {
    vi.mocked(api.getDestGroups).mockRejectedValue(new Error('boom'))
    renderAccess(ui(1), { route })
    expect(await screen.findByText('读取白名单信息失败')).toBeTruthy()
    vi.mocked(api.getDestGroups).mockResolvedValue([group()])
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('白名单 · 仅报告 · 第 3 天')).toBeTruthy()
  })
})
