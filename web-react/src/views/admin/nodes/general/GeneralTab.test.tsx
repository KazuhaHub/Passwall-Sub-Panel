// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api, mountPage } from '../testHarness'
import { forward, landing, metadataPuts, serve } from '../lines/editorHarness'
import { GeneralTab } from './GeneralTab'

const toast = vi.hoisted(() => vi.fn())
vi.mock('@/components/feedback/toasts', () => ({ pushToast: toast, pushSnack: toast }))

function open(over: Parameters<typeof landing>[1] = {}, onDirtyChange = vi.fn()) {
  const node = landing([forward], { hide_direct: true, ...over })
  serve(node)
  // The harness answers for a line editor with one extra draft line; this tab sends exactly the node's own lines.
  api.post.mockResolvedValue({ data: { groups: [{ id: 1, name: 'VIP' }, { id: 2, name: 'Staff' }], node_groups: [1], saved_node_groups: [1, 2], relays: node.relays?.map(() => [1]) } })
  mountPage(<GeneralTab node={node} onDirtyChange={onDirtyChange} />)
  return { node, onDirtyChange }
}

beforeEach(() => toast.mockReset())

/** The region is a country picker: type part of the name, then choose the country. */
async function pickRegion(name: string) {
  const input = screen.getByLabelText(/Region/)
  fireEvent.change(input, { target: { value: name } })
  fireEvent.keyDown(input, { key: 'ArrowDown' })
  fireEvent.click(await screen.findByRole('option', { name: new RegExp(name) }))
}

describe('General tab', () => {
  it('saves the four fields and the current flow, and nothing that belongs to a line, the exit or the order', async () => {
    open()
    fireEvent.change(await screen.findByLabelText(/Display name/), { target: { value: 'Taipei 01' } })
    await pickRegion('Taiwan')
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(metadataPuts()).toHaveLength(1))
    // `relays` absent keeps every line and the direct line's switch; `flow` must be sent or the server clears it.
    expect(metadataPuts()[0][1]).toEqual({ display_name: 'Taipei 01', server_address: 'land.example', region: 'TW', tags: ['premium'], flow: 'xtls-rprx-vision' })
    expect(api.put).toHaveBeenCalledTimes(1)
  })

  it('reports unsaved edits to the page and clears them when the edit is undone', async () => {
    const { onDirtyChange } = open()
    const name = await screen.findByLabelText(/Display name/)
    expect(onDirtyChange).toHaveBeenLastCalledWith(false)
    fireEvent.change(name, { target: { value: 'x' } })
    await waitFor(() => expect(onDirtyChange).toHaveBeenLastCalledWith(true))
    fireEvent.change(name, { target: { value: 'Landing' } })
    await waitFor(() => expect(onDirtyChange).toHaveBeenLastCalledWith(false))
  })

  it('refuses a blank name before any request and says which field', async () => {
    open()
    fireEvent.change(await screen.findByLabelText(/Display name/), { target: { value: ' ' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findAllByText('Required')).not.toHaveLength(0)
    expect(api.put).not.toHaveBeenCalled()
  })

  it('previews the groups that would see the node, and the one it would leave', async () => {
    open()
    await waitFor(() => expect(screen.getByTestId('node-groups').textContent).toContain('VIP'))
    expect(screen.getByTestId('node-groups-removed').textContent).toContain('Staff')
    await pickRegion('Japan')
    await waitFor(() => expect(api.post.mock.calls.at(-1)![1]).toMatchObject({ region: 'JP', relays: [{ id: 'F1', region: 'CN', tags: ['cmcc'] }] }))
  })

  it('reminds that its region and tags do not reach existing lines, only when there are lines', async () => {
    open()
    expect(await screen.findByText(/does not change lines that already exist|existing lines/i)).toBeTruthy()
  })
})
