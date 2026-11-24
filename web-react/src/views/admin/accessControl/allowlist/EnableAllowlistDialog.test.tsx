// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as api from '@/api/accessControl'
import { confirmMock, renderAccess, snack } from '@/test/accessControlHarness'
import { detail, group, preview, status } from '@/test/allowlistFixtures'
import EnableAllowlistDialog from './EnableAllowlistDialog'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})

const off = (id: number, name: string, members: number) => group({
  group_id: id, group_name: name, members, state: 'off', updated_at: null, list_ids: [], lists: [], base_list_id: 0, extra_list_id: 0, base_count: 0,
  ineligible: [], pending_nodes: [], would_deny_recent: null,
})
const dev = off(1, 'Dev', 24)
const guests = off(2, 'Guests', 51)
const reuse = group({ group_id: 3, group_name: 'Staff', members: 9, state: 'off', updated_at: 77, list_ids: [10], lists: [{ id: 10, name: 'Guest list', entry_count: 40, state: 'ready' }], ineligible: [], pending_nodes: [] })
const on = group({ group_id: 4, group_name: 'Ops', state: 'on' })

const list = (over: Partial<api.DestList>): api.DestList => ({
  id: 10, name: 'Guest list', kind: 'custom', side: 'dest', entry_count: 40, regexp_count: 0, state: 'ready', empty: false, last_fetched_at: 1, last_error: '',
  owner_group_id: 0, updated_at: 1, used_by: [], ...over,
})
const budget = {} as api.DestBudget

const onClose = vi.fn()
const onSaved = vi.fn()
const onOpenGroupReport = vi.fn()
const onViewNodes = vi.fn()
const dlg = (over: Partial<Parameters<typeof EnableAllowlistDialog>[0]> = {}) => (
  <EnableAllowlistDialog open groups={[dev, guests, reuse, on]} initialGroupId={null} onClose={onClose} onSaved={onSaved}
    onOpenGroupReport={onOpenGroupReport} onViewNodes={onViewNodes} {...over} />
)

async function pickList(name: string) {
  fireEvent.click(await screen.findByRole('checkbox', { name }))
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestLists).mockResolvedValue({
    items: [
      list({}), list({ id: 11, name: 'Remote feed', kind: 'remote', state: 'pending', last_fetched_at: null }),
      list({ id: 14, name: 'Empty custom', empty: true, entry_count: 0 }), list({ id: 12, name: 'Mine', owner_group_id: 9 }), list({ id: 13, name: 'Source side', side: 'source' }),
    ], refresh_hours: 24, budget,
  })
  vi.mocked(api.getDestStatus).mockResolvedValue(status())
  vi.mocked(api.getDestPolicies).mockResolvedValue({ allow: [], block: [], observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget })
  vi.mocked(api.getDestSourceRules).mockResolvedValue({ items: [], geoip_available: true })
  vi.mocked(api.getDestSettings).mockResolvedValue({ settings: {}, defaults: {}, effective: { dest_policy_apply_min_seconds: 60, dest_list_refresh_hours: 24, dest_rules_update_mode: 'manual', dest_hit_retention_days: 30, dest_trial_retention_days: 7 } } as unknown as api.DestSettingsView)
  vi.mocked(api.previewDestGroup).mockResolvedValue(preview())
  vi.mocked(api.getDestGroup).mockResolvedValue(detail())
  vi.mocked(api.putDestGroup).mockResolvedValue(group({ group_id: 2, state: 'report' }))
})

