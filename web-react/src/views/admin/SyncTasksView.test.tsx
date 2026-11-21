// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, lastParams, list, mountPage, toast } from './logs/testHarness'
import { useAuthStore } from '@/stores/auth'
import SyncTasksView from './SyncTasksView'

const mount = (entry = '/admin/sync-tasks') => mountPage(<SyncTasksView />, entry, '/admin/sync-tasks')
const here = (m: ReturnType<typeof mount>) => m.router.state.location.pathname + m.router.state.location.search
const deferred = <T,>() => { let resolve!: (v: T) => void; const promise = new Promise<T>(r => { resolve = r }); return { promise, resolve } }

const pending = { id: 8, type: 'node_update', status: 'pending', summary: 'pending-current-write', target_type: 'node', target_id: 4, attempts: 3, next_run_at: '2026-09-20T00:00:00Z', last_error: 'dial tcp: connection refused' }
const retired = { id: 7, type: 'node_update', status: 'retired', summary: 'retired-old-node-write', target_type: 'node', target_id: 3 }
const done = { id: 6, type: 'user_resync', status: 'succeeded', summary: 'finished-write', target_type: 'user', target_id: 2, attempts: 1, next_run_at: '2026-09-19T00:00:00Z' }
const reads = (items: unknown[], total = items.length) => installReads({ '/admin/sync-tasks': list(items, total) })

const rowOf = async (text: string) => (await screen.findByText(text)).closest('tr') as HTMLElement
function openRowMenu(row: HTMLElement) {
  const trigger = within(row).getByRole('button', { name: /More actions for task/ })
  fireEvent.pointerDown(trigger, { button: 0, pointerType: 'mouse' })
  fireEvent.mouseDown(trigger, { button: 0 })
  fireEvent.click(trigger)
}

