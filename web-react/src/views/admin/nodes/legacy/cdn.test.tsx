// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { api, installReads, list, mount, node, snack } from '@/test/adminSaveHarness'
import CdnLineFields, { defaultClientTLS } from '../../CdnLineFields'
import { LegacyProbe, openMeta } from './legacyHarness'
import type { RelayLine } from '@/api/types'

const entry = { id: 2, name: 'entry', url: '', panel_type: 'psp', capabilities: ['inbound.read', 'inbound.update', 'inbound.create', 'inbound.enable'], enabled: true }
const landingServer = { ...entry, id: 1, name: 'server' }

const cdnLine: RelayLine = {
  id: 'C1', name: 'CF', mode: 'cdn', domain: 'cdn.example.com', address: '104.16.1.1', port: 443, sni: 'cdn.example.com', host: 'cdn.example.com', enabled: true,
  cdn_issues: [
    { code: 'cert_not_covering', warn: true, params: { domain: 'cdn.example.com' } },
    { code: 'port_differs', warn: false, params: { port: '443', origin_port: '8443' } },
  ],
}

const metadataPuts = () => api.put.mock.calls.filter(([url]) => String(url) === '/admin/nodes/1/metadata')

function setup(relays: unknown[]) {
  const landing = { ...node, panel_name: 'server', relays }
  installReads({ '/admin/nodes': list([landing]), '/admin/servers': list([landingServer, entry]) })
  api.put.mockImplementation(async (_url, body) => ({ data: { ...landing, ...body } }))
  return landing
}

describe('CdnLineFields', () => {
  it('shows the fields, the no-probe note and the issues of the saved node', () => {
    mount(<CdnLineFields line={cdnLine} onPatch={vi.fn()} />)
    expect(screen.getByTestId('cdn-line').textContent).toContain('admin:nodes.cdn.no_entry')
    expect(screen.getByLabelText('admin:nodes.cdn.tip')).toBeTruthy()
    expect((screen.getByLabelText(/admin:nodes.cdn.domain/) as HTMLInputElement).value).toBe('cdn.example.com')
    expect((screen.getByLabelText(/admin:nodes.cdn.address/) as HTMLInputElement).value).toBe('104.16.1.1')
    expect((screen.getByLabelText(/admin:nodes.cdn.port/) as HTMLInputElement).value).toBe('443')
    expect((screen.getByLabelText(/admin:nodes.relay.sni/) as HTMLInputElement).value).toBe('cdn.example.com')
    expect((screen.getByLabelText(/admin:nodes.cdn.host/) as HTMLInputElement).value).toBe('cdn.example.com')
    const items = within(screen.getByTestId('cdn-issues')).getAllByRole('listitem')
    expect(items.map(i => i.getAttribute('data-code'))).toEqual(['cert_not_covering', 'port_differs'])
  })

  it('keeps the saved address, SNI and Host when the domain changes', () => {
    const onPatch = vi.fn()
    mount(<CdnLineFields line={{ ...cdnLine, address: cdnLine.domain! }} onPatch={onPatch} />)
    fireEvent.change(screen.getByLabelText(/admin:nodes.cdn.domain/), { target: { value: 'new.example.com' } })
    expect(onPatch).toHaveBeenCalledWith({ domain: 'new.example.com' })
  })
})

describe('CdnLineFields client TLS', () => {
  it('defaults from the port and follows it until the admin sets it', () => {
    expect(defaultClientTLS(443)).toBe(true)
    expect(defaultClientTLS(8443)).toBe(true)
    expect([80, 8080, 8880, 2052, 2082, 2086, 2095].some(defaultClientTLS)).toBe(false)
    const onPatch = vi.fn()
    mount(<CdnLineFields line={cdnLine} onPatch={onPatch} />)
    expect((screen.getByLabelText(/admin:nodes.cdn.client_tls$/) as HTMLInputElement).checked).toBe(true)
    fireEvent.change(screen.getByLabelText(/admin:nodes.cdn.port/), { target: { value: '8080' } })
    expect(onPatch).toHaveBeenLastCalledWith({ port: 8080 })
  })

  it('keeps an explicit TLS choice even when it matches the old port default', () => {
    const onPatch = vi.fn()
    mount(<CdnLineFields line={{ ...cdnLine, client_tls: true }} onPatch={onPatch} />)
    fireEvent.change(screen.getByLabelText(/admin:nodes.cdn.port/), { target: { value: '8080' } })
    expect(onPatch).toHaveBeenLastCalledWith({ port: 8080 })
    fireEvent.click(screen.getByLabelText(/admin:nodes.cdn.client_tls$/))
    expect(onPatch).toHaveBeenLastCalledWith({ client_tls: false })
  })

  it('says why a landing cannot go through a CDN from the code, in the locale', () => {
    const line = { ...cdnLine, cdn_issues: [{ code: 'landing_not_capable', warn: true, params: { reason: 'English reason', reason_code: 'reality', node: 'JP' } }] }
    mount(<CdnLineFields line={line} onPatch={vi.fn()} />)
    expect(screen.getByTestId('cdn-issues').textContent).toBe('admin:nodes.cdn.reason.reality')
  })
})

