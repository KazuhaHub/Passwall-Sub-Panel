// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it } from 'vitest'
import { api, deferred, editRow, installReads, list, mount } from '@/test/adminSaveHarness'
import RuleSetsView from './RuleSetsView'

const row = { slug: 'custom', name: 'old-name', sort: 1, enabled: true, proxy_group_order: [], content: 'rules: []' }

it('reopens with the saved rule while the background list refresh is pending', async () => {
  installReads({ '/admin/rules': list([row]) })
  mount(<RuleSetsView />)
  const dialog = await editRow()
  fireEvent.change(within(dialog).getByDisplayValue('old-name'), { target: { value: 'new-name' } })

  const pending = deferred<{ data: unknown }>()
  const normalGet = api.get.getMockImplementation()!
  api.get.mockImplementation((url: string, ...args: unknown[]) =>
    url === '/admin/rules' ? pending.promise : normalGet(url, ...args),
  )
  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))

  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  const reopened = await editRow('new-name')
  expect(within(reopened).getByDisplayValue('new-name')).toBeTruthy()

  await act(async () => pending.resolve({ data: list([{ ...row, name: 'new-name' }]) }))
})
