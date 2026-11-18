// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { AxiosError } from 'axios'
import { describe, expect, it, vi } from 'vitest'
import { api, installReads, list, mount, server, snack } from '@/test/adminSaveHarness'
import ServersView from './ServersView'

const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))

const refusal = (data: Record<string, unknown>) => new AxiosError('x', '409', undefined, undefined, { status: 409, statusText: '', headers: {}, config: {} as never, data })

async function deleteRow() {
  const row = (await screen.findByText('server')).closest('tr')!
  fireEvent.click(within(row).getByRole('button', { name: 'admin:servers.action.delete' }))
}

describe('ServersView delete refused for nodes', () => {
  it('lists the blocking nodes and names a hidden listener by its line', async () => {
    installReads({ '/admin/servers': list([server]) })
    confirmation.mockResolvedValue(true)
    api.delete.mockRejectedValue(refusal({ code: 'server_has_nodes', error: 'server has nodes', nodes: [
      { node_id: 1, node: 'hk-node' },
      { node_id: 77, node: 'tunnel-77', tunnel_line_of: 'US-1', tunnel_line: 'Mobile', tunnel_line_of_node_id: 5 },
    ] }))
    mount(<ServersView />)
    await deleteRow()
    const listing = await screen.findByTestId('blocking-nodes')
    expect(within(listing).getAllByRole('listitem')).toHaveLength(2)
    expect(within(listing).getByText('admin:servers.confirm.has_nodes_item')).toBeTruthy()
    expect(within(listing).getByText('admin:servers.confirm.has_nodes_tunnel_item')).toBeTruthy()
    expect(api.delete).toHaveBeenCalledWith('/admin/servers/1', expect.objectContaining({ _skipErrorToast: true }))
  })

  it('shows the error text for any other refusal', async () => {
    installReads({ '/admin/servers': list([server]) })
    confirmation.mockResolvedValue(true)
    api.delete.mockRejectedValue(refusal({ error: 'cannot delete' }))
    mount(<ServersView />)
    await deleteRow()
    await waitFor(() => expect(snack).toHaveBeenCalledWith('cannot delete', 'error'))
    expect(screen.queryByTestId('blocking-nodes')).toBeNull()
  })

  it('says the server could not be deleted when the failure has no message', async () => {
    installReads({ '/admin/servers': list([server]) })
    confirmation.mockResolvedValue(true)
    api.delete.mockRejectedValue(new Error(''))
    mount(<ServersView />)
    await deleteRow()
    await waitFor(() => expect(snack).toHaveBeenCalledWith('admin:servers.toast.delete_failed', 'error'))
  })
})
