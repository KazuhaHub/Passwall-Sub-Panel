// @vitest-environment jsdom
import { QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { api } from '../testHarness'
import { SetMultiplierDialog } from './SetMultiplierDialog'
import type { MultiplierLine } from './multiplierModel'

afterEach(cleanup)
const line = (node: number, server: number, old: number): MultiplierLine => ({ key: `${node}.direct`, node_id: node, line_id: 'direct', server_id: server, label: `N${node}`, old })

function open(lines: MultiplierLine[]) {
  const onClose = vi.fn(); const onDone = vi.fn()
  render(<QueryClientProvider client={makeTestQueryClient()}><SetMultiplierDialog lines={lines} onClose={onClose} onDone={onDone} /></QueryClientProvider>)
  return { onClose, onDone }
}

describe('the multiplier dialog', () => {
  it('previews old and new per line and saves one request per server', async () => {
    api.put.mockResolvedValue({ data: { changed: 1 } })
    const { onClose, onDone } = open([line(1, 10, 1000), line(2, 11, 2000), line(3, 10, 1000)])
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: '1.5' } })
    const rows = screen.getAllByTestId('multiplier-row')
    expect(within(rows[1]).getByText('×2')).toBeTruthy()
    expect(within(rows[1]).getByText('×1.5')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(onDone).toHaveBeenCalled()
    expect(api.put.mock.calls.map(c => c[0]).sort()).toEqual(['/admin/servers/10/line-factors', '/admin/servers/11/line-factors'])
    expect(api.put.mock.calls.find(c => c[0].endsWith('/10/line-factors'))![1].lines).toHaveLength(2)
  })
  it('keeps Apply off for an invalid value and stays open when a save fails', async () => {
    api.put.mockRejectedValue(new Error('boom'))
    const { onClose } = open([line(1, 10, 1000)])
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: 'abc' } })
    expect((screen.getByRole('button', { name: 'Apply' }) as HTMLButtonElement).disabled).toBe(true)
    fireEvent.change(box, { target: { value: '3' } })
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }))
    await screen.findByText(/boom/)
    expect(onClose).not.toHaveBeenCalled()
  })
})
