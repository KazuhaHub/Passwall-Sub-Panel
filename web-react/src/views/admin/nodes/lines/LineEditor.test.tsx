// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { AxiosError } from 'axios'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { RelayLine } from '@/api/types'
import { api, mountPage } from '../testHarness'
import { forward, landing, listener, metadataPuts, serve, servers, tunnel } from './editorHarness'
import { LineEditor } from './LineEditor'

const toast = vi.hoisted(() => vi.fn())
vi.mock('@/components/feedback/toasts', () => ({ pushToast: toast, pushSnack: toast }))

function open(lineId: string | null, over: { onSaved?: (r: { addedId?: string; isNew: boolean }) => void; onDirtyChange?: (d: boolean) => void } = {}) {
  const props = { onSaved: vi.fn(), onDirtyChange: vi.fn(), ...over }
  mountPage(<LineEditor nodeId={1} lineId={lineId} {...props} />)
  return props
}
const refusal = (data: Record<string, unknown>) => new AxiosError('x', '409', undefined, undefined, { status: 409, statusText: '', headers: {}, config: {} as never, data })
const plan = (action: string) => refusal({ code: 'confirm_required', params: { kind: 'tunnel', action, plan: 'p1', creates: [{ line_id: 'F1', line_name: 'GZ', entry_panel_id: 2, landing_name: 'Landing', port: 20443 }], removes: [], refresh_needed: true } })

beforeEach(() => toast.mockReset())

describe('the type cards of a new line', () => {
  it('say why direct forward and encrypted tunnel are off when no other server can be the entry', async () => {
    serve(landing(), [servers[0]])
    open(null)
    const forwardCard = await screen.findByRole('radio', { name: 'Direct forward' })
    expect(forwardCard.getAttribute('aria-disabled')).toBe('true')
    expect(within(forwardCard.closest('[role="radiogroup"]')!.parentElement!).getAllByText('Needs another Passwall Node server as the entry').length).toBeGreaterThan(0)
    expect(screen.getByRole('radio', { name: 'Encrypted tunnel' }).getAttribute('aria-disabled')).toBe('true')
    expect(screen.getByRole('radio', { name: 'CDN' }).getAttribute('aria-disabled')).toBeNull()
    expect(screen.getByRole('radio', { name: 'External entry' }).getAttribute('aria-disabled')).toBeNull()
  })

  it('say a tunnel needs the landing itself on a Passwall Node server', async () => {
    serve(landing(), servers.map(s => (s.id === 1 ? { ...s, panel_type: '3xui' } : s)))
    open(null)
    const card = await screen.findByRole('radio', { name: 'Encrypted tunnel' })
    expect(card.getAttribute('aria-disabled')).toBe('true')
    expect(screen.getByText('The landing node must run on a Passwall Node server')).toBeTruthy()
    expect(screen.getByRole('radio', { name: 'Direct forward' }).getAttribute('aria-disabled')).toBeNull()
  })
})

