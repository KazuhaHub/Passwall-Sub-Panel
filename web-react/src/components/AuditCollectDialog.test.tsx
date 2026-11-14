// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as api from '@/api/accessControl'
import { confirmMock, renderAccess, setTestLang, snack } from '@/test/accessControlHarness'
import AuditCollectDialog from './AuditCollectDialog'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})

const node = (over: Partial<api.DestNodeStatus> = {}): api.DestNodeStatus => ({
  panel_id: 5, agent_id: 'a5', panel_name: '东京 01', kind: 'psp', engine: 'xray', agent_version: '4.2.0',
  supports: { policy: true, hits: true, usage: false }, collect: 'hits', collect_effective: 'hits', collecting: true, state: 'applied',
  fallback_reason: '', over_limit: null, sniffing_insufficient: [], minted_at: null, pending_since: null, applied_at: 1, applied_rules: 3,
  last_report_at: 1, rules_data_version: null, geoip_unavailable: false, allowlist_groups: [], hits_24h: 1204, ...over,
})
const onClose = vi.fn()
const open = (n = node()) => renderAccess(<AuditCollectDialog node={n} onClose={onClose} />)

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestStatus).mockResolvedValue({ nodes: [node()], apply_eta_ms: 150_000, policy_notes: {}, totals: {} } as unknown as api.DestStatus)
  vi.mocked(api.getDestSettings).mockResolvedValue({ settings: {} as never, defaults: {}, effective: { dest_hit_retention_days: 14 } as never })
  vi.mocked(api.setAuditCollect).mockResolvedValue()
})

describe('the recording-mode dialog (S14/S15)', () => {
  it('offers Don\'t record and Rule matches only, and not the per-site usage mode (a later phase), each with what it records', async () => {
    open()
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('东京 01 记录什么？')).toBeTruthy()
    const radios = within(dialog).getAllByRole('radio')
    expect(radios.map(r => (r.closest('label') as HTMLElement).textContent)).toEqual([
      expect.stringContaining('不记录'), expect.stringContaining('只记录规则命中'),
    ])
    expect(within(dialog).getByText(/只执行策略，不留任何记录/)).toBeTruthy()
    // The retention in the words is the setting, not a literal.
    expect(await within(dialog).findByText(/保留 14 天/)).toBeTruthy()
    expect(within(dialog).queryByText(/按网站用量/)).toBeNull()
  })

  it('labels each stored value for what it is: unset is the default (hits), hits_and_usage is its own mode, never "rule matches only"', async () => {
    const byValue = (d: HTMLElement, v: string) => within(d).getAllByRole('radio').find(r => (r as HTMLInputElement).value === v) as HTMLInputElement
    const unset = open(node({ collect: '' as never }))
    expect(byValue(await screen.findByRole('dialog'), 'hits').checked).toBe(true)
    unset.unmount()
    open(node({ collect: 'hits_and_usage' }))
    const dialog = await screen.findByRole('dialog')
    const usage = within(dialog).getByRole('radio', { name: /命中和按网站用量/ }) as HTMLInputElement
    expect(usage.checked).toBe(true)
    expect(usage.disabled).toBe(true)
    expect(byValue(dialog, 'hits').checked).toBe(false)
    // Nothing changed yet: nothing to save. Picking a mode it can be set to does save.
    expect((within(dialog).getByRole('button', { name: '保存' }) as HTMLButtonElement).disabled).toBe(true)
    fireEvent.click(byValue(dialog, 'hits'))
    fireEvent.click(within(dialog).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.setAuditCollect).toHaveBeenCalledWith(5, 'hits'))
  })

  it('starts on the node\'s mode; Save is disabled until it changes, then sends only that key with the restart sentence', async () => {
    open()
    const dialog = await screen.findByRole('dialog')
    const save = within(dialog).getByRole('button', { name: '保存' }) as HTMLButtonElement
    expect((within(dialog).getByRole('radio', { name: /只记录规则命中/ }) as HTMLInputElement).checked).toBe(true)
    expect(save.disabled).toBe(true)
    expect(within(dialog).getByText('保存后这台节点会重启代理程序一次。')).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('radio', { name: /不记录/ }))
    expect(save.disabled).toBe(false)
    fireEvent.click(save)
    await waitFor(() => expect(api.setAuditCollect).toHaveBeenCalledWith(5, 'off'))
    // No second confirmation: the sentence beside the button is the warning.
    expect(confirmMock).not.toHaveBeenCalled()
    await waitFor(() => expect(snack).toHaveBeenCalledWith('已保存，东京 01 会在约 3 分钟内重启代理程序一次', 'success'))
    expect(onClose).toHaveBeenCalled()
  })

  it('a node too old to record can only be set to Don\'t record, and says to upgrade', async () => {
    open(node({ supports: { policy: true, hits: false, usage: false }, collect: 'off' }))
    const dialog = await screen.findByRole('dialog')
    expect((within(dialog).getByRole('radio', { name: /只记录规则命中/ }) as HTMLInputElement).disabled).toBe(true)
    expect(within(dialog).getByText('节点版本过旧，需升级')).toBeTruthy()
  })

  it('a sing-box node only enforces: the dialog is read-only and says so', async () => {
    open(node({ engine: 'sing-box', supports: { policy: true, hits: false, usage: false } }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('sing-box 节点只执行，不记录')).toBeTruthy()
    for (const r of within(dialog).queryAllByRole('radio')) expect((r as HTMLInputElement).disabled).toBe(true)
    expect(within(dialog).queryByRole('button', { name: '保存' })).toBeNull()
  })

  it('a refusal is said in the dialog and the dialog stays', async () => {
    vi.mocked(api.setAuditCollect).mockRejectedValue(Object.assign(new Error('x'), { isAxiosError: true, response: { status: 400, data: { error: 'nope' } } }))
    open()
    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('radio', { name: /不记录/ }))
    fireEvent.click(within(dialog).getByRole('button', { name: '保存' }))
    expect(await within(dialog).findByRole('alert')).toBeTruthy()
    expect(onClose).not.toHaveBeenCalled()
  })

  it('closing with an unsaved choice asks first', async () => {
    open()
    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('radio', { name: /不记录/ }))
    confirmMock.mockResolvedValueOnce(false)
    fireEvent.click(within(dialog).getByRole('button', { name: '取消' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '放弃未保存的修改？' })))
    expect(onClose).not.toHaveBeenCalled()
  })

  it('in English: the term table\'s words', async () => {
    setTestLang('en-US')
    open()
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('What does 东京 01 record?')).toBeTruthy()
    expect(within(dialog).getByRole('radio', { name: /Don't record/ })).toBeTruthy()
    expect(within(dialog).getByRole('radio', { name: /Rule matches only/ })).toBeTruthy()
  })
})