describe('step 1: the group', () => {
  it('lists only the groups that have no allowlist, with their members, and goes on with the one picked', async () => {
    renderAccess(dlg())
    const d = await screen.findByRole('dialog', { name: '启用白名单' })
    expect(within(d).getByText('只列出尚未启用白名单的分组')).toBeTruthy()
    expect(within(d).getByRole('radio', { name: /Dev/ })).toBeTruthy()
    expect(within(d).getByText('51 人')).toBeTruthy()
    expect(within(d).queryByRole('radio', { name: /Ops/ })).toBeNull()
    const next = within(d).getByRole('button', { name: '下一步' }) as HTMLButtonElement
    expect(next.disabled).toBe(true)
    fireEvent.click(within(d).getByRole('radio', { name: /Guests/ }))
    expect(next.disabled).toBe(false)
  })

  it('says so when every group already has one, and cannot go on', async () => {
    renderAccess(dlg({ groups: [on] }))
    expect(await screen.findByText('所有分组都已启用白名单')).toBeTruthy()
    expect((screen.getByRole('button', { name: '下一步' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('adds a search over eight groups', async () => {
    const many = Array.from({ length: 9 }, (_, i) => off(20 + i, `Team ${i}`, i))
    renderAccess(dlg({ groups: many }))
    fireEvent.change(await screen.findByRole('textbox', { name: '搜索分组' }), { target: { value: 'Team 3' } })
    expect(screen.getByRole('radio', { name: /Team 3/ })).toBeTruthy()
    expect(screen.queryByRole('radio', { name: /Team 4/ })).toBeNull()
  })

  it('a group handed in starts at step 2, with its name in the title', async () => {
    renderAccess(dlg({ initialGroupId: 2 }))
    expect(await screen.findByText('本组成员只能访问这些列表里的目的地')).toBeTruthy()
  })
})

describe('step 2: the lists', () => {
  it('needs a list, offers the destination lists that belong to no group, and warns about one that was never downloaded', async () => {
    renderAccess(dlg({ initialGroupId: 2 }))
    const next = await screen.findByRole('button', { name: '下一步' }) as HTMLButtonElement
    expect(next.disabled).toBe(true)
    expect(await screen.findByRole('checkbox', { name: 'Guest list' })).toBeTruthy()
    expect(screen.queryByRole('checkbox', { name: 'Mine' })).toBeNull()
    expect(screen.queryByRole('checkbox', { name: 'Source side' })).toBeNull()
    // The server refuses an empty custom list, so it is not offered.
    expect(screen.queryByRole('checkbox', { name: 'Empty custom' })).toBeNull()
    fireEvent.click(screen.getByRole('checkbox', { name: 'Remote feed' }))
    expect(await screen.findByText('仅报告时跳过，开启前必须就绪')).toBeTruthy()
    expect(next.disabled).toBe(false)
  })

  it('prefills the base list from the template, and lets it be edited', async () => {
    renderAccess(dlg({ initialGroupId: 2 }))
    expect(await screen.findByText('已预填 1 条：dns.example.net')).toBeTruthy()
    expect(screen.getByText('订阅模板里经代理发出的 DNS 主机；缺了它，成员会全部打不开', { exact: false })).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '编辑' }))
    expect((screen.getByRole('textbox', { name: '基础放行' }) as HTMLTextAreaElement).value).toBe('dns.example.net')
  })

  it('says when the template gave no DNS host, and still lets it go on', async () => {
    vi.mocked(api.previewDestGroup).mockResolvedValue(preview({ base_hosts: [], base_hosts_source: 'none' }))
    renderAccess(dlg({ initialGroupId: 2 }))
    expect(await screen.findByText('没能从订阅模板读出 DNS 主机，请手动填写')).toBeTruthy()
    await pickList('Guest list')
    expect((screen.getByRole('button', { name: '下一步' }) as HTMLButtonElement).disabled).toBe(false)
  })
})

describe('step 3: the effect, and one write', () => {
  async function toStep3(initial = 2) {
    renderAccess(dlg({ initialGroupId: initial }))
    await pickList('Guest list')
    fireEvent.click(await screen.findByRole('button', { name: '下一步' }))
    return await screen.findByText('本组成员都不再获得以下 2 台节点：', { exact: false })
  }

  it('shows the members, the usable nodes and the ones the group loses, with the reasons', async () => {
    await toStep3()
    expect(screen.getByText('US West A · 3X-UI')).toBeTruthy()
    expect(screen.getByText('Osaka 05 · 节点版本过旧')).toBeTruthy()
    expect(screen.getByText('访问不受限，只记录本应被拒绝的目的地', { exact: false })).toBeTruthy()
    expect(api.previewDestGroup).toHaveBeenLastCalledWith(2, { state: 'report', list_ids: [10] }, expect.anything())
  })

  it('starts report-only by default and writes once, with the base hosts, then reports back', async () => {
    await toStep3()
    expect((screen.getByRole('radio', { name: /^仅报告/ }) as HTMLInputElement).checked).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: '开始仅报告' }))
    await waitFor(() => expect(api.putDestGroup).toHaveBeenCalledTimes(1))
    expect(api.putDestGroup).toHaveBeenCalledWith(2, { state: 'report', list_ids: [10], base_hosts: ['dns.example.net'] })
    await waitFor(() => expect(onSaved).toHaveBeenCalledWith({ groupId: 2, state: 'report', created: true }))
    expect(snack).toHaveBeenCalledWith('「Guests」已开始仅报告，约 2 分钟内生效', 'success')
    // First enforced thing on the fleet: asked once before it went out.
    expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '启用第一条访问策略？' }))
  })

  it('can go straight to on: the button says so, the checklist asks, and the single write follows it', async () => {
    await toStep3()
    fireEvent.click(screen.getByRole('radio', { name: /^开启/ }))
    expect(await screen.findByRole('button', { name: '开启白名单' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: '开始仅报告' })).toBeNull()
    const turnOnButton = screen.getByRole('button', { name: '开启白名单' })
    await waitFor(() => expect((turnOnButton as HTMLButtonElement).disabled).toBe(false))
    fireEvent.click(turnOnButton)
    const turnOn = await screen.findByRole('alertdialog', { name: '让「Guests」开启白名单？' })
    expect(api.putDestGroup).not.toHaveBeenCalled()
    const go = await within(turnOn).findByRole('button', { name: '开启白名单' })
    await waitFor(() => expect((go as HTMLButtonElement).disabled).toBe(false))
    fireEvent.click(go)
    await waitFor(() => expect(api.putDestGroup).toHaveBeenCalledTimes(1))
    expect(api.putDestGroup).toHaveBeenCalledWith(2, { state: 'on', list_ids: [10], base_hosts: ['dns.example.net'] })
    await waitFor(() => expect(onSaved).toHaveBeenCalledWith({ groupId: 2, state: 'on', created: true }))
  })

  it('no usable node is asked about first, as the one destructive question, and no is no write', async () => {
    vi.mocked(api.previewDestGroup).mockResolvedValue(preview({ eligible_nodes: 0 }))
    confirmMock.mockResolvedValueOnce(false)
    await toStep3()
    expect(await screen.findByText('启用后本组成员将没有任何可用节点')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '开始仅报告' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '开始仅报告后「Guests」的成员将没有任何可用节点？', destructive: true })))
    expect(api.putDestGroup).not.toHaveBeenCalled()
  })

  it('names the nodes that enforce but do not record', async () => {
    vi.mocked(api.previewDestGroup).mockResolvedValue(preview({ non_recording_nodes: 3 }))
    await toStep3()
    expect(await screen.findByText('3 台节点只执行、不记录，仅报告的数据里没有它们的流量')).toBeTruthy()
  })

  it('a refusal stays on step 3 in an alert, in the words of the bundle', async () => {
    const err = Object.assign(new Error('x'), { isAxiosError: true, response: { status: 400, data: { code: 'dest_policy_invalid', field: 'base_hosts' } } })
    vi.mocked(api.putDestGroup).mockRejectedValue(err)
    await toStep3()
    fireEvent.click(screen.getByRole('button', { name: '开始仅报告' }))
    expect(await screen.findByRole('alert')).toBeTruthy()
    expect(onSaved).not.toHaveBeenCalled()
  })

  it('a group that is off with its lists kept sends the updated_at it was read at and no base hosts', async () => {
    renderAccess(dlg({ initialGroupId: 3 }))
    fireEvent.click(await screen.findByRole('button', { name: '下一步' }))
    expect(await screen.findByText('本组成员都不再获得以下 2 台节点：', { exact: false })).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '开始仅报告' }))
    await waitFor(() => expect(api.putDestGroup).toHaveBeenCalledTimes(1))
    expect(api.putDestGroup).toHaveBeenCalledWith(3, { state: 'report', list_ids: [10], updated_at: 77 })
    await waitFor(() => expect(onSaved).toHaveBeenCalledWith({ groupId: 3, state: 'report', created: false }))
  })
})