describe('adding a line', () => {
  it('adds a direct forward: the entry fills the name and address, and the save sends the fresh lines plus the new one', async () => {
    serve(landing([tunnel]))
    const props = open(null)
    fireEvent.click(await screen.findByRole('radio', { name: 'Direct forward' }))
    fireEvent.change(await screen.findByLabelText(/Entry server/), { target: { value: '2' } })
    expect((screen.getByLabelText(/Line name/) as HTMLInputElement).value).toBe('Entry GZ')
    expect((screen.getByLabelText(/Entry address/) as HTMLInputElement).value).toBe('gz.example')
    fireEvent.change(screen.getByLabelText(/Entry port/), { target: { value: '20443' } })
    // The picture follows the draft.
    expect(screen.getAllByRole('img', { name: /Entry GZ/ }).length).toBeGreaterThan(0)
    fireEvent.click(screen.getByRole('button', { name: 'Save line' }))
    await waitFor(() => expect(metadataPuts()).toHaveLength(1))
    const body = metadataPuts()[0][1] as Record<string, unknown> & { relays: Record<string, unknown>[] }
    expect(Object.keys(body).sort()).toEqual(['flow', 'hide_direct', 'relays'])
    expect(body.relays[0]).toMatchObject({ id: 'T1', mode: 'tunnel', entry_panel_id: 3 })
    expect(body.relays[1]).toMatchObject({ name: 'Entry GZ', address: 'gz.example', port: 20443, entry_panel_id: 2, enabled: true, region: 'TW', tags: ['premium'] })
    expect(body.relays[1]).not.toHaveProperty('id')
    await waitFor(() => expect(props.onSaved).toHaveBeenCalledWith({ addedId: 'N1', isNew: true }))
    expect(toast).toHaveBeenCalledWith(expect.objectContaining({ message: 'Line "Entry GZ" added' }))
  })

  it('a CDN line needs its domain and then leaves the defaults to the server', async () => {
    serve(landing())
    open(null)
    fireEvent.click(await screen.findByRole('radio', { name: 'CDN' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Save line' }))
    expect(await screen.findByText('A CDN line needs its CDN domain.')).toBeTruthy()
    expect(metadataPuts()).toHaveLength(0)
    fireEvent.change(screen.getByLabelText(/CDN domain/), { target: { value: 'cdn.example.com' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save line' }))
    await waitFor(() => expect(metadataPuts()).toHaveLength(1))
    const line = (metadataPuts()[0][1] as { relays: Record<string, unknown>[] }).relays[0]
    expect(line).toMatchObject({ mode: 'cdn', domain: 'cdn.example.com', address: '', port: 443 })
    for (const k of ['entry_panel_id', 'client_tls', 'sni', 'host']) expect((line as Record<string, unknown>)[k]).toBeUndefined()
    expect(line).not.toHaveProperty('cdn_issues')
  })

  it('an invalid multiplier blocks the save', async () => {
    serve(landing())
    open(null)
    fireEvent.click(await screen.findByRole('radio', { name: 'External entry' }))
    fireEvent.change(await screen.findByLabelText(/Entry address/), { target: { value: 'relay.example.com' } })
    fireEvent.change(screen.getByLabelText('Traffic multiplier'), { target: { value: '0.0004' } })
    fireEvent.click(screen.getByRole('button', { name: /Save line/ }))
    expect(await screen.findAllByText('Enter a multiplier between 0 and 100 (up to 3 decimals)')).toBeTruthy()
    expect(metadataPuts()).toHaveLength(0)
  })
})

describe('editing a saved line', () => {
  it('saves one line and sends every other line, hide_direct and flow as they were read', async () => {
    serve(landing([forward, tunnel], { hide_direct: true }))
    const props = open('F1')
    const port = await screen.findByLabelText(/Entry port/)
    // The multiplier reads as a decimal, not as thousandths.
    expect((screen.getByLabelText('Traffic multiplier') as HTMLInputElement).value).toBe('1.5')
    fireEvent.change(port, { target: { value: '20444' } })
    expect(props.onDirtyChange).toHaveBeenLastCalledWith(true)
    fireEvent.click(screen.getByRole('button', { name: 'Save line' }))
    await waitFor(() => expect(metadataPuts()).toHaveLength(1))
    const body = metadataPuts()[0][1] as { relays: Record<string, unknown>[]; hide_direct: boolean; flow: string }
    expect(body).toMatchObject({ hide_direct: true, flow: 'xtls-rprx-vision' })
    expect(body.relays[0]).toMatchObject({ id: 'F1', port: 20444, region: 'CN', tags: ['cmcc'] })
    expect(body.relays[0]).not.toHaveProperty('traffic_factor_milli')
    expect(body.relays[1]).toMatchObject({ id: 'T1', mode: 'tunnel' })
    expect(body.relays[1]).not.toHaveProperty('tunnel')
    expect(toast).toHaveBeenCalledWith(expect.objectContaining({ message: 'Line saved' }))
  })

  it('previews the groups that would receive the line with the labels as typed', async () => {
    serve(landing([forward]))
    api.post.mockResolvedValue({ data: { groups: [{ id: 1, name: 'VIP' }], node_groups: [1], saved_node_groups: [1], relays: [[]] } })
    open('F1')
    expect((await screen.findByTestId('line-visibility')).textContent).toBe('No group can see this line; it appears in no subscription')
    fireEvent.change(screen.getByLabelText('Region'), { target: { value: 'HK' } })
    await waitFor(() => {
      const last = api.post.mock.calls.filter(([u]) => u === '/admin/nodes/1/line-visibility').at(-1)![1]
      expect(last.relays).toEqual([{ id: 'F1', region: 'HK', tags: ['cmcc'] }])
    })
    expect(metadataPuts()).toHaveLength(0)
  })

  it('shows what the server says of a CDN line and keeps its kind fixed', async () => {
    const cdn: RelayLine = { id: 'C1', name: 'CF', mode: 'cdn', domain: 'cdn.example.com', address: '104.16.1.1', port: 443, enabled: true, cdn_issues: [{ code: 'cert_not_covering', warn: true, params: { domain: 'cdn.example.com' } }, { code: 'port_differs', warn: false, params: { port: '443', origin_port: '8443' } }] }
    serve(landing([cdn]))
    open('C1')
    const issues = await screen.findByTestId('cdn-issues')
    expect(within(issues).getAllByRole('listitem').map(i => i.getAttribute('data-code'))).toEqual(['cert_not_covering', 'port_differs'])
    expect(screen.getByText(/The kind of a line cannot change once it is saved/)).toBeTruthy()
    expect(screen.queryByLabelText(/Entry server/)).toBeNull()
  })
})

describe('switching a saved line between direct forward and encrypted tunnel', () => {
  it('shows the server\'s plan in a dialog of its own, then repeats the switch confirmed and shows the listener', async () => {
    serve(landing([forward]))
    const switched = landing([{ ...forward, mode: 'tunnel', port: 0, tunnel: listener, running: false }])
    api.post.mockImplementation(async (url: string) => {
      if (String(url).endsWith('/line-visibility')) return { data: { groups: [], node_groups: [], saved_node_groups: [], relays: [[]] } }
      throw new Error(`unexpected ${url}`)
    })
    api.post.mockRejectedValueOnce(plan('switch_mode')).mockResolvedValueOnce({ data: switched })
    open('F1')
    fireEvent.click(await screen.findByRole('button', { name: 'Switch to encrypted tunnel' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByTestId('tunnel-plan').textContent).toContain('20443')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/nodes/1/lines/F1/mode', { mode: 'tunnel', confirm: true, confirm_plan: 'p1' }, expect.anything()))
    expect(await screen.findByTestId('tunnel-card', {}, { timeout: 4000 })).toBeTruthy()
    expect(toast).toHaveBeenCalledWith(expect.objectContaining({ message: 'The line now runs as an encrypted tunnel. Subscribers must refresh.' }))
  })

  it('changes nothing when the plan is declined', async () => {
    serve(landing([forward]))
    api.post.mockResolvedValue({ data: { groups: [], node_groups: [], saved_node_groups: [], relays: [[]] } })
    api.post.mockRejectedValueOnce(plan('switch_mode'))
    open('F1')
    fireEvent.click(await screen.findByRole('button', { name: 'Switch to encrypted tunnel' }))
    fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(api.post.mock.calls.filter(([u]) => String(u).endsWith('/mode'))).toHaveLength(1)
    expect(screen.queryByTestId('tunnel-card')).toBeNull()
  })
})

describe('the entry listener of a tunnel line', () => {
  it('saves only the listener, on its own endpoint, and then shows what the server settled', async () => {
    serve(landing([tunnel]))
    const props = open('T1')
    const port = await screen.findByTestId('tunnel-port')
    fireEvent.change(port, { target: { value: '31999' } })
    // Unsaved listener edits count as unsaved: closing the sheet must ask.
    await waitFor(() => expect(props.onDirtyChange).toHaveBeenLastCalledWith(true))
    api.put.mockResolvedValueOnce({ data: { relays: [{ ...tunnel, tunnel: { ...listener, port: 31999 } }] } })
    fireEvent.click(screen.getByRole('button', { name: 'Save listener' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/admin/nodes/1/lines/T1/tunnel', expect.objectContaining({ port: 31999, private_key: 'PRIV', public_key: 'PUB' }), expect.anything()))
    expect(metadataPuts()).toHaveLength(0)
    await waitFor(() => expect(props.onDirtyChange).toHaveBeenLastCalledWith(false))
    expect(toast).toHaveBeenCalledWith(expect.objectContaining({ message: 'Listener saved' }))
  })
})

describe('the direct line', () => {
  it('sends the fresh lines unchanged with its own switch and multiplier', async () => {
    serve(landing([forward]))
    open('direct')
    const sw = await screen.findByRole('switch', { name: 'Publish direct line' })
    expect(sw.getAttribute('aria-checked')).toBe('true')
    fireEvent.click(sw)
    fireEvent.change(screen.getByLabelText('Direct line traffic multiplier'), { target: { value: '2' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save line' }))
    await waitFor(() => expect(metadataPuts()).toHaveLength(1))
    const body = metadataPuts()[0][1] as { relays: { id: string }[] }
    expect(body).toMatchObject({ flow: 'xtls-rprx-vision', hide_direct: true, traffic_factor_milli: 2000 })
    expect(body.relays.map(r => r.id)).toEqual(['F1'])
  })
})
