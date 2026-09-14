// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { NodeAgentIssue } from '@/api/types'
import type { NodeIssueListParams } from '@/api/nodeIssues'
import { api, installReads, list, mount, snack } from '@/test/adminSaveHarness'
import { useAuthStore } from '@/stores/auth'
import { DEFAULT_ASYNC_CONCURRENCY } from '@/utils/promises'
import NodeIssuesView from './NodeIssuesView'

const mocks = vi.hoisted(() => ({ narrow: vi.fn(() => false), confirm: vi.fn() }))
vi.mock('@mui/material', async importOriginal => ({
  ...await importOriginal<typeof import('@mui/material')>(),
  useMediaQuery: mocks.narrow,
}))
vi.mock('@/components/ConfirmHost', () => ({ confirm: mocks.confirm }))

function issue(id: number, overrides: Partial<NodeAgentIssue> = {}): NodeAgentIssue {
  return {
    id, agent_id: 'agt_canada_bulk_test', server_id: 7, server_name: 'Canada bulk test',
    code: 'core_telemetry_failed', key: `cli_${id}`, detail: `Original report ${id}`,
    first_seen_at: '2026-09-14T00:00:00Z', last_seen_at: '2026-09-14T01:00:00Z',
    created_at: '2026-09-14T00:00:00Z', updated_at: '2026-09-14T01:00:00Z',
    ...overrides,
  }
}

const bulkKey = (name: string) => `admin:node_issues.bulk.${name}`
const pageBox = () => screen.getByRole('checkbox', { name: bulkKey('select_page') }) as HTMLInputElement
const groupBoxes = () => screen.getAllByRole('checkbox', { name: bulkKey('select_group') }) as HTMLInputElement[]
const bulkButton = () => screen.getByRole('button', { name: bulkKey('acknowledge') }) as HTMLButtonElement
const reads = () => api.get.mock.calls.filter(([url]) => url === '/admin/node-issues')
const lastParams = () => reads().at(-1)![1].params as NodeIssueListParams
const postedIDs = () => api.post.mock.calls.map(([url]) => Number(String(url).match(/node-issues\/(\d+)\/acknowledge/)?.[1]))

async function waitForList() {
  await waitFor(() => expect(pageBox().disabled).toBe(false))
}

