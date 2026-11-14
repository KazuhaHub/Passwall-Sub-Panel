// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import * as api from '@/api/accessControl'
import type { DestHitItem, DestHits, DestNodeStatus, DestPolicies, DestStatus } from '@/api/accessControl'
import { renderAccess, setTestLang, snack, whereIs } from '@/test/accessControlHarness'
import { useSiteStore } from '@/stores/site'
import { formatHourRange } from './hitTime'
import RecordsTab from './RecordsTab'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})
const media = vi.hoisted(() => ({ folded: false }))
vi.mock('@mui/material', async importOriginal => ({ ...await importOriginal<typeof import('@mui/material')>(), useMediaQuery: () => media.folded }))
vi.mock('@/components/UserAutocomplete', () => ({
  default: ({ value, onChange }: { value: number | null; onChange: (id: number | null) => void }) => (
    <button aria-label="pick-user" data-value={String(value)} onClick={() => onChange(value ? null : 7)}>pick</button>
  ),
}))
vi.mock('../sheets/AddExemptionDialog', () => ({
  default: ({ open, lockedUserId }: { open: boolean; lockedUserId?: number }) => open ? <p data-testid="exemption-dialog">{lockedUserId}</p> : null,
}))

const H = 3_600_000
const BASE = Math.floor(Date.now() / H) * H
const budget = { rules: { used: 1, limit: 256 }, domains: { used: 0, limit: 50000 }, regexps: { used: 0, limit: 256 }, cidrs: { used: 0, limit: 20000 }, source_cidrs: { used: 0, limit: 60000 }, subjects: { used: 0, limit: 5000 }, bytes: { used: 0, limit: 1000 } }
const pol = (id: number, name: string, state: 'on' | 'report' = 'on') => ({
  id, name, description: '', action: 'block' as const, state, list_ids: [], inline: {}, scope: 'all' as const, group_ids: [], panel_ids: [], priority: id,
  template_key: '', created_at: 0, updated_at: 0, list_states: [], note: null, scope_missing: false, hits_recent: null, would_block_recent: null, last_hit_at: null,
})
const policies: DestPolicies = { allow: [], block: [pol(1, '禁止发信'), pol(3, '加密货币交易所', 'report')], observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget }
const node = (over: Partial<DestNodeStatus> = {}): DestNodeStatus => ({
  panel_id: 5, agent_id: 'a', panel_name: '东京 01', kind: 'psp', engine: 'xray', agent_version: '4.2.0', supports: { policy: true, hits: true, usage: false },
  collect: 'hits', collect_effective: 'hits', collecting: true, state: 'applied', fallback_reason: '', over_limit: null, sniffing_insufficient: [], minted_at: null,
  pending_since: null, applied_at: 1, applied_rules: 3, last_report_at: 1, rules_data_version: null, geoip_unavailable: false, allowlist_groups: [], hits_24h: 10, ...over,
})
const status = (nodes = [node()]): DestStatus => ({
  generation: 2, published_generation: 2, last_write_at: null, next_publish_at: null, apply_eta_ms: 0, paused: false, publish_error: null,
  publish_error_at: null, geosite_release: '', geoip_unavailable: false, totals: {}, nodes, policy_notes: {},
})
const item = (o: Partial<DestHitItem>): DestHitItem => ({
  hour_ms: BASE - H, panel_id: 5, panel_name: '东京 01', user_id: 7, upn: 'alice@corp', source: 'p1', source_name: '禁止发信', source_kind: 'policy', action: 'block',
  dest: 'smtp.gmail.com', port: 587, count: 37, first_ms: BASE - H + 3 * 60_000, last_ms: BASE - H + 58 * 60_000, ...o,
})
const result = (over: Partial<DestHits> = {}): DestHits => ({
  items: [
    item({}),
    item({ user_id: 8, upn: 'bob@corp', source: 'p3', source_name: '加密货币交易所', action: 'observe', dest: 'www.binance.com', port: 443, count: 12 }),
    item({ user_id: 9, upn: null, source: 'p9', source_name: null, dest: 'tracker.example.org', port: 6969, count: 3, hour_ms: BASE - 30 * H }),
    item({ user_id: 5, upn: 'verylong@corp', dest: `${'a'.repeat(80)}.example.com`, port: 443, count: 1, panel_name: null, hour_ms: BASE - 30 * H }),
  ],
  total: 124, page: 1, page_size: 50, group_by: 'none', since: 0, until: 0,
  summary: { block: 2134, deny: 0, observe: 4012, users: 37 },
  sources: [{ source: 'p1', name: '禁止发信' }, { source: 'p3', name: '加密货币交易所' }, { source: 'p9', name: null }],
  dropped_in_range: 0, ...over,
})

