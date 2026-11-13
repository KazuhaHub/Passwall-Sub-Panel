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
  vi.mocked(api.getDestSettings).mockResolvedValue({ settings: {}, defaults: {}, effective: { dest_policy_apply_min_seconds: 45, dest_list_refresh_hours: 24, dest_rules_update_mode: 'manual' } } as unknown as api.DestSettingsView)
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
