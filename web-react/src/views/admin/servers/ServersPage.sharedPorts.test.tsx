// @vitest-environment jsdom
import { screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, list, mount, server } from './testHarness'
import { openServerTab } from './install/installationHarness'
import ServersPage from './ServersPage'

const psp = { ...server, panel_type: 'psp' }

const options = { listeners: [], lines: [] }

const view = (over: Record<string, unknown> = {}) => ({
  support: { supported: true }, withheld: [], fronts: [], max: 16,
  defaults: { port: 443, loopback_low: 30000, loopback_high: 30999, probe_low: 31000, probe_high: 31999 }, ...over,
})

// The sheet's own behaviour is tested in fronts/SharedPortsDialog.test.tsx; this file keeps what belongs to the servers page.
function install(fronts: unknown, ports: unknown[] = []) {
  installReads({ '/admin/servers': list([psp]) })
  const base = api.get.getMockImplementation()!
  api.get.mockImplementation(async (url: string, cfg?: { params?: { what?: string } }) => {
    if (url === '/admin/servers/1/fronts') return { data: fronts }
    if (url === '/admin/servers/1/fronts/options') return { data: options }
    if (url === '/admin/servers/1/ports') return { data: { ports } }
    if (url === '/admin/servers/1/fronts/suggest') return { data: cfg?.params?.what === 'camouflage' ? { host: 'www.asus.com', port: 443 } : { port: 30012 } }
    return base(url)
  })
}

async function openDialog() {
  await openServerTab(psp as never, 'maintenance', 'admin:servers.action.shared_ports')
  return screen.findByRole('dialog')
}

describe('ServersPage shared ports', () => {
  it('offers the dialog for a Passwall Node server only', async () => {
    installReads({ '/admin/servers': list([server]) })
    mount(<ServersPage />)
    await openServerTab(server as never, 'maintenance')
    await screen.findByRole('button', { name: 'admin:servers.action.upgrade_xray' })
    expect(screen.queryByRole('button', { name: 'admin:servers.action.shared_ports' })).toBeNull()
  })

  it('says Xray is needed and disables adding when the server cannot run fronts', async () => {
    install(view({ support: { supported: false, reason: 'engine_not_xray' } }))
    mount(<ServersPage />)
    const dialog = await openDialog()
    await within(dialog).findByText('admin:servers.fronts.unsupported.engine_not_xray')
    // Off with its reason (a tooltip), not removed: aria-disabled keeps it focusable.
    const add = within(dialog).getByRole('button', { name: 'admin:servers.fronts.add' })
    expect(add.getAttribute('aria-disabled')).toBe('true')
  })
})
