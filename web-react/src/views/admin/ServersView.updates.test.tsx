// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Server } from '@/api/servers'
import type { NodeReleaseCatalog } from '@/api/nodeReleases'
import { api, installReads, list, mount } from '@/test/adminSaveHarness'
import { useAuthStore } from '@/stores/auth'
import english from '@/locales/en-US/admin.json'
import ServersView from './ServersView'

const releaseReads = vi.hoisted(() => vi.fn())
vi.mock('@/api/nodeReleases', () => ({ listNodeReleases: releaseReads }))
vi.mock('./NativeAgentUpgradeDialog', () => ({
  NativeAgentUpgradeDialog: ({ server }: { server: Server | null }) => server
    ? <div role="dialog" aria-label="native-upgrade-preview">{server.id}</div> : null,
}))

const native: Server = {
  id: 7, name: 'native beta', panel_type: 'psp', update_channel: 'beta',
  panel_version: 'v0.0.1-beta3 (abcdef0)', url: '', capabilities: ['core.upgrade'],
  auth_method: '', has_api_token: false, has_password: false, insecure_https: false,
}
const catalog: NodeReleaseCatalog = {
  checked_at: '2026-09-14T00:00:00Z',
  releases: [{
    version: 'v0.0.1-beta4', channel: 'testing', published_at: '2026-09-13T00:00:00Z',
    release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/v0.0.1-beta4',
    notes: '', methods: ['linux'], platforms: [{ os: 'linux', arch: 'amd64' }, { os: 'linux', arch: 'arm64' }],
  }],
}
const sui: Server = {
  ...native, id: 8, name: 'S-UI server', panel_type: 'sui', panel_version: '1.5.0',
  url: 'https://sui.example.test', capabilities: [], auth_method: 'token', has_api_token: true,
  latest_sui_version: 'v1.6.2', update_available: true,
}
const xui: Server = {
  ...sui, id: 9, name: '3X-UI server', panel_type: '3xui', panel_version: '3.4.2',
  latest_sui_version: undefined, latest_xui_version: 'v3.7.0',
  capabilities: ['panel.upgrade', 'core.upgrade'],
}

beforeEach(() => {
  // Hooks may return a teardown function; do not accidentally return the mock.
  releaseReads.mockResolvedValue(catalog)
})
const rowFor = async (name: string) => (await screen.findByText(name)).closest('tr')!
const upgradeWrites = () => api.post.mock.calls.filter(([url]) => url !== '/admin/servers/probe')