describe('NodesView CDN lines', () => {
  it('adds a CDN line and sends a clean payload that leaves defaults to the server', async () => {
    setup([])
    mount(<LegacyProbe />)
    const dialog = await openMeta()
    fireEvent.click(within(dialog).getByRole('button', { name: 'admin:nodes.relay.add' }))
    fireEvent.mouseDown(within(dialog).getByLabelText('admin:nodes.relay.entry'))
    fireEvent.click(await screen.findByRole('option', { name: 'admin:nodes.relay.entry_cdn' }))
    expect(await within(dialog).findByTestId('cdn-line')).toBeTruthy()
    // None of the forward / tunnel controls remain.
    expect(within(dialog).queryByTestId('tunnel-line')).toBeNull()
    expect(within(dialog).queryByLabelText(/admin:nodes.relay.address/)).toBeNull()
    fireEvent.change(within(dialog).getByLabelText(/admin:nodes.cdn.domain/), { target: { value: 'cdn.example.com' } })
    expect((within(dialog).getByLabelText(/admin:nodes.relay.sni/) as HTMLInputElement).placeholder).toBe('cdn.example.com')
    expect((within(dialog).getByLabelText(/admin:nodes.cdn.host/) as HTMLInputElement).placeholder).toBe('cdn.example.com')
    expect((within(dialog).getByLabelText(/admin:nodes.cdn.port/) as HTMLInputElement).value).toBe('443')
    fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
    await waitFor(() => expect(metadataPuts()).toHaveLength(1))
    const line = (metadataPuts()[0][1] as { relays: Record<string, unknown>[] }).relays[0]
    expect(line).toMatchObject({ mode: 'cdn', domain: 'cdn.example.com', address: '', port: 443 })
    expect(line.entry_panel_id).toBeUndefined()
    expect(line.client_tls).toBeUndefined()
    expect(line.sni).toBeUndefined()
    expect(line.host).toBeUndefined()
    expect(line).not.toHaveProperty('cdn_issues')
  })

  it('blocks the save of a CDN line with no domain instead of dropping it', async () => {
    setup([])
    mount(<LegacyProbe />)
    const dialog = await openMeta()
    fireEvent.click(within(dialog).getByRole('button', { name: 'admin:nodes.relay.add' }))
    fireEvent.mouseDown(within(dialog).getByLabelText('admin:nodes.relay.entry'))
    fireEvent.click(await screen.findByRole('option', { name: 'admin:nodes.relay.entry_cdn' }))
    // Blank (so the browser's own required check passes) is still no domain.
    fireEvent.change(within(dialog).getByLabelText(/admin:nodes.cdn.domain/), { target: { value: '  ' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('admin:nodes.cdn.domain_required', 'warning'))
    expect(metadataPuts()).toHaveLength(0)
  })

  it('round-trips a saved CDN line, shows its issues, fixes its kind, and warns after a save that has some', async () => {
    const landing = setup([cdnLine])
    // The server answers with the issues it finds on the saved node.
    api.put.mockImplementation(async (_url, body) => ({ data: { ...landing, ...body, relays: [cdnLine] } }))
    mount(<LegacyProbe />)
    const dialog = await openMeta()
    expect(within(dialog).getByTestId('cdn-issues')).toBeTruthy()
    // The kind of a saved CDN line cannot change.
    expect(within(dialog).getByLabelText('admin:nodes.relay.entry').getAttribute('aria-disabled')).toBe('true')
    fireEvent.change(within(dialog).getByLabelText(/admin:nodes.cdn.address/), { target: { value: '104.16.9.9' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
    await waitFor(() => expect(metadataPuts()).toHaveLength(1))
    expect((metadataPuts()[0][1] as { relays: unknown[] }).relays[0]).toMatchObject({ id: 'C1', mode: 'cdn', domain: 'cdn.example.com', address: '104.16.9.9' })
    await waitFor(() => expect(snack).toHaveBeenCalledWith('admin:nodes.cdn.saved_warnings', 'warning'))
  })

  it('does not offer a CDN kind on a saved forward line', async () => {
    setup([{ id: 'F1', name: 'Plain', address: 'fwd.example', port: 8443, enabled: true }])
    mount(<LegacyProbe />)
    const dialog = await openMeta()
    fireEvent.mouseDown(within(dialog).getByLabelText('admin:nodes.relay.entry'))
    const option = await screen.findByRole('option', { name: 'admin:nodes.relay.entry_cdn' })
    expect(option.getAttribute('aria-disabled')).toBe('true')
  })
})
