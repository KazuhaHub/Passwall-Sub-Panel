// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { NodeAgentIssue } from '@/api/types'
import { api, installReads, lastParams, list, mountPage, toast } from './logs/testHarness'
import { useAuthStore } from '@/stores/auth'
import NodeIssuesView from './NodeIssuesView'

const mount = (entry = '/admin/node-issues') => mountPage(<NodeIssuesView />, entry, '/admin/node-issues')
const here = (m: ReturnType<typeof mount>) => m.router.state.location.pathname + m.router.state.location.search

function issue(id: number, over: Partial<NodeAgentIssue> = {}): NodeAgentIssue {
  return {
    id, agent_id: 'agt_long_original_agent_identity_123456789', server_id: 7, server_name: 'Canada test server',
    code: 'core_telemetry_failed', key: 'xray/26.7.28', detail: `original diagnostic for record ${id}`,
    first_seen_at: '2026-09-14T00:00:00Z', last_seen_at: '2026-09-14T01:00:00Z',
    created_at: '2026-09-14T00:00:00Z', updated_at: '2026-09-14T01:00:00Z', ...over,
  }
}
const reads = (items: NodeAgentIssue[], total = items.length) => installReads({ '/admin/node-issues': list(items, total) })
const url = '/admin/node-issues'
const calls = () => api.get.mock.calls.filter(([u]) => u === url)
const rowOf = async (text: string) => (await screen.findByText(text)).closest('tr') as HTMLElement
const openFirst = async () => { fireEvent.click(await screen.findByText('Canada test server')); return screen.findByRole('dialog') }

