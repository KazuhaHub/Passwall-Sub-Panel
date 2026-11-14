// @vitest-environment jsdom
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { confirmMock, renderAccess, setTestLang, translate, whereIs } from '@/test/accessControlHarness'
import type { DestListsView, DestNodeStatus, DestPolicies, DestStatus } from '@/api/accessControl'
import * as api from '@/api/accessControl'
import AccessControlView from './AccessControlView'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})
// The tabs and sheets are other tests' business; the page is tested for what it owns.
vi.mock('./policies/PoliciesTab', () => ({ default: () => <p data-testid="policies-tab" /> }))
vi.mock('./lists/ListsTab', () => ({ default: () => <p data-testid="lists-tab" /> }))
vi.mock('./records/RecordsTab', () => ({
  default: (p: { onOpenUser: (id: number) => void; onTestDestination: (h: string) => void; onOpenSettings: (f?: string) => void; onOpenNodes: () => void; onGoToPolicies: () => void }) => (
    <div data-testid="records-tab">
      <button onClick={() => p.onOpenUser(7)}>open-user</button>
      <button onClick={() => p.onTestDestination('smtp.gmail.com')}>test-dest</button>
      <button onClick={() => p.onOpenSettings('retention')}>retention</button>
      <button onClick={() => p.onOpenNodes()}>nodes</button>
      <button onClick={() => p.onGoToPolicies()}>policies</button>
    </div>
  ),
}))
vi.mock('./sheets/TestSheet', () => ({ default: ({ open }: { open: boolean }) => open ? <p data-testid="test-sheet" /> : null }))
vi.mock('./sheets/CoverageSheet', () => ({ default: ({ open }: { open: boolean }) => open ? <p data-testid="coverage-sheet" /> : null }))
vi.mock('./sheets/ExemptionsSheet', () => ({ default: ({ open }: { open: boolean }) => open ? <p data-testid="exemptions-sheet" /> : null }))
vi.mock('./lists/ListEntriesSheet', () => ({ default: ({ listId, onEdit }: { listId: number | null; onEdit: (id: number) => void }) => listId ? <button data-testid="entries-sheet" onClick={() => onEdit(listId)}>{listId}</button> : null }))
vi.mock('./AccessSettingsDialog', () => ({ default: ({ open }: { open: boolean }) => open ? <p data-testid="settings-dialog" /> : null }))
vi.mock('./AccountDrawerHost', () => ({ default: ({ userId }: { userId: number | null }) => userId ? <p data-testid="account-drawer">{userId}</p> : null }))
vi.mock('@/components/UserAutocomplete', () => ({ default: ({ fullWidth }: { fullWidth?: boolean }) => <input aria-label="pick" data-fullwidth={String(!!fullWidth)} /> }))

const node = (over: Partial<DestNodeStatus> = {}): DestNodeStatus => ({
  panel_id: 1, agent_id: 'a1', panel_name: 'Tokyo 01', kind: 'psp', engine: 'xray', agent_version: '4.1.2',
  supports: { policy: true, hits: false, usage: false }, collect: '', collect_effective: '', collecting: false, state: 'applied',
  fallback_reason: '', over_limit: null, sniffing_insufficient: [], minted_at: null, pending_since: null, applied_at: 1, applied_rules: 3,
  last_report_at: 1, rules_data_version: null, geoip_unavailable: false, allowlist_groups: [], hits_24h: null, ...over,
})
const status = (over: Partial<DestStatus> = {}): DestStatus => ({
  generation: 2, published_generation: 2, last_write_at: null, next_publish_at: null, apply_eta_ms: 0, paused: false, publish_error: null,
  publish_error_at: null, geosite_release: '', geoip_unavailable: false, totals: {}, nodes: [node()], policy_notes: {}, ...over,
})
const budget = { rules: { used: 1, limit: 256 }, domains: { used: 0, limit: 50000 }, regexps: { used: 0, limit: 256 }, cidrs: { used: 0, limit: 20000 }, source_cidrs: { used: 0, limit: 60000 }, subjects: { used: 0, limit: 5000 }, bytes: { used: 0, limit: 1000 } }
const policies: DestPolicies = {
  allow: [], observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget,
  block: [{ id: 1, name: 'No mail', description: '', action: 'block', state: 'on', list_ids: [], inline: { ports: '25' }, scope: 'all', group_ids: [], panel_ids: [], priority: 1, template_key: '', created_at: 0, updated_at: 0, list_states: [], note: null, scope_missing: false, hits_recent: null, would_block_recent: null, last_hit_at: null }],
}
const lists: DestListsView = { items: [], refresh_hours: 24, budget }

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestStatus).mockResolvedValue(status())
  vi.mocked(api.getDestPolicies).mockResolvedValue(policies)
  vi.mocked(api.getDestLists).mockResolvedValue(lists)
  vi.mocked(api.getDestSourceRules).mockResolvedValue({ items: [], geoip_available: true })
  vi.mocked(api.getDestSettings).mockResolvedValue({ settings: { dest_policy_apply_min_seconds: 0, dest_list_refresh_hours: 0, dest_rules_update_mode: '', dest_hit_retention_days: 0, dest_trial_retention_days: 0 }, defaults: {}, effective: { dest_policy_apply_min_seconds: 60, dest_list_refresh_hours: 24, dest_rules_update_mode: 'manual', dest_hit_retention_days: 30, dest_trial_retention_days: 7 } })
})

