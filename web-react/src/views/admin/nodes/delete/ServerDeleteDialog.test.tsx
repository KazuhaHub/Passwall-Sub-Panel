// @vitest-environment jsdom
import { QueryClientProvider } from '@tanstack/react-query'
import { AxiosError } from 'axios'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { api, installReads } from '../testHarness'
import { ServerDeleteDialog } from './ServerDeleteDialog'

afterEach(cleanup)
const empty = { server_id: 3, nodes: [], lines_entering: [], egress_users: [], route_users: [] }
const entering = (pending: boolean) => ({ node_id: 4, line_id: 'x', landing_name: 'Tokyo', line_name: 'Via GZ', line_type: 'forward', landing_pending_delete: pending })
const refusal = (data: Record<string, unknown>) => new AxiosError('x', '409', undefined, undefined, { status: 409, statusText: '', headers: {}, config: {} as never, data })

function open() {
  const onDeleted = vi.fn()
  render(
    <QueryClientProvider client={makeTestQueryClient()}><MemoryRouter>
      <ServerDeleteDialog server={{ id: 3, name: 'GZ' }} onClose={vi.fn()} onDeleted={onDeleted} />
    </MemoryRouter></QueryClientProvider>,
  )
  return onDeleted
}

describe('the server delete dialog', () => {
  it('blocks on nodes, entering lines and routes: lists each with where to fix it, and offers no delete button', async () => {
    installReads({ '/admin/servers/3/delete-impact': {
      ...empty,
      nodes: [{ node_id: 77, node: 'tunnel-77', tunnel_line_of_node_id: 5, tunnel_line_of: 'US-1', tunnel_line: 'Mobile' }],
      lines_entering: [entering(true)],
      route_users: [{ node_id: 8, node: 'Osaka', routes: ['r1'] }],
    } })
    open()
    expect(await screen.findByText('Server "GZ" cannot be deleted: 3 items depend on it')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Delete server' })).toBeNull()
    expect(within(screen.getByTestId('server-nodes')).getByText('US-1 · Mobile (entry listener)')).toBeTruthy()
    expect(within(screen.getByTestId('server-lines')).getAllByRole('listitem')).toHaveLength(1)
    // A line whose landing is being deleted says to wait for its sync task, with a link to the tasks.
    expect(within(screen.getByTestId('server-lines')).getByRole('link', { name: 'Sync tasks' }).getAttribute('href')).toBe('/admin/sync-tasks')
    expect(within(screen.getByTestId('server-lines')).getByRole('link', { name: 'View line' }).getAttribute('href')).toBe('/admin/nodes?line=4.x')
    expect(within(screen.getByTestId('server-routes')).getByRole('link', { name: 'View exit' }).getAttribute('href')).toBe('/admin/nodes/8?tab=exit')
  })

  it('warns about nodes that use the server as their exit but does not block on them, and deletes after the confirm', async () => {
    installReads({ '/admin/servers/3/delete-impact': { ...empty, egress_users: [{ node_id: 1, node: 'Tokyo' }] } })
    api.delete.mockResolvedValue({ data: null })
    const onDeleted = open()
    expect(await screen.findByTestId('server-egress')).toBeTruthy()
    expect(api.delete).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Delete server' }))
    await waitFor(() => expect(onDeleted).toHaveBeenCalled())
    expect(api.delete.mock.calls[0][0]).toBe('/admin/servers/3')
  })

  it('answers a 409 server_has_lines at delete time by reading again and showing the lines, with no error toast', async () => {
    let reads = 0
    api.get.mockImplementation(async () => ({ data: reads++ === 0 ? empty : { ...empty, lines_entering: [entering(true)] } }))
    api.delete.mockRejectedValue(refusal({ code: 'server_has_lines', error: 'lines', lines: [entering(true)] }))
    const onDeleted = open()
    fireEvent.click(await screen.findByRole('button', { name: 'Delete server' }))
    expect(await screen.findByText('Server "GZ" cannot be deleted: 1 item depends on it')).toBeTruthy()
    expect(screen.getByText(/landing node is being deleted/)).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Delete server' })).toBeNull()
    expect(onDeleted).not.toHaveBeenCalled()
  })
})