async function openDetails(index = 0) {
  const buttons = await screen.findAllByRole('button', { name: 'admin:node_issues.open_details' })
  fireEvent.click(buttons[index])
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

beforeEach(() => {
  mocks.narrow.mockReturnValue(false)
  mocks.confirm.mockResolvedValue(true)
})

describe('Node issues bulk review keeps original record scope and review semantics', () => {
  it('selects a summary only as its pending original IDs, not another group or an already reviewed report', async () => {
    installReads({ '/admin/node-issues': list([
      issue(407), issue(406), issue(405, { acknowledged_at: '2026-09-14T02:00:00Z' }),
      issue(408, { code: 'object_pending_timeout' }),
    ]) })
    mount(<NodeIssuesView />)
    await waitForList()
    expect(screen.queryByRole('button', { name: bulkKey('acknowledge') })).toBeNull()
    fireEvent.click(groupBoxes()[0])
    expect(pageBox().checked).toBe(false)
    expect(pageBox().getAttribute('data-indeterminate')).toBe('true')
    fireEvent.click(bulkButton())
    await waitFor(() => expect(snack).toHaveBeenCalledWith(bulkKey('done'), 'success'))
    expect(postedIDs()).toEqual([407, 406])
    expect(api.post.mock.calls.map(([, body, config]) => [body, config])).toEqual([
      [undefined, { _skipErrorToast: true }], [undefined, { _skipErrorToast: true }],
    ])
    expect(mocks.confirm).toHaveBeenCalledTimes(1)
    expect(mocks.confirm.mock.calls[0][0]).toMatchObject({
      title: bulkKey('confirm_title'), message: bulkKey('confirm_message'),
    })
    expect(api.put).not.toHaveBeenCalled()
    expect(api.delete).not.toHaveBeenCalled()
    expect(snack.mock.calls.some(([message]) => /recover|resolved|恢复/i.test(message))).toBe(false)
  })

  it('selects only current-page pending IDs even when the API total spans unseen pages', async () => {
    installReads({ '/admin/node-issues': {
      ...list([issue(1), issue(2, { code: 'object_pending_timeout' }), issue(3, { acknowledged_at: '2026-09-14T02:00:00Z' })]),
      total: 80,
    } })
    mount(<NodeIssuesView />)
    await waitForList()
    fireEvent.click(pageBox())
    expect(pageBox().checked).toBe(true)
    fireEvent.click(bulkButton())
    await waitFor(() => expect(snack).toHaveBeenCalledWith(bulkKey('done'), 'success'))
    expect(postedIDs()).toEqual([1, 2])
    expect(screen.queryByRole('button', { name: bulkKey('acknowledge') })).toBeNull()
  })

  it('does not post any review when confirmation is cancelled', async () => {
    mocks.confirm.mockResolvedValue(false)
    installReads({ '/admin/node-issues': list([issue(1), issue(2)]) })
    mount(<NodeIssuesView />)
    await waitForList()
    fireEvent.click(pageBox())
    fireEvent.click(bulkButton())
    await waitFor(() => expect(mocks.confirm).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(bulkButton().disabled).toBe(false))
    expect(pageBox().checked).toBe(true)
    expect(api.post).not.toHaveBeenCalled()
    expect(snack).not.toHaveBeenCalled()
  })

  it('supports individual original-record selection and marks its parent summary indeterminate', async () => {
    installReads({ '/admin/node-issues': list([issue(407), issue(406)]) })
    mount(<NodeIssuesView />)
    await waitForList()
    const dialog = await openDetails()
    const recordBoxes = within(dialog).getAllByRole('checkbox', { name: bulkKey('select_record') })
    fireEvent.click(recordBoxes[1])
    await closeDetails()
    expect(groupBoxes()[0].checked).toBe(false)
    expect(groupBoxes()[0].getAttribute('data-indeterminate')).toBe('true')
    fireEvent.click(bulkButton())
    await waitFor(() => expect(snack).toHaveBeenCalledWith(bulkKey('done'), 'success'))
    expect(postedIDs()).toEqual([406])
  })

  it('removes successful IDs but preserves failed pending IDs for retry after a partial result', async () => {
    const records = [issue(407), issue(406), issue(405)]
    const reviewed = new Set<number>()
    let retry = false
    api.get.mockImplementation(async () => ({ data: list(records.filter(record => !reviewed.has(record.id))) }))
    api.post.mockImplementation(async (url: string) => {
      const id = Number(url.match(/node-issues\/(\d+)\//)?.[1])
      if (id === 406 && !retry) throw new Error('Only this original report failed')
      reviewed.add(id)
      return { data: {} }
    })
    mount(<NodeIssuesView />)
    await waitForList()
    fireEvent.click(pageBox())
    fireEvent.click(bulkButton())
    await waitFor(() => expect(snack).toHaveBeenCalledWith(bulkKey('partial'), 'warning'))
    await waitFor(() => expect(bulkButton().disabled).toBe(false))
    expect(pageBox().checked).toBe(true)
    expect(postedIDs()).toEqual([407, 406, 405])
    expect(snack).not.toHaveBeenCalledWith(bulkKey('done'), 'success')
    const dialog = await openDetails()
    expect(within(dialog).getByText('cli_406')).toBeTruthy()
    expect(within(dialog).queryByText('cli_407')).toBeNull()
    expect(within(dialog).queryByText('cli_405')).toBeNull()
    await closeDetails()
    retry = true
    fireEvent.click(bulkButton())
    await waitFor(() => expect(snack).toHaveBeenCalledWith(bulkKey('done'), 'success'))
    expect(postedIDs()).toEqual([407, 406, 405, 406])
    expect(screen.queryByRole('button', { name: bulkKey('acknowledge') })).toBeNull()
  })

  it('never shows fake success when every original report fails and keeps the full selection', async () => {
    installReads({ '/admin/node-issues': list([issue(1), issue(2)]) })
    api.post.mockRejectedValue(new Error('Review writes unavailable'))
    mount(<NodeIssuesView />)
    await waitForList()
    fireEvent.click(pageBox())
    fireEvent.click(bulkButton())
    await waitFor(() => expect(snack).toHaveBeenCalledWith(bulkKey('partial'), 'warning'))
    await waitFor(() => expect(bulkButton().disabled).toBe(false))
    expect(postedIDs()).toEqual([1, 2])
    expect(pageBox().checked).toBe(true)
    expect(snack).not.toHaveBeenCalledWith(bulkKey('done'), 'success')
    const dialog = await openDetails()
    expect(within(dialog).getAllByRole('checkbox', { name: bulkKey('select_record') })).toHaveLength(2)
    expect(within(dialog).queryByText('admin:node_issues.state.acknowledged')).toBeNull()
  })

  it('clears selection explicitly without making a review request', async () => {
    installReads({ '/admin/node-issues': list([issue(1), issue(2)]) })
    mount(<NodeIssuesView />)
    await waitForList()
    fireEvent.click(pageBox())
    fireEvent.click(screen.getByRole('button', { name: bulkKey('clear') }))
    expect(pageBox().checked).toBe(false)
    expect(screen.queryByRole('button', { name: bulkKey('acknowledge') })).toBeNull()
    expect(api.post).not.toHaveBeenCalled()
  })

  it.each(['page', 'review filter', 'submitted search', 'refresh'] as const)(
    'clears stale selection when changing %s, even if refreshed results reuse the same IDs',
    async action => {
      installReads({ '/admin/node-issues': { ...list([issue(1), issue(2)]), total: 60 } })
      mount(<NodeIssuesView />)
      await waitForList()
      fireEvent.click(pageBox())
      const initialReadCount = reads().length
      if (action === 'page') fireEvent.click(screen.getByRole('button', { name: 'Go to next page' }))
      else if (action === 'review filter') await chooseReview('all')
      else if (action === 'submitted search') {
        const input = screen.getByRole('textbox', { name: 'admin:node_issues.search' })
        fireEvent.change(input, { target: { value: 'Canada' } })
        fireEvent.submit(input.closest('form')!)
      } else fireEvent.click(screen.getByRole('button', { name: 'admin:node_issues.refresh' }))
      await waitFor(() => expect(reads().length).toBeGreaterThan(initialReadCount))
      await waitFor(() => expect(pageBox().checked).toBe(false))
      expect(screen.queryByRole('button', { name: bulkKey('acknowledge') })).toBeNull()
      expect(api.post).not.toHaveBeenCalled()
    },
  )

  it('does not expose selection or bulk writes to read-only accounts', async () => {
    useAuthStore.setState({ role: 'user' })
    installReads({ '/admin/node-issues': list([issue(1), issue(2)]) })
    mount(<NodeIssuesView />)
    await screen.findByText('Canada bulk test')
    expect(screen.queryByRole('checkbox')).toBeNull()
    expect(screen.queryByRole('button', { name: bulkKey('acknowledge') })).toBeNull()
    const dialog = await openDetails()
    expect(within(dialog).queryByRole('checkbox')).toBeNull()
    expect(api.post).not.toHaveBeenCalled()
  })

  it('blocks duplicate confirmation and duplicate writes until the active batch finishes', async () => {
    let resolveConfirm!: (answer: boolean) => void
    let resolvePost!: (value: { data: object }) => void
    mocks.confirm.mockImplementation(() => new Promise<boolean>(resolve => { resolveConfirm = resolve }))
    api.post.mockImplementation(() => new Promise<{ data: object }>(resolve => { resolvePost = resolve }))
    installReads({ '/admin/node-issues': list([issue(1)]) })
    mount(<NodeIssuesView />)
    await waitForList()
    fireEvent.click(pageBox())
    fireEvent.click(bulkButton())
    expect(bulkButton().disabled).toBe(true)
    fireEvent.click(bulkButton())
    expect(mocks.confirm).toHaveBeenCalledTimes(1)
    expect(api.post).not.toHaveBeenCalled()
    await act(async () => { resolveConfirm(true) })
    await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
    expect(bulkButton().disabled).toBe(true)
    expect(pageBox().disabled).toBe(true)
    fireEvent.click(bulkButton())
    expect(api.post).toHaveBeenCalledTimes(1)
    await act(async () => { resolvePost({ data: {} }) })
    await waitFor(() => expect(snack).toHaveBeenCalledWith(bulkKey('done'), 'success'))
    expect(api.post).toHaveBeenCalledTimes(1)
  })

  it('does not begin a confirmed bulk write if operation permission was removed while confirming', async () => {
    let resolveConfirm!: (answer: boolean) => void
    mocks.confirm.mockImplementation(() => new Promise<boolean>(resolve => { resolveConfirm = resolve }))
    installReads({ '/admin/node-issues': list([issue(1)]) })
    mount(<NodeIssuesView />)
    await waitForList()
    fireEvent.click(pageBox())
    fireEvent.click(bulkButton())
    await act(async () => { useAuthStore.setState({ role: 'user' }) })
    await act(async () => { resolveConfirm(true) })
    expect(screen.queryByRole('checkbox')).toBeNull()
    expect(api.post).not.toHaveBeenCalled()
    expect(snack).not.toHaveBeenCalled()
  })

  it('bounds concurrent original-record requests and finishes every selected ID', async () => {
    const records = Array.from({ length: 19 }, (_, index) => issue(index + 1))
    let inFlight = 0
    let maximumInFlight = 0
    let pending: Array<() => void> = []
    api.post.mockImplementation(() => new Promise<{ data: object }>(resolve => {
      inFlight++
      maximumInFlight = Math.max(maximumInFlight, inFlight)
      pending.push(() => { inFlight--; resolve({ data: {} }) })
    }))
    installReads({ '/admin/node-issues': list(records) })
    mount(<NodeIssuesView />)
    await waitForList()
    fireEvent.click(pageBox())
    fireEvent.click(bulkButton())
    await waitFor(() => expect(api.post).toHaveBeenCalledTimes(DEFAULT_ASYNC_CONCURRENCY))
    expect(screen.getByRole('status').textContent).toBe(bulkKey('progress'))
    while (pending.length) {
      const current = pending
      pending = []
      await act(async () => { current.forEach(resolve => resolve()) })
    }
    await waitFor(() => expect(snack).toHaveBeenCalledWith(bulkKey('done'), 'success'))
    expect(maximumInFlight).toBeLessThanOrEqual(DEFAULT_ASYNC_CONCURRENCY)
    expect(inFlight).toBe(0)
    expect(postedIDs()).toEqual(records.map(record => record.id))
    expect(screen.queryByRole('button', { name: bulkKey('acknowledge') })).toBeNull()
  })

  it('keeps last-valid-page recovery after the final filtered report is bulk reviewed', async () => {
    let reviewed = false
    const first = issue(1, { server_name: 'First page server' })
    const last = issue(26, { server_name: 'Last page server' })
    api.get.mockImplementation(async (_url: string, config: { params: NodeIssueListParams }) => ({ data: {
      ...list(config.params.page === 2 ? (reviewed ? [] : [last]) : [first]),
      total: reviewed ? 25 : 26,
    } }))
    api.post.mockImplementation(async () => { reviewed = true; return { data: {} } })
    mount(<NodeIssuesView />)
    await screen.findByText('First page server')
    fireEvent.click(screen.getByRole('button', { name: 'Go to next page' }))
    await screen.findByText('Last page server')
    await waitForList()
    fireEvent.click(pageBox())
    fireEvent.click(bulkButton())
    await waitFor(() => expect(lastParams().page).toBe(1))
    expect(await screen.findByText('First page server')).toBeTruthy()
    expect(screen.queryByText('Last page server')).toBeNull()
    expect(postedIDs()).toEqual([26])
    expect((screen.getByRole('button', { name: 'Go to next page' }) as HTMLButtonElement).disabled).toBe(true)
    expect(screen.queryByRole('button', { name: bulkKey('acknowledge') })).toBeNull()
  })

  it('offers the same per-summary pending selection in mobile cards without a table', async () => {
    mocks.narrow.mockReturnValue(true)
    installReads({ '/admin/node-issues': list([
      issue(407), issue(406), issue(408, { code: 'object_pending_timeout' }),
    ]) })
    mount(<NodeIssuesView />)
    await waitForList()
    expect(screen.queryByRole('table')).toBeNull()
    expect(groupBoxes()).toHaveLength(2)
    fireEvent.click(groupBoxes()[1])
    fireEvent.click(bulkButton())
    await waitFor(() => expect(snack).toHaveBeenCalledWith(bulkKey('done'), 'success'))
    expect(postedIDs()).toEqual([408])
  })
})
