// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, installReads, list, mount } from '@/test/adminSaveHarness'
import SyncTasksView from './SyncTasksView'

// Purge is destructive and gated by ConfirmHost, which is not mounted in this
// harness; stub it to resolve true so the underlying request still fires and
// its busy state is observable.
vi.mock('@/components/ConfirmHost', () => ({ confirm: vi.fn(async () => true) }))

// SLOW-NETWORK FEEDBACK: on a 3-10s link, a click that gets no visible
// response reads as a dead button and invites a duplicate request. These
// cases hold the underlying request open (a promise the test resolves by
// hand) and assert the control goes busy immediately and ignores a second
// click while the request is in flight.

function deferred<T = unknown>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>(res => { resolve = res })
  return { promise, resolve }
}

const pending1 = { id: 1, type: 'node_update', status: 'pending', summary: 'first-pending-task', target_type: 'node', target_id: 1 }

it('shows the Refresh button as busy while a refetch is in flight, and does not double-fetch on a second click', async () => {
  installReads({ '/admin/sync-tasks': list([pending1]) })
  mount(<SyncTasksView />)
  await screen.findByText(pending1.summary)

  // The initial load has already resolved (isPending is false); a manual
  // refresh now drives isFetching, which is what the Refresh button must key
  // off (isPending stays false once data exists).
  const gate = deferred<{ data: unknown }>()
  const callsBeforeClick = api.get.mock.calls.length
  api.get.mockImplementation(async () => gate.promise)

  const button = screen.getByRole('button', { name: 'admin:sync_tasks.refresh' })
  fireEvent.click(button)
  fireEvent.click(button)

  await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(true))
  expect(button.getAttribute('aria-busy')).toBe('true')
  expect(within(button).getByRole('progressbar')).toBeTruthy()
  // Only one new GET must have been issued despite the second click.
  expect(api.get.mock.calls.length).toBe(callsBeforeClick + 1)

  gate.resolve({ data: list([pending1]) })
})

it('Refresh button recovers once the refetch settles', async () => {
  installReads({ '/admin/sync-tasks': list([pending1]) })
  mount(<SyncTasksView />)
  await screen.findByText(pending1.summary)
  const gate = deferred<{ data: unknown }>()
  api.get.mockImplementation(async () => gate.promise)
  const button = screen.getByRole('button', { name: 'admin:sync_tasks.refresh' })
  fireEvent.click(button)
  await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(true))
  gate.resolve({ data: list([pending1]) })
  await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false))
})

it('shows a per-row Retry action as busy while it is in flight and ignores a second click', async () => {
  installReads({ '/admin/sync-tasks': list([pending1]) })
  mount(<SyncTasksView />)
  const row = (await screen.findByText(pending1.summary)).closest('tr')!

  const gate = deferred<{ data: unknown }>()
  api.post.mockImplementation(async () => gate.promise)

  const retryButton = within(row).getByTestId('ReplayIcon').closest('button') as HTMLButtonElement
  fireEvent.click(retryButton)
  fireEvent.click(retryButton)

  await waitFor(() => expect(retryButton.disabled).toBe(true))
  expect(retryButton.getAttribute('aria-busy')).toBe('true')
  expect(within(retryButton).getByRole('progressbar')).toBeTruthy()
  expect(api.post.mock.calls.length).toBe(1)
  expect(api.post).toHaveBeenCalledWith(`/admin/sync-tasks/${pending1.id}/retry`)

  gate.resolve({ data: {} })
})

it('Retry action recovers once the request settles', async () => {
  installReads({ '/admin/sync-tasks': list([pending1]) })
  mount(<SyncTasksView />)
  const row = (await screen.findByText(pending1.summary)).closest('tr')!
  const gate = deferred<{ data: unknown }>()
  api.post.mockImplementation(async () => gate.promise)
  const retryButton = within(row).getByTestId('ReplayIcon').closest('button') as HTMLButtonElement
  fireEvent.click(retryButton)
  await waitFor(() => expect(retryButton.disabled).toBe(true))
  gate.resolve({ data: {} })
  await waitFor(() => expect(retryButton.disabled).toBe(false))
})

it('shows the per-row Cancel action as busy while it is in flight and ignores a second click', async () => {
  installReads({ '/admin/sync-tasks': list([pending1]) })
  mount(<SyncTasksView />)
  const row = (await screen.findByText(pending1.summary)).closest('tr')!

  const gate = deferred<{ data: unknown }>()
  api.post.mockImplementation(async () => gate.promise)

  const cancelButton = within(row).getByTestId('CloseIcon').closest('button') as HTMLButtonElement
  fireEvent.click(cancelButton)
  fireEvent.click(cancelButton)

  await waitFor(() => expect(cancelButton.disabled).toBe(true))
  expect(cancelButton.getAttribute('aria-busy')).toBe('true')
  expect(api.post.mock.calls.length).toBe(1)
  expect(api.post).toHaveBeenCalledWith(`/admin/sync-tasks/${pending1.id}/cancel`)

  gate.resolve({ data: {} })
})

it('Cancel action recovers once the request settles', async () => {
  installReads({ '/admin/sync-tasks': list([pending1]) })
  mount(<SyncTasksView />)
  const row = (await screen.findByText(pending1.summary)).closest('tr')!
  const gate = deferred<{ data: unknown }>()
  api.post.mockImplementation(async () => gate.promise)
  const cancelButton = within(row).getByTestId('CloseIcon').closest('button') as HTMLButtonElement
  fireEvent.click(cancelButton)
  await waitFor(() => expect(cancelButton.disabled).toBe(true))
  gate.resolve({ data: {} })
  await waitFor(() => expect(cancelButton.disabled).toBe(false))
})

it('shows the Purge button as busy while the purge request is in flight', async () => {
  installReads({ '/admin/sync-tasks': list([pending1]) })
  mount(<SyncTasksView />)
  await screen.findByText(pending1.summary)

  const gate = deferred<{ data: unknown }>()
  api.post.mockImplementation(async () => gate.promise)

  const purgeButton = screen.getByRole('button', { name: 'admin:sync_tasks.purge' }) as HTMLButtonElement
  fireEvent.click(purgeButton)
  fireEvent.click(purgeButton)

  await waitFor(() => expect(purgeButton.disabled).toBe(true))
  expect(purgeButton.getAttribute('aria-busy')).toBe('true')
  // confirm() resolves true synchronously (mocked), so exactly one purge POST
  // should have been issued despite the second click.
  expect(api.post.mock.calls.length).toBe(1)

  gate.resolve({ data: { deleted: 1 } })
})

it('Purge button recovers once the purge request settles', async () => {
  installReads({ '/admin/sync-tasks': list([pending1]) })
  mount(<SyncTasksView />)
  await screen.findByText(pending1.summary)
  const gate = deferred<{ data: unknown }>()
  api.post.mockImplementation(async () => gate.promise)
  const purgeButton = screen.getByRole('button', { name: 'admin:sync_tasks.purge' }) as HTMLButtonElement
  fireEvent.click(purgeButton)
  await waitFor(() => expect(purgeButton.disabled).toBe(true))
  gate.resolve({ data: { deleted: 1 } })
  await waitFor(() => expect(purgeButton.disabled).toBe(false))
})
