// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { renderAccess, snack, whereIs } from '@/test/accessControlHarness'
import * as api from '@/api/accessControl'
import CoverageSheet from './CoverageSheet'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})

const node = (over: Partial<api.DestNodeStatus>): api.DestNodeStatus => ({
  panel_id: 1, agent_id: 'a1', panel_name: 'Tokyo 01', kind: 'psp', engine: 'xray', agent_version: '4', supports: { policy: true, hits: false, usage: false },
  collect: '', collect_effective: '', collecting: false, state: 'applied', fallback_reason: '', over_limit: null, sniffing_insufficient: [],
  minted_at: null, pending_since: null, applied_at: Date.now() - 60_000, applied_rules: 5, last_report_at: null, rules_data_version: null,
  geoip_unavailable: false, allowlist_groups: [], hits_24h: null, ...over,
})
const status = (nodes: api.DestNodeStatus[]) => ({
  generation: 1, published_generation: 1, last_write_at: null, next_publish_at: null, apply_eta_ms: 0, paused: false, publish_error: null, publish_error_at: null,
  geosite_release: '20261112', geoip_unavailable: false, totals: {}, policy_notes: {}, nodes,
}) as api.DestStatus

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestSettings).mockResolvedValue({ settings: {}, defaults: {}, effective: { dest_policy_apply_min_seconds: 45, dest_list_refresh_hours: 24, dest_rules_update_mode: 'manual', dest_hit_retention_days: 30 } } as unknown as api.DestSettingsView)
  vi.mocked(api.getDestStatus).mockResolvedValue(status([
    node({}), node({ panel_id: 2, agent_id: 'a2', panel_name: 'Frankfurt', state: 'rejected' }),
    node({ panel_id: 3, agent_id: 'a3', panel_name: 'US A', kind: 'x-ui', state: 'unsupported_kind', rules_data_version: null }),
    node({ panel_id: 4, agent_id: 'a4', panel_name: 'Osaka', state: 'unsupported_version' }),
    node({ panel_id: 5, agent_id: 'a5', panel_name: 'Seoul', rules_data_version: '20260101' }),
  ]))
})

describe('CoverageSheet', () => {
  it('counts the five chips, shows every node and the apply window from the settings', async () => {
    renderAccess(<CoverageSheet open onClose={() => {}} filter={null} onFilterChange={() => {}} onOpenSettings={() => {}} />)
    expect(await screen.findByText('节点覆盖 · 5 台')).toBeTruthy()
    expect(screen.getAllByTestId('node-policy-row')).toHaveLength(5)
    expect(await screen.findByText(/最后一次修改后等 45 秒再下发/)).toBeTruthy()
    expect(screen.getByText(/仍在使用旧版规则数据/)).toBeTruthy()
  })

  it('a chip filters, is aria-pressed, and pressing it again clears', async () => {
    const onFilterChange = vi.fn()
    const { rerender: _r } = renderAccess(<CoverageSheet open onClose={() => {}} filter="problem" onFilterChange={onFilterChange} onOpenSettings={() => {}} />)
    await screen.findByText('节点覆盖 · 5 台')
    expect(screen.getAllByTestId('node-policy-row')).toHaveLength(1)
    const chip = screen.getByRole('button', { name: /有问题/ })
    expect(chip.getAttribute('aria-pressed')).toBe('true')
    fireEvent.click(chip)
    expect(onFilterChange).toHaveBeenCalledWith(null)
  })

  it('an empty filter says so and offers to clear it', async () => {
    const onFilterChange = vi.fn()
    vi.mocked(api.getDestStatus).mockResolvedValue(status([node({})]))
    renderAccess(<CoverageSheet open onClose={() => {}} filter="upgrade" onFilterChange={onFilterChange} onOpenSettings={() => {}} />)
    expect(await screen.findByText('没有符合的节点')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '清除筛选' }))
    expect(onFilterChange).toHaveBeenCalledWith(null)
  })

  it('Apply again retries the agent; the edit link opens the settings on the apply window', async () => {
    vi.mocked(api.retryDestAgent).mockResolvedValue({ retried: true })
    const onOpenSettings = vi.fn()
    renderAccess(<CoverageSheet open onClose={() => {}} filter={null} onFilterChange={() => {}} onOpenSettings={onOpenSettings} />)
    fireEvent.click(await screen.findByRole('button', { name: '重新下发' }))
    await waitFor(() => expect(api.retryDestAgent).toHaveBeenCalledWith('a2'))
    fireEvent.click(screen.getByRole('button', { name: '修改' }))
    expect(onOpenSettings).toHaveBeenCalledWith('apply')
  })

  it('a retry with nothing to retry is not announced as a success', async () => {
    vi.mocked(api.retryDestAgent).mockResolvedValue({ retried: false })
    renderAccess(<CoverageSheet open onClose={() => {}} filter={null} onFilterChange={() => {}} onOpenSettings={() => {}} />)
    fireEvent.click(await screen.findByRole('button', { name: '重新下发' }))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('这台节点没有被拒绝的策略，无需重新下发', 'info'))
  })

  it('Upgrade goes to the servers page for that node', async () => {
    const { router } = renderAccess(<CoverageSheet open onClose={() => {}} filter={null} onFilterChange={() => {}} onOpenSettings={() => {}} />)
    const row = (await screen.findAllByTestId('node-policy-row')).find(r => within(r).queryByText('Osaka'))!
    fireEvent.click(within(row).getByRole('button', { name: '升级' }))
    await waitFor(() => expect(whereIs(router)).toBe('/admin/servers?q=Osaka'))
  })
})

