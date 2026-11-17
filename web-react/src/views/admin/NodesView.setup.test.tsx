// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, list, mount, node, server } from '@/test/adminSaveHarness'
import NodesView from './NodesView'

describe('node setup', () => {
  it('requires a reviewed plan before it creates an inbound', async () => {
    api.post.mockImplementation(async (path: string) => {
      if (path === '/admin/nodes/generate-reality-keypair') {
        return { data: { private_key: 'private', public_key: 'public', short_id: 'abcd' } }
      }
      if (path === '/admin/nodes/setup-preview') {
        return { data: { token: 'reviewed', bindings: [{ panel_id: server.id, name: server.name, kind: 'landing', port: 443 }] } }
      }
      if (path === '/admin/nodes') return { data: node }
      throw new Error(`Unexpected POST ${path}`)
    })
    installReads({ '/admin/servers': list([{ ...server, region: 'US', capabilities: [...server.capabilities, 'inbound.delete'] }]) })
    mount(<NodesView />)
    const create = await screen.findByRole('button', { name: 'admin:nodes.create' })
    await waitFor(() => expect(create).toHaveProperty('disabled', false))
    fireEvent.click(create)
    const dialog = await screen.findByRole('dialog')
    const click = (name: string) => fireEvent.click(within(dialog).getByRole('button', { name }))
    expect((within(dialog).getByLabelText(/admin:nodes.field.display_name/) as HTMLInputElement).value).toBe(server.name)
    expect((within(dialog).getByLabelText(/admin:nodes.field.region/) as HTMLInputElement).value).toBe('US')
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'admin:nodes.setup.next' })).toHaveProperty('disabled', false))
    click('admin:nodes.setup.next')
    click('admin:nodes.relay.entry_external')
    fireEvent.change(within(dialog).getByLabelText(/admin:nodes.relay.address/), { target: { value: 'relay.example.test' } })
    click('admin:nodes.setup.next')
    click('admin:nodes.setup.review')
    await within(dialog).findByRole('table', { name: 'admin:nodes.setup.review' })
    expect(api.post.mock.calls.some(([path]) => path === '/admin/nodes')).toBe(false)
    click('admin:nodes.setup.confirm')
    await waitFor(() => expect(api.post.mock.calls.find(([path]) => path === '/admin/nodes')?.[1]).toMatchObject({
      setup: { confirm: 'reviewed' },
      relays: [{ address: 'relay.example.test' }],
      inbound: { port: 443, protocol: 'vless' },
    }))
  })

  it('shows direct and CDN lines with their own multiplier in the lines tab', async () => {
    installReads({ '/admin/nodes': list([{ ...node, relays: [
      { id: 'cdn', mode: 'cdn', name: '', address: '192.0.2.35', domain: 'cdn.example.test', port: 443, enabled: true, traffic_factor_milli: 1500 },
    ] }]) })
    mount(<NodesView />)
    fireEvent.click(await screen.findByRole('tab', { name: 'admin:nodes.lines.title' }))
    const lines = await screen.findByRole('table', { name: 'admin:nodes.lines.title' })
    expect(within(lines).getByText('×1.5')).toBeTruthy()
    expect(within(lines).getByText(/admin:nodes.setup.kind.landing/)).toBeTruthy()
    fireEvent.change(screen.getByLabelText('admin:nodes.lines.filter'), { target: { value: '192.0.2.35' } })
    expect(within(lines).queryByText(/admin:nodes.setup.kind.landing/)).toBeNull()
  })
})
