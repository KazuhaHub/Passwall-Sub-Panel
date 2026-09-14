// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { NodeAgentIssue } from '@/api/types'
import type { NodeIssueListParams } from '@/api/nodeIssues'
import { api, installReads, list, mount, snack } from '@/test/adminSaveHarness'
import { useAuthStore } from '@/stores/auth'
import NodeIssuesView from './NodeIssuesView'

const narrowScreen = vi.hoisted(() => vi.fn(() => false))
vi.mock('@mui/material', async importOriginal => ({
  ...await importOriginal<typeof import('@mui/material')>(),
  useMediaQuery: narrowScreen,
}))

function issue(id: number, overrides: Partial<NodeAgentIssue> = {}): NodeAgentIssue {
  return {
    id, agent_id: 'agt_long_original_agent_identity_123456789',
    server_id: 7, server_name: 'Canada test server',
    code: 'core_telemetry_failed', key: 'xray/26.7.28',
    detail: `original diagnostic for record ${id}`,
    first_seen_at: '2026-09-14T00:00:00Z', last_seen_at: '2026-09-14T01:00:00Z',
    created_at: '2026-09-14T00:00:00Z', updated_at: '2026-09-14T01:00:00Z',
    ...overrides,
  }
}

beforeEach(() => {
  narrowScreen.mockReturnValue(false)
})

const reads = () => api.get.mock.calls.filter(([url]) => url === '/admin/node-issues')
const lastParams = () => reads().at(-1)![1].params as NodeIssueListParams
const searchInput = () => screen.getByRole('textbox', { name: 'admin:node_issues.search' })
const detailButtons = () => screen.getAllByRole('button', { name: 'admin:node_issues.open_details' })

async function openDetails(index = 0) {
  await waitFor(() => expect(detailButtons().length).toBeGreaterThan(index))
  fireEvent.click(detailButtons()[index])
  return screen.findByRole('dialog')
}

async function closeDetails() {
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.close' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
}

async function chooseReview(value: 'all' | 'acknowledged' | 'unacknowledged') {
  fireEvent.mouseDown(screen.getByRole('combobox', { name: 'admin:node_issues.table.state' }))
  fireEvent.click(await screen.findByRole('option', { name: `admin:node_issues.filter.${value}` }))
}

