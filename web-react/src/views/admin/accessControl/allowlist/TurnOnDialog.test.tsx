// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as api from '@/api/accessControl'
import { renderAccess, snack } from '@/test/accessControlHarness'
import { detail, preview, status } from '@/test/allowlistFixtures'
import TurnOnDialog from './TurnOnDialog'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})

const onConfirm = vi.fn()
const onClose = vi.fn()
const onViewReport = vi.fn()
const onViewNodes = vi.fn()
const dialog = (over: Partial<Parameters<typeof TurnOnDialog>[0]> = {}) => (
  <TurnOnDialog open group={{ id: 1, name: 'Guests', members: 51 }} listIds={[10]} reportDay={3} onConfirm={onConfirm} onClose={onClose}
    onViewReport={onViewReport} onViewNodes={onViewNodes} {...over} />
)
const stale = Object.assign(new Error('x'), { isAxiosError: true, response: { status: 409, data: { code: 'dest_group_stale' } } })
const notReady = Object.assign(new Error('x'), { isAxiosError: true, response: { status: 409, data: { code: 'dest_list_not_ready', not_ready_lists: [{ id: 3, name: 'Remote feed' }] } } })

beforeEach(() => {
  vi.clearAllMocks()
  onConfirm.mockResolvedValue(undefined)
  vi.mocked(api.getDestStatus).mockResolvedValue(status())
  vi.mocked(api.getDestGroup).mockResolvedValue(detail())
  vi.mocked(api.previewDestGroup).mockResolvedValue(preview())
})

