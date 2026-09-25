// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, installReads, list, mount, snack } from '@/test/adminSaveHarness'
import RuleSetsView from './RuleSetsView'

const confirmMock = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmMock }))


// A deferred promise the test resolves on its own schedule, standing in for a
// slow (3-10s) upstream response.
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>(res => { resolve = res })
  return { promise, resolve }
}

const customRow = {
  slug: 'custom', name: 'custom-rules', sort: 1, enabled: true, direct_subscription_domain: false,
  proxy_group_order: [], content: 'rules: []',
}
const seededRow = {
  slug: 'default_rules', name: 'seeded-rules', sort: 2, enabled: true, direct_subscription_domain: false,
  proxy_group_order: [], content: 'rules: []',
}

it('keeps the delete icon busy until the request settles, and ignores a second click', async () => {
  installReads({ '/admin/rules': list([customRow]) })
  confirmMock.mockResolvedValue(true)
  const pending = deferred()
  api.delete.mockReturnValue(pending.promise)
  mount(<RuleSetsView />)

  const cell = await screen.findByText('custom-rules')
  const row = cell.closest('tr')!
  const deleteButton = within(row).getByTestId('DeleteOutlinedIcon').closest('button')!
  await waitFor(() => expect((deleteButton as HTMLButtonElement).disabled).toBe(false))

  fireEvent.click(deleteButton)
  // Busy immediately — before the confirm dialog's own promise, let alone the
  // delete request, has had a chance to settle.
  expect((deleteButton as HTMLButtonElement).disabled).toBe(true)
  expect(deleteButton.getAttribute('aria-busy')).toBe('true')

  await waitFor(() => expect(api.delete).toHaveBeenCalledTimes(1))
  // A second click while the row is still busy must not re-open the confirm
  // dialog or fire a second delete.
  fireEvent.click(deleteButton)
  expect(confirmMock).toHaveBeenCalledTimes(1)
  expect(api.delete).toHaveBeenCalledTimes(1)
  expect(within(deleteButton).getByRole('progressbar')).toBeTruthy()

  pending.resolve()
  await waitFor(() => expect((deleteButton as HTMLButtonElement).disabled).toBe(false))
  expect(deleteButton.getAttribute('aria-busy')).toBeNull()
  expect(snack).toHaveBeenCalledWith('admin:rules.toast.deleted', 'success')
})

it('keeps the reset-to-default icon busy until the request settles, and ignores a second click', async () => {
  installReads({ '/admin/rules': list([seededRow]) })
  confirmMock.mockResolvedValue(true)
  const pending = deferred()
  api.post.mockImplementation(async (url: string) => {
    if (url === '/admin/rules/default_rules/reset') return pending.promise
    return { data: { groups: [], builtins: [], nodes: [], regions: [], tags: [], issues: [] } }
  })
  mount(<RuleSetsView />)

  const cell = await screen.findByText('seeded-rules')
  const row = cell.closest('tr')!
  const resetButton = within(row).getByTestId('RestartAltIcon').closest('button')!
  await waitFor(() => expect((resetButton as HTMLButtonElement).disabled).toBe(false))

  fireEvent.click(resetButton)
  expect((resetButton as HTMLButtonElement).disabled).toBe(true)
  expect(resetButton.getAttribute('aria-busy')).toBe('true')

  await waitFor(() => expect(confirmMock).toHaveBeenCalledTimes(1))
  fireEvent.click(resetButton)
  expect(confirmMock).toHaveBeenCalledTimes(1)
  expect(within(resetButton).getByRole('progressbar')).toBeTruthy()

  pending.resolve()
  await waitFor(() => expect((resetButton as HTMLButtonElement).disabled).toBe(false))
  expect(snack).toHaveBeenCalledWith('admin:rules.toast.reset', 'success')
})
