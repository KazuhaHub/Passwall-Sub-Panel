// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api, installReads, list, mount, node } from '@/test/adminSaveHarness'
import { LegacyProbe, openListener, openMeta } from './legacyHarness'

const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))

const entry = { id: 2, name: 'entry', url: '', panel_type: 'psp', capabilities: ['inbound.read', 'inbound.update', 'inbound.create', 'inbound.enable'], enabled: true }
const landingServer = { ...entry, id: 1, name: 'server' }

const listener = {
  node_id: 77, name: 'tunnel-77', enabled: true, port: 40123, protocol: 'vless', flow: 'xtls-rprx-vision', security: 'reality',
  server_names: ['www.asus.com'], target: 'www.asus.com:443', short_ids: ['ab12'], public_key: 'PUB', private_key: 'PRIV',
  config_sync_state: 'synced', applied: true, egress_panel_id: 1, egress_ready: true, routes: 0,
}
const tunnelLine = { id: 'T1', name: 'Mobile', address: 'entry.example', port: 40123, enabled: true, entry_panel_id: 2, mode: 'tunnel', running: true, tunnel: listener }

function setup(relays: unknown[]) {
  const landing = { ...node, panel_name: 'server', relays }
  installReads({ '/admin/nodes': list([landing]), '/admin/servers': list([landingServer, entry]) })
  api.put.mockImplementation(async (_url, body) => ({ data: { ...landing, ...body } }))
  return landing
}

beforeEach(() => confirmation.mockReset())

describe('the legacy metadata editor: exit tunnel and listener nodes', () => {
  it('keeps an explicitly configured camouflage target when editing the SNI', async () => {
    const landing = { ...node, egress_panel_id: 2 }
    const link = {
      exists: true, protocol: 'vless_reality', exit_address: '', reality_server_name: 'original.example',
      reality_target: 'custom.example:8443', server_address: 'entry.example', up_bytes: 0, down_bytes: 0,
    }
    installReads({
      '/admin/nodes': list([landing]),
      '/admin/servers': list([landingServer, entry]),
      '/admin/transit/links/1/2': link,
    })
    api.put.mockImplementation(async (url: string, body: object) =>
      ({ data: { ...(url === '/admin/transit/links/1/2' ? link : landing), ...body } }))
    mount(<LegacyProbe />)
    const dialog = await openMeta()
    const advanced = await within(dialog).findByRole('button', { name: 'admin:nodes.egress.advanced' })
    expect(advanced.getAttribute('aria-expanded')).toBe('false')
    fireEvent.click(advanced)
    fireEvent.change(within(dialog).getByLabelText('admin:nodes.egress.reality_server_name'), { target: { value: 'new.example' } })
    const target = within(dialog).getByLabelText('admin:nodes.egress.reality_target') as HTMLInputElement
    expect(target.value).toBe(link.reality_target)
    fireEvent.change(target, { target: { value: 'other.example:9443' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'common:actions.ok' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/admin/transit/links/1/2', expect.objectContaining({
      reality_server_name: 'new.example',
      reality_target: 'other.example:9443',
    })))
  })

  it('shows a tunnel listener node with the line banner and without the fields the server ignores', async () => {
    setup([tunnelLine])
    const childNode = { ...node, id: 77, display_name: 'tunnel-77', parent_node_id: 1, parent_line_id: 'T1', parent_node_name: 'old-name', relays: [] }
    const reads = api.get.getMockImplementation()!
    api.get.mockImplementation(async (url: string, ...rest: unknown[]) =>
      url === '/admin/nodes/77' ? { data: { node: childNode, clients: [] } } : reads(url, ...rest))
    mount(<LegacyProbe />)
    await openListener()
    const banner = await screen.findByTestId('listener-banner')
    expect(banner.textContent).toContain('admin:nodes.tunnel.listener_banner')
    expect(screen.queryByLabelText(/admin:nodes.field.display_name/)).toBeNull()
    expect(screen.queryByLabelText(/admin:nodes.field.region/)).toBeNull()
    expect(screen.queryByText('admin:nodes.relay.title')).toBeNull()
    expect(screen.queryByTestId('egress-section')).toBeNull()
  })
})