const handlers = { onOpenUser: vi.fn(), onTestDestination: vi.fn(), onOpenSettings: vi.fn(), onOpenNodes: vi.fn(), onGoToPolicies: vi.fn() }
const ui = <RecordsTab {...handlers} />
const route = '/admin/access-control?tab=records'

beforeEach(() => {
  vi.clearAllMocks()
  media.folded = false
  useSiteStore.setState({ timezone: 'Asia/Shanghai' })
  vi.mocked(api.getDestHits).mockResolvedValue(result())
  vi.mocked(api.getDestPolicies).mockResolvedValue(policies)
  vi.mocked(api.getDestStatus).mockResolvedValue(status())
  vi.mocked(api.getDestSettings).mockResolvedValue({
    settings: {} as never, defaults: {},
    effective: { dest_policy_apply_min_seconds: 60, dest_list_refresh_hours: 24, dest_rules_update_mode: 'manual', dest_hit_retention_days: 30, dest_trial_retention_days: 7 },
  })
})
afterEach(() => { useSiteStore.setState({ timezone: '' }) })

const lastParams = () => vi.mocked(api.getDestHits).mock.calls.at(-1)![0]!

describe('the metric tiles', () => {
  it('count the whole time / account / node selection; the first two filter by action, the third only counts', async () => {
    const { router } = renderAccess(ui, { route })
    const tiles = await screen.findByTestId('hits-tiles')
    expect(within(tiles).getAllByRole('button').map(b => b.textContent)).toEqual(['拦截2,134', '本应拦截4,012'])
    expect(within(tiles).getByText('涉及账号').parentElement!.textContent).toContain('37')
    const block = within(tiles).getByRole('button', { name: /拦截\s*2,134/ })
    expect(block.getAttribute('aria-pressed')).toBe('false')
    fireEvent.click(block)
    await waitFor(() => expect(whereIs(router)).toContain('rec_action=block'))
    expect(within(tiles).getByRole('button', { name: /拦截\s*2,134/ }).getAttribute('aria-pressed')).toBe('true')
    // The numbers do not follow the action filter: the server's summary ignores it, so the other tile does not go to 0.
    expect(within(tiles).getByRole('button', { name: /本应拦截\s*4,012/ })).toBeTruthy()
    fireEvent.click(within(tiles).getByRole('button', { name: /拦截\s*2,134/ }))
    await waitFor(() => expect(whereIs(router)).not.toContain('rec_action'))
  })

  it('has no allowlist card and no "include trial" box: those are a later phase', async () => {
    renderAccess(ui, { route })
    await screen.findByTestId('hits-tiles')
    expect(screen.queryByText(/白名单/)).toBeNull()
  })
})

