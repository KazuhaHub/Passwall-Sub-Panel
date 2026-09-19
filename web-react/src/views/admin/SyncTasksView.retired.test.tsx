// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it } from 'vitest'
import { api, installReads, list, mount } from '@/test/adminSaveHarness'
import SyncTasksView from './SyncTasksView'

const retired = { id: 7, type: 'node_update', status: 'retired', summary: 'retired-old-node-write', target_type: 'node', target_id: 3 }
const pending = { id: 8, type: 'node_update', status: 'pending', summary: 'pending-current-write', target_type: 'node', target_id: 4 }

it('keeps retired tasks visible with detail/purge but disables individual actions and selection', async () => {
  installReads({ '/admin/sync-tasks': list([retired]) })
  mount(<SyncTasksView />)
  const row = (await screen.findByText(retired.summary)).closest('tr')!
  expect(within(row).getByText('admin:sync_tasks.status.retired')).toBeTruthy()
  expect((within(row).getByRole('checkbox') as HTMLInputElement).disabled).toBe(true)
  expect((within(row).getByTestId('ReplayIcon').closest('button') as HTMLButtonElement).disabled).toBe(true)
  expect((within(row).getByTestId('CloseIcon').closest('button') as HTMLButtonElement).disabled).toBe(true)
  expect(screen.getByRole('button', { name: 'admin:sync_tasks.purge' })).toBeTruthy()
  fireEvent.click(within(row).getByTestId('VisibilityIcon').closest('button')!)
  await screen.findByRole('dialog')
  expect(api.post).not.toHaveBeenCalled()
})

it('excludes retired tasks from select-all and bulk retry calls', async () => {
  installReads({ '/admin/sync-tasks': list([retired, pending]) })
  mount(<SyncTasksView />)
  await screen.findByText(pending.summary)
  fireEvent.click(screen.getAllByRole('checkbox')[0])
  const retiredRow = screen.getByText(retired.summary).closest('tr')!
  expect((within(retiredRow).getByRole('checkbox') as HTMLInputElement).checked).toBe(false)
  fireEvent.click(screen.getByRole('button', { name: 'admin:sync_tasks.batch_retry' }))
  await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/sync-tasks/8/retry'))
  expect(api.post.mock.calls.every(([url]) => url === '/admin/sync-tasks/8/retry')).toBe(true)
})

it('keeps the batch selection across a same-scope refresh', async () => {
  // The loader used to clear the selection on every read, so a plain refresh
  // dropped a batch an operator was assembling. The selection may only reset
  // when the QUERY changes (page / filters).
  installReads({ '/admin/sync-tasks': list([pending]) })
  // The harness returns the same array instance every call, which React's
  // state bail-out hides; clone it so a refresh really delivers new objects.
  const base = api.get.getMockImplementation()!
  api.get.mockImplementation(async (url: string) => {
    const res = await base(url)
    if (url !== '/admin/sync-tasks') return res
    return { data: { ...res.data, items: res.data.items.map((r: object) => ({ ...r })) } }
  })
  mount(<SyncTasksView />)

  const row = (await screen.findByText(pending.summary)).closest('tr')!
  const box = within(row).getByRole('checkbox') as HTMLInputElement
  fireEvent.click(box)
  await waitFor(() => expect(box.checked).toBe(true))

  const before = api.get.mock.calls.length
  fireEvent.click(screen.getByRole('button', { name: 'admin:sync_tasks.refresh' }))
  await waitFor(() => expect(api.get.mock.calls.length).toBeGreaterThan(before))

  // Re-query the row: a detached input keeps its `checked` value, so asserting
  // on the pre-refresh node would pass even if the row were unmounted.
  const refreshed = (await screen.findByText(pending.summary)).closest('tr')!
  await waitFor(() =>
    expect((within(refreshed).getByRole('checkbox') as HTMLInputElement).checked).toBe(true),
  )
})
