// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { AxiosError } from 'axios'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api, mountPage } from '../testHarness'
import { listener, tunnel } from './editorHarness'
import { ListenerCard } from './ListenerCard'

const toast = vi.hoisted(() => vi.fn())
vi.mock('@/components/feedback/toasts', () => ({ pushToast: toast, pushSnack: toast }))

const refusal = (data: Record<string, unknown>, status = 409) => new AxiosError('x', String(status), undefined, undefined, { status, statusText: '', headers: {}, config: {} as never, data })

function card(line = tunnel) {
  const props = { nodeId: 5, line, present: vi.fn().mockResolvedValue(true), onRelays: vi.fn(), onDirty: vi.fn() }
  mountPage(<ListenerCard {...props} />)
  return props
}
const saveButton = () => screen.getByRole('button', { name: 'Save listener' }) as HTMLButtonElement
const val = (id: string) => (screen.getByTestId(id) as HTMLInputElement).value

beforeEach(() => toast.mockReset())

describe('ListenerCard', () => {
  it('shows the status in words, the REALITY values and masks the private key', () => {
    card()
    expect(screen.getByTestId('tunnel-running').textContent).toBe('Running: the line is published')
    expect(screen.getByTestId('tunnel-applied').textContent).toBe('Listener applied on the entry server')
    expect(screen.getByTestId('tunnel-link').textContent).toBe('The tunnel runs on both servers')
    expect(val('tunnel-port')).toBe('31002')
    expect(val('tunnel-sni')).toBe('www.asus.com')
    expect(val('tunnel-target')).toBe('www.asus.com:443')
    expect(val('tunnel-short-id')).toBe('ab12')
    expect(val('tunnel-public-key')).toBe('PUB')
    const priv = screen.getByTestId('tunnel-private-key') as HTMLInputElement
    expect(priv.type).toBe('password')
    fireEvent.click(screen.getByRole('button', { name: 'Show' }))
    expect(priv.type).toBe('text')
  })

  it('says pending when the listener is not ready, and leaves out an absent private key', () => {
    const { private_key: _p, ...noKey } = listener
    card({ ...tunnel, running: false, tunnel: { ...noKey, applied: false, egress_ready: false, config_sync_state: 'pending' } })
    expect(screen.getByTestId('tunnel-running').textContent).toContain('Not running yet')
    expect(screen.getByTestId('tunnel-applied').textContent).toContain('Waiting for the entry server')
    expect(screen.getByTestId('tunnel-link').textContent).toContain('Waiting for the tunnel')
    expect(screen.queryByTestId('tunnel-private-key')).toBeNull()
  })

  it('pre-fills random values without saving, then saves exactly those on the listener\'s own endpoint', async () => {
    api.get.mockImplementation(async (_u: string, cfg?: { params?: { what?: string } }) => ({ data: cfg?.params?.what === 'camouflage' ? { host: 'www.example.org', port: 443 } : { port: 41999 } }))
    api.post.mockResolvedValue({ data: { private_key: 'NEWPRIV', public_key: 'NEWPUB', short_id: 'ffee0011' } })
    const props = card()
    const randoms = screen.getAllByRole('button', { name: 'Random' })
    fireEvent.click(randoms[0])
    await waitFor(() => expect(val('tunnel-port')).toBe('41999'))
    fireEvent.click(randoms[1])
    await waitFor(() => expect(val('tunnel-sni')).toBe('www.example.org'))
    expect(val('tunnel-target')).toBe('www.example.org:443')
    fireEvent.click(screen.getByRole('button', { name: 'Random keys' }))
    await waitFor(() => expect(val('tunnel-public-key')).toBe('NEWPUB'))
    expect(val('tunnel-short-id')).toBe('ffee0011')
    expect(api.put).not.toHaveBeenCalled()
    api.put.mockResolvedValue({ data: { relays: [tunnel] } })
    fireEvent.click(saveButton())
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put.mock.calls[0][0]).toBe('/admin/nodes/5/lines/T1/tunnel')
    expect(api.put.mock.calls[0][1]).toMatchObject({ port: 41999, server_name: 'www.example.org', public_key: 'NEWPUB', private_key: 'NEWPRIV', short_id: 'ffee0011' })
    await waitFor(() => expect(props.onRelays).toHaveBeenCalledWith([tunnel]))
  })

  it('keeps Save off until something is edited, tells the editor, and names a refusal', async () => {
    api.put.mockRejectedValue(refusal({ error: 'port 40200 is used by listener x' }, 400))
    const props = card()
    expect(saveButton().disabled).toBe(true)
    fireEvent.change(screen.getByTestId('tunnel-port'), { target: { value: '40200' } })
    expect(saveButton().disabled).toBe(false)
    await waitFor(() => expect(props.onDirty).toHaveBeenLastCalledWith(true))
    fireEvent.click(saveButton())
    await waitFor(() => expect(toast).toHaveBeenCalledWith(expect.objectContaining({ message: 'port 40200 is used by listener x', severity: 'error' })))
  })

  it('fills in the public key from an edited private key, and holds Save while the key is malformed', async () => {
    api.post.mockResolvedValueOnce({ data: { public_key: 'DERIVED' } })
    card()
    fireEvent.change(screen.getByTestId('tunnel-private-key'), { target: { value: 'NEWPRIVATE' } })
    await waitFor(() => expect(val('tunnel-public-key')).toBe('DERIVED'), { timeout: 2000 })
    expect(api.post).toHaveBeenCalledWith('/admin/nodes/reality-public-key', { private_key: 'NEWPRIVATE' }, expect.anything())
    api.post.mockRejectedValueOnce(refusal({ error: 'not a valid REALITY private key' }, 400))
    fireEvent.change(screen.getByTestId('tunnel-private-key'), { target: { value: 'garbage' } })
    await screen.findByText('not a valid REALITY private key', {}, { timeout: 2000 })
    expect(saveButton().disabled).toBe(true)
    expect(api.put).not.toHaveBeenCalled()
  })

  it('shows the diff first, then copies from the landing through the plan presenter and echoes the plan', async () => {
    const diff = [{ field: 'flow', from: '', to: 'xtls-rprx-vision' }]
    api.get.mockResolvedValue({ data: { diff } })
    api.post.mockRejectedValueOnce(refusal({ code: 'confirm_required', params: { kind: 'tunnel', action: 'sync_from_landing', plan: 'PLAN', diff, line_id: 'T1', refresh_needed: true } })).mockResolvedValueOnce({ data: { diff: [], relays: [tunnel] } })
    const props = card()
    fireEvent.click(screen.getByRole('button', { name: 'Copy from landing' }))
    await waitFor(() => expect(props.onRelays).toHaveBeenCalled())
    expect(props.present).toHaveBeenCalledTimes(1)
    expect(props.present.mock.calls[0][0].lines.join(' ')).toContain('flow')
    expect(api.post).toHaveBeenLastCalledWith('/admin/nodes/5/lines/T1/tunnel/sync', { confirm: true, confirm_plan: 'PLAN' }, expect.anything())
  })

  it('writes nothing when the copy is declined, and says so when the landing cannot be copied', async () => {
    const diff = [{ field: 'flow', from: '', to: 'x' }]
    api.get.mockResolvedValue({ data: { diff } })
    api.post.mockRejectedValue(refusal({ code: 'confirm_required', params: { kind: 'tunnel', action: 'sync_from_landing', plan: 'P', diff, refresh_needed: true } }))
    const props = card()
    props.present.mockResolvedValue(false)
    fireEvent.click(screen.getByRole('button', { name: 'Copy from landing' }))
    await waitFor(() => expect(props.present).toHaveBeenCalled())
    await waitFor(() => expect((screen.getByRole('button', { name: 'Copy from landing' }) as HTMLButtonElement).disabled).toBe(false))
    expect(api.post).toHaveBeenCalledTimes(1)
    expect(props.onRelays).not.toHaveBeenCalled()
    api.get.mockRejectedValue(refusal({ error: 'the landing is not REALITY' }, 400))
    fireEvent.click(screen.getByRole('button', { name: 'Copy from landing' }))
    await waitFor(() => expect(toast).toHaveBeenCalledWith(expect.objectContaining({ message: 'the landing is not REALITY', severity: 'error' })))
  })

  it('links to the listener node\'s own page', () => {
    card()
    expect(within(screen.getByTestId('tunnel-card')).getByText('Listener settings').closest('a')?.getAttribute('href')).toBe('/admin/nodes/77')
  })
})