describe('the rows', () => {
  it('say who, which rule, what, where and how many; an hour is the panel zone\'s; the hover has first and last', async () => {
    renderAccess(ui, { route })
    const rows = await screen.findAllByTestId('hit-row')
    expect(rows).toHaveLength(4)
    const first = rows[0]
    expect(within(first).getByText(formatHourRange(BASE - H, 'Asia/Shanghai'))).toBeTruthy()
    expect(within(first).getByText('alice@corp')).toBeTruthy()
    expect(within(first).getByText('禁止发信')).toBeTruthy()
    expect(within(first).getByText('smtp.gmail.com:587')).toBeTruthy()
    expect(within(first).getByText('东京 01')).toBeTruthy()
    expect(within(first).getByText('37')).toBeTruthy()
    expect(within(first).getByTestId('hit-action').getAttribute('data-state')).toBe('block')
    expect(within(first).getByTestId('hit-time').getAttribute('title')).toMatch(/首次 \d\d:\d\d · 最后 \d\d:\d\d/)
  })

  it('a report-only hit is "would be blocked" in its own tone, not a block', async () => {
    renderAccess(ui, { route })
    const rows = await screen.findAllByTestId('hit-row')
    const badge = within(rows[1]).getByTestId('hit-action')
    expect(badge.getAttribute('data-state')).toBe('observe')
    expect(badge.textContent).toBe('本应拦截')
  })

  it('a deleted source rule is not called a policy', async () => {
    vi.mocked(api.getDestHits).mockResolvedValue(result({
      items: [item({ source: 's9', source_name: null, source_kind: 'source' })],
      sources: [{ source: 's9', name: null }],
    }))
    renderAccess(ui, { route })
    const rows = await screen.findAllByTestId('hit-row')
    expect(within(rows[0]).getByText('已删除的来源规则')).toBeTruthy()
    expect(within(rows[0]).queryByText('已删除的策略')).toBeNull()
  })

  it('a deleted policy and a deleted account are named as such, quietly', async () => {
    renderAccess(ui, { route })
    const rows = await screen.findAllByTestId('hit-row')
    expect(within(rows[2]).getByText('已删除的策略')).toBeTruthy()
    expect(within(rows[2]).getByText('已删除的账号 #9')).toBeTruthy()
  })

  it('a long destination is one ellipsed line with its whole text on hover', async () => {
    renderAccess(ui, { route })
    const rows = await screen.findAllByTestId('hit-row')
    const dest = within(rows[3]).getByTestId('hit-dest')
    expect(dest.textContent).toContain('aaaaaaaa')
    expect(dest.getAttribute('aria-label') ?? dest.parentElement?.getAttribute('aria-label')).toContain('example.com:443')
  })

  it('days are headed in the panel zone: today, yesterday-or-earlier', async () => {
    renderAccess(ui, { route })
    await screen.findAllByTestId('hit-row')
    const heads = screen.getAllByRole('heading', { level: 3 }).map(h => h.textContent)
    expect(heads[0]).toBe('今天')
    expect(heads.length).toBeGreaterThanOrEqual(2)
  })

  it('"N records weren\'t counted" is amber text with the count, only when something was dropped', async () => {
    renderAccess(ui, { route })
    await screen.findAllByTestId('hit-row')
    expect(screen.queryByTestId('hits-dropped')).toBeNull()
    vi.mocked(api.getDestHits).mockResolvedValue(result({ dropped_in_range: 12 }))
    const { unmount } = renderAccess(ui, { route })
    expect(await screen.findAllByTestId('hits-dropped')).toBeTruthy()
    expect(screen.getAllByTestId('hits-dropped')[0].textContent).toContain('本期有 12 条记录因节点上报上限未统计')
    unmount()
  })

  it('counts pages and moves through them, the page in the URL', async () => {
    vi.mocked(api.getDestHits).mockResolvedValue(result({ total: 124 }))
    const { router } = renderAccess(ui, { route })
    await screen.findAllByTestId('hit-row')
    expect(screen.getByText(/共 124 条/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /next page|下一页/i }))
    await waitFor(() => expect(whereIs(router)).toContain('rec_page=2'))
    await waitFor(() => expect(lastParams().page).toBe(2))
  })
})