describe('Node issues presentation and durable record actions', () => {
  it('groups summaries by agent/code, but opens every original record and unabridged diagnostic', async () => {
    const items = [
      issue(407, { key: 'cli_407', detail: 'roster object is pending\nfull first detail' }),
      issue(406, { key: 'cli_406', detail: 'roster object is pending\nfull second detail' }),
      issue(408, { code: 'object_pending_timeout', key: 'cli_408' }),
    ]
    installReads({ '/admin/node-issues': list(items) })
    mount(<NodeIssuesView />)
    await waitFor(() => expect(detailButtons()).toHaveLength(2))
    const rows = within(screen.getByRole('table')).getAllByRole('row')
    expect(rows).toHaveLength(3)
    expect(within(rows[1]).getByText('2')).toBeTruthy()
    expect(screen.queryByText(items[0].agent_id)).toBeNull()
    expect(screen.queryByText('cli_407')).toBeNull()
    const dialog = await openDetails()
    expect(within(dialog).getByText('core_telemetry_failed')).toBeTruthy()
    expect(within(dialog).getByText(items[0].agent_id)).toBeTruthy()
    expect(within(dialog).getByText('cli_407')).toBeTruthy()
    expect(within(dialog).getByText('cli_406')).toBeTruthy()
    expect(within(dialog).getByText('roster object is pending full first detail')).toBeTruthy()
    expect(within(dialog).getByText('roster object is pending full second detail')).toBeTruthy()
    expect(within(dialog).getAllByText('admin:node_issues.record_id')).toHaveLength(2)
    expect(api.post).not.toHaveBeenCalled()
  })

  it('keeps matching codes on different agents separate even when their server names match', async () => {
    installReads({ '/admin/node-issues': list([
      issue(1), issue(2, { agent_id: 'agt_other_agent_identity', server_id: 8 }),
    ]) })
    mount(<NodeIssuesView />)
    await waitFor(() => expect(detailButtons()).toHaveLength(2))
    const dialog = await openDetails(1)
    expect(within(dialog).getByText('agt_other_agent_identity')).toBeTruthy()
    expect(within(dialog).queryByText(issue(1).agent_id)).toBeNull()
    expect(within(dialog).getAllByText('admin:node_issues.record_id')).toHaveLength(1)
  })

  it('uses available server display metadata without guessing names for detached agents', async () => {
    const detached = issue(2, {
      agent_id: 'agt_detached_identity_with_long_suffix', server_id: undefined, server_name: undefined,
    })
    installReads({ '/admin/node-issues': list([
      issue(1, { server_name: '' }), issue(3, { server_name: 'Server display name' }), detached,
    ]) })
    mount(<NodeIssuesView />)
    expect(await screen.findByText('Server display name')).toBeTruthy()
    expect(screen.getByText('admin:node_issues.unknown_server')).toBeTruthy()
    expect(screen.queryByText(`${detached.agent_id.slice(0, 12)}…`)).toBeNull()
    const dialog = await openDetails(1)
    expect(within(dialog).getByText(detached.agent_id)).toBeTruthy()
    expect(within(dialog).getByText('admin:node_issues.unknown_server')).toBeTruthy()
  })

  it('shows acknowledgement state only as review metadata, retaining acknowledged records in all-review results', async () => {
    installReads({ '/admin/node-issues': list([
      issue(1), issue(2, { acknowledged_at: '2026-09-14T02:00:00Z' }),
    ]) })
    mount(<NodeIssuesView />)
    await openDetails()
    expect(within(screen.getByRole('dialog')).getAllByText('admin:node_issues.record_id')).toHaveLength(2)
    await closeDetails()
    await chooseReview('all')
    expect(await screen.findByText('admin:node_issues.state.mixed')).toBeTruthy()
    expect(lastParams()).not.toHaveProperty('acknowledged')
  })

  it('acknowledges only the selected original ID and never reports recovery or writes other records', async () => {
    const first = issue(407, { key: 'cli_407' })
    const second = issue(406, { key: 'cli_406' })
    installReads({ '/admin/node-issues': list([first, second]) })
    api.post.mockImplementation(async (url: string) => {
      expect(url).toBe('/admin/node-issues/406/acknowledge')
      installReads({ '/admin/node-issues': list([first]) })
      return { data: {} }
    })
    mount(<NodeIssuesView />)
    const dialog = await openDetails()
    fireEvent.click(within(dialog).getAllByRole('button', { name: 'admin:node_issues.acknowledge' })[1])
    await waitFor(() => expect(snack).toHaveBeenCalledWith('admin:node_issues.acknowledged', 'success'))
    await waitFor(() => expect(within(screen.getByRole('dialog')).queryByText('cli_406')).toBeNull())
    expect(within(screen.getByRole('dialog')).getByText('cli_407')).toBeTruthy()
    expect(api.post.mock.calls).toEqual([['/admin/node-issues/406/acknowledge']])
    expect(snack.mock.calls.some(([message]) => /recover|resolved|恢复/i.test(message))).toBe(false)
    expect(api.put).not.toHaveBeenCalled()
    expect(api.delete).not.toHaveBeenCalled()
  })

  it('does not hide or falsely mark a record reviewed when acknowledgement fails', async () => {
    installReads({ '/admin/node-issues': list([issue(407, { key: 'cli_407' })]) })
    api.post.mockRejectedValue(new Error('acknowledgement refused'))
    mount(<NodeIssuesView />)
    const dialog = await openDetails()
    const initialReadCount = reads().length
    fireEvent.click(within(dialog).getByRole('button', { name: 'admin:node_issues.acknowledge' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/node-issues/407/acknowledge'))
    await waitFor(() => expect((within(dialog).getByRole('button', { name: 'admin:node_issues.acknowledge' }) as HTMLButtonElement).disabled).toBe(false))
    expect(within(dialog).getByText('cli_407')).toBeTruthy()
    expect(within(dialog).getByText('admin:node_issues.state.unacknowledged')).toBeTruthy()
    expect(within(dialog).queryByText('admin:node_issues.state.acknowledged')).toBeNull()
    expect(snack).not.toHaveBeenCalled()
    expect(reads()).toHaveLength(initialReadCount)
  })

  it('allows read-only accounts to inspect diagnostics without acknowledgement buttons', async () => {
    useAuthStore.setState({ role: 'user' })
    installReads({ '/admin/node-issues': list([issue(1)]) })
    mount(<NodeIssuesView />)
    const dialog = await openDetails()
    expect(within(dialog).getByText(issue(1).agent_id)).toBeTruthy()
    expect(within(dialog).queryByRole('button', { name: 'admin:node_issues.acknowledge' })).toBeNull()
    expect(api.post).not.toHaveBeenCalled()
  })

  it('submits trimmed keyword only on search and clears it back to the default filter', async () => {
    installReads({ '/admin/node-issues': list([issue(1)]) })
    mount(<NodeIssuesView />)
    await openDetails()
    await closeDetails()
    const initialReadCount = reads().length
    fireEvent.change(searchInput(), { target: { value: '  pending record  ' } })
    expect(reads()).toHaveLength(initialReadCount)
    fireEvent.submit(searchInput().closest('form')!)
    await waitFor(() => expect(lastParams()).toEqual({ page: 1, page_size: 25, view: 'attention', acknowledged: false, keyword: 'pending record' }))
    expect(screen.queryByRole('dialog')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'admin:node_issues.clear_search' }))
    await waitFor(() => expect(lastParams()).toEqual({ page: 1, page_size: 25, view: 'attention', acknowledged: false }))
    expect((searchInput() as HTMLInputElement).value).toBe('')
  })

  it('re-runs an unchanged submitted keyword on an explicit search action', async () => {
    installReads({ '/admin/node-issues': list([issue(1)]) })
    mount(<NodeIssuesView />)
    await screen.findByText('Canada test server')
    const initialReadCount = reads().length
    fireEvent.click(screen.getByRole('button', { name: 'admin:node_issues.search_action' }))
    await waitFor(() => expect(reads()).toHaveLength(initialReadCount + 1))
    expect(lastParams()).toEqual({ page: 1, page_size: 25, view: 'attention', acknowledged: false })
  })

  it('keeps review filters and pagination in API requests, resetting page when the filter changes', async () => {
    installReads({ '/admin/node-issues': { ...list([issue(1)]), total: 60 } })
    mount(<NodeIssuesView />)
    await screen.findByText('Canada test server')
    expect(lastParams()).toEqual({ page: 1, page_size: 25, view: 'attention', acknowledged: false })
    fireEvent.click(screen.getByRole('button', { name: 'Go to next page' }))
    await waitFor(() => expect(lastParams()).toEqual({ page: 2, page_size: 25, view: 'attention', acknowledged: false }))
    await chooseReview('acknowledged')
    await waitFor(() => expect(lastParams()).toEqual({ page: 1, page_size: 25, view: 'attention', acknowledged: true }))
    await chooseReview('all')
    await waitFor(() => expect(lastParams()).toEqual({ page: 1, page_size: 25, view: 'attention' }))
    await chooseReview('unacknowledged')
    await waitFor(() => expect(lastParams()).toEqual({ page: 1, page_size: 25, view: 'attention', acknowledged: false }))
  })

  it('returns to the last valid page after acknowledging its last filtered record', async () => {
    let acknowledged = false
    const firstPage = issue(1, { server_name: 'First page server' })
    const lastRecord = issue(26, { server_name: 'Last page server' })
    api.get.mockImplementation(async (_url: string, config: { params: NodeIssueListParams }) => ({
      data: {
        items: config.params.page === 2 ? (acknowledged ? [] : [lastRecord]) : [firstPage],
        total: acknowledged ? 25 : 26,
        page: config.params.page,
        page_size: config.params.page_size,
      },
    }))
    api.post.mockImplementation(async (url: string) => {
      expect(url).toBe('/admin/node-issues/26/acknowledge')
      acknowledged = true
      return { data: {} }
    })
    mount(<NodeIssuesView />)
    await screen.findByText('First page server')
    fireEvent.click(screen.getByRole('button', { name: 'Go to next page' }))
    await screen.findByText('Last page server')
    const dialog = await openDetails()
    fireEvent.click(within(dialog).getByRole('button', { name: 'admin:node_issues.acknowledge' }))
    await waitFor(() => expect(lastParams()).toEqual({ page: 1, page_size: 25, view: 'attention', acknowledged: false }))
    expect(await screen.findByText('First page server')).toBeTruthy()
    expect(screen.queryByText('Last page server')).toBeNull()
    expect(screen.queryByText('admin:node_issues.empty_attention')).toBeNull()
    expect(api.post.mock.calls).toEqual([['/admin/node-issues/26/acknowledge']])
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect((screen.getByRole('button', { name: 'Go to next page' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('distinguishes a successful empty-review result from a failed initial load and allows retry', async () => {
    api.get.mockRejectedValue(new Error('offline'))
    mount(<NodeIssuesView />)
    expect(await screen.findByText('admin:node_issues.load_failed')).toBeTruthy()
    expect(screen.queryByText('admin:node_issues.empty_attention')).toBeNull()
    expect(screen.queryByText('admin:node_issues.empty')).toBeNull()
    installReads({ '/admin/node-issues': list([]) })
    fireEvent.click(screen.getByRole('button', { name: 'admin:node_issues.refresh' }))
    expect(await screen.findByText('admin:node_issues.empty_attention')).toBeTruthy()
    expect(screen.queryByText('admin:node_issues.load_failed')).toBeNull()
    await chooseReview('all')
    expect(await screen.findByText('admin:node_issues.empty')).toBeTruthy()
    expect(screen.queryByText('admin:node_issues.empty_attention')).toBeNull()
  })

  it('preserves last-known diagnostics when refresh fails instead of showing an empty result', async () => {
    installReads({ '/admin/node-issues': list([issue(1)]) })
    mount(<NodeIssuesView />)
    await screen.findByText('Canada test server')
    api.get.mockRejectedValue(new Error('offline refresh'))
    fireEvent.click(screen.getByRole('button', { name: 'admin:node_issues.refresh' }))
    expect(await screen.findByText('admin:node_issues.load_failed')).toBeTruthy()
    expect(screen.getByText('Canada test server')).toBeTruthy()
    expect(screen.queryByText('admin:node_issues.empty_attention')).toBeNull()
    expect(detailButtons()).toHaveLength(1)
  })

  it('does not let an older page response overwrite a newer keyword result', async () => {
    const oldRequests: Array<{ signal: AbortSignal; resolve: (value: unknown) => void }> = []
    api.get.mockImplementation((_url: string, config: { params: NodeIssueListParams; signal: AbortSignal }) => {
      if (config.params.keyword === 'new selection') return Promise.resolve({ data: list([issue(2, { server_name: 'New selection server' })]) })
      return new Promise(resolve => { oldRequests.push({ signal: config.signal, resolve }) })
    })
    mount(<NodeIssuesView />)
    await waitFor(() => expect(oldRequests.length).toBeGreaterThan(0))
    fireEvent.change(searchInput(), { target: { value: 'new selection' } })
    fireEvent.submit(searchInput().closest('form')!)
    expect(await screen.findByText('New selection server')).toBeTruthy()
    expect(oldRequests.every(request => request.signal.aborted)).toBe(true)
    await act(async () => {
      oldRequests.forEach(request => request.resolve({ data: list([issue(1, { server_name: 'Obsolete server' })]) }))
    })
    expect(screen.getByText('New selection server')).toBeTruthy()
    expect(screen.queryByText('Obsolete server')).toBeNull()
    expect(screen.queryByText('admin:node_issues.load_failed')).toBeNull()
  })

  it('renders compact cards, not a wide summary table, on narrow screens', async () => {
    narrowScreen.mockReturnValue(true)
    installReads({ '/admin/node-issues': list([
      issue(407, { key: 'cli_407' }), issue(406, { key: 'cli_406' }),
    ]) })
    mount(<NodeIssuesView />)
    expect(await screen.findByText('Canada test server')).toBeTruthy()
    expect(screen.queryByRole('table')).toBeNull()
    expect(screen.getByText('admin:node_issues.mobile_meta')).toBeTruthy()
    expect(detailButtons()).toHaveLength(1)
    const dialog = await openDetails()
    expect(within(dialog).getByText('cli_407')).toBeTruthy()
    expect(within(dialog).getByText('cli_406')).toBeTruthy()
  })
})
