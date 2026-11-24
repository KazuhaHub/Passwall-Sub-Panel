// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { nativeServer, provisioning, releaseCatalog, waiting } from './install/installationHarness'
import { api, installReads, list, mount, server } from './testHarness'
import { dropInstallation, leaveInstallation } from './install/installHandoff'
import ServersPage from './ServersPage'

const releaseReads = vi.hoisted(() => vi.fn())
vi.mock('@/api/nodeReleases', () => ({ listNodeReleases: releaseReads }))
beforeEach(() => { releaseReads.mockResolvedValue(releaseCatalog); dropInstallation(nativeServer.id) })

describe('the server pages and their addresses', () => {
  it('shows no such server for a malformed id on the installation page, without asking the panel', async () => {
    mount(<ServersPage />, '/admin/servers/abc/install')
    expect(await screen.findByText('admin:servers.detail.install_not_found')).toBeTruthy()
    expect(api.get).not.toHaveBeenCalled()
  })

  it('says a server that is not a Passwall Node has no installation page', async () => {
    installReads({ '/admin/servers': list([server]) })
    mount(<ServersPage />, '/admin/servers/1/install')
    expect(await screen.findByText('admin:servers.detail.install_not_found')).toBeTruthy()
    expect(api.get.mock.calls.some(([url]) => String(url).includes('node-installation'))).toBe(false)
  })

  it('keeps the installation page and the add page from anyone who is not an administrator', async () => {
    useAuthStore.setState({ role: 'operator' })
    installReads({ '/admin/servers': list([nativeServer]) })
    mount(<ServersPage />, '/admin/servers/7/install')
    expect(await screen.findByText('admin:servers.install_reinstall.forbidden')).toBeTruthy()
    expect(api.get.mock.calls.some(([url]) => String(url).includes('node-installation'))).toBe(false)
    mount(<ServersPage />, '/admin/servers/new')
    expect(await screen.findByText('admin:servers.detail.create_forbidden')).toBeTruthy()
  })

  it('opens a server just created on what the create handed over, without reading its credential again', async () => {
    installReads({ '/admin/servers': list([]), '/admin/servers/7/node-agent-status': waiting })
    leaveInstallation({ server: nativeServer, provisioning, creating: true })
    mount(<ServersPage />, '/admin/servers/7/install')
    await screen.findByRole('combobox', { name: 'admin:servers.native.agent_version' })
    expect(screen.getByRole('heading', { level: 1 }).textContent).toContain('admin:servers.passwall_node_install.title')
    expect(api.get.mock.calls.some(([url]) => String(url).endsWith('/node-installation'))).toBe(false)
  })

  it('reads the installation of an existing node by its address, and the back link returns to the list as it was left', async () => {
    installReads({ '/admin/servers': list([nativeServer]), '/admin/servers/7/node-installation': provisioning, '/admin/servers/7/node-agent-status': waiting })
    const { router } = mount(<ServersPage />, '/admin/servers?q=native')
    router.navigate('/admin/servers/7/install', { state: { from: '/admin/servers?q=native' } })
    await screen.findByRole('combobox', { name: 'admin:servers.native.agent_version' })
    expect(api.get).toHaveBeenCalledWith('/admin/servers/7/node-installation', expect.anything())
    expect(screen.getByRole('link', { name: 'admin:servers.title' }).getAttribute('href')).toBe('/admin/servers?q=native')
    fireEvent.click(screen.getByRole('link', { name: 'admin:servers.title' }))
    await waitFor(() => expect(router.state.location.search).toBe('?q=native'))
  })
})