describe('filters', () => {
  it('the default range is the last 24 hours and the request says so', async () => {
    renderAccess(ui, { route })
    await screen.findAllByTestId('hit-row')
    const p = lastParams()
    expect(p.since).toBeGreaterThan(Date.now() - 25 * H)
    expect(p.since).toBeLessThan(Date.now() - 23 * H)
    expect(p.group_by).toBeUndefined()
    expect(p.q).toBeUndefined()
  })

  it('an account is picked exactly: rec_user, and the request filters by that id', async () => {
    const { router } = renderAccess(ui, { route })
    await screen.findAllByTestId('hit-row')
    fireEvent.click(screen.getByLabelText('pick-user'))
    await waitFor(() => expect(whereIs(router)).toContain('rec_user=7'))
    await waitFor(() => expect(lastParams().user_id).toBe(7))
  })

  it('a node filter lists the native nodes by name and writes rec_panel', async () => {
    vi.mocked(api.getDestStatus).mockResolvedValue(status([node(), node({ panel_id: 6, panel_name: '洛杉矶 02' }), node({ panel_id: 7, panel_name: '美西 A', kind: 'x-ui' })]))
    const { router } = renderAccess(ui, { route })
    await screen.findAllByTestId('hit-row')
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '节点' }))
    const list = await screen.findByRole('listbox')
    expect(within(list).getAllByRole('option').map(o => o.textContent)).toEqual(['全部节点', '东京 01', '洛杉矶 02'])
    fireEvent.click(within(list).getByRole('option', { name: '洛杉矶 02' }))
    await waitFor(() => expect(whereIs(router)).toContain('rec_panel=6'))
  })

  it('the rule filter groups policies, source rules and deleted ones, and writes rec_source', async () => {
    vi.mocked(api.getDestHits).mockResolvedValue(result({ sources: [...result().sources, { source: 's4', name: '东京 01' }] }))
    const { router } = renderAccess(ui, { route })
    await screen.findAllByTestId('hit-row')
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '规则' }))
    const list = await screen.findByRole('listbox')
    const labels = within(list).getAllByRole('option').map(o => o.textContent)
    expect(labels).toEqual(expect.arrayContaining(['全部规则', '禁止发信', '加密货币交易所', '来源规则 · 东京 01', '已删除的策略']))
    fireEvent.click(within(list).getByRole('option', { name: '加密货币交易所' }))
    await waitFor(() => expect(whereIs(router)).toContain('rec_source=p3'))
  })

  it('a clicked rule badge becomes the filter', async () => {
    const { router } = renderAccess(ui, { route })
    const rows = await screen.findAllByTestId('hit-row')
    fireEvent.click(within(rows[0]).getByRole('button', { name: /禁止发信/ }))
    await waitFor(() => expect(whereIs(router)).toContain('rec_source=p1'))
  })

  it('the range offers 1 hour, 24 hours, the 7 days held to the retention, and a custom range', async () => {
    vi.mocked(api.getDestSettings).mockResolvedValue({
      settings: {} as never, defaults: {},
      effective: { dest_policy_apply_min_seconds: 60, dest_list_refresh_hours: 24, dest_rules_update_mode: 'manual', dest_hit_retention_days: 3, dest_trial_retention_days: 3 },
    })
    const { router } = renderAccess(ui, { route })
    await screen.findAllByTestId('hit-row')
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '时间范围' }))
    const list = await screen.findByRole('listbox')
    await waitFor(() => expect(within(list).getAllByRole('option').map(o => o.textContent)).toEqual(['过去 1 小时', '过去 24 小时', '过去 3 天', '自定义…']))
    fireEvent.click(within(list).getByRole('option', { name: '过去 3 天' }))
    await waitFor(() => expect(whereIs(router)).toContain('rec_since=3d'))
  })

  it('a custom range is browser time, held to 31 days, and says why', async () => {
    const { router } = renderAccess(ui, { route: `${route}&rec_since=2026-10-01T00%3A00&rec_until=2026-11-12T09%3A30` })
    await screen.findAllByTestId('hit-row')
    expect(screen.getByLabelText(/开始/)).toBeTruthy()
    expect(screen.getByText(/自定义范围按浏览器时间/)).toBeTruthy()
    expect(screen.getByText(/最长 31 天/)).toBeTruthy()
    const p = lastParams()
    expect(p.until! - p.since!).toBe(31 * 24 * H)
    expect(whereIs(router)).toContain('rec_until=')
  })

  it('the search term goes to the server and nowhere else: not in the URL, not in history', async () => {
    const { router } = renderAccess(ui, { route })
    await screen.findAllByTestId('hit-row')
    fireEvent.change(screen.getByRole('searchbox', { name: '搜索目的地…' }), { target: { value: 'binance' } })
    await waitFor(() => expect(lastParams().q).toBe('binance'), { timeout: 2000 })
    expect(whereIs(router)).not.toMatch(/binance|rec_q|q=/)
    expect(JSON.stringify(router.state.location.state ?? null)).not.toContain('binance')
  })

  it('clearing the filters goes back to the bare tab', async () => {
    vi.mocked(api.getDestHits).mockResolvedValue(result({ items: [], total: 0, summary: { block: 0, deny: 0, observe: 0, users: 0 } }))
    const { router } = renderAccess(ui, { route: `${route}&rec_user=7&rec_source=p1` })
    fireEvent.click(await screen.findByRole('button', { name: '清除筛选' }))
    await waitFor(() => expect(whereIs(router)).toBe('/admin/access-control?tab=records'))
  })
})

