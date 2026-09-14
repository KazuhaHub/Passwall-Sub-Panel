// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { NodeAgentIssue } from '@/api/types'
import type { NodeIssueListParams } from '@/api/nodeIssues'
import { api, installReads, list, mount, snack } from '@/test/adminSaveHarness'
import NodeIssuesView from './NodeIssuesView'

const mocks = vi.hoisted(() => ({ narrow: vi.fn(() => false), confirm: vi.fn() }))
vi.mock('@mui/material', async importOriginal => ({
  ...await importOriginal<typeof import('@mui/material')>(),
  useMediaQuery: mocks.narrow,
}))
vi.mock('@/components/ConfirmHost', () => ({ confirm: mocks.confirm }))

function issue(id: number, overrides: Partial<NodeAgentIssue> = {}): NodeAgentIssue {
  return {
    id, agent_id: 'agt_original_full_technical_identity_1234567890',
    server_id: 7, server_name: 'Canada noise test', code: 'object_pending_timeout',
    key: `cli_${id}`, detail: `Full original diagnostic ${id}`,
    first_seen_at: '2026-09-14T00:00:00Z', last_seen_at: '2026-09-14T01:00:00Z',
    created_at: '2026-09-14T00:00:00Z', updated_at: '2026-09-14T01:00:00Z',
    ...overrides,
  }
}

const viewKey = (name: string) => `admin:node_issues.view.${name}`
const bulkKey = (name: string) => `admin:node_issues.bulk.${name}`
const viewControl = () => screen.getByRole('tab', { name: viewKey('attention') }) as HTMLButtonElement
const pageBox = () => screen.getByRole('checkbox', { name: bulkKey('select_page') }) as HTMLInputElement
const reads = () => api.get.mock.calls.filter(([url]) => url === '/admin/node-issues')
const lastParams = () => reads().at(-1)![1].params as NodeIssueListParams
const detailButtons = () => screen.getAllByRole('button', { name: 'admin:node_issues.open_details' })

async function chooseView(value: 'attention' | 'diagnostic' | 'all') {
  fireEvent.click(await screen.findByRole('tab', { name: viewKey(value) }))
}

async function waitForList() {
  await waitFor(() => expect(pageBox().disabled).toBe(false))
}

async function openDetails(index = 0) {
  await waitFor(() => expect(detailButtons().length).toBeGreaterThan(index))
  fireEvent.click(detailButtons()[index])
  return screen.findByRole('dialog')
}

beforeEach(() => {
  mocks.narrow.mockReturnValue(false)
  mocks.confirm.mockResolvedValue(true)
})