describe('the admin-only gates', () => {
  // Gate 4 of 4 on the page itself: after its hooks, the page checks the
  // capability again and bounces anyone without it, asking no adminGroup read.
  it('sends an operator to the dashboard and reads nothing', async () => {
    renderAccess(<AccessControlView />, { role: 'operator' })
    expect(await screen.findByTestId('dashboard')).toBeTruthy()
    expect(api.getDestStatus).not.toHaveBeenCalled()
    expect(api.getDestPolicies).not.toHaveBeenCalled()
    expect(api.getDestLists).not.toHaveBeenCalled()
  })
})

describe('the page', () => {
  it('shows the title and the verdict, and mounts only the open tab', async () => {
    renderAccess(<AccessControlView />)
    expect(await screen.findByText('访问控制', { selector: 'h4' })).toBeTruthy()
    expect(await screen.findByText('策略在 1 台节点上生效')).toBeTruthy()
    expect(screen.getByTestId('policies-tab')).toBeTruthy()
    expect(screen.queryByTestId('lists-tab')).toBeNull()
  })

  it('switching tabs REPLACES the tab param, and a later phase\'s tab reads as the policies tab without being rewritten', async () => {
    const { router } = renderAccess(<AccessControlView />, { route: '/admin/access-control?tab=allowlist' })
    expect(await screen.findByTestId('policies-tab')).toBeTruthy()
    expect(whereIs(router)).toBe('/admin/access-control?tab=allowlist')
    fireEvent.click(screen.getByRole('tab', { name: '列表' }))
    expect(await screen.findByTestId('lists-tab')).toBeTruthy()
    expect(whereIs(router)).toBe('/admin/access-control?tab=lists')
    expect(router.state.historyAction).toBe('REPLACE')
  })

  it('offers policies, lists and records, no tab for a later phase, and names a tunable only through its setting', async () => {
    renderAccess(<AccessControlView />)
    await screen.findByTestId('policies-tab')
    expect(screen.getAllByRole('tab').map(t => t.textContent)).toEqual(['策略', '列表', '记录'])
    fireEvent.click(screen.getByRole('button', { name: '说明' }))
    // The cap and the window are the ones the server reports, not literals.
    expect(await screen.findByText(/最多 256 条/)).toBeTruthy()
    expect(screen.getByText(/等 60 秒再下发/)).toBeTruthy()
  })

  it('the subtitle says it also records the rule hits', async () => {
    renderAccess(<AccessControlView />)
    expect(await screen.findByText('决定用户能通过原生节点访问哪些目的地，并记录规则命中。')).toBeTruthy()
  })

  it('mounts the records tab from its URL, and its help names the retention from the setting', async () => {
    renderAccess(<AccessControlView />, { route: '/admin/access-control?tab=records' })
    expect(await screen.findByTestId('records-tab')).toBeTruthy()
    expect(screen.queryByTestId('policies-tab')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: '说明' }))
    expect(await screen.findByText(/命中记录保留 30 天/)).toBeTruthy()
    expect(screen.getByText(/「本应拦截」是仅报告的策略命中的连接/)).toBeTruthy()
  })

  it('the records tab opens an account drawer, the test sheet with the destination in state and never the URL, the retention field, the node coverage and the policies tab', async () => {
    const { router } = renderAccess(<AccessControlView />, { route: '/admin/access-control?tab=records' })
    fireEvent.click(await screen.findByText('test-dest'))
    expect(await screen.findByTestId('test-sheet')).toBeTruthy()
    expect(whereIs(router)).toBe('/admin/access-control?tab=records&sheet=test')
    expect(whereIs(router)).not.toContain('gmail')
    expect((router.state.location.state as { prefill?: { target?: string } }).prefill?.target).toBe('smtp.gmail.com')
    await act(async () => { await router.navigate(-1) })
    fireEvent.click(await screen.findByText('open-user'))
    expect((await screen.findByTestId('account-drawer')).textContent).toBe('7')
    await act(async () => { await router.navigate(-1) })
    fireEvent.click(await screen.findByText('retention'))
    expect(await screen.findByTestId('settings-dialog')).toBeTruthy()
    fireEvent.click(await screen.findByText('nodes'))
    expect(await screen.findByTestId('coverage-sheet')).toBeTruthy()
    fireEvent.click(await screen.findByText('policies'))
    expect(await screen.findByTestId('policies-tab')).toBeTruthy()
  })

  it('opens the sheets with push, one at a time, and a prefill never reaches the URL', async () => {
    const { router } = renderAccess(<AccessControlView />, { route: '/admin/access-control?tab=policies' })
    await screen.findByTestId('policies-tab')
    fireEvent.click(screen.getByRole('button', { name: '测试目的地' }))
    expect(await screen.findByTestId('test-sheet')).toBeTruthy()
    expect(whereIs(router)).toBe('/admin/access-control?tab=policies&sheet=test')
    expect(router.state.historyAction).toBe('PUSH')
  })

  it('a deep link to the entries sheet reads the list id from the URL', async () => {
    renderAccess(<AccessControlView />, { route: '/admin/access-control?tab=lists&sheet=list&list=9' })
    expect((await screen.findByTestId('entries-sheet')).textContent).toBe('9')
  })

  it('Edit from the entries sheet closes the sheet in the same navigation', async () => {
    const { router } = renderAccess(<AccessControlView />, { route: '/admin/access-control?tab=lists&sheet=list&list=9' })
    fireEvent.click(await screen.findByTestId('entries-sheet'))
    await waitFor(() => expect(whereIs(router)).toBe('/admin/access-control?tab=lists'))
    expect(screen.queryByTestId('entries-sheet')).toBeNull()
  })

  it('Edit from a PUSHED entries sheet goes Back first, so history keeps one page entry, then opens the dialog', async () => {
    const { router } = renderAccess(<AccessControlView />, { route: '/admin/access-control?tab=lists' })
    await screen.findByTestId('lists-tab')
    await act(async () => { await router.navigate('/admin/access-control?tab=lists&sheet=list&list=9', { state: { drawer: 'sheet' } }) })
    fireEvent.click(await screen.findByTestId('entries-sheet'))
    await waitFor(() => expect(screen.queryByTestId('entries-sheet')).toBeNull())
    await waitFor(() => expect((router.state.location.state as { openList?: number } | null)?.openList).toBe(9))
    expect(whereIs(router)).toBe('/admin/access-control?tab=lists')
    // Nothing of the sheet is left behind it to come back to.
    await act(async () => { await router.navigate(-1) })
    expect(whereIs(router)).toBe('/admin/access-control?tab=lists')
  })

  it('the menu opens the settings dialog and pause asks first, then pauses', async () => {
    vi.mocked(api.pauseDest).mockResolvedValue()
    renderAccess(<AccessControlView />)
    await screen.findByTestId('policies-tab')
    fireEvent.click(screen.getByRole('button', { name: '更多操作' }))
    fireEvent.click(await screen.findByText('数据与下发设置…'))
    expect(await screen.findByTestId('settings-dialog')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '更多操作' }))
    fireEvent.click(await screen.findByText('暂停全部执行…'))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '暂停全部访问控制？', destructive: true })))
    await waitFor(() => expect(api.pauseDest).toHaveBeenCalledWith(true))
  })
})