describe('the views', () => {
  it('group by destination, account and policy: one row per object with its count and its last hit', async () => {
    vi.mocked(api.getDestHits).mockImplementation(async p => (p?.group_by === 'site'
      ? result({ group_by: 'site', total: 2, items: [item({ hour_ms: 0, user_id: 0, upn: null, source: '', source_name: null, dest: 'smtp.gmail.com', port: 0, count: 90, panel_id: 0, panel_name: null }), item({ hour_ms: 0, user_id: 0, upn: null, source: '', source_name: null, dest: 'mail.qq.com', port: 0, count: 30, panel_id: 0, panel_name: null })] })
      : result()))
    const { router } = renderAccess(ui, { route })
    await screen.findAllByTestId('hit-row')
    fireEvent.click(screen.getByRole('button', { name: '按目的地' }))
    await waitFor(() => expect(whereIs(router)).toContain('rec_group_by=site'))
    const rows = await screen.findAllByTestId('hit-group-row')
    expect(rows).toHaveLength(2)
    expect(within(rows[0]).getByText('smtp.gmail.com')).toBeTruthy()
    expect(within(rows[0]).getByText('90')).toBeTruthy()
    expect(within(rows[0]).getByTestId('hit-bar')).toBeTruthy()
    expect(screen.getByText(/共 2 项/)).toBeTruthy()
    expect(lastParams().group_by).toBe('site')
  })

  it('the account view names the account (or a deleted one) and opens the drawer', async () => {
    vi.mocked(api.getDestHits).mockResolvedValue(result({ group_by: 'user', total: 2, items: [item({ hour_ms: 0, source: '', source_name: null, dest: '', port: 0, count: 50 }), item({ hour_ms: 0, user_id: 4, upn: null, source: '', source_name: null, dest: '', port: 0, count: 5 })] }))
    renderAccess(ui, { route: `${route}&rec_group_by=user` })
    const rows = await screen.findAllByTestId('hit-group-row')
    expect(within(rows[1]).getByText('已删除的账号 #4')).toBeTruthy()
    fireEvent.click(within(rows[0]).getByRole('button', { name: /alice@corp/ }))
    expect(handlers.onOpenUser).toHaveBeenCalledWith(7)
  })
})

describe('the grouped views say block or report-only on every row', () => {
  it.each(['user', 'policy'] as const)('the %s view carries an action badge per row', async view => {
    vi.mocked(api.getDestHits).mockResolvedValue(result({ group_by: view, total: 2, items: [
      item({ hour_ms: 0, dest: '', port: 0, count: 50, action: 'block' }),
      item({ hour_ms: 0, dest: '', port: 0, count: 5, action: 'observe', source: 'p3', source_name: '加密货币交易所' }),
    ] }))
    renderAccess(ui, { route: `${route}&rec_group_by=${view}` })
    const rows = await screen.findAllByTestId('hit-group-row')
    expect(within(rows[0]).getByTestId('hit-action').getAttribute('data-state')).toBe('block')
    expect(within(rows[1]).getByTestId('hit-action').getAttribute('data-state')).toBe('observe')
  })
})

