// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { AxiosError } from 'axios'
import { describe, expect, it, vi } from 'vitest'
import { api, mount, snack } from '@/test/adminSaveHarness'
import type { RelayLine, TunnelListener } from '@/api/types'
import TunnelLineEditor, { mergeServerLine } from './TunnelLineEditor'

const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))

const refusal = (data: Record<string, unknown>, status = 409) => new AxiosError('x', String(status), undefined, undefined, {
  status, statusText: '', headers: {}, config: {} as never, data,
})

const listener: TunnelListener = {
  node_id: 77, name: 'tunnel-77', enabled: true, port: 40123, protocol: 'vless', flow: 'xtls-rprx-vision', security: 'reality',
  server_names: ['www.asus.com'], target: 'www.asus.com:443', short_ids: ['ab12cd34'], public_key: 'PUBKEY', private_key: 'PRIVKEY',
  fingerprint: 'chrome', config_sync_state: 'synced', applied: true, egress_panel_id: 2, egress_ready: true, routes: 2,
  egress_probe: { state: 'ok', median_rtt_ms: 40, loss_percent: 0, withheld: false },
}
const tunnelLine: RelayLine = { id: 'L1', name: 'Mobile', address: 'entry.example', port: 40123, enabled: true, entry_panel_id: 1, mode: 'tunnel', running: true, tunnel: listener }
const forwardLine: RelayLine = { id: 'L1', name: 'Mobile', address: 'entry.example', port: 8443, enabled: true, entry_panel_id: 1 }

function renderEditor(line: RelayLine, over: Partial<Parameters<typeof TunnelLineEditor>[0]> = {}) {
  const props = {
    nodeId: 5, line, savedEntry: line.id ? 1 : 0, onPatch: vi.fn(), onSwitched: vi.fn(), onRelays: vi.fn(), onOpenListener: vi.fn(), ...over,
  }
  mount(<TunnelLineEditor {...props} />)
  return props
}

const toggle = (name: string) => screen.getByRole('button', { name })

describe('TunnelLineEditor mode switch', () => {
  it('renders nothing for an external entry', () => {
    renderEditor({ ...forwardLine, entry_panel_id: 0 })
    expect(screen.queryByTestId('tunnel-line')).toBeNull()
  })

  it('only records the mode on a new line, with no port or listener', () => {
    const props = renderEditor({ name: 'new', address: 'a', port: 0, enabled: true, entry_panel_id: 1 })
    fireEvent.click(toggle('admin:nodes.tunnel.mode_tunnel'))
    expect(props.onPatch).toHaveBeenCalledWith({ mode: 'tunnel', port: 0, sni: '', host: '' })
    expect(api.post).not.toHaveBeenCalled()
    expect(screen.queryByTestId('tunnel-card')).toBeNull()
  })

  it('shows the pre-fill hint for a new tunnel line', () => {
    renderEditor({ name: 'new', address: 'a', port: 0, enabled: true, entry_panel_id: 1, mode: 'tunnel' })
    expect(screen.getByTestId('tunnel-new-hint').textContent).toBe('admin:nodes.tunnel.new_hint')
  })

  it('asks before switching a saved line, then repeats the switch with the confirmation', async () => {
    confirmation.mockResolvedValue(true)
    const landing = { id: 5, relays: [{ ...tunnelLine }] }
    api.post
      .mockRejectedValueOnce(refusal({ code: 'confirm_required', params: { kind: 'tunnel', action: 'switch_mode', creates: [{ line_id: 'L1', line_name: 'Mobile', entry_panel_id: 1, landing_name: 'HK' }], removes: [], refresh_needed: true } }))
      .mockResolvedValueOnce({ data: landing })
    const props = renderEditor(forwardLine)
    fireEvent.click(toggle('admin:nodes.tunnel.mode_tunnel'))
    await waitFor(() => expect(props.onSwitched).toHaveBeenCalledWith(landing))
    expect(confirmation).toHaveBeenCalledTimes(1)
    expect(confirmation.mock.calls[0][0].title).toBe('admin:nodes.tunnel.confirm.switch_mode_title')
    expect(api.post).toHaveBeenNthCalledWith(1, '/admin/nodes/5/lines/L1/mode', { mode: 'tunnel' }, expect.anything())
    expect(api.post).toHaveBeenNthCalledWith(2, '/admin/nodes/5/lines/L1/mode', { mode: 'tunnel', confirm: true }, expect.anything())
    expect(snack).toHaveBeenCalledWith('admin:nodes.tunnel.switched_tunnel', 'success')
  })

  it('changes nothing when the admin declines', async () => {
    confirmation.mockResolvedValue(false)
    api.post.mockRejectedValue(refusal({ code: 'confirm_required', params: { kind: 'tunnel', action: 'switch_mode', creates: [], removes: [{ node_id: 77, name: 'tunnel-77', line_name: 'Mobile', port: 40123 }], refresh_needed: true } }))
    const props = renderEditor(tunnelLine)
    fireEvent.click(toggle('admin:nodes.tunnel.mode_forward'))
    await waitFor(() => expect(confirmation).toHaveBeenCalled())
    await waitFor(() => expect((toggle('admin:nodes.tunnel.mode_forward') as HTMLButtonElement).disabled).toBe(false))
    expect(api.post).toHaveBeenCalledTimes(1)
    expect(props.onSwitched).not.toHaveBeenCalled()
    expect(snack).not.toHaveBeenCalled()
  })

  it('reports a refusal that is not a confirmation', async () => {
    api.post.mockRejectedValue(refusal({ error: 'entry server cannot run tunnels' }, 400))
    const props = renderEditor(forwardLine)
    fireEvent.click(toggle('admin:nodes.tunnel.mode_tunnel'))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('entry server cannot run tunnels', 'error'))
    expect(props.onSwitched).not.toHaveBeenCalled()
  })

  it('does not offer a switch for a saved line whose entry was changed in the form', () => {
    renderEditor({ ...forwardLine, entry_panel_id: 3 })
    expect(screen.queryByTestId('tunnel-line')).toBeNull()
  })
})

