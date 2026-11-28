// @vitest-environment jsdom
import { fireEvent, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { installReads, list, mount, server } from './testHarness'
import { openMenuOf } from './install/installationHarness'
import ServersPage from './ServersPage'

describe('ServersPage line multipliers', () => {
  // Batch editing of line multipliers moved to the Nodes page (its own dialog and tests live there).
  // Nothing in the Servers area offers it any more.
  it('offers no line multipliers in the row menu or on the server page', async () => {
    installReads({ '/admin/servers': list([server]) })
    mount(<ServersPage />)
    await openMenuOf(server as never)
    expect(screen.queryByRole('menuitem', { name: 'admin:servers.action.line_factors' })).toBeNull()
    fireEvent.click(screen.getByRole('menuitem', { name: 'admin:servers.action.open' }))
    for (const tab of ['overview', 'settings', 'maintenance']) {
      fireEvent.click(await screen.findByRole('tab', { name: `admin:servers.detail.tabs.${tab}` }))
      expect(screen.queryByText('admin:servers.action.line_factors')).toBeNull()
    }
  })
})

describe('ServersPage client identities', () => {
  it('shows the count and a warning chip only when flagged', async () => {
    installReads({ '/admin/servers': list([
      { ...server, id: 1, name: 'calm', client_identities: 12 },
      { ...server, id: 2, name: 'busy', client_identities: 21000, client_identities_warning: true },
    ]) })
    mount(<ServersPage />)
    const busy = (await screen.findByText('busy', { exact: false })).closest('td')!
    const calm = (await screen.findByText('calm', { exact: false })).closest('td')!
    expect(within(busy).getByTestId('client-identities-warning')).toBeTruthy()
    expect(within(calm).queryByTestId('client-identities-warning')).toBeNull()
    expect(within(calm).getByTestId('client-identities')).toBeTruthy()
  })
})
