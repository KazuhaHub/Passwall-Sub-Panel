// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, list, mount, node, server } from '@/test/adminSaveHarness'
import ConfirmHost from '@/components/ConfirmHost'
import NodesView from './NodesView'

/**
 * SLOW-NETWORK FEEDBACK for the managed-node row actions: a delete/detach/
 * recreate-inbound click that only disables the icon after the round trip
 * reads as dead on a 3-10s link. These pin that each icon shows its own busy
 * state for the whole request and swallows a duplicate click, and that the
 * claim dialog's user picker never renders as an empty list while it is
 * still loading.
 */

function deferred<T = void>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(res => { resolve = res })
  return { promise, resolve }
}

// The capability set the "delete" / "recreate inbound" icons gate on — the
// shared harness fixture omits inbound.delete on purpose (other suites pin
// that the icon disables without it), so these tests add it back.
const fullyCapableServer = {
  ...server,
  capabilities: ['inbound.read', 'inbound.update', 'inbound.create', 'inbound.enable', 'inbound.delete'],
}


async function rowFor(name: string) {
  return (await screen.findByText(name)).closest('tr')!
}

describe('NodesView managed-row actions', () => {
  it('shows busy on the delete icon for the whole request and ignores a second click', async () => {
    installReads({ '/admin/nodes': list([node]), '/admin/servers': list([fullyCapableServer]) })
    const gate = deferred<{ data: unknown }>()
    api.delete.mockReturnValue(gate.promise)
    mount(<><ConfirmHost /><NodesView /></>)

    const row = await rowFor(node.display_name)
    const deleteButton = within(row).getByTestId('DeleteOutlinedIcon').closest('button')!
    await waitFor(() => expect(deleteButton.disabled).toBe(false))
    fireEvent.click(deleteButton)

    const confirmDialog = await screen.findByRole('dialog')
    fireEvent.click(within(confirmDialog).getByRole('button', { name: 'admin:nodes.action.delete' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    await waitFor(() => expect(deleteButton.getAttribute('aria-busy')).toBe('true'))
    expect(deleteButton.disabled).toBe(true)
    expect(screen.getByRole('progressbar')).toBeTruthy()

    fireEvent.click(deleteButton)
    expect(api.delete).toHaveBeenCalledTimes(1)

    gate.resolve({ data: {} })
    await waitFor(() => expect(deleteButton.getAttribute('aria-busy')).toBeNull())
  })

  it('shows busy on the detach icon for the whole request and ignores a second click', async () => {
    installReads({ '/admin/nodes': list([node]), '/admin/servers': list([fullyCapableServer]) })
    const gate = deferred<{ data: unknown }>()
    api.post.mockImplementation((url: string) => url.endsWith('/detach') ? gate.promise : Promise.resolve({ data: {} }))
    mount(<><ConfirmHost /><NodesView /></>)

    const row = await rowFor(node.display_name)
    const detachButton = within(row).getByTestId('LinkOffIcon').closest('button')!
    fireEvent.click(detachButton)

    const confirmDialog = await screen.findByRole('dialog')
    fireEvent.click(within(confirmDialog).getByRole('button', { name: 'admin:nodes.action.detach' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    await waitFor(() => expect(detachButton.getAttribute('aria-busy')).toBe('true'))
    expect(detachButton.disabled).toBe(true)
    expect(screen.getByRole('progressbar')).toBeTruthy()

    fireEvent.click(detachButton)
    expect(api.post.mock.calls.filter(([url]) => url.endsWith('/detach'))).toHaveLength(1)

    gate.resolve({ data: {} })
    await waitFor(() => expect(detachButton.getAttribute('aria-busy')).toBeNull())
  })

  it('shows busy on the recreate-inbound icon for the whole request and ignores a second click', async () => {
    installReads({ '/admin/nodes': list([node]), '/admin/servers': list([fullyCapableServer]) })
    const gate = deferred<{ data: unknown }>()
    api.post.mockImplementation((url: string) => url.endsWith('/recreate-inbound') ? gate.promise : Promise.resolve({ data: {} }))
    mount(<><ConfirmHost /><NodesView /></>)

    const row = await rowFor(node.display_name)
    const recreateButton = within(row).getByTestId('CloudSyncIcon').closest('button')!
    fireEvent.click(recreateButton)

    const confirmDialog = await screen.findByRole('dialog')
    fireEvent.click(within(confirmDialog).getByRole('button', {
      name: 'admin:nodes.action.recreate_inbound',
    }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    await waitFor(() => expect(recreateButton.getAttribute('aria-busy')).toBe('true'))
    expect(recreateButton.disabled).toBe(true)
    expect(screen.getByRole('progressbar')).toBeTruthy()

    fireEvent.click(recreateButton)
    expect(api.post.mock.calls.filter(([url]) => url.endsWith('/recreate-inbound'))).toHaveLength(1)

    gate.resolve({ data: {} })
    await waitFor(() => expect(recreateButton.getAttribute('aria-busy')).toBeNull())
  })
})

describe('NodesView claim dialog', () => {
  it('shows a loading state for the user picker instead of an empty list', async () => {
    const unmanaged = {
      PanelID: 1, PanelName: 'server', InboundID: 9, Protocol: 'vless', Port: 443,
      Remark: 'legacy', Enable: true, ClientCount: 2,
    }
    installReads({
      '/admin/nodes': list([]),
      '/admin/servers': list([fullyCapableServer]),
      '/admin/nodes/unmanaged': list([unmanaged]),
    })
    const gate = deferred<{ data: unknown }>()
    api.get.mockImplementation((url: string) => {
      if (url === '/admin/users') return gate.promise
      const values: Record<string, unknown> = {
        '/admin/nodes': list([]),
        '/admin/nodes/separator': list([]),
        '/admin/servers': list([fullyCapableServer]),
        '/admin/nodes/unmanaged': list([unmanaged]),
      }
      if (url in values) return Promise.resolve({ data: values[url] })
      throw new Error(`Unexpected GET ${url}`)
    })
    mount(<NodesView />)

    fireEvent.click(screen.getByRole('tab', { name: 'admin:nodes.tab_unmanaged' }))
    const combobox = await screen.findByRole('combobox', { name: 'admin:nodes.unmanaged_server_label' })
    fireEvent.mouseDown(combobox)
    fireEvent.click(await screen.findByRole('option', { name: fullyCapableServer.name }))

    fireEvent.click(await screen.findByRole('button', { name: 'admin:nodes.claim' }))

    const dialog = await screen.findByRole('dialog')
    // While listUsers is still in flight the picker must say so rather than
    // rendering the same empty-placeholder-only Select a truly empty result
    // would show — an operator can't tell "no users" from "not loaded yet".
    expect(within(dialog).getByText('common:status.loading')).toBeTruthy()
    expect(within(dialog).getByRole('combobox').getAttribute('aria-disabled')).toBe('true')

    gate.resolve({ data: { items: [{ id: 2, upn: 'user@example.test', display_name: 'A User' }], total: 1, page: 1, page_size: 200 } })
    await waitFor(() => expect(within(dialog).queryByText('common:status.loading')).toBeNull())
    expect(within(dialog).getByRole('combobox').getAttribute('aria-disabled')).not.toBe('true')
    fireEvent.mouseDown(within(dialog).getByRole('combobox'))
    expect(await screen.findByRole('option', { name: 'A User (user@example.test)' })).toBeTruthy()
  })
})
