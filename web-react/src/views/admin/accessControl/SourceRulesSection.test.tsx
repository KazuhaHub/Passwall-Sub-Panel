// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as api from '@/api/accessControl'
import { confirmMock, renderAccess } from '@/test/accessControlHarness'
import SourceRulesSection, { isCidr, parseCidrLines } from './SourceRulesSection'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})

const rule = (over: Partial<api.DestSourceRule> = {}): api.DestSourceRule => ({ node_id: 4, countries: [], cidrs: [], state: 'off', updated_at: null, ...over })

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestSourceRule).mockResolvedValue({ rule: rule(), native: true, geoip_available: true })
  vi.mocked(api.getDestSourceRules).mockResolvedValue({ items: [], geoip_available: true })
  vi.mocked(api.getDestPolicies).mockResolvedValue({ allow: [], block: [], observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget: {} as api.DestBudget })
  vi.mocked(api.getDestStatus).mockResolvedValue({ nodes: [], policy_notes: {}, totals: {}, apply_eta_ms: 120_000 } as unknown as api.DestStatus)
})

describe('CIDR lines', () => {
  it('accepts IPv4 and IPv6 prefixes and reports the numbers of the bad lines', () => {
    expect(isCidr('203.0.113.0/24')).toBe(true)
    expect(isCidr('2001:db8::/32')).toBe(true)
    expect(isCidr('203.0.113.0/33')).toBe(false)
    expect(isCidr('300.0.0.0/8')).toBe(false)
    expect(isCidr('example.com')).toBe(false)
    expect(parseCidrLines('10.0.0.0/8\n\nnope\n 192.168.0.0/16 ')).toEqual({ cidrs: ['10.0.0.0/8', '192.168.0.0/16'], bad: [3] })
  })
})

describe('SourceRulesSection', () => {
  it('renders nothing for an inbound that is not on a native node', async () => {
    vi.mocked(api.getDestSourceRule).mockResolvedValue({ rule: rule(), native: false, geoip_available: true })
    renderAccess(<SourceRulesSection nodeId={4} />)
    await waitFor(() => expect(api.getDestSourceRule).toHaveBeenCalled())
    expect(screen.queryByTestId('source-rules-section')).toBeNull()
  })

  it('says a country rule is not enforced when there is no GeoIP database', async () => {
    vi.mocked(api.getDestSourceRule).mockResolvedValue({ rule: rule(), native: true, geoip_available: false })
    renderAccess(<SourceRulesSection nodeId={4} />)
    expect(await screen.findByText('没有 GeoIP 库：国家规则不会执行')).toBeTruthy()
  })

  it('saves countries, cidrs and state with updated_at, after the first-publish confirmation, with a restart sentence beside it', async () => {
    vi.mocked(api.getDestSourceRule).mockResolvedValue({ rule: rule({ updated_at: 1234 }), native: true, geoip_available: true })
    vi.mocked(api.putDestSourceRule).mockResolvedValue({ rule: rule(), geoip_available: true })
    renderAccess(<SourceRulesSection nodeId={4} />)
    expect(await screen.findByText('保存后约 2 分钟内生效，节点会重启代理程序一次。')).toBeTruthy()
    const input = screen.getByLabelText('拒绝的国家或地区')
    fireEvent.change(input, { target: { value: 'China' } })
    fireEvent.keyDown(input, { key: 'ArrowDown' })
    fireEvent.click(await screen.findByRole('option', { name: /\(CN\)/ }))
    fireEvent.click(screen.getByRole('switch', { name: '启用' }))
    fireEvent.click(screen.getByRole('button', { name: '保存来源规则' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '启用第一条访问策略？' })))
    await waitFor(() => expect(api.putDestSourceRule).toHaveBeenCalledWith(4, { countries: ['CN'], cidrs: [], state: 'on', updated_at: 1234 }))
  })

  it('refuses a bad CIDR line before sending it, by line number', async () => {
    renderAccess(<SourceRulesSection nodeId={4} />)
    const box = await screen.findByLabelText('拒绝的来源 IP 段（可选，每行一个）')
    fireEvent.change(box, { target: { value: '10.0.0.0/8\nnope' } })
    expect(screen.getByText('第 2 行不是有效的 IP 段')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '保存来源规则' }))
    expect(api.putDestSourceRule).not.toHaveBeenCalled()
  })

  it('explains a stale rule and reloads it', async () => {
    vi.mocked(api.putDestSourceRule).mockRejectedValue(Object.assign(new Error('x'), { isAxiosError: true, response: { status: 409, data: { code: 'dest_source_rule_stale' } } }))
    renderAccess(<SourceRulesSection nodeId={4} />)
    fireEvent.click(await screen.findByRole('switch', { name: '启用' }))
    fireEvent.click(screen.getByRole('button', { name: '保存来源规则' }))
    expect(await screen.findByText(/刚被其他人修改过，已重新载入/)).toBeTruthy()
    await waitFor(() => expect(vi.mocked(api.getDestSourceRule).mock.calls.length).toBeGreaterThan(1))
  })

  it('registers a dirty check that the node dialog asks before it closes', async () => {
    const guard = { current: null as import('./SourceRulesSection').SourceDraftHandle | null }
    vi.mocked(api.getDestSourceRule).mockResolvedValue({ rule: rule({ updated_at: 1 }), native: true, geoip_available: true })
    renderAccess(<SourceRulesSection nodeId={4} dirtyRef={guard} />)
    await screen.findByTestId('source-rules-section')
    expect(guard.current?.dirty()).toBe(false)
    fireEvent.click(screen.getByRole('switch', { name: '启用' }))
    expect(guard.current?.dirty()).toBe(true)
  })
})