describe('SyncTasksView', () => {
  it('says why a waiting task is waiting, and shows the next try only for it', async () => {
    reads([pending, done])
    mount()
    const wait = await rowOf(pending.summary)
    expect(within(wait).getByText('Pending')).toBeTruthy()
    expect(within(wait).getByText('Tried 3 times, waiting to retry')).toBeTruthy()
    expect(within(wait).getByText('dial tcp: connection refused')).toBeTruthy()
    expect(within(wait).getByRole('time')).toBeTruthy()
    // A finished task's stored "next run" is a leftover, not a promise.
    expect(within(await rowOf(done.summary)).queryByRole('time')).toBeNull()
    expect(await screen.findByText('2 results')).toBeTruthy()
  })

  it('keeps the status, type and page in the request and in the address', async () => {
    reads([pending], 60)
    const m = mount('/admin/sync-tasks?status=pending&type=node_update&page=2')
    await screen.findByText(pending.summary)
    expect(lastParams('/admin/sync-tasks')).toEqual({ page: 2, page_size: 25, status: 'pending', type: 'node_update' })
    fireEvent.click(screen.getByRole('button', { name: /^Status/ }))
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Succeeded' }))
    await waitFor(() => expect(here(m)).toBe('/admin/sync-tasks?status=succeeded&type=node_update'))
  })

  it('opens a task in a sheet with the whole error and the stored record', async () => {
    reads([pending])
    const m = mount()
    fireEvent.click(await screen.findByText(pending.summary))
    const sheet = await screen.findByRole('dialog')
    expect(here(m)).toBe('/admin/sync-tasks?row=8')
    expect(within(sheet).getAllByText('dial tcp: connection refused').length).toBeGreaterThan(0)
    expect(within(sheet).getByText(/"summary": "pending-current-write"/)).toBeTruthy()
    // The shell's "queued" toast links to this route; the sheet does not change it.
    expect(m.router.state.location.pathname).toBe('/admin/sync-tasks')
  })

  it('retries from the row: one request, busy meanwhile, a second click ignored', async () => {
    reads([pending])
    mount()
    const row = await rowOf(pending.summary)
    const gate = deferred<{ data: unknown }>()
    api.post.mockImplementation(async () => gate.promise)
    const button = within(row).getByRole('button', { name: 'Retry' }) as HTMLButtonElement
    fireEvent.click(button)
    fireEvent.click(button)
    await waitFor(() => expect(button.disabled).toBe(true))
    expect(button.getAttribute('aria-busy')).toBe('true')
    expect(api.post).toHaveBeenCalledTimes(1)
    expect(api.post).toHaveBeenCalledWith('/admin/sync-tasks/8/retry')
    gate.resolve({ data: {} })
    await waitFor(() => expect(button.disabled).toBe(false))
  })

  it('cancels from the row menu only after a confirmation that names the task', async () => {
    reads([pending])
    mount()
    openRowMenu(await rowOf(pending.summary))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Cancel' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText('Cancel task #8 (Node update)? It will not be retried.')).toBeTruthy()
    expect(api.post).not.toHaveBeenCalled()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel task' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/sync-tasks/8/cancel'))
  })

  it('leaves a retired task readable but not actionable, and out of select-all', async () => {
    reads([retired, pending])
    mount()
    const row = await rowOf(retired.summary)
    expect(within(row).getByText('Retired (backend migrated)')).toBeTruthy()
    expect(within(row).queryByRole('checkbox')).toBeNull()
    expect(within(row).getByRole('button', { name: 'Retry' }).getAttribute('aria-disabled')).toBe('true')
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all rows on this page' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Bulk retry' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/sync-tasks/8/retry'))
    expect(api.post.mock.calls.every(([url]) => url === '/admin/sync-tasks/8/retry')).toBe(true)
    await waitFor(() => expect(toast).toHaveBeenCalledWith('Retried 1 tasks', 'success'))
  })

  it('confirms a bulk cancel with the count, then cancels each selected task', async () => {
    reads([pending, { ...pending, id: 9, summary: 'second-write' }])
    mount()
    await screen.findByText(pending.summary)
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all rows on this page' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Bulk cancel' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText('Cancel 2 selected sync tasks?')).toBeTruthy()
    expect(api.post).not.toHaveBeenCalled()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Bulk cancel' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledTimes(2))
    expect(api.post.mock.calls.map(([u]) => u).sort()).toEqual(['/admin/sync-tasks/8/cancel', '/admin/sync-tasks/9/cancel'])
  })

  it('keeps the selection across a refresh of the same list', async () => {
    reads([pending])
    mount()
    const row = await rowOf(pending.summary)
    fireEvent.click(within(row).getByRole('checkbox'))
    const before = api.get.mock.calls.length
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(api.get.mock.calls.length).toBeGreaterThan(before))
    await waitFor(() => expect((within(row).getByRole('checkbox') as HTMLInputElement).getAttribute('aria-checked')).toBe('true'))
  })

  it('purges the finished tasks for an admin, after a confirmation; an operator has no purge', async () => {
    reads([done])
    const m = mount()
    await screen.findByText(done.summary)
    fireEvent.click(screen.getByRole('button', { name: 'More' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Purge finished tasks' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(api.post).not.toHaveBeenCalled()
    api.post.mockResolvedValue({ data: { deleted: 2 } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Purge finished tasks' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/sync-tasks/purge'))
    m.unmount()

    useAuthStore.setState({ role: 'operator', userId: 2, hasToken: true })
    mount()
    await screen.findByText(done.summary)
    expect(screen.queryByRole('button', { name: 'More' })).toBeNull()
    // Retry and cancel stay open to the operator, as before.
    expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy()
  })

  it('reports a failed read instead of an empty queue', async () => {
    installReads({ '/admin/sync-tasks': new Error('offline') })
    mount()
    expect(await screen.findByText('Sync tasks are temporarily unavailable')).toBeTruthy()
    expect(screen.queryByText('No sync tasks')).toBeNull()
  })
})