describe('Node issues: grouping, the sheet and the review of single reports', () => {
  it('groups by agent and kind of problem, but the sheet lists every original report and the unabridged diagnostic', async () => {
    const items = [
      issue(407, { key: 'cli_407', detail: 'roster object is pending\nfull first detail' }),
      issue(406, { key: 'cli_406' }),
      issue(408, { code: 'object_pending_timeout', key: 'cli_408' }),
    ]
    reads(items)
    const m = mount()
    await screen.findAllByText('Canada test server')
    // Two summaries (statistics, sync) for three reports; no technical identity in the list.
    expect(within(screen.getByRole('table')).getAllByRole('row')).toHaveLength(3)
    expect(screen.queryByText(items[0].agent_id)).toBeNull()
    expect(screen.queryByText('cli_407')).toBeNull()
    fireEvent.click(screen.getByText('Node statistics could not be read'))
    const sheet = await screen.findByRole('dialog')
    expect(here(m)).toContain('row=')
    expect(within(sheet).getAllByText(/^Report #/)).toHaveLength(2)
    expect(within(sheet).getByText(items[0].agent_id)).toBeTruthy()
    expect(within(sheet).getByText('cli_407')).toBeTruthy()
    expect(within(sheet).getByText('cli_406')).toBeTruthy()
    expect(within(sheet).getByText(/roster object is pending\s+full first detail/)).toBeTruthy()
    expect(api.post).not.toHaveBeenCalled()
  })

  it('keeps the same code on different agents apart, and does not guess a name for a detached agent', async () => {
    const detached = issue(2, { agent_id: 'agt_detached_identity_with_long_suffix', server_id: undefined, server_name: undefined })
    reads([issue(1), detached])
    mount()
    expect(await screen.findByText('Canada test server')).toBeTruthy()
    expect(screen.getByText('Unlinked node agent')).toBeTruthy()
    expect(screen.queryByText(`${detached.agent_id.slice(0, 12)}…`)).toBeNull()
    fireEvent.click(screen.getByText('Unlinked node agent'))
    const sheet = await screen.findByRole('dialog')
    expect(within(sheet).getByText(detached.agent_id)).toBeTruthy()
    expect(within(sheet).queryByText(issue(1).agent_id)).toBeNull()
  })

  it('reviews exactly the report that was chosen: one request, the list is read again, no recovery claimed', async () => {
    const first = issue(407, { key: 'cli_407' })
    const second = issue(406, { key: 'cli_406' })
    reads([first, second])
    api.post.mockImplementation(async (u: string) => {
      expect(u).toBe('/admin/node-issues/406/acknowledge')
      reads([first])
      return { data: {} }
    })
    mount()
    const sheet = await openFirst()
    fireEvent.click(within(sheet).getAllByRole('button', { name: 'Mark as reviewed' })[1])
    await waitFor(() => expect(toast).toHaveBeenCalledWith('Marked as reviewed', 'success'))
    await waitFor(() => expect(within(screen.getByRole('dialog')).queryByText('Report #406')).toBeNull())
    expect(api.post.mock.calls).toEqual([['/admin/node-issues/406/acknowledge']])
    expect(toast.mock.calls.some(([m]) => /recover/i.test(m))).toBe(false)
    expect(api.put).not.toHaveBeenCalled()
    expect(api.delete).not.toHaveBeenCalled()
  })

  it('does not hide or mark a report reviewed when the review fails', async () => {
    reads([issue(407, { key: 'cli_407' })])
    api.post.mockRejectedValue(new Error('refused'))
    mount()
    const sheet = await openFirst()
    const before = calls().length
    const button = within(sheet).getByRole('button', { name: 'Mark as reviewed' })
    fireEvent.click(button)
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/node-issues/407/acknowledge'))
    await waitFor(() => expect((within(sheet).getByRole('button', { name: 'Mark as reviewed' }) as HTMLButtonElement).disabled).toBe(false))
    expect(within(sheet).getAllByText(/Needs review/).length).toBeGreaterThan(0)
    expect(toast).not.toHaveBeenCalled()
    expect(calls()).toHaveLength(before)
  })

  it('lets an account without the operate capability read everything and review nothing', async () => {
    useAuthStore.setState({ role: 'user' })
    reads([issue(1)])
    mount()
    const sheet = await openFirst()
    expect(within(sheet).getByText(issue(1).agent_id)).toBeTruthy()
    expect(within(sheet).queryByRole('button', { name: 'Mark as reviewed' })).toBeNull()
    expect(screen.queryByRole('checkbox')).toBeNull()
  })

  it('asks the server for the page, the review state and the trimmed keyword, all from the address', async () => {
    reads([issue(1)], 60)
    const m = mount('/admin/node-issues?page=2&state=acknowledged&q=%20pending%20')
    await screen.findByText('Canada test server')
    expect(lastParams(url)).toEqual({ page: 2, page_size: 25, view: 'attention', acknowledged: true, keyword: 'pending' })
    fireEvent.click(screen.getByRole('radio', { name: 'All review states' }))
    await waitFor(() => expect(lastParams(url)).toEqual({ page: 1, page_size: 25, view: 'attention', keyword: 'pending' }))
    expect(here(m)).toBe('/admin/node-issues?state=all&q=+pending+')
  })

  it('shows the range in records, not in groups, and steps back when a page no longer exists', async () => {
    let ack = false
    const a = issue(1, { server_name: 'First page server' })
    const b = issue(26, { server_name: 'Last page server' })
    api.get.mockImplementation(async (_u: string, cfg: { params: { page: number } }) => ({
      data: { items: cfg.params.page === 2 ? (ack ? [] : [b]) : [a], total: ack ? 25 : 26 },
    }))
    api.post.mockImplementation(async () => { ack = true; return { data: {} } })
    mount('/admin/node-issues?page=2')
    fireEvent.click(await screen.findByText('Last page server'))
    const sheet = await screen.findByRole('dialog')
    // Counted in reports, as the server counts: the one report on the last page, not one group.
    expect(screen.getByText('26–26 of 26', { exact: false })).toBeTruthy()
    fireEvent.click(within(sheet).getByRole('button', { name: 'Mark as reviewed' }))
    await waitFor(() => expect(lastParams(url)).toEqual({ page: 1, page_size: 25, view: 'attention', acknowledged: false }))
    expect(await screen.findByText('First page server')).toBeTruthy()
    expect(screen.queryByText('No important reports need review. Startup records remain available under Diagnostics.')).toBeNull()
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('tells a failed first load from an empty result, and keeps the last records when a refresh fails', async () => {
    installReads({ [url]: new Error('offline') })
    const m = mount()
    expect(await screen.findByText('Node issue reports are temporarily unavailable')).toBeTruthy()
    expect(screen.queryByText('No important reports need review. Startup records remain available under Diagnostics.')).toBeNull()
    reads([])
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('No important reports need review. Startup records remain available under Diagnostics.')).toBeTruthy()
    m.unmount()

    reads([issue(1)])
    mount()
    await screen.findByText('Canada test server')
    installReads({ [url]: new Error('offline refresh') })
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(await screen.findByText(/Could not load reports/)).toBeTruthy()
    expect(screen.getByText('Canada test server')).toBeTruthy()
  })

  it('does not let an older response overwrite a newer keyword result', async () => {
    const old: Array<{ signal: AbortSignal; resolve: (v: unknown) => void }> = []
    api.get.mockImplementation((_u: string, cfg: { params: { keyword?: string }; signal: AbortSignal }) => {
      if (cfg.params.keyword === 'new') return Promise.resolve({ data: list([issue(2, { server_name: 'New selection server' })]) })
      return new Promise(resolve => { old.push({ signal: cfg.signal, resolve }) })
    })
    mount()
    await waitFor(() => expect(old.length).toBeGreaterThan(0))
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'new' } })
    fireEvent.keyDown(screen.getByRole('searchbox'), { key: 'Enter' })
    expect(await screen.findByText('New selection server')).toBeTruthy()
    expect(old.every(r => r.signal.aborted)).toBe(true)
    old.forEach(r => r.resolve({ data: list([issue(1, { server_name: 'Obsolete server' })]) }))
    await new Promise(r => setTimeout(r, 20))
    expect(screen.getByText('New selection server')).toBeTruthy()
    expect(screen.queryByText('Obsolete server')).toBeNull()
  })
})

