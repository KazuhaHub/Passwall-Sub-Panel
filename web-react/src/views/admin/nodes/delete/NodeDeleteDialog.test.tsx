// @vitest-environment jsdom
import { QueryClientProvider } from '@tanstack/react-query'
import { AxiosError } from 'axios'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { StrictMode } from 'react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { NodeDeleteImpact } from '@/api/nodes'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { api, installReads } from '../testHarness'
import { NodeDeleteDialog, type NodeDeleteTarget } from './NodeDeleteDialog'

afterEach(cleanup)

const line = (id: string, type: 'direct' | 'forward' | 'tunnel' | 'cdn', name: string) => ({ node_id: 5, line_id: id, landing_name: 'Tokyo', line_name: name, line_type: type })
const impact = (over: Partial<NodeDeleteImpact> = {}): NodeDeleteImpact => ({
  node: { id: 5, name: 'Tokyo' },
  lines: [line('direct', 'direct', 'Tokyo'), line('a', 'forward', 'GZ'), line('b', 'tunnel', 'SH'), line('c', 'cdn', 'CF')],
  tunnels: [{ node_id: 9, line_id: 'b', line_name: 'SH', entry_panel_id: 2, port: 31002 }],
  blocked_by_routes: [], ...over,
})
const refusal = (data: Record<string, unknown>) => new AxiosError('x', '409', undefined, undefined, { status: 409, statusText: '', headers: {}, config: {} as never, data })
const tunnelPlan = (removes: unknown[]) => refusal({ error: 'e', code: 'confirm_required', params: { kind: 'tunnel', action: 'delete_node', plan: 'p1', removes } })

function open(over: Partial<NodeDeleteTarget> = {}, extra: { onDone?: () => void; onClose?: () => void } = {}) {
  const onDone = extra.onDone ?? vi.fn()
  const onClose = extra.onClose ?? vi.fn()
  render(
    <StrictMode><QueryClientProvider client={makeTestQueryClient()}><MemoryRouter>
      <NodeDeleteDialog target={{ nodeId: 5, name: 'Tokyo', mode: 'delete', ...over }} entryOf={l => (l.line_id === 'a' ? 'GZ' : undefined)}
        serverName={id => `srv${id}`} onClose={onClose} onDone={onDone} />
    </MemoryRouter></QueryClientProvider></StrictMode>,
  )
  return { onDone, onClose }
}
const deletes = () => api.delete.mock.calls

describe('the node delete dialog', () => {
  it('reads the impact first, draws every line, names the verb and count, and sends nothing until confirmed', async () => {
    installReads({ '/admin/nodes/5/delete-impact': impact() })
    open()
    // While the impact is unread there is no delete button.
    expect(screen.queryByRole('button', { name: /^Delete node/ })).toBeNull()
    const button = await screen.findByRole('button', { name: 'Delete node and 4 lines' })
    expect(within(screen.getByTestId('impact-lines')).getAllByRole('listitem')).toHaveLength(4)
    expect(screen.getByTestId('impact-listeners').textContent).toContain('srv2')
    expect(screen.getByTestId('impact-listeners').textContent).toContain('31002')
    // Focus opens on Cancel, never on the destructive button.
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Cancel' })))
    expect(deletes()).toHaveLength(0)
    expect(button).toBeTruthy()
  })

  it('deletes only after the confirm, and closes the dialog when the server accepts', async () => {
    installReads({ '/admin/nodes/5/delete-impact': impact({ tunnels: [] }) })
    api.delete.mockResolvedValue({ data: null })
    const { onDone } = open()
    fireEvent.click(await screen.findByRole('button', { name: 'Delete node and 4 lines' }))
    await waitFor(() => expect(onDone).toHaveBeenCalled())
    expect(deletes()).toHaveLength(1)
    expect(deletes()[0][0]).toBe('/admin/nodes/5')
  })

  it('accepts the server plan without asking again when it only closes the listeners the dialog listed', async () => {
    installReads({ '/admin/nodes/5/delete-impact': impact() })
    api.delete.mockRejectedValueOnce(tunnelPlan([{ node_id: 9, line_id: 'b', entry_panel_id: 2, port: 31002 }])).mockResolvedValueOnce({ data: null })
    const { onDone } = open()
    fireEvent.click(await screen.findByRole('button', { name: 'Delete node and 4 lines' }))
    await waitFor(() => expect(onDone).toHaveBeenCalled())
    expect(deletes()).toHaveLength(2)
    expect(deletes()[1][1]).toEqual({ params: { confirm_tunnels: 'true', confirm_plan: 'p1' } })
  })

  it('shows a plan that lists something the dialog did not, inside the same dialog, and sends nothing if declined', async () => {
    installReads({ '/admin/nodes/5/delete-impact': impact() })
    api.delete.mockRejectedValueOnce(tunnelPlan([{ node_id: 11, line_id: 'zz', entry_panel_id: 3, port: 40000 }]))
    const { onDone } = open()
    fireEvent.click(await screen.findByRole('button', { name: 'Delete node and 4 lines' }))
    expect(await screen.findByTestId('impact-plan')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByTestId('impact-plan')).toBeNull())
    expect(deletes()).toHaveLength(1)
    expect(onDone).not.toHaveBeenCalled()
  })

  it('offers no delete button when shared-port routes block it, and says what to do instead', async () => {
    installReads({ '/admin/nodes/5/delete-impact': impact({ blocked_by_routes: ['shared port 443 on hk1, route a.com'] }) })
    open()
    expect(await screen.findByText('Node "Tokyo" cannot be deleted')).toBeTruthy()
    expect(screen.getByTestId('blocked-routes').textContent).toContain('shared port 443 on hk1, route a.com')
    expect(screen.queryByRole('button', { name: /^Delete node/ })).toBeNull()
    expect(screen.getByRole('button', { name: 'Go to servers' }).getAttribute('href')).toBe('/admin/servers')
  })

  it('says so when the impact cannot be read: retry, or an explicitly named delete without the check', async () => {
    installReads({ '/admin/nodes/5/delete-impact': new Error('offline') })
    api.delete.mockResolvedValue({ data: null })
    open()
    expect(await screen.findByText('Could not check what this affects')).toBeTruthy()
    expect(screen.queryByRole('button', { name: /^Delete node and/ })).toBeNull()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy()
    expect(deletes()).toHaveLength(0)
    fireEvent.click(screen.getByRole('button', { name: 'Delete without checking' }))
    await waitFor(() => expect(deletes()).toHaveLength(1))
  })

  it('does not block "remove from panel only" on the routes, and names that verb and the count', async () => {
    installReads({ '/admin/nodes/5/delete-impact': impact({ blocked_by_routes: ['shared port 443'] }) })
    api.post.mockResolvedValue({ data: null })
    const { onDone } = open({ mode: 'detach' })
    fireEvent.click(await screen.findByRole('button', { name: 'Remove node and 4 lines from the panel' }))
    await waitFor(() => expect(onDone).toHaveBeenCalled())
    expect(api.post.mock.calls[0][0]).toBe('/admin/nodes/5/detach')
  })
})