describe('the verdict line', () => {
  it('says paused and offers to resume, after a confirmation', async () => {
    vi.mocked(api.getDestStatus).mockResolvedValue(status({ paused: true }))
    vi.mocked(api.pauseDest).mockResolvedValue()
    renderAccess(<AccessControlView />)
    expect(await screen.findByText('访问控制已暂停：所有节点都不执行策略')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '恢复执行' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '恢复访问控制？' })))
    await waitFor(() => expect(api.pauseDest).toHaveBeenCalledWith(false))
  })

  it('a saved change waits: only the main sentence is the live region, the countdown is aria-hidden, and Apply now publishes', async () => {
    vi.mocked(api.getDestStatus).mockResolvedValue(status({ generation: 3, next_publish_at: Date.now() + 45_000, apply_eta_ms: 100_000 }))
    vi.mocked(api.publishDest).mockResolvedValue({ published_generation: 3, generation: 3, unpublished: false })
    renderAccess(<AccessControlView />)
    const live = await screen.findByRole('status')
    expect(live.textContent).toBe('改动已保存，约 2 分钟内下发到 1 台节点')
    const bar = screen.getByTestId('access-verdict')
    const hidden = [...bar.querySelectorAll('[aria-hidden="true"]')].map(el => el.textContent ?? '')
    expect(hidden.some(text => /秒后下发/.test(text))).toBe(true)
    expect(hidden.some(text => /读取于/.test(text))).toBe(true)
    expect(live.textContent).not.toMatch(/秒/)
    fireEvent.click(within(bar).getByRole('button', { name: '立即下发' }))
    await waitFor(() => expect(api.publishDest).toHaveBeenCalledTimes(1))
  })

  it('3X-UI and S-UI not enforcing is written in the detail and does not change the tone', async () => {
    vi.mocked(api.getDestStatus).mockResolvedValue(status({ nodes: [node(), node({ panel_id: 2, kind: 'x-ui', state: 'unsupported_kind' })] }))
    renderAccess(<AccessControlView />)
    const bar = await screen.findByTestId('access-verdict')
    expect(bar.getAttribute('data-tone')).toBe('ok:applied')
    expect(bar.textContent).toContain('不执行：3X-UI / S-UI 1 台（设计如此）')
  })

  it('names how many nodes record hits after what is not enforcing, and writes nothing for zero', async () => {
    vi.mocked(api.getDestStatus).mockResolvedValue(status({ nodes: [node({ collecting: true }), node({ panel_id: 2, collecting: true }), node({ panel_id: 3, kind: 'x-ui', state: 'unsupported_kind' })] }))
    renderAccess(<AccessControlView />)
    const bar = await screen.findByTestId('access-verdict')
    expect(bar.textContent).toContain('不执行：3X-UI / S-UI 1 台（设计如此） · 记录命中的节点 2 台')
    cleanup()
    vi.mocked(api.getDestStatus).mockResolvedValue(status({ nodes: [node()] }))
    renderAccess(<AccessControlView />)
    const quiet = await screen.findByTestId('access-verdict')
    expect(quiet.textContent).not.toContain('记录命中的节点')
  })

  it('a node count of recording nodes stands alone when nothing is skipped, and in English reads as a plural', async () => {
    vi.mocked(api.getDestStatus).mockResolvedValue(status({ nodes: [node({ collecting: true })] }))
    renderAccess(<AccessControlView />)
    expect((await screen.findByTestId('verdict-recording')).textContent).toBe('记录命中的节点 1 台')
    cleanup()
    setTestLang('en-US')
    vi.mocked(api.getDestStatus).mockResolvedValue(status({ nodes: [node({ collecting: true }), node({ panel_id: 2, collecting: true })] }))
    renderAccess(<AccessControlView />)
    expect((await screen.findByTestId('verdict-recording')).textContent).toBe('2 nodes recording hits')
  })

  it('a report-only policy counts as something deployed: no node to run it is a failure, not "nothing enabled"', async () => {
    vi.mocked(api.getDestPolicies).mockResolvedValue({ ...policies, block: [{ ...policies.block[0], state: 'report' }] })
    vi.mocked(api.getDestStatus).mockResolvedValue(status({ nodes: [node({ kind: 'x-ui', state: 'unsupported_kind' })] }))
    renderAccess(<AccessControlView />)
    expect(await screen.findByText('有 1 条启用的策略，但没有任何节点会执行它们')).toBeTruthy()
  })

  it('lists native panels that are not enrolled yet as quiet text in the summary', async () => {
    vi.mocked(api.getDestStatus).mockResolvedValue(status({ nodes: [node(), node({ panel_id: 2, state: 'unsupported_version', agent_id: '' })] }))
    renderAccess(<AccessControlView />)
    const bar = await screen.findByTestId('access-verdict')
    expect(bar.getAttribute('data-tone')).toBe('ok:applied')
    expect(bar.textContent).toContain('1 台未接入')
  })

  it('draws nothing green when nothing is enabled', async () => {
    vi.mocked(api.getDestPolicies).mockResolvedValue({ ...policies, block: [] })
    vi.mocked(api.getDestStatus).mockResolvedValue(status({ nodes: [node({ state: 'none' })] }))
    renderAccess(<AccessControlView />)
    expect(await screen.findByText('还没有启用的策略')).toBeTruthy()
    expect(screen.getByTestId('access-verdict').getAttribute('data-tone')).toBe('quiet:idle')
  })

  it('reports a failed read with a retry, and a 503 as not wired', async () => {
    vi.mocked(api.getDestStatus).mockRejectedValueOnce(new Error('boom'))
    renderAccess(<AccessControlView />)
    expect(await screen.findByText('读取节点状态失败')).toBeTruthy()
    expect(screen.getByRole('button', { name: '重试' })).toBeTruthy()
  })
})