describe('Node issues: the views and the bulk review', () => {
  const sync = (id: number, code: string, over: Partial<NodeAgentIssue> = {}) => issue(id, { code, ...over })

  it('starts on the important issues and carries view, keyword and a reset page across views', async () => {
    reads([issue(1)], 60)
    mount('/admin/node-issues?page=2&q=Canada')
    await screen.findByText('Canada test server')
    expect(lastParams(url)).toMatchObject({ view: 'attention', page: 2, keyword: 'Canada', acknowledged: false })
    fireEvent.click(screen.getByRole('tab', { name: 'Diagnostics' }))
    await waitFor(() => expect(lastParams(url)).toMatchObject({ view: 'diagnostic', page: 1, keyword: 'Canada' }))
    fireEvent.click(screen.getByRole('tab', { name: 'All records' }))
    await waitFor(() => expect(lastParams(url)).toMatchObject({ view: 'all', page: 1, keyword: 'Canada' }))
  })

  it('shows what the server returns for the diagnostic view as the startup record', async () => {
    api.get.mockImplementation(async (_u: string, cfg: { params: { view: string } }) => ({
      data: list(cfg.params.view === 'diagnostic' ? [issue(2, { server_name: 'Statistics report server' })] : [issue(1, { code: 'task_replay_fenced', server_name: 'Task report server' })]),
    }))
    mount()
    expect(await screen.findByText('Task report server')).toBeTruthy()
    fireEvent.click(screen.getByRole('tab', { name: 'Diagnostics' }))
    expect(await screen.findByText('Statistics report server')).toBeTruthy()
    expect(screen.getByText('Node startup records')).toBeTruthy()
    expect(screen.queryByText('Task report server')).toBeNull()
  })

  it('keeps unknown codes visible, and keeps stable codes and agent ids out of the list', async () => {
    const unknown = issue(1, { code: 'future_unknown_safety_condition', server_id: undefined, server_name: undefined, agent_id: 'agt_detached_full_technical_identity_1234567890', detail: 'Raw unknown safety diagnostic' })
    const task = issue(2, { code: 'task_identity_conflict', detail: 'Original task identity failure' })
    reads([unknown, task])
    mount()
    await screen.findByText('Other reports need checking')
    expect(screen.getByText('Task status needs checking')).toBeTruthy()
    for (const r of [unknown, task]) {
      expect(screen.queryByText(r.code)).toBeNull()
      expect(screen.queryByText(r.agent_id)).toBeNull()
      expect(screen.queryByText(r.detail!)).toBeNull()
    }
    fireEvent.click(screen.getByText('Other reports need checking'))
    const sheet = await screen.findByRole('dialog')
    const fold = within(sheet).getByText('Technical details and original logs').closest('details') as HTMLDetailsElement
    expect(fold.open).toBe(false)
    expect(within(sheet).getAllByText(unknown.code).length).toBeGreaterThan(0)
    expect(within(sheet).getByText('Raw unknown safety diagnostic')).toBeTruthy()
  })

  it('merges related synchronization codes into one line, and a bulk review posts every unreviewed original id after a confirmation', async () => {
    const records = [
      sync(407, 'core_convergence_failed'), sync(406, 'object_pending_timeout'), sync(405, 'directives_ahead_of_roster'),
      sync(404, 'attachment_unknown_listener', { acknowledged_at: '2026-09-14T02:00:00Z' }),
    ]
    reads(records)
    mount()
    await screen.findByText('Configuration or clients could not be synchronized')
    expect(within(screen.getByRole('table')).getAllByRole('row')).toHaveLength(2)
    records.forEach(r => expect(screen.queryByText(r.code)).toBeNull())
    fireEvent.click(screen.getByRole('checkbox', { name: /Configuration or clients could not be synchronized/ }))
    fireEvent.click(await screen.findByRole('button', { name: 'Mark selected as reviewed' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(api.post).not.toHaveBeenCalled()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Mark selected as reviewed' }))
    await waitFor(() => expect(toast).toHaveBeenCalledWith('Marked 3 records as reviewed', 'success'))
    expect(api.post.mock.calls).toEqual([
      ['/admin/node-issues/407/acknowledge', undefined, { _skipErrorToast: true }],
      ['/admin/node-issues/406/acknowledge', undefined, { _skipErrorToast: true }],
      ['/admin/node-issues/405/acknowledge', undefined, { _skipErrorToast: true }],
    ])
    expect(api.put).not.toHaveBeenCalled()
    expect(api.delete).not.toHaveBeenCalled()
  })

  it('keeps the failed ones selected when part of a bulk review fails', async () => {
    const records = [sync(2, 'core_convergence_failed'), issue(1)]
    reads(records)
    api.post.mockImplementation(async (u: string) => { if (u.includes('/2/')) throw new Error('refused'); return { data: {} } })
    mount()
    await screen.findByText('Configuration or clients could not be synchronized')
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all rows on this page' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Mark selected as reviewed' }))
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Mark selected as reviewed' }))
    await waitFor(() => expect(toast).toHaveBeenCalledWith('Reviewed 1; 1 failed. Failed reports still on this page remain selected for retry.', 'warning'))
    const row = await rowOf('Configuration or clients could not be synchronized')
    await waitFor(() => expect(within(row).getByRole('checkbox').getAttribute('aria-checked')).toBe('true'))
    expect(within(await rowOf('Node statistics could not be read')).getByRole('checkbox').getAttribute('aria-checked')).toBe('false')
  })

  it('locks the filters while a bulk confirmation is waiting, and unlocks after Cancel', async () => {
    reads([issue(1)])
    mount()
    await screen.findByText('Canada test server')
    fireEvent.click(screen.getByRole('checkbox', { name: /Node statistics could not be read/ }))
    fireEvent.click(await screen.findByRole('button', { name: 'Mark selected as reviewed' }))
    const dialog = await screen.findByRole('alertdialog')
    // The tab is disabled behind the dialog; what is asked of the server cannot change under it.
    expect(screen.getByRole('tab', { name: 'Diagnostics', hidden: true }).hasAttribute('data-disabled')).toBe(true)
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Diagnostics' }).hasAttribute('data-disabled')).toBe(false))
    expect(api.post).not.toHaveBeenCalled()
  })

  it('a bulk review from the sheet reviews that group only', async () => {
    reads([sync(407, 'core_convergence_failed'), sync(406, 'object_pending_timeout'), issue(1)])
    mount()
    fireEvent.click(await screen.findByText('Configuration or clients could not be synchronized'))
    const sheet = await screen.findByRole('dialog')
    fireEvent.click(within(sheet).getByRole('button', { name: 'Mark all 2 as reviewed' }))
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Mark selected as reviewed' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledTimes(2))
    expect(api.post.mock.calls.map(([u]) => u).sort()).toEqual(['/admin/node-issues/406/acknowledge', '/admin/node-issues/407/acknowledge'])
  })
})