describe('a preview the server refuses', () => {
  const refused = Object.assign(new Error('x'), { isAxiosError: true, response: { status: 400, data: { code: 'dest_list_too_broad', field: 'list_ids' } } })

  it('is said on step 3 with a way back to the lists, and the button stays off', async () => {
    vi.mocked(api.previewDestGroup).mockRejectedValue(refused)
    renderAccess(dlg({ initialGroupId: 3 }))
    fireEvent.click(await screen.findByRole('button', { name: '下一步' }))
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toContain('列表里有过宽的条目')
    expect((screen.getByRole('button', { name: '开始仅报告' }) as HTMLButtonElement).disabled).toBe(true)
    expect(screen.queryByTestId('enable-kpi-skeleton')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: '返回修改名单' }))
    expect(await screen.findByText('本组成员只能访问这些列表里的目的地')).toBeTruthy()
  })
})

describe('editing a group that is on', () => {
  it('the button is named for the state chosen when it differs from the one saved', async () => {
    const live = group({ group_id: 4, group_name: 'Ops', state: 'report', updated_at: 5, ineligible: [], pending_nodes: [] })
    renderAccess(dlg({ groups: [live], initialGroupId: 4 }))
    fireEvent.click(await screen.findByRole('button', { name: '下一步' }))
    expect(await screen.findByRole('button', { name: '保存' })).toBeTruthy()
    fireEvent.click(screen.getByRole('radio', { name: /^开启/ }))
    expect(await screen.findByRole('button', { name: '开启白名单' })).toBeTruthy()
  })

  it('asks before a list is taken out of it, naming the list', async () => {
    const live = group({ group_id: 4, group_name: 'Ops', state: 'on', updated_at: 5, list_ids: [10, 11], lists: [{ id: 10, name: 'Guest list', entry_count: 40, state: 'ready' }, { id: 11, name: 'Remote feed', entry_count: 3, state: 'ready' }], ineligible: [], pending_nodes: [] })
    confirmMock.mockResolvedValueOnce(false)
    renderAccess(dlg({ groups: [live], initialGroupId: 4 }))
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Remote feed' }))
    fireEvent.click(await screen.findByRole('button', { name: '下一步' }))
    fireEvent.click(await screen.findByRole('button', { name: '保存' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '从「Ops」的名单里去掉 Remote feed？' })))
    expect(api.putDestGroup).not.toHaveBeenCalled()
  })
})