describe('recording (2c)', () => {
  const rec = (over: Partial<api.DestNodeStatus> = {}) => node({
    supports: { policy: true, hits: true, usage: false }, collect: 'hits', collect_effective: 'hits', collecting: true, hits_24h: 1204, last_report_at: Date.now() - 30_000, ...over,
  })
  const sheet = () => renderAccess(<CoverageSheet open onClose={() => {}} filter={null} onFilterChange={() => {}} onOpenSettings={() => {}} />)

  it('says what is recorded, over the retention that is set, and that the raw log stays on the node', async () => {
    vi.mocked(api.getDestStatus).mockResolvedValue(status([rec(), rec({ panel_id: 2, agent_id: 'a2', panel_name: 'Osaka', collecting: true }), node({ panel_id: 3, agent_id: 'a3', panel_name: 'Seoul' })]))
    sheet()
    const box = await screen.findByTestId('recording-note')
    expect(box.textContent).toBe('2 台只记录规则命中（保留 30 天）· 原始访问日志不离开节点')
  })

  it('no node recording is said, not drawn as 0', async () => {
    sheet()
    expect((await screen.findByTestId('recording-note')).textContent).toBe('没有节点在记录命中 · 原始访问日志不离开节点')
  })

  it('every native row has "set the recording" in its menu; a 3X-UI row does not', async () => {
    vi.mocked(api.getDestStatus).mockResolvedValue(status([rec(), node({ panel_id: 3, agent_id: 'a3', panel_name: 'US A', kind: 'x-ui', state: 'unsupported_kind' })]))
    sheet()
    fireEvent.click(await screen.findByRole('button', { name: 'Tokyo 01 的更多操作' }))
    expect(await screen.findByRole('menuitem', { name: '设置记录方式…' })).toBeTruthy()
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    fireEvent.click(screen.getByRole('button', { name: 'US A 的更多操作' }))
    expect(screen.queryByRole('menuitem', { name: '设置记录方式…' })).toBeNull()
  })

  it('opens the recording dialog on that node and saves its mode', async () => {
    vi.mocked(api.getDestStatus).mockResolvedValue(status([rec()]))
    vi.mocked(api.setAuditCollect).mockResolvedValue()
    sheet()
    fireEvent.click(await screen.findByRole('button', { name: 'Tokyo 01 的更多操作' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '设置记录方式…' }))
    const dialog = (await screen.findAllByRole('dialog')).find(d => d.textContent?.includes('记录什么'))!
    fireEvent.click(within(dialog).getByRole('radio', { name: /不记录/ }))
    fireEvent.click(within(dialog).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.setAuditCollect).toHaveBeenCalledWith(1, 'off'))
  })

  it('every native row has "set the hostname matching" in its menu, opening that dialog and saving', async () => {
    vi.mocked(api.getDestStatus).mockResolvedValue(status([rec(), node({ panel_id: 3, agent_id: 'a3', panel_name: 'US A', kind: 'x-ui', state: 'unsupported_kind' })]))
    vi.mocked(api.setDestResolve).mockResolvedValue()
    sheet()
    fireEvent.click(await screen.findByRole('button', { name: 'US A 的更多操作' }))
    expect(screen.queryByRole('menuitem', { name: '设置主机名匹配方式…' })).toBeNull()
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    fireEvent.click(screen.getByRole('button', { name: 'Tokyo 01 的更多操作' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '设置主机名匹配方式…' }))
    const dialog = (await screen.findAllByRole('dialog')).find(d => d.textContent?.includes('IP 规则对主机名的匹配方式'))!
    fireEvent.click(within(dialog).getByRole('radio', { name: /按需解析：/ }))
    fireEvent.click(within(dialog).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.setDestResolve).toHaveBeenCalledWith(1, 'on_demand'))
  })

  it('a row on demand says so, and a node that stayed as-is says why', async () => {
    vi.mocked(api.getDestStatus).mockResolvedValue(status([
      rec({ resolve: 'on_demand', resolve_effective: 'on_demand', resolve_unavailable: null }),
      rec({ panel_id: 2, agent_id: 'a2', panel_name: 'Osaka', resolve: 'on_demand', resolve_effective: '', resolve_unavailable: 'version' }),
      rec({ panel_id: 3, agent_id: 'a3', panel_name: 'Seoul', resolve: 'on_demand', resolve_effective: '', resolve_unavailable: 'route_only' }),
      rec({ panel_id: 4, agent_id: 'a4', panel_name: 'Lima', resolve: 'as_is', resolve_effective: '', resolve_unavailable: null }),
    ]))
    sheet()
    await screen.findByText('Seoul')
    const lines = screen.getAllByTestId('node-resolve').map(e => e.textContent)
    expect(lines).toEqual([
      'IP 规则对主机名：按需解析',
      'IP 规则对主机名：按原样',
      'IP 规则对主机名：按原样',
    ])
    expect(screen.getByText('节点版本不支持，按原样执行')).toBeTruthy()
    expect(screen.getByText('入站 routeOnly 开启，按原样执行')).toBeTruthy()
  })

  it('the dialog opens above the drawer: it is a modal, the drawer is not raised', async () => {
    vi.mocked(api.getDestStatus).mockResolvedValue(status([rec()]))
    sheet()
    fireEvent.click(await screen.findByRole('button', { name: 'Tokyo 01 的更多操作' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '设置记录方式…' }))
    const dialog = (await screen.findAllByRole('dialog')).find(d => d.textContent?.includes('记录什么'))!
    const z = (el: Element | null) => { while (el) { const v = Number(getComputedStyle(el).zIndex); if (v) return v; el = el.parentElement } return 0 }
    const drawer = document.querySelector('.MuiDrawer-root')
    expect(z(dialog.closest('.MuiDialog-root'))).toBeGreaterThan(z(drawer))
  })
})
