// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as api from '@/api/accessControl'
import { confirmMock, renderAccess, setTestLang, snack } from '@/test/accessControlHarness'
import DestResolveDialog from './DestResolveDialog'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})

const node = (over: Partial<api.DestNodeStatus> = {}): api.DestNodeStatus => ({
  panel_id: 5, agent_id: 'a5', panel_name: '东京 01', kind: 'psp', engine: 'xray', agent_version: '4.2.0',
  supports: { policy: true, hits: true, usage: false }, collect: 'hits', collect_effective: 'hits', collecting: true, state: 'applied',
  resolve: 'as_is', resolve_effective: '', resolve_unavailable: null,
  fallback_reason: '', over_limit: null, sniffing_insufficient: [], minted_at: null, pending_since: null, applied_at: 1, applied_rules: 3,
  last_report_at: 1, rules_data_version: null, geoip_unavailable: false, allowlist_groups: [], hits_24h: 1204, ...over,
})
const onClose = vi.fn()
const open = (n = node()) => renderAccess(<DestResolveDialog node={n} onClose={onClose} />)

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestStatus).mockResolvedValue({ nodes: [node()], apply_eta_ms: 150_000, policy_notes: {}, totals: {} } as unknown as api.DestStatus)
  vi.mocked(api.getDestSettings).mockResolvedValue({ settings: {} as never, defaults: {}, effective: {} as never })
  vi.mocked(api.setDestResolve).mockResolvedValue()
})

describe('the hostname-matching dialog', () => {
  it('offers as-is and on-demand, and shows if-non-match disabled with its reason', async () => {
    open()
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('IP 规则对主机名的匹配方式')).toBeTruthy()
    const radios = within(dialog).getAllByRole('radio') as HTMLInputElement[]
    expect(radios.map(r => r.value)).toEqual(['as_is', 'on_demand', 'if_non_match'])
    expect(radios.map(r => r.disabled)).toEqual([false, false, true])
    expect(radios[0].checked).toBe(true)
    expect(within(dialog).getByText('按原样（默认）：不做 DNS。IP 段与内网地址规则只对以 IP 发起的连接生效；带假 SNI/Host 的连接会被改去 SNI 指向的地址，IP 规则看不到它。')).toBeTruthy()
    expect(within(dialog).getByText('按需解析：路由走到第一条 IP 规则时解析一次主机名，任一解析结果落在被拦网段即拦截。代价：每个连接多一次 DNS 查询与一次往返延迟（Xray 另外在拨号时再解析一次）。')).toBeTruthy()
    expect(within(dialog).getByText('不匹配时解析（IPIfNonMatch）：不提供——会让仅报告和试运行分组改变判定。')).toBeTruthy()
  })

  it('states the caveats and the one restart', async () => {
    open()
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText(/Xray 在解析失败时放行/)).toBeTruthy()
    expect(within(dialog).getByText(/拨号时再解析一次/, { selector: '[data-testid="resolve-caveats"] *' })).toBeTruthy()
    expect(within(dialog).getByText(/routeOnly:true/)).toBeTruthy()
    expect(within(dialog).getByText('切到按需解析后，这台节点会重启代理程序一次。')).toBeTruthy()
  })

  it('Save is disabled until the choice changes, then sends on_demand and closes', async () => {
    open()
    const dialog = await screen.findByRole('dialog')
    const save = within(dialog).getByRole('button', { name: '保存' }) as HTMLButtonElement
    expect(save.disabled).toBe(true)
    fireEvent.click(within(dialog).getByRole('radio', { name: /按需解析：/ }))
    expect(save.disabled).toBe(false)
    fireEvent.click(save)
    await waitFor(() => expect(api.setDestResolve).toHaveBeenCalledWith(5, 'on_demand'))
    expect(confirmMock).not.toHaveBeenCalled()
    await waitFor(() => expect(snack).toHaveBeenCalledWith('已保存，东京 01 会在约 3 分钟内重启代理程序一次', 'success'))
    expect(onClose).toHaveBeenCalled()
  })

  it('starts on the node\'s stored mode, and the disabled row cannot be chosen', async () => {
    open(node({ resolve: 'on_demand', resolve_effective: 'on_demand' }))
    const dialog = await screen.findByRole('dialog')
    expect((within(dialog).getByRole('radio', { name: /按需解析：/ }) as HTMLInputElement).checked).toBe(true)
    fireEvent.click(within(dialog).getByRole('radio', { name: /不匹配时解析/ }))
    expect((within(dialog).getByRole('radio', { name: /按需解析：/ }) as HTMLInputElement).checked).toBe(true)
    expect((within(dialog).getByRole('button', { name: '保存' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('says why a node runs as-is despite on-demand', async () => {
    open(node({ resolve: 'on_demand', resolve_effective: '', resolve_unavailable: 'route_only' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('入站 routeOnly 开启，按原样执行')).toBeTruthy()
  })

  it('a refusal is said in the dialog and the dialog stays', async () => {
    vi.mocked(api.setDestResolve).mockRejectedValue(Object.assign(new Error('x'), { isAxiosError: true, response: { status: 400, data: { error: 'nope' } } }))
    open()
    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('radio', { name: /按需解析：/ }))
    fireEvent.click(within(dialog).getByRole('button', { name: '保存' }))
    expect(await within(dialog).findByRole('alert')).toBeTruthy()
    expect(onClose).not.toHaveBeenCalled()
  })

  it('closing with an unsaved choice asks first', async () => {
    open()
    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('radio', { name: /按需解析：/ }))
    confirmMock.mockResolvedValueOnce(false)
    fireEvent.click(within(dialog).getByRole('button', { name: '取消' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '放弃未保存的修改？' })))
    expect(onClose).not.toHaveBeenCalled()
  })

  it('in English', async () => {
    setTestLang('en-US')
    open()
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('How IP rules treat host names')).toBeTruthy()
    expect(within(dialog).getByRole('radio', { name: /As is \(default\)/ })).toBeTruthy()
    expect(within(dialog).getByRole('radio', { name: /Resolve on demand/ })).toBeTruthy()
    expect((within(dialog).getByRole('radio', { name: /Resolve if nothing matches/ }) as HTMLInputElement).disabled).toBe(true)
  })
})
