// @vitest-environment jsdom
import { QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { api, installReads } from '../testHarness'
import { BulkDeleteDialog } from './BulkDeleteDialog'

afterEach(cleanup)
const impactOf = (id: number, lines: number, blocked: string[] = []) => ({
  node: { id, name: `N${id}` }, tunnels: [], blocked_by_routes: blocked,
  lines: Array.from({ length: lines }, (_, i) => ({ node_id: id, line_id: i === 0 ? 'direct' : `l${i}`, landing_name: `N${id}`, line_name: i === 0 ? `N${id}` : `L${i}`, line_type: i === 0 ? 'direct' : 'forward' })),
})
function open(onDone = vi.fn()) {
  render(
    <QueryClientProvider client={makeTestQueryClient()}><MemoryRouter>
      <BulkDeleteDialog nodeIds={[1, 2, 3]} nameOf={id => `N${id}`} entryOf={() => 'GZ'} serverName={id => `s${id}`} onClose={vi.fn()} onDone={onDone} />
    </MemoryRouter></QueryClientProvider>,
  )
  return onDone
}

describe('the bulk delete dialog', () => {
  it('counts only the nodes it can delete, lists the blocked and unreadable ones apart, and deletes exactly those after the confirm', async () => {
    installReads({
      '/admin/nodes/1/delete-impact': impactOf(1, 3), '/admin/nodes/2/delete-impact': impactOf(2, 2, ['shared port 443']),
      '/admin/nodes/3/delete-impact': new Error('offline'),
    })
    api.delete.mockResolvedValue({ data: null })
    const onDone = open()
    const button = await screen.findByRole('button', { name: 'Delete 1 nodes and 3 lines' })
    expect(within(screen.getByTestId('bulk-skipped')).getAllByRole('listitem')).toHaveLength(2)
    expect(api.delete).not.toHaveBeenCalled()
    fireEvent.click(button)
    await waitFor(() => expect(onDone).toHaveBeenCalledWith({ ok: 1, failed: 0 }))
    expect(api.delete.mock.calls.map(c => c[0])).toEqual(['/admin/nodes/1'])
  })
})