describe('TunnelLineEditor tunnel card', () => {
  it('shows the status, the listener port, REALITY values and masks the private key', () => {
    renderEditor(tunnelLine)
    expect(screen.getByTestId('tunnel-running').textContent).toBe('admin:nodes.tunnel.running')
    expect(screen.getByTestId('tunnel-applied').textContent).toBe('admin:nodes.tunnel.applied')
    expect(screen.getByTestId('tunnel-link').textContent).toBe('admin:nodes.tunnel.link_ready')
    expect(screen.getByTestId('tunnel-probe').dataset.state).toBe('ok')
    expect((screen.getByTestId('tunnel-port') as HTMLInputElement).value).toBe('40123')
    expect((screen.getByTestId('tunnel-sni') as HTMLInputElement).value).toBe('www.asus.com')
    expect((screen.getByTestId('tunnel-target') as HTMLInputElement).value).toBe('www.asus.com:443')
    expect((screen.getByTestId('tunnel-short-id') as HTMLInputElement).value).toBe('ab12cd34')
    expect((screen.getByTestId('tunnel-public-key') as HTMLInputElement).value).toBe('PUBKEY')
    expect((screen.getByTestId('tunnel-fingerprint') as HTMLInputElement).value).toBe('chrome')
    const priv = screen.getByTestId('tunnel-private-key') as HTMLInputElement
    expect(priv.type).toBe('password')
    fireEvent.click(screen.getByRole('button', { name: 'admin:nodes.tunnel.show_key' }))
    expect(priv.type).toBe('text')
    expect(screen.getByTestId('tunnel-routes-note')).toBeTruthy()
  })

  it('says pending when the line or the listener is not ready, and leaves out an absent private key', () => {
    const { private_key: _private, ...noKey } = listener
    renderEditor({ ...tunnelLine, running: false, tunnel: { ...noKey, applied: false, egress_ready: false, config_sync_state: 'pending' } })
    expect(screen.getByTestId('tunnel-running').textContent).toBe('admin:nodes.tunnel.pending')
    expect(screen.getByTestId('tunnel-applied').textContent).toBe('admin:nodes.tunnel.not_applied')
    expect(screen.getByTestId('tunnel-link').textContent).toBe('admin:nodes.tunnel.link_pending')
    expect(screen.queryByTestId('tunnel-private-key')).toBeNull()
  })

  it('pre-fills random values without saving anything', async () => {
    api.get.mockImplementation(async (_url: string, cfg?: { params?: { what?: string } }) => ({
      data: cfg?.params?.what === 'camouflage' ? { host: 'www.example.org', port: 443 } : { port: 41999 },
    }))
    api.post.mockResolvedValue({ data: { private_key: 'NEWPRIV', public_key: 'NEWPUB', short_id: 'ffee0011' } })
    renderEditor(tunnelLine)
    const card = screen.getByTestId('tunnel-card')
    const randoms = within(card).getAllByRole('button', { name: 'admin:nodes.tunnel.random' })
    fireEvent.click(randoms[0])
    await waitFor(() => expect((screen.getByTestId('tunnel-port') as HTMLInputElement).value).toBe('41999'))
    fireEvent.click(randoms[1])
    await waitFor(() => expect((screen.getByTestId('tunnel-sni') as HTMLInputElement).value).toBe('www.example.org'))
    expect((screen.getByTestId('tunnel-target') as HTMLInputElement).value).toBe('www.example.org:443')
    fireEvent.click(within(card).getByRole('button', { name: 'admin:nodes.tunnel.random_keys' }))
    await waitFor(() => expect((screen.getByTestId('tunnel-public-key') as HTMLInputElement).value).toBe('NEWPUB'))
    expect((screen.getByTestId('tunnel-private-key') as HTMLInputElement).value).toBe('NEWPRIV')
    expect((screen.getByTestId('tunnel-short-id') as HTMLInputElement).value).toBe('ffee0011')
    expect(api.get).toHaveBeenCalledWith('/admin/servers/1/fronts/suggest', expect.objectContaining({ params: { what: 'port' } }))
    expect(api.put).not.toHaveBeenCalled()

    api.put.mockResolvedValue({ data: { relays: [{ ...tunnelLine }] } })
    fireEvent.click(within(card).getByRole('button', { name: 'admin:nodes.tunnel.save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put.mock.calls[0][0]).toBe('/admin/nodes/5/lines/L1/tunnel')
    expect(api.put.mock.calls[0][1]).toMatchObject({ port: 41999, server_name: 'www.example.org', public_key: 'NEWPUB', private_key: 'NEWPRIV', short_id: 'ffee0011' })
  })

  it('keeps Save off until something is edited and surfaces a refusal by name', async () => {
    api.put.mockRejectedValue(refusal({ error: 'port 40200 is used by listener x' }, 400))
    renderEditor(tunnelLine)
    const save = screen.getByRole('button', { name: 'admin:nodes.tunnel.save' }) as HTMLButtonElement
    expect(save.disabled).toBe(true)
    fireEvent.change(screen.getByTestId('tunnel-port'), { target: { value: '40200' } })
    expect(save.disabled).toBe(false)
    fireEvent.click(save)
    await waitFor(() => expect(snack).toHaveBeenCalledWith('port 40200 is used by listener x', 'error'))
  })

  it('fills in the public key when the private key is edited, and shows a malformed key under the field', async () => {
    api.post.mockResolvedValueOnce({ data: { public_key: 'DERIVED' } })
    renderEditor(tunnelLine)
    fireEvent.change(screen.getByTestId('tunnel-private-key'), { target: { value: 'NEWPRIVATE' } })
    await waitFor(() => expect((screen.getByTestId('tunnel-public-key') as HTMLInputElement).value).toBe('DERIVED'), { timeout: 2000 })
    expect(api.post).toHaveBeenCalledWith('/admin/nodes/reality-public-key', { private_key: 'NEWPRIVATE' }, expect.anything())

    api.post.mockRejectedValueOnce(refusal({ error: 'not a valid REALITY private key' }, 400))
    fireEvent.change(screen.getByTestId('tunnel-private-key'), { target: { value: 'garbage' } })
    await screen.findByText('not a valid REALITY private key', {}, { timeout: 2000 })
    expect((screen.getByRole('button', { name: 'admin:nodes.tunnel.save' }) as HTMLButtonElement).disabled).toBe(true)
    expect(api.put).not.toHaveBeenCalled()
  })

  it('derives the public key before saving a private key typed just now, and surfaces a refused pair', async () => {
    api.post.mockResolvedValueOnce({ data: { public_key: 'DERIVED' } })
    api.put.mockResolvedValue({ data: { relays: [{ ...tunnelLine }] } })
    renderEditor(tunnelLine)
    fireEvent.change(screen.getByTestId('tunnel-private-key'), { target: { value: 'NEWPRIVATE' } })
    fireEvent.click(screen.getByRole('button', { name: 'admin:nodes.tunnel.save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect(api.put.mock.calls[0][1]).toMatchObject({ private_key: 'NEWPRIVATE', public_key: 'DERIVED' })
  })

  const syncRefusal = (diff: unknown[]) => refusal({ code: 'confirm_required', params: { kind: 'tunnel', action: 'sync_from_landing', plan: 'PLAN', diff, line_id: 'L1', refresh_needed: true } })

  it('shows the diff before copying from the landing and echoes the plan on the confirmed request', async () => {
    confirmation.mockResolvedValue(true)
    const diff = [{ field: 'flow', from: '', to: 'xtls-rprx-vision' }]
    api.get.mockResolvedValue({ data: { diff } })
    api.post.mockRejectedValueOnce(syncRefusal(diff)).mockResolvedValueOnce({ data: { diff: [], relays: [{ ...tunnelLine }] } })
    const props = renderEditor(tunnelLine)
    fireEvent.click(screen.getByRole('button', { name: 'admin:nodes.tunnel.sync' }))
    await waitFor(() => expect(props.onRelays).toHaveBeenCalled())
    expect(api.get).toHaveBeenCalledWith('/admin/nodes/5/lines/L1/tunnel/sync', expect.anything())
    expect(confirmation).toHaveBeenCalledTimes(1)
    expect(confirmation.mock.calls[0][0].message).toContain('admin:nodes.tunnel.confirm.diff_item')
    expect(api.post).toHaveBeenLastCalledWith('/admin/nodes/5/lines/L1/tunnel/sync', { confirm: true, confirm_plan: 'PLAN' }, expect.anything())
  })

  it('writes nothing when the sync is declined', async () => {
    confirmation.mockResolvedValue(false)
    const diff = [{ field: 'flow', from: '', to: 'x' }]
    api.get.mockResolvedValue({ data: { diff } })
    api.post.mockRejectedValue(syncRefusal(diff))
    const props = renderEditor(tunnelLine)
    fireEvent.click(screen.getByRole('button', { name: 'admin:nodes.tunnel.sync' }))
    await waitFor(() => expect(confirmation).toHaveBeenCalled())
    await waitFor(() => expect((screen.getByRole('button', { name: 'admin:nodes.tunnel.sync' }) as HTMLButtonElement).disabled).toBe(false))
    expect(api.post).toHaveBeenCalledTimes(1)
    expect(props.onRelays).not.toHaveBeenCalled()
    expect(snack).not.toHaveBeenCalled()
  })

  it('shows the message when the landing cannot be copied', async () => {
    api.get.mockRejectedValue(refusal({ error: 'the landing is not REALITY' }, 400))
    renderEditor(tunnelLine)
    fireEvent.click(screen.getByRole('button', { name: 'admin:nodes.tunnel.sync' }))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('the landing is not REALITY', 'error'))
    expect(confirmation).not.toHaveBeenCalled()
    expect(api.post).not.toHaveBeenCalled()
  })

  it('opens the listener settings from the button and from the routes note', () => {
    const props = renderEditor(tunnelLine)
    fireEvent.click(screen.getByRole('button', { name: 'admin:nodes.tunnel.open_listener' }))
    fireEvent.click(screen.getByRole('button', { name: 'admin:nodes.tunnel.routes_open' }))
    expect(props.onOpenListener).toHaveBeenNthCalledWith(1, 77)
    expect(props.onOpenListener).toHaveBeenNthCalledWith(2, 77)
  })
})

describe('mergeServerLine', () => {
  it('takes the server fields for one line and keeps the admin\'s edits and other rows', () => {
    const rows = [{ ...forwardLine, name: 'edited' }, { ...forwardLine, id: 'L2', name: 'other' }]
    const out = mergeServerLine(rows, [{ ...tunnelLine }], 'L1')
    expect(out[0]).toMatchObject({ name: 'edited', mode: 'tunnel', port: 40123, running: true, tunnel: listener })
    expect(out[1]).toBe(rows[1])
  })
  it('keeps the listener object when it did not change', () => {
    const row = { ...tunnelLine, tunnel: { ...listener } }
    expect(mergeServerLine([row], [{ ...tunnelLine }], 'L1')[0].tunnel).toBe(row.tunnel)
  })
})