describe('the header on a phone', () => {
  const phone = (on: boolean) => {
    const original = window.matchMedia
    window.matchMedia = ((q: string) => ({ matches: on, media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, onchange: null, dispatchEvent: () => false })) as typeof window.matchMedia
    return () => { window.matchMedia = original }
  }

  it('keeps the test button and the menu with the actions and gives the account picker the full width', async () => {
    const restore = phone(true)
    try {
      renderAccess(<AccessControlView />)
      const pick = await screen.findByLabelText('pick')
      expect(pick.getAttribute('data-fullwidth')).toBe('true')
      expect(screen.getByRole('button', { name: '测试目的地' })).toBeTruthy()
      expect(screen.getByRole('button', { name: '更多操作' })).toBeTruthy()
      // All three sit in one actions container, so the menu never wraps away from the page's actions.
      const row = pick.closest('[data-header-actions]')
      expect(row?.contains(screen.getByRole('button', { name: '更多操作' }))).toBe(true)
      expect(row?.contains(screen.getByRole('button', { name: '测试目的地' }))).toBe(true)
    } finally { restore() }
  })

  it('puts the test button and the menu together at the right of the first row, and the en-US picker label is short', async () => {
    const restore = phone(true)
    try {
      renderAccess(<AccessControlView />)
      const test = await screen.findByRole('button', { name: '测试目的地' })
      const menu = screen.getByRole('button', { name: '更多操作' })
      // Both live in the one actions row, the test button first (it carries the free space on xs).
      expect(test.closest('[data-header-actions]')).toBe(menu.closest('[data-header-actions]'))
      expect(test.compareDocumentPosition(menu) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    } finally { restore() }
    expect(translate('admin:access_control.header.pick_user').length).toBeLessThanOrEqual(8)
    setTestLang('en-US')
    expect(translate('admin:access_control.header.pick_user').length).toBeLessThanOrEqual(14)
  })

  it('leaves the picker at its own width on a desktop', async () => {
    const restore = phone(false)
    try {
      renderAccess(<AccessControlView />)
      expect((await screen.findByLabelText('pick')).getAttribute('data-fullwidth')).toBe('false')
    } finally { restore() }
  })
})
