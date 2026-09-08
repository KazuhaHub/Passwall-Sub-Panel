// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it } from 'vitest'
import { editRow, installReads, list, mount } from '@/test/adminSaveHarness'
import RuleSetsView from './RuleSetsView'

const row = { slug: 'custom', name: 'old-name', sort: 1, enabled: true, proxy_group_order: [], content: 'rules: []' }

it('reopens with the saved rule without reloading the stale list', async () => {
  installReads({ '/admin/rules': list([row]) })
  mount(<RuleSetsView />)
  const dialog = await editRow()
  fireEvent.change(within(dialog).getByDisplayValue('old-name'), { target: { value: 'new-name' } })

  fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))

  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  const reopened = await editRow('new-name')
  expect(within(reopened).getByDisplayValue('new-name')).toBeTruthy()
})