describe('Node issue attention views and low-noise category presentation', () => {
  it('requests the attention view by default without changing the unreviewed-record filter', async () => {
    installReads({ '/admin/node-issues': list([issue(1)]) })
    mount(<NodeIssuesView />)
    await waitForList()
    expect(lastParams()).toEqual({ page: 1, page_size: 25, view: 'attention', acknowledged: false })
    expect(screen.getByRole('tablist', { name: viewKey('label') })).toBeTruthy()
    expect(viewControl().getAttribute('aria-selected')).toBe('true')
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(api.post).not.toHaveBeenCalled()
  })

  it('resets pagination and original-ID selection when switching the server-filtered result view', async () => {
    installReads({ '/admin/node-issues': { ...list([issue(1), issue(2)]), total: 60 } })
    mount(<NodeIssuesView />)
    await waitForList()
    fireEvent.click(screen.getByRole('button', { name: 'Go to next page' }))
    await waitFor(() => expect(lastParams().page).toBe(2))
    await waitForList()
    fireEvent.click(pageBox())
    expect(screen.getByRole('button', { name: bulkKey('acknowledge') })).toBeTruthy()
    await chooseView('diagnostic')
    await waitFor(() => expect(lastParams()).toEqual({ page: 1, page_size: 25, view: 'diagnostic', acknowledged: false }))
    await waitFor(() => expect(pageBox().checked).toBe(false))
    expect(screen.queryByRole('button', { name: bulkKey('acknowledge') })).toBeNull()
    expect(api.post).not.toHaveBeenCalled()
    await chooseView('all')
    await waitFor(() => expect(lastParams()).toEqual({ page: 1, page_size: 25, view: 'all', acknowledged: false }))
  })

  it('retains an explicit server keyword across attention, diagnostic and all views', async () => {
    installReads({ '/admin/node-issues': list([issue(1)]) })
    mount(<NodeIssuesView />)
    await waitForList()
    const search = screen.getByRole('textbox', { name: 'admin:node_issues.search' })
    fireEvent.change(search, { target: { value: '  Canada  ' } })
    fireEvent.submit(search.closest('form')!)
    await waitFor(() => expect(lastParams()).toMatchObject({ view: 'attention', keyword: 'Canada' }))
    await chooseView('diagnostic')
    await waitFor(() => expect(lastParams()).toMatchObject({ view: 'diagnostic', keyword: 'Canada', page: 1 }))
    await chooseView('all')
    await waitFor(() => expect(lastParams()).toMatchObject({ view: 'all', keyword: 'Canada', page: 1 }))
    expect((search as HTMLInputElement).value).toBe('  Canada  ')
  })

  it('renders diagnostic records returned by the server rather than silently dropping them in the browser', async () => {
    api.get.mockImplementation(async (_url: string, config: { params: NodeIssueListParams }) => ({ data: list(
      config.params.view === 'diagnostic' ? [issue(2, { code: 'core_telemetry_failed', server_name: 'Statistics report server' })]
        : [issue(1, { code: 'task_replay_fenced', server_name: 'Task report server' })],
    ) }))
    mount(<NodeIssuesView />)
    expect(await screen.findByText('Task report server')).toBeTruthy()
    await chooseView('diagnostic')
    expect(await screen.findByText('Statistics report server')).toBeTruthy()
    expect(screen.queryByText('Task report server')).toBeNull()
    expect(screen.getByText('admin:node_issues.startup_title')).toBeTruthy()
    expect(api.post).not.toHaveBeenCalled()
  })

  it('keeps unknown codes and safety reports visible but removes technical IDs and codes from the main list', async () => {
    const detached = issue(1, {
      code: 'future_unknown_safety_condition', server_id: undefined, server_name: undefined,
      agent_id: 'agt_detached_full_technical_identity_1234567890', detail: 'Raw unknown safety diagnostic',
    })
    const task = issue(2, { code: 'task_identity_conflict', detail: 'Original task identity failure' })
    installReads({ '/admin/node-issues': list([detached, task]) })
    mount(<NodeIssuesView />)
    await waitForList()
    expect(detailButtons()).toHaveLength(2)
    expect(screen.getByText('admin:node_issues.category_titles.other')).toBeTruthy()
    expect(screen.getByText('admin:node_issues.category_titles.tasks')).toBeTruthy()
    expect(screen.getByText('admin:node_issues.unknown_server')).toBeTruthy()
    for (const record of [detached, task]) {
      expect(screen.queryByText(record.code)).toBeNull()
      expect(screen.queryByText(record.agent_id)).toBeNull()
      expect(screen.queryByText(`${record.agent_id.slice(0, 12)}…`)).toBeNull()
      expect(screen.queryByText(record.detail!)).toBeNull()
    }
    const dialog = await openDetails()
    const technical = within(dialog).getByRole('button', { name: 'admin:node_issues.technical_details' })
    expect(technical.getAttribute('aria-expanded')).toBe('false')
    fireEvent.click(technical)
    expect(technical.getAttribute('aria-expanded')).toBe('true')
    expect(within(dialog).getAllByText(detached.code).length).toBeGreaterThan(0)
    expect(within(dialog).getByText(detached.agent_id)).toBeTruthy()
    expect(within(dialog).getByText(detached.detail!)).toBeTruthy()
    expect(api.post).not.toHaveBeenCalled()
  })

  it('merges related synchronization codes into one server summary while bulk review still posts every pending original ID', async () => {
    const records = [
      issue(407, { code: 'core_convergence_failed' }),
      issue(406, { code: 'object_pending_timeout' }),
      issue(405, { code: 'directives_ahead_of_roster' }),
      issue(404, { code: 'attachment_unknown_listener', acknowledged_at: '2026-09-14T02:00:00Z' }),
    ]
    installReads({ '/admin/node-issues': list(records) })
    mount(<NodeIssuesView />)
    await waitForList()
    expect(detailButtons()).toHaveLength(1)
    expect(screen.getByText('admin:node_issues.category_titles.sync')).toBeTruthy()
    expect(screen.getAllByRole('checkbox', { name: bulkKey('select_group') })).toHaveLength(1)
    records.forEach(record => expect(screen.queryByText(record.code)).toBeNull())
    fireEvent.click(screen.getByRole('checkbox', { name: bulkKey('select_group') }))
    fireEvent.click(screen.getByRole('button', { name: bulkKey('acknowledge') }))
    await waitFor(() => expect(snack).toHaveBeenCalledWith(bulkKey('done'), 'success'))
    expect(api.post.mock.calls).toEqual([
      ['/admin/node-issues/407/acknowledge', undefined, { _skipErrorToast: true }],
      ['/admin/node-issues/406/acknowledge', undefined, { _skipErrorToast: true }],
      ['/admin/node-issues/405/acknowledge', undefined, { _skipErrorToast: true }],
    ])
    expect(api.put).not.toHaveBeenCalled()
    expect(api.delete).not.toHaveBeenCalled()
  })

  it('keeps compact original-record review controls outside the collapsed technical section and preserves distinct raw diagnostics inside it', async () => {
    const records = [
      issue(407, { code: 'object_pending_timeout', detail: 'Pending roster original diagnostic\nsecond line' }),
      issue(406, { code: 'directives_ahead_of_roster', detail: 'Directive original diagnostic: exact reason' }),
    ]
    installReads({ '/admin/node-issues': list(records) })
    mount(<NodeIssuesView />)
    await waitForList()
    const dialog = await openDetails()
    const technical = within(dialog).getByRole('button', { name: 'admin:node_issues.technical_details' })
    expect(technical.getAttribute('aria-expanded')).toBe('false')
    expect(within(dialog).getAllByRole('checkbox', { name: bulkKey('select_record') })).toHaveLength(2)
    expect(within(dialog).getAllByRole('button', { name: 'admin:node_issues.acknowledge' })).toHaveLength(2)
    // The accordion keeps the raw DOM mounted, but its presentation starts collapsed.
    expect(within(dialog).getByText('Pending roster original diagnostic second line')).toBeTruthy()
    fireEvent.click(technical)
    expect(technical.getAttribute('aria-expanded')).toBe('true')
    records.forEach(record => {
      expect(within(dialog).getAllByText(record.code).length).toBeGreaterThan(0)
      expect(within(dialog).getAllByText(record.key!).length).toBeGreaterThan(0)
    })
    expect(within(dialog).getByText('Directive original diagnostic: exact reason')).toBeTruthy()
    expect(within(dialog).getByText(records[0].agent_id)).toBeTruthy()
    expect(api.post).not.toHaveBeenCalled()
  })

  it('locks result-view switching while a bulk confirmation is pending and unlocks after cancellation', async () => {
    let resolveConfirm!: (answer: boolean) => void
    mocks.confirm.mockImplementation(() => new Promise<boolean>(resolve => { resolveConfirm = resolve }))
    installReads({ '/admin/node-issues': list([issue(1)]) })
    mount(<NodeIssuesView />)
    await waitForList()
    fireEvent.click(pageBox())
    fireEvent.click(screen.getByRole('button', { name: bulkKey('acknowledge') }))
    expect(viewControl().disabled).toBe(true)
    await act(async () => { resolveConfirm(false) })
    expect(viewControl().disabled).toBe(false)
    expect(lastParams().view).toBe('attention')
    expect(api.post).not.toHaveBeenCalled()
  })

  it('does not allow an older attention response to overwrite a newer diagnostic result', async () => {
    const oldRequests: Array<{ signal: AbortSignal; resolve: (value: unknown) => void }> = []
    api.get.mockImplementation((_url: string, config: { params: NodeIssueListParams; signal: AbortSignal }) => {
      if (config.params.view === 'diagnostic') return Promise.resolve({ data: list([
        issue(2, { code: 'core_telemetry_failed', server_name: 'Current diagnostic server' }),
      ]) })
      return new Promise(resolve => { oldRequests.push({ signal: config.signal, resolve }) })
    })
    mount(<NodeIssuesView />)
    await waitFor(() => expect(oldRequests.length).toBeGreaterThan(0))
    await chooseView('diagnostic')
    expect(await screen.findByText('Current diagnostic server')).toBeTruthy()
    expect(oldRequests.every(request => request.signal.aborted)).toBe(true)
    await act(async () => {
      oldRequests.forEach(request => request.resolve({ data: list([issue(1, { server_name: 'Obsolete attention server' })]) }))
    })
    expect(screen.getByText('Current diagnostic server')).toBeTruthy()
    expect(screen.queryByText('Obsolete attention server')).toBeNull()
    expect(lastParams().view).toBe('diagnostic')
  })

  it('uses the same clean category summaries and collapsed diagnostics on mobile cards', async () => {
    mocks.narrow.mockReturnValue(true)
    installReads({ '/admin/node-issues': list([
      issue(407, { code: 'object_pending_timeout' }), issue(406, { code: 'directives_ahead_of_roster' }),
    ]) })
    mount(<NodeIssuesView />)
    await waitForList()
    expect(screen.queryByRole('table')).toBeNull()
    expect(detailButtons()).toHaveLength(1)
    expect(screen.getByText('admin:node_issues.category_titles.sync')).toBeTruthy()
    expect(screen.queryByText('object_pending_timeout')).toBeNull()
    expect(screen.queryByText('directives_ahead_of_roster')).toBeNull()
    const dialog = await openDetails()
    expect(within(dialog).getByRole('button', { name: 'admin:node_issues.technical_details' }).getAttribute('aria-expanded')).toBe('false')
    expect(within(dialog).getAllByRole('checkbox', { name: bulkKey('select_record') })).toHaveLength(2)
  })
})