describe('the turn-on dialog', () => {
  it('says what turning on does, in the owner words, and confirms with one plain-looking destructive button', async () => {
    renderAccess(dialog())
    const d = await screen.findByRole('alertdialog', { name: '让「Guests」开启白名单？' })
    expect(await within(d).findByText('开启后，本组 51 人访问名单外的目的地会被拒绝，约 2 分钟内生效。可以随时改回仅报告。')).toBeTruthy()
    expect(await within(d).findByText('所有名单都已就绪')).toBeTruthy()
    expect(within(d).getByText('基础放行不为空')).toBeTruthy()
    expect(within(d).getByText('仅报告第 3 天')).toBeTruthy()
    const go = within(d).getByRole('button', { name: '开启白名单' })
    await waitFor(() => expect((go as HTMLButtonElement).disabled).toBe(false))
    fireEvent.click(go)
    await waitFor(() => expect(onConfirm).toHaveBeenCalledTimes(1))
  })

  it('opens with focus on Cancel, so a stray Enter cannot turn the allowlist on', async () => {
    renderAccess(dialog())
    const d = await screen.findByRole('alertdialog')
    await waitFor(() => expect(document.activeElement).toBe(within(d).getByRole('button', { name: '取消' })))
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it('only a list that is not ready stops it; everything else is a note', async () => {
    vi.mocked(api.getDestGroup).mockResolvedValue(detail({ readiness: {
      lists_ready: false, not_ready_lists: [{ id: 3, name: 'Remote feed' }], unapplied_nodes: 2, base_nonempty: false, remaining_sites_24h: 41,
      top_sites: [{ site: 'gstatic.com', count: 5, added: false }, { site: 'figma.com', count: 4, added: false }, { site: 'ok.com', count: 3, added: true }],
    } }))
    renderAccess(dialog())
    const d = await screen.findByRole('alertdialog')
    expect(await within(d).findByText('名单「Remote feed」还没下载成功，不能开启')).toBeTruthy()
    expect((within(d).getByRole('button', { name: '开启白名单' }) as HTMLButtonElement).disabled).toBe(true)
    expect(within(d).getByTestId('check-lists').getAttribute('data-tone')).toBe('failing')
    // The rest do not block, and are amber.
    expect(within(d).getByText('2 台可用节点还没生效本组规则').closest('[data-testid="check-unapplied"]')?.getAttribute('data-tone')).toBe('attention')
    expect(within(d).getByText(/基础放行为空/).closest('[data-testid="check-base"]')?.getAttribute('data-tone')).toBe('attention')
    expect(within(d).getByText(/过去 24 小时仍有 41 个主域名本应被拒绝，最多的是 gstatic.com、figma.com/)).toBeTruthy()
    fireEvent.click(within(d).getByRole('button', { name: '看报告' }))
    expect(onViewReport).toHaveBeenCalled()
    fireEvent.click(within(d).getByRole('button', { name: '查看' }))
    expect(onViewNodes).toHaveBeenCalled()
  })

  it('a list chosen in the dialog that the preview says is not ready stops it too', async () => {
    vi.mocked(api.previewDestGroup).mockResolvedValue(preview({ not_ready_lists: [{ id: 3, name: 'Remote feed' }] }))
    renderAccess(dialog())
    const d = await screen.findByRole('alertdialog')
    expect(await within(d).findByText('名单「Remote feed」还没下载成功，不能开启')).toBeTruthy()
    expect((within(d).getByRole('button', { name: '开启白名单' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('a group that was never reported says it cannot show what would be refused', async () => {
    renderAccess(dialog({ reportDay: null }))
    expect(await screen.findByText('还没有仅报告的数据：开启前看不到会拦到什么')).toBeTruthy()
  })

  it('a failed read has a retry and does not let it through', async () => {
    vi.mocked(api.getDestGroup).mockRejectedValue(new Error('boom'))
    renderAccess(dialog())
    const d = await screen.findByRole('alertdialog')
    expect(await within(d).findByRole('button', { name: '重试' })).toBeTruthy()
    expect((within(d).getByRole('button', { name: '开启白名单' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('a refused turn-on for a list that is not ready is said inside, and the dialog stays', async () => {
    onConfirm.mockRejectedValue(notReady)
    renderAccess(dialog())
    const d = await screen.findByRole('alertdialog')
    const go = await within(d).findByRole('button', { name: '开启白名单' })
    await waitFor(() => expect((go as HTMLButtonElement).disabled).toBe(false))
    fireEvent.click(go)
    expect((await within(d).findAllByText('名单「Remote feed」还没下载成功，不能开启')).length).toBeGreaterThan(0)
    expect(onClose).not.toHaveBeenCalled()
  })

  it('a stale write closes it with the sentence that says the state moved', async () => {
    onConfirm.mockRejectedValue(stale)
    renderAccess(dialog())
    const go = await screen.findByRole('button', { name: '开启白名单' })
    await waitFor(() => expect((go as HTMLButtonElement).disabled).toBe(false))
    fireEvent.click(go)
    await waitFor(() => expect(snack).toHaveBeenCalledWith('状态已被其他人修改，已重新载入', 'warning'))
    expect(onClose).toHaveBeenCalled()
  })

  it('lists taken out in the edit dialog are no longer counted: only the chosen lists are asked about', async () => {
    vi.mocked(api.getDestGroup).mockResolvedValue(detail({ readiness: { lists_ready: false, not_ready_lists: [{ id: 3, name: 'Remote feed' }], unapplied_nodes: 0, base_nonempty: true, remaining_sites_24h: 0, top_sites: [] } }))
    vi.mocked(api.previewDestGroup).mockResolvedValue(preview({ not_ready_lists: [] }))
    renderAccess(dialog({ listIds: [10], savedListIds: [10, 3] }))
    const d = await screen.findByRole('alertdialog')
    expect(await within(d).findByText('所有名单都已就绪')).toBeTruthy()
    await waitFor(() => expect((within(d).getByRole('button', { name: '开启白名单' }) as HTMLButtonElement).disabled).toBe(false))
  })

  it('a first allowlist is judged by the base hosts typed in the dialog, not by a row that is not there', async () => {
    vi.mocked(api.getDestGroup).mockResolvedValue(detail({ readiness: { lists_ready: true, not_ready_lists: [], unapplied_nodes: 0, base_nonempty: true, remaining_sites_24h: 5, top_sites: [] } }))
    renderAccess(dialog({ baseCount: 0, hasRow: false }))
    expect((await screen.findByText(/基础放行为空/)).closest('[data-testid="check-base"]')?.getAttribute('data-tone')).toBe('attention')
    expect(screen.queryByRole('button', { name: '看报告' })).toBeNull()
  })

  it('says the group will also lose the nodes that cannot enforce it', async () => {
    renderAccess(dialog())
    expect(await screen.findByText('同时，本组成员不再获得 2 台不执行白名单的节点。')).toBeTruthy()
  })

  it('with lists edited, it cannot be confirmed while the preview is pending or refused', async () => {
    vi.mocked(api.previewDestGroup).mockReturnValue(new Promise(() => {}))
    const first = renderAccess(dialog({ listIds: [10], savedListIds: [10, 3] }))
    const d = await screen.findByRole('alertdialog')
    await within(d).findByText('所有名单都已就绪')
    expect((within(d).getByRole('button', { name: '开启白名单' }) as HTMLButtonElement).disabled).toBe(true)
    first.unmount()
    vi.mocked(api.previewDestGroup).mockRejectedValue(new Error('boom'))
    renderAccess(dialog({ listIds: [10], savedListIds: [10, 3] }))
    const d2 = await screen.findByRole('alertdialog')
    await within(d2).findByText('所有名单都已就绪')
    await waitFor(() => expect((within(d2).getByRole('button', { name: '开启白名单' }) as HTMLButtonElement).disabled).toBe(true))
  })
})
