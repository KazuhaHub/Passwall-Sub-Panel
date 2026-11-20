// @vitest-environment jsdom
import { fireEvent, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, list, mount, node, server } from '@/test/adminSaveHarness'
import { LegacyProbe, openCreateWizard } from './legacyHarness'

describe('node setup', () => {
  it('requires a reviewed plan before it creates an inbound', async () => {
    api.post.mockImplementation(async (path: string, request?: { setup?: { link_ports?: Record<string, number> } }) => {
      if (path === '/admin/nodes/generate-reality-keypair') {
        return { data: { private_key: 'private', public_key: 'public', short_id: 'abcd' } }
      }
      if (path === '/admin/nodes/setup-preview') {
        const port = request?.setup?.link_ports?.[`9/${server.id}`]
        return { data: { token: port ? 'reviewed-edit' : 'reviewed', bindings: [
          { panel_id: server.id, name: server.name, kind: 'landing', port: 443 },
          { panel_id: server.id, peer_id: 9, kind: 'link', port: port ?? 31001 },
          { panel_id: server.id, peer_id: 10, kind: 'link', port: 31002, reused: true },
        ] } }
      }
      if (path === '/admin/nodes') return { data: node }
      throw new Error(`Unexpected POST ${path}`)
    })
    installReads({ '/admin/servers': list([{ ...server, region: 'US', capabilities: [...server.capabilities, 'inbound.delete'] }]) })
    mount(<LegacyProbe />)
    const dialog = await openCreateWizard()
    const click = (name: string) => fireEvent.click(within(dialog).getByRole('button', { name }))
    expect((within(dialog).getByLabelText(/admin:nodes.field.display_name/) as HTMLInputElement).value).toBe(server.name)
    expect((within(dialog).getByLabelText(/admin:nodes.field.region/) as HTMLInputElement).value).toBe('US')
    const sni = within(dialog).getByLabelText(/admin:nodes.create_dialog.reality_server_names/) as HTMLInputElement
    const target = within(dialog).getByLabelText(/admin:nodes.create_dialog.reality_dest/) as HTMLInputElement
    const previousSite = sni.value
    click('admin:nodes.egress.random_site')
    expect(sni.value).not.toBe(previousSite)
    expect(target.value).toBe(`${sni.value}:443`)
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'admin:nodes.setup.next' })).toHaveProperty('disabled', false))
    click('admin:nodes.setup.next')
    click('admin:nodes.relay.entry_external')
    fireEvent.change(within(dialog).getByLabelText(/admin:nodes.relay.address/), { target: { value: 'relay.example.test' } })
    click('admin:nodes.setup.next')
    click('admin:nodes.setup.review')
    await within(dialog).findByRole('table', { name: 'admin:nodes.setup.review' })
    const linkPort = within(dialog).getByRole('spinbutton', { name: 'admin:nodes.setup.link_port' }) as HTMLInputElement
    expect(linkPort.value).toBe('31001')
    fireEvent.change(linkPort, { target: { value: '31999' } })
    click('admin:nodes.setup.review')
    await within(dialog).findByRole('button', { name: 'admin:nodes.setup.confirm' })
    expect(api.post.mock.calls.some(([path]) => path === '/admin/nodes')).toBe(false)
    click('admin:nodes.setup.confirm')
    await waitFor(() => expect(api.post.mock.calls.find(([path]) => path === '/admin/nodes')?.[1]).toMatchObject({
      setup: { confirm: 'reviewed-edit', link_ports: { [`9/${server.id}`]: 31999 } },
      relays: [{ address: 'relay.example.test' }],
      inbound: { port: 443, protocol: 'vless' },
    }))
  })
})