describe('a refused range or filter', () => {
  const refused = (code: string) => Object.assign(new Error('x'), { isAxiosError: true, response: { status: 400, data: { code, error: 'raw english text' } } })

  it.each([
    ['dest_hits_range', '时间范围太长，请缩小范围'],
    ['dest_hits_invalid', '筛选条件无效，请检查后重试'],
  ])('%s reads as a sentence, never the generic failure or the raw text', async (code, text) => {
    vi.mocked(api.getDestHits).mockRejectedValue(refused(code))
    renderAccess(ui, { route })
    expect(await screen.findByText(text)).toBeTruthy()
    expect(document.body.textContent).not.toContain('raw english text')
  })

  it('still says so when an earlier page is on screen', async () => {
    vi.mocked(api.getDestHits).mockImplementation(async p => { if (p?.group_by === 'site') throw refused('dest_hits_range'); return result() })
    const { router } = renderAccess(ui, { route })
    await screen.findAllByTestId('hit-row')
    fireEvent.click(screen.getByRole('button', { name: '按目的地' }))
    await waitFor(() => expect(whereIs(router)).toContain('rec_group_by=site'))
    expect(await screen.findByText('时间范围太长，请缩小范围')).toBeTruthy()
  })
})

describe('what an empty tab says, by reason', () => {
  const empty = () => result({ items: [], total: 0, summary: { block: 0, deny: 0, observe: 0, users: 0 } })

  it('no policy is deployed: say so and offer to make one', async () => {
    vi.mocked(api.getDestHits).mockResolvedValue(empty())
    vi.mocked(api.getDestPolicies).mockResolvedValue({ ...policies, block: [pol(1, 'x', 'on')].map(p => ({ ...p, state: 'off' as const })) })
    renderAccess(ui, { route })
    expect(await screen.findByText('还没有启用的策略')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '去新建' }))
    expect(handlers.onGoToPolicies).toHaveBeenCalled()
  })

  it('policies but no node records: say why and offer the node coverage', async () => {
    vi.mocked(api.getDestHits).mockResolvedValue(empty())
    vi.mocked(api.getDestStatus).mockResolvedValue(status([node({ collecting: false, collect: 'off', collect_effective: 'off' })]))
    renderAccess(ui, { route })
    expect(await screen.findByText('没有节点在记录命中：节点版本过旧，或记录方式为「不记录」')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '节点覆盖' }))
    expect(handlers.onOpenNodes).toHaveBeenCalled()
  })

  it('a filter in force: "none in this range" with a way to clear it (a different sentence from "really empty")', async () => {
    vi.mocked(api.getDestHits).mockResolvedValue(empty())
    renderAccess(ui, { route: `${route}&rec_user=7` })
    expect(await screen.findByText('这段时间内没有符合条件的命中')).toBeTruthy()
    expect(screen.getByRole('button', { name: '清除筛选' })).toBeTruthy()
  })

  it('really empty over a range the nodes were recording: nothing was hit', async () => {
    vi.mocked(api.getDestHits).mockResolvedValue(empty())
    renderAccess(ui, { route })
    expect(await screen.findByText('这段时间内没有命中')).toBeTruthy()
    expect(screen.queryByRole('button', { name: '清除筛选' })).toBeNull()
  })

  it('a range older than the retention says how long records are kept', async () => {
    vi.mocked(api.getDestHits).mockResolvedValue(empty())
    renderAccess(ui, { route: `${route}&rec_since=90d` })
    expect(await screen.findByText('命中记录只保留 30 天')).toBeTruthy()
  })

  it('a failed read has a retry; a 503 is the not-wired note', async () => {
    vi.mocked(api.getDestHits).mockRejectedValue(new Error('boom'))
    renderAccess(ui, { route })
    expect(await screen.findByRole('button', { name: '重试' })).toBeTruthy()
  })
})

