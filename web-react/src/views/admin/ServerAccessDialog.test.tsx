// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as api from '@/api/accessControl'
import { renderAccess, setTestLang, whereIs } from '@/test/accessControlHarness'
import ServerAccessDialog from './ServerAccessDialog'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})

const node = (over: Partial<api.DestNodeStatus> = {}): api.DestNodeStatus => ({
  panel_id: 5, agent_id: 'a5', panel_name: '东京 01', kind: 'psp', engine: 'xray', agent_version: '4.2.0',
  supports: { policy: true, hits: true, usage: false }, collect: 'hits', collect_effective: 'hits', collecting: true, state: 'applied',
  fallback_reason: '', over_limit: null, sniffing_insufficient: [], minted_at: null, pending_since: null, applied_at: Date.now() - 180_000, applied_rules: 9,
  last_report_at: Date.now() - 30_000, rules_data_version: null, geoip_unavailable: false, allowlist_groups: [], hits_24h: 1204, ...over,
})
const show = (n: api.DestNodeStatus) => renderAccess(<ServerAccessDialog node={n} serverName="东京 01" onClose={() => {}} />, { route: '/admin/servers' })

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestStatus).mockResolvedValue({ nodes: [node()], apply_eta_ms: 120_000, policy_notes: {}, totals: {} } as unknown as api.DestStatus)
  vi.mocked(api.getDestSettings).mockResolvedValue({ settings: {} as never, defaults: {}, effective: { dest_hit_retention_days: 30 } as never })
})

describe('the single-node dialog: recording and the last 24 hours (S15, 2c)', () => {
  it('shows the mode with its retention and a Change, and the hits with a link to the records of this node', async () => {
    const { router } = show(node())
    const recording = await screen.findByTestId('server-recording')
    expect(recording.textContent).toContain('记录方式')
    expect(recording.textContent).toContain('只记录规则命中')
    expect(await within(recording).findByText('保留 30 天')).toBeTruthy()
    const hits = screen.getByTestId('server-hits')
    expect(hits.textContent).toContain('24 小时命中')
    expect(hits.textContent).toContain('1,204')
    fireEvent.click(within(hits).getByRole('button', { name: '查看记录' }))
    await waitFor(() => expect(whereIs(router)).toBe('/admin/access-control?tab=records&rec_panel=5'))
  })

  it('Change opens the recording dialog on this node', async () => {
    show(node())
    fireEvent.click(await within(await screen.findByTestId('server-recording')).findByRole('button', { name: '修改' }))
    expect(await screen.findByText('东京 01 记录什么？')).toBeTruthy()
  })

  it('shows the hostname matching mode and Change opens its dialog', async () => {
    show(node({ resolve: 'on_demand', resolve_effective: 'on_demand' }))
    const box = await screen.findByTestId('server-resolve')
    expect(box.textContent).toContain('主机名匹配')
    expect(box.textContent).toContain('按需解析')
    fireEvent.click(within(box).getByRole('button', { name: '修改' }))
    expect(await screen.findByText('IP 规则对主机名的匹配方式')).toBeTruthy()
  })

  it('a node held at as-is shows why next to the mode', async () => {
    show(node({ resolve: 'on_demand', resolve_effective: '', resolve_unavailable: 'route_only' }))
    const box = await screen.findByTestId('server-resolve')
    expect(box.textContent).toContain('按原样')
    expect(box.textContent).toContain('入站 routeOnly 开启，按原样执行')
  })

  it('a node with no IP rules shows the neutral note, not the pending text', async () => {
    show(node({ resolve: 'on_demand', resolve_effective: '', resolve_unavailable: 'no_ip_rules' }))
    const box = await screen.findByTestId('server-resolve')
    expect(box.textContent).toContain('策略里没有 IP 段或内网规则，无需解析，按原样执行')
    expect(box.textContent).not.toContain('待下发')
  })

  it('a node that does not record has no hit block and says it does not record', async () => {
    show(node({ collect: 'off', collect_effective: 'off', collecting: false, hits_24h: 0 }))
    const recording = await screen.findByTestId('server-recording')
    expect(recording.textContent).toContain('不记录')
    expect(screen.queryByTestId('server-hits')).toBeNull()
  })

  it('sing-box only enforces; a node too old must be upgraded: each says so and has no hit block', async () => {
    const { unmount } = show(node({ engine: 'sing-box', supports: { policy: true, hits: false, usage: false }, hits_24h: null }))
    expect((await screen.findByTestId('server-recording')).textContent).toContain('sing-box 节点只执行，不记录')
    expect(screen.queryByTestId('server-hits')).toBeNull()
    unmount()
    show(node({ supports: { policy: true, hits: false, usage: false }, hits_24h: null, state: 'unsupported_version' }))
    expect((await screen.findByTestId('server-recording')).textContent).toContain('节点版本过旧，需升级')
    expect(screen.queryByTestId('server-hits')).toBeNull()
  })

  it('a count the server cannot give is a dash, not a 0', async () => {
    show(node({ hits_24h: null }))
    const hits = await screen.findByTestId('server-hits')
    expect(hits.textContent).toContain('—')
    expect(hits.textContent).not.toMatch(/\b0\b/)
  })

  it('in English', async () => {
    setTestLang('en-US')
    show(node())
    const recording = await screen.findByTestId('server-recording')
    expect(recording.textContent).toContain('Recording')
    expect(recording.textContent).toContain('Rule matches only')
    expect(screen.getByTestId('server-hits').textContent).toContain('Hits, 24 hours')
  })
})