describe('Server update hints and paired tray icons', () => {
  it('uses matching tray icons for install, Node upgrade and core selection', async () => {
    installReads({ '/admin/servers': list([native]) })
    mount(<ServersView />)
    fireEvent.click(within(await rowFor(native.name)).getByRole('button', { name: 'admin:servers.action.more' }))
    expect(within(screen.getByRole('menuitem', { name: 'admin:servers.install_reinstall.action' }))
      .getByTestId('DownloadOutlinedIcon')).toBeTruthy()
    expect(within(screen.getByRole('menuitem', { name: 'admin:servers.agent_upgrade.action' }))
      .getByTestId('UploadOutlinedIcon')).toBeTruthy()
    expect(within(screen.getByRole('menuitem', { name: 'admin:servers.action.select_core' }))
      .getByTestId('UploadOutlinedIcon')).toBeTruthy()
    expect(screen.queryByTestId('UpgradeIcon')).toBeNull()
    expect(screen.queryByTestId('SystemUpdateAltIcon')).toBeNull()
    expect(english.servers.install_reinstall.action).toBe('Install / Reinstall')
    expect(english.servers.install_reinstall.title).toBe('Install / Reinstall · {{name}}')
  })

  it('reads one shared catalog, shows only the newer matching-channel Node, and opens a preview without upgrading', async () => {
    const stable = { ...native, id: 10, name: 'native stable', update_channel: 'stable' as const }
    const current = { ...native, id: 11, name: 'native current', panel_version: 'v0.0.1-beta4' }
    installReads({ '/admin/servers': list([native, stable, current]) })
    mount(<ServersView />)
    const target = await rowFor(native.name)
    const hint = await within(target).findByRole('button', { name: 'admin:servers.agent_upgrade.available' })
    expect(within(await rowFor(stable.name)).queryByText('admin:servers.update_available_chip')).toBeNull()
    expect(within(await rowFor(current.name)).queryByText('admin:servers.update_available_chip')).toBeNull()
    expect(releaseReads).toHaveBeenCalledTimes(1)
    expect(releaseReads.mock.calls[0][0]).toBeInstanceOf(AbortSignal)
    fireEvent.click(hint)
    expect(screen.getByRole('dialog', { name: 'native-upgrade-preview' }).textContent).toBe('7')
    expect(upgradeWrites()).toHaveLength(0)
    expect(api.put).not.toHaveBeenCalled()
    expect(api.delete).not.toHaveBeenCalled()
  })

  it('distinguishes a catalog failure from an available update and makes no upgrade request', async () => {
    releaseReads.mockRejectedValue(new Error('offline catalog'))
    installReads({ '/admin/servers': list([native]) })
    mount(<ServersView />)
    expect(await screen.findByText('admin:servers.native.update_check_failed')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'admin:servers.agent_upgrade.available' })).toBeNull()
    expect(upgradeWrites()).toHaveLength(0)
  })

  it('keeps update metadata visible without an upgrade action for an operator', async () => {
    useAuthStore.setState({ role: 'operator' })
    installReads({ '/admin/servers': list([native]) })
    mount(<ServersView />)
    expect(await screen.findByText('admin:servers.update_available_chip')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'admin:servers.agent_upgrade.available' })).toBeNull()
    expect(screen.queryByRole('dialog', { name: 'native-upgrade-preview' })).toBeNull()
    expect(upgradeWrites()).toHaveLength(0)
  })

  it('links the S-UI hint to its official release without pretending it supports remote upgrades, preserving the 3X-UI hint', async () => {
    installReads({ '/admin/servers': list([sui, xui]) })
    mount(<ServersView />)
    const suiRow = await rowFor(sui.name)
    const hint = within(suiRow).getByRole('link', { name: 'admin:servers.sui_update.available' })
    expect(hint.getAttribute('href')).toBe('https://github.com/alireza0/s-ui/releases/latest')
    expect(hint.getAttribute('target')).toBe('_blank')
    expect(hint.getAttribute('rel')).toBe('noopener noreferrer')
    expect(within(await rowFor(xui.name)).getByText('admin:servers.update_available_chip')).toBeTruthy()
    fireEvent.click(within(suiRow).getByRole('button', { name: 'admin:servers.action.more' }))
    expect(screen.queryByRole('menuitem', { name: 'admin:servers.action.upgrade_panel' })).toBeNull()
    expect(screen.queryByRole('menuitem', { name: 'admin:servers.action.upgrade_xray' })).toBeNull()
    expect(releaseReads).not.toHaveBeenCalled()
    expect(upgradeWrites()).toHaveLength(0)
  })

  it('merges fresh upstream update fields from a successful probe, including false to clear an old hint', async () => {
    installReads({ '/admin/servers': list([sui]) })
    api.post.mockResolvedValue({ data: { ok: true, panel_version: '1.6.2', latest_sui_version: 'v1.6.2', update_available: false } })
    mount(<ServersView />)
    const target = await rowFor(sui.name)
    await waitFor(() => expect(within(target).getByText('S-UI 1.6.2')).toBeTruthy())
    expect(within(target).queryByRole('link', { name: 'admin:servers.sui_update.available' })).toBeNull()
    expect(upgradeWrites()).toHaveLength(0)
  })

  it('shows a S-UI update on the first page visit when shared metadata finishes after the connection probes', async () => {
    const cold = { ...sui, latest_sui_version: undefined, update_available: undefined }
    const current = { ...cold, id: 10, name: 'current S-UI', panel_version: '1.6.2' }
    installReads({ '/admin/servers': list([cold, current]) })
    const ordinaryRead = api.get.getMockImplementation()!
    let resolveMetadata!: (result: { data: { version: string } }) => void
    api.get.mockImplementation((url: string, ...args: unknown[]) => url === '/admin/servers/sui-release'
      ? new Promise(resolve => { resolveMetadata = resolve }) : ordinaryRead(url, ...args))
    api.post.mockImplementation(async (_url: string, body: { id: number }) => ({
      data: { ok: true, panel_version: body.id === cold.id ? '1.5.0' : '1.6.2' },
    }))
    mount(<ServersView />)
    const target = await rowFor(cold.name)
    await waitFor(() => expect(api.post).toHaveBeenCalledTimes(2))
    expect(within(target).queryByRole('link', { name: 'admin:servers.sui_update.available' })).toBeNull()
    await act(async () => resolveMetadata({ data: { version: 'v1.6.2' } }))
    expect(within(target).getByRole('link', { name: 'admin:servers.sui_update.available' })).toBeTruthy()
    expect(within(await rowFor(current.name)).queryByRole('link', { name: 'admin:servers.sui_update.available' })).toBeNull()
    expect(api.get.mock.calls.filter(([url]) => url === '/admin/servers/sui-release')).toHaveLength(1)
    expect(upgradeWrites()).toHaveLength(0)
  })
})