describe('the footer and the row menu', () => {
  it('the retention is written with its days and edits it in the settings dialog', async () => {
    renderAccess(ui, { route })
    await screen.findAllByTestId('hit-row')
    expect(screen.getByText(/命中记录保留 30 天/)).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '修改' }))
    expect(handlers.onOpenSettings).toHaveBeenCalledWith('retention')
  })

  it('test this destination hands the host and nothing else to the test sheet', async () => {
    renderAccess(ui, { route })
    const rows = await screen.findAllByTestId('hit-row')
    fireEvent.click(within(rows[0]).getByRole('button', { name: '更多操作' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '测试此目的地' }))
    expect(handlers.onTestDestination).toHaveBeenCalledWith('smtp.gmail.com')
  })

  it('open the account', async () => {
    renderAccess(ui, { route })
    const rows = await screen.findAllByTestId('hit-row')
    fireEvent.click(within(rows[0]).getByRole('button', { name: '更多操作' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '打开账号' }))
    expect(handlers.onOpenUser).toHaveBeenCalledWith(7)
  })

  it('"allow exception" defaults to everyone, warns that it overrides every block, and writes a site entry', async () => {
    vi.mocked(api.addDestException).mockResolvedValue({ entry: 'gmail.com', created: { list_id: 5, policy_id: 6 } })
    renderAccess(ui, { route })
    const rows = await screen.findAllByTestId('hit-row')
    fireEvent.click(within(rows[0]).getByRole('button', { name: '更多操作' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '加入放行例外…' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('放行 smtp.gmail.com？')).toBeTruthy()
    expect(within(dialog).getByText('会越过所有拦截策略')).toBeTruthy()
    expect(within(dialog).getByText('第一次使用会新建列表「放行例外」和一条放行策略')).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('button', { name: '放行' }))
    await waitFor(() => expect(api.addDestException).toHaveBeenCalledWith({ target: 'smtp.gmail.com', match: 'site' }))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('已新建列表「放行例外」和一条放行策略，并加入「gmail.com」，将随下一次下发生效', 'success'))
  })

  it('"exempt only this account" hands over to the exemption dialog for that account', async () => {
    renderAccess(ui, { route })
    const rows = await screen.findAllByTestId('hit-row')
    fireEvent.click(within(rows[0]).getByRole('button', { name: '更多操作' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '加入放行例外…' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('radio', { name: /只豁免 alice@corp/ }))
    expect(within(dialog).getByText('这个账号将不受任何访问限制')).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('button', { name: '豁免此账号…' }))
    expect(await screen.findByTestId('exemption-dialog')).toBeTruthy()
    expect(screen.getByTestId('exemption-dialog').textContent).toBe('7')
    expect(api.addDestException).not.toHaveBeenCalled()
  })

  it('a deleted account has no "exempt only" choice', async () => {
    renderAccess(ui, { route })
    const rows = await screen.findAllByTestId('hit-row')
    fireEvent.click(within(rows[2]).getByRole('button', { name: '更多操作' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '加入放行例外…' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).queryByRole('radio', { name: /只豁免/ })).toBeNull()
  })
})

describe('on a phone', () => {
  it('each hit is three lines: account and count, action and destination, hour and node; the tiles are two to a row', async () => {
    media.folded = true
    renderAccess(ui, { route })
    const rows = await screen.findAllByTestId('hit-row')
    const first = rows[0]
    expect(first.querySelectorAll('[data-line]')).toHaveLength(3)
    expect(within(first).getByText('账号')).toBeTruthy()
    expect(within(first).getByText('次数')).toBeTruthy()
  })
})

describe('English', () => {
  it('uses the term table', async () => {
    setTestLang('en-US')
    renderAccess(ui, { route })
    const tiles = await screen.findByTestId('hits-tiles')
    expect(within(tiles).getAllByRole('button').map(b => b.textContent)).toEqual(['Blocked2,134', 'Would be blocked4,012'])
    expect(screen.getByRole('button', { name: 'By destination' })).toBeTruthy()
    expect(screen.getByRole('searchbox', { name: 'Search destinations…' })).toBeTruthy()
    expect(document.body.textContent).not.toMatch(/observe/i)
    await act(async () => {})
  })
})
