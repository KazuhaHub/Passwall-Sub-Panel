// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { NodeReleaseCatalog } from '@/api/nodeReleases'
import type { NativeAgentStatus, NativeInstallationFiles, NativeServerProvisioning, Server } from '@/api/servers'
import { api, installReads, list, mount } from '@/test/adminSaveHarness'
import { useAuthStore } from '@/stores/auth'
import ConfirmHost from '@/components/ConfirmHost'
import ServersView, { isNodeReleaseVersion, NativeInstallationDialog } from './ServersView'
import { hostFromURL } from './NodesView'

const copy = vi.hoisted(() => vi.fn().mockResolvedValue(true))
vi.mock('@/utils/clipboard', () => ({ copyToClipboard: copy }))
const releaseReads = vi.hoisted(() => vi.fn())
vi.mock('@/api/nodeReleases', () => ({ listNodeReleases: releaseReads }))

const releaseCatalog: NodeReleaseCatalog = {
  checked_at: '2026-09-12T13:00:00Z',
  releases: ['v1.2.3', 'v1.2.3-beta.2', 'v1.2.3-beta.1'].map(version => ({
    version, channel: version.includes('-') ? 'testing' : 'stable',
    published_at: '2026-09-12T12:00:00Z', notes: 'Reviewed contract fixture',
    release_url: `https://github.com/KazuhaHub/Passwall-Node/releases/tag/${version}`,
    methods: ['linux', 'docker', 'manual'],
    platforms: (['linux', 'darwin', 'windows'] as const).flatMap(os =>
      (['amd64', 'arm64'] as const).map(arch => ({ os, arch }))),
  })),
}
beforeEach(() => releaseReads.mockResolvedValue(releaseCatalog))

const nativeServer: Server = {
  id: 7, name: 'test-native', panel_type: 'psp', url: 'psp://agt_7', capabilities: [],
  auth_method: '', has_api_token: false, has_password: false, insecure_https: false,
}
const provisioning: NativeServerProvisioning = {
  server: nativeServer, agent_id: 'agt_7', credential: 'existing-long-lived-secret', endpoint: 'https://panel.test/v1/node/sync',
}
const waiting: NativeAgentStatus = { state: 'waiting', configured_nodes: 0 }

function reads(status: NativeAgentStatus = waiting) {
  installReads({
    '/admin/servers': list([nativeServer]),
    '/admin/servers/7/node-installation': provisioning,
    '/admin/servers/7/node-agent-status': status,
  })
}

function versionInput(): HTMLInputElement {
  return screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' }).parentElement!.querySelector('input')!
}

async function selectVersion(version: string) {
  const channel = version.includes('-') ? 'testing' : 'stable'
  const channelSelect = await screen.findByRole('combobox', { name: 'admin:servers.native.release_channel' })
  fireEvent.mouseDown(channelSelect)
  fireEvent.click(await screen.findByRole('option', { name: `admin:servers.native.release_${channel}` }))
  const versionSelect = screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' })
  await waitFor(() => expect(versionSelect.getAttribute('aria-disabled')).not.toBe('true'))
  fireEvent.mouseDown(versionSelect)
  fireEvent.click(await screen.findByRole('option', { name: version }))
}

function copyScript(): HTMLButtonElement {
  return screen.getByRole('button', { name: 'admin:servers.native.copy_script' })
}

async function selectMethod(method: 'linux' | 'docker' | 'manual', container: HTMLElement = document.body) {
  fireEvent.mouseDown(within(container).getByRole('combobox', { name: 'admin:servers.native.method_label' }))
  fireEvent.click(await screen.findByRole('option', { name: `admin:servers.native.method.${method}` }))
}

async function openInstallation(server: Server) {
  const row = (await screen.findByText(server.name)).closest('tr')!
  fireEvent.click(await within(row).findByRole('button', { name: 'admin:servers.action.more' }))
  fireEvent.click(await screen.findByRole('menuitem', { name: 'admin:servers.install_reinstall.action' }))
  fireEvent.click(await screen.findByRole('button', { name: 'admin:servers.install_reinstall.continue' }))
}

function generatedFiles(method: 'docker' | 'manual'): NativeInstallationFiles {
  return {
    method, os: 'linux', ...(method === 'manual' ? { arch: 'amd64' as const } : {}),
    files: [
      { name: 'credential', content: `${provisioning.credential}\n`, sensitive: true },
      { name: method === 'docker' ? 'compose.yaml' : 'node.env', content: method === 'docker'
        ? 'services:\n  node:\n    image: ghcr.io/kazuhahub/passwall-node:v1.2.3-beta.1\n    network_mode: host\n'
        : `NODE_ENDPOINT=${provisioning.endpoint}\nNODE_AGENT_ID=${provisioning.agent_id}\n` },
    ],
    steps: [
      { title: 'Private files', commands: ['chmod 0600 ./credential'] },
      { title: 'Start the node', commands: [method === 'docker' ? 'docker compose up -d'
        : `./passwall-node --endpoint '${provisioning.endpoint}' --agent-id '${provisioning.agent_id}' --credential-file ./credential --data-dir ./data`] },
    ],
  }
}

function materialContents(): string[] {
  return screen.getAllByLabelText('admin:servers.native.file_content').map(input => (input as HTMLTextAreaElement).value)
}

function materialPreviews(materials: NativeInstallationFiles): string[] {
  // Sensitive single-line password inputs remove CR/LF for display. Copy and
  // download below must still use the original file bytes, including its LF.
  return materials.files.map(file => file.sensitive ? file.content.replace(/[\r\n]/g, '') : file.content)
}

afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('Passwall Node installation', () => {
  it.each([['3xui', '3X-UI'], ['sui', 'S-UI']] as const)('opens one existing %s install/reinstall entry with its original backend and configures that same record', async (panelType, label) => {
    const upstream: Server = { ...nativeServer, panel_type: panelType, auth_method: 'token', url: 'https://upstream.test', has_api_token: true }
    installReads({ '/admin/servers': list([upstream]) })
    mount(<ServersView />)
    const row = (await screen.findByText(upstream.name)).closest('tr')!
    fireEvent.click(within(row).getByRole('button', { name: 'admin:servers.action.more' }))
    const entry = screen.getByRole('menuitem', { name: 'admin:servers.install_reinstall.action' })
    expect(entry.getAttribute('aria-disabled')).not.toBe('true')
    fireEvent.click(entry)
    expect(screen.getByRole('combobox', { name: 'admin:servers.install_reinstall.backend' }).textContent).toBe(label)
    expect(screen.getByText('admin:servers.install_reinstall.manual_unverified')).toBeTruthy()
    expect(api.get.mock.calls.some(([url]) => String(url).includes('node-installation') || String(url).includes('node-migration'))).toBe(false)
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.install_reinstall.configure_original' }))
    expect(screen.getByRole('heading', { name: 'admin:servers.edit_title' })).toBeTruthy()
    expect((screen.getByRole('textbox', { name: /admin:servers.field.name/ }) as HTMLInputElement).value).toBe(upstream.name)
    expect(screen.getByRole('combobox', { name: 'admin:servers.field.panel_type' }).getAttribute('aria-disabled')).toBe('true')
    expect(api.put).not.toHaveBeenCalled()
    expect(api.post.mock.calls.every(([url]) => url === '/admin/servers/probe')).toBe(true)
  })

  it('generates and copies a single-use node command only after an exact verified release, without changing the fixed identity', async () => {
    reads()
    const generated = { server_id: nativeServer.id, command: 'curl -fsSL https://panel.test/private-once | sudo bash',
      expires_at: new Date(Date.now() + 15 * 60_000).toISOString() }
    api.post.mockResolvedValue({ data: generated })
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    const button = screen.getByRole('button', { name: 'admin:servers.native.generate_command' }) as HTMLButtonElement
    expect(button.disabled).toBe(true)
    fireEvent.click(button)
    expect(api.post).not.toHaveBeenCalled()
    await selectVersion('v1.2.3')
    fireEvent.click(button)
    const command = await screen.findByLabelText('admin:servers.native.install_command') as HTMLTextAreaElement
    expect(command.value).toBe(generated.command)
    expect(command.readOnly).toBe(true)
    expect(screen.getByText('admin:servers.native.command_expires')).toBeTruthy()
    expect(api.post).toHaveBeenCalledWith('/admin/servers/7/node-install-command', { version: 'v1.2.3' }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.copy_command' }))
    await waitFor(() => expect(copy).toHaveBeenCalledWith(generated.command))
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(provisioning.credential)
    expect((screen.getByLabelText('admin:servers.native.agent_id') as HTMLInputElement).value).toBe(provisioning.agent_id)
    expect(api.post).toHaveBeenCalledTimes(1)
    expect(api.put).not.toHaveBeenCalled()
  })

  it('cannot request a one-click command for an arbitrary unreviewed version injected into the selection', async () => {
    reads()
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' }).getAttribute('aria-disabled')).not.toBe('true'))
    fireEvent.change(versionInput(), { target: { value: 'v99.99.99' } })
    expect(versionInput().value).toBe('')
    const button = screen.getByRole('button', { name: 'admin:servers.native.generate_command' }) as HTMLButtonElement
    expect(button.disabled).toBe(true)
    fireEvent.click(button)
    expect(api.post).not.toHaveBeenCalled()
  })

  it('does not request a command when the loaded provisioning belongs to another server identity', async () => {
    reads()
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={{ ...provisioning,
      server: { ...nativeServer, id: 8 } }} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectVersion('v1.2.3')
    const button = screen.getByRole('button', { name: 'admin:servers.native.generate_command' }) as HTMLButtonElement
    expect(button.disabled).toBe(true)
    fireEvent.click(button)
    expect(api.post).not.toHaveBeenCalled()
  })

  it('disables copying after a generated command expires without rotating the fixed credential', async () => {
    reads()
    api.post.mockImplementation(async () => ({ data: { server_id: 7, command: 'short-lived command',
      expires_at: new Date(Date.now() + 500).toISOString() } }))
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectVersion('v1.2.3')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_command' }))
    await screen.findByLabelText('admin:servers.native.install_command')
    await screen.findByText('admin:servers.native.command_expired')
    const button = screen.getByRole('button', { name: 'admin:servers.native.copy_command' }) as HTMLButtonElement
    expect(button.disabled).toBe(true)
    fireEvent.click(button)
    expect(copy).not.toHaveBeenCalled()
    expect(api.post).toHaveBeenCalledTimes(1)
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(provisioning.credential)
  })

  it.each(['version', 'method', 'close', 'server'] as const)('aborts a pending one-click command on %s change and ignores its late private response', async change => {
    const secondServer: Server = { ...nativeServer, id: 8, name: 'second-native', url: 'psp://agt_8' }
    const secondProvisioning = { ...provisioning, server: secondServer, agent_id: 'agt_8', credential: 'second-fixed-credential' }
    installReads({ '/admin/servers': list([nativeServer, secondServer]),
      '/admin/servers/7/node-installation': provisioning, '/admin/servers/8/node-installation': secondProvisioning,
      '/admin/servers/7/node-agent-status': waiting, '/admin/servers/8/node-agent-status': waiting })
    let finish!: (response: { data: { server_id: number; command: string; expires_at: string } }) => void
    api.post.mockImplementation((url: string) => url.endsWith('/node-install-command')
      ? new Promise(resolve => { finish = resolve }) : Promise.resolve({ data: {} }))
    mount(<ServersView />)
    await openInstallation(nativeServer)
    await selectVersion('v1.2.3')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_command' }))
    await waitFor(() => expect(api.post.mock.calls.some(([url]) => url === '/admin/servers/7/node-install-command')).toBe(true))
    const request = api.post.mock.calls.find(([url]) => url === '/admin/servers/7/node-install-command')![2]
    if (change === 'version') await selectVersion('v1.2.3-beta.1')
    else if (change === 'method') await selectMethod('manual')
    else {
      fireEvent.click(screen.getByRole('button', { name: 'common:actions.close' }))
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
      await openInstallation(change === 'server' ? secondServer : nativeServer)
      await screen.findByLabelText('admin:servers.native.credential')
    }
    expect(request.signal.aborted).toBe(true)
    await act(async () => { finish({ data: { server_id: 7, command: 'obsolete private command',
      expires_at: new Date(Date.now() + 15 * 60_000).toISOString() } }) })
    expect(screen.queryByLabelText('admin:servers.native.install_command')).toBeNull()
    expect(copy).not.toHaveBeenCalled()
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value)
      .toBe(change === 'server' ? secondProvisioning.credential : provisioning.credential)
  })

  it.each(['wrong-server', 'expired', 'empty'] as const)('rejects an invalid %s command response instead of exposing it', async invalid => {
    reads()
    api.post.mockResolvedValue({ data: { server_id: invalid === 'wrong-server' ? 8 : 7,
      command: invalid === 'empty' ? '' : 'private command',
      expires_at: new Date(Date.now() + (invalid === 'expired' ? -60_000 : 15 * 60_000)).toISOString() } })
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectVersion('v1.2.3')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_command' }))
    await screen.findByText('admin:servers.native.command_failed')
    expect(screen.queryByLabelText('admin:servers.native.install_command')).toBeNull()
    expect(copy).not.toHaveBeenCalled()
  })

  it('opens the unified install entry without upgrade capabilities and reuses the original identity and credential on reopen', async () => {
    reads()
    mount(<ServersView />)
    const row = (await screen.findByText(nativeServer.name)).closest('tr')!
    fireEvent.click(within(row).getByRole('button', { name: 'admin:servers.action.more' }))
    expect(screen.getAllByRole('menuitem', { name: 'admin:servers.install_reinstall.action' })).toHaveLength(1)
    expect(screen.getByRole('menuitem', { name: 'admin:servers.agent_upgrade.action' })).toBeTruthy()
    fireEvent.click(await screen.findByRole('menuitem', { name: 'admin:servers.install_reinstall.action' }))
    expect(screen.getByRole('combobox', { name: 'admin:servers.install_reinstall.backend' }).textContent).toBe('Passwall Node')
    expect(api.get.mock.calls.some(([url]) => String(url).includes('node-installation'))).toBe(false)
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.install_reinstall.continue' }))
    await screen.findByLabelText('admin:servers.native.agent_version')
    expect(versionInput().value).toBe('')
    expect(copyScript().disabled).toBe(true)
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(provisioning.credential)
    expect((screen.getByLabelText('admin:servers.native.agent_id') as HTMLInputElement).value).toBe(provisioning.agent_id)
    expect(screen.getByRole('heading', { name: 'admin:servers.install_reinstall.title' })).toBeTruthy()
    expect(screen.getByText('admin:servers.passwall_node_install.existing_hint')).toBeTruthy()
    expect(screen.queryByLabelText('admin:servers.migration.cli_command')).toBeNull()
    expect(api.get).toHaveBeenCalledWith('/admin/servers/7/node-installation', expect.objectContaining({ signal: expect.any(AbortSignal) }))
    expect(api.post.mock.calls.some(([url]) => String(url).includes('rotate-node-credential'))).toBe(false)
    expect(screen.getByText('admin:servers.native.private_warning')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'common:actions.close' }))
    await openInstallation(nativeServer)
    await screen.findByLabelText('admin:servers.native.credential')
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(provisioning.credential)
    expect((screen.getByLabelText('admin:servers.native.agent_id') as HTMLInputElement).value).toBe(provisioning.agent_id)
    expect(api.get.mock.calls.filter(([url]) => url === '/admin/servers/7/node-installation').length).toBeGreaterThanOrEqual(2)
    expect(api.get.mock.calls.some(([url]) => String(url).includes('node-migration-preview'))).toBe(false)
    expect(api.post.mock.calls.every(([url]) => url === '/admin/servers/probe')).toBe(true)
    expect(api.put).not.toHaveBeenCalled()
    expect(api.delete).not.toHaveBeenCalled()
  })

  it('hides the unified install entry for a non-administrator without reading fixed credentials', async () => {
    reads()
    useAuthStore.setState({ role: 'operator' })
    mount(<ServersView />)
    const row = (await screen.findByText(nativeServer.name)).closest('tr')!
    fireEvent.click(within(row).getByRole('button', { name: 'admin:servers.action.more' }))
    expect(screen.queryByRole('menuitem', { name: 'admin:servers.install_reinstall.action' })).toBeNull()
    expect(api.get.mock.calls.some(([url]) => String(url).endsWith('/node-installation') || String(url).includes('node-migration-preview'))).toBe(false)
  })

  it('defaults to Passwall Node first and continues to Linux installation in the same dialog after one create', async () => {
    installReads({ '/admin/servers': list([]), '/admin/servers/7/node-agent-status': waiting })
    api.post.mockResolvedValue({ data: provisioning })
    mount(<ServersView />)
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.create' }))
    const createDialog = await screen.findByRole('dialog')
    expect(within(createDialog).getByRole('combobox', { name: 'admin:servers.field.panel_type' }).textContent).toBe('Passwall Node')
    expect(within(createDialog).getByRole('combobox', { name: 'admin:servers.native.method_label' }).textContent).toBe('admin:servers.native.method.linux')
    expect(screen.queryByLabelText('admin:servers.native.agent_version')).toBeNull()
    fireEvent.mouseDown(within(createDialog).getByRole('combobox', { name: 'admin:servers.field.panel_type' }))
    const panelTypes = await screen.findAllByRole('option')
    expect(panelTypes.map(option => option.textContent)).toEqual(['Passwall Node', '3X-UI', 'S-UI'])
    fireEvent.click(screen.getByRole('option', { name: 'Passwall Node' }))
    fireEvent.change(within(createDialog).getByRole('textbox', { name: /admin:servers.field.name/ }), { target: { value: nativeServer.name } })
    fireEvent.click(within(createDialog).getByRole('button', { name: 'admin:servers.native.create_continue' }))
    await screen.findByLabelText('admin:servers.native.agent_version')
    expect(screen.getByRole('dialog')).toBe(createDialog)
    expect(api.post).toHaveBeenCalledWith('/admin/servers', { name: nativeServer.name, panel_type: 'psp', remark: undefined })
    expect(api.post.mock.calls.filter(([url]) => url === '/admin/servers')).toHaveLength(1)
    expect(screen.getByRole('combobox', { name: 'admin:servers.native.method_label' }).textContent).toBe('admin:servers.native.method.linux')
    expect(versionInput().value).toBe('')
    expect(screen.getByRole('heading', { name: /^admin:servers\.passwall_node_install\.title/ })).toBeTruthy()
    expect(screen.queryByText('admin:servers.passwall_node_install.existing_hint')).toBeNull()
    expect(api.get.mock.calls.some(([url]) => String(url).endsWith('/node-installation'))).toBe(false)
  })

  it.each(['docker', 'manual'] as const)('hands the selected %s installation method into step two without recreating or rereading credentials', async method => {
    installReads({ '/admin/servers': list([]), '/admin/servers/7/node-agent-status': waiting })
    api.post.mockResolvedValue({ data: provisioning })
    mount(<ServersView />)
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.create' }))
    const createDialog = await screen.findByRole('dialog')
    await selectMethod(method, createDialog)
    if (method === 'manual') {
      fireEvent.mouseDown(within(createDialog).getByRole('combobox', { name: 'admin:servers.native.platform_label' }))
      fireEvent.click(await screen.findByRole('option', { name: 'admin:servers.native.platform.darwin' }))
      fireEvent.mouseDown(within(createDialog).getByRole('combobox', { name: 'admin:servers.native.architecture' }))
      fireEvent.click(await screen.findByRole('option', { name: /^arm64\b/ }))
    }
    fireEvent.change(within(createDialog).getByRole('textbox', { name: /admin:servers.field.name/ }), { target: { value: nativeServer.name } })
    fireEvent.click(within(createDialog).getByRole('button', { name: 'admin:servers.native.create_continue' }))
    await screen.findByLabelText('admin:servers.native.agent_id')
    expect(screen.getByRole('dialog')).toBe(createDialog)
    expect(screen.queryByRole('button', { name: 'admin:servers.native.create_continue' })).toBeNull()
    expect(screen.getByRole('combobox', { name: 'admin:servers.native.method_label' }).textContent).toBe(`admin:servers.native.method.${method}`)
    if (method === 'manual') {
      expect(screen.getByRole('combobox', { name: 'admin:servers.native.platform_label' }).textContent).toBe('admin:servers.native.platform.darwin')
      expect(screen.getByRole('combobox', { name: 'admin:servers.native.architecture' }).textContent).toBe('arm64 (aarch64)')
    }
    expect((screen.getByLabelText('admin:servers.native.agent_id') as HTMLInputElement).value).toBe(provisioning.agent_id)
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(provisioning.credential)
    expect(api.post.mock.calls.filter(([url]) => url === '/admin/servers')).toHaveLength(1)
    expect(api.get.mock.calls.some(([url]) => String(url).endsWith('/node-installation'))).toBe(false)
  })

  it.each([['3xui', '3X-UI'], ['sui', 'S-UI']] as const)('creates an upstream %s panel only after explicitly selecting its adapter', async (panelType, label) => {
    installReads({ '/admin/servers': list([]) })
    api.post.mockResolvedValue({ data: { ...nativeServer, panel_type: panelType, url: 'https://upstream.example.test', has_api_token: true } })
    mount(<ServersView />)
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.create' }))
    const createDialog = await screen.findByRole('dialog')
    fireEvent.mouseDown(within(createDialog).getByRole('combobox', { name: 'admin:servers.field.panel_type' }))
    fireEvent.click(await screen.findByRole('option', { name: label }))
    expect(screen.queryByRole('combobox', { name: 'admin:servers.native.method_label' })).toBeNull()
    fireEvent.change(within(createDialog).getByRole('textbox', { name: /admin:servers.field.name/ }), { target: { value: 'upstream' } })
    fireEvent.change(within(createDialog).getByRole('textbox', { name: /admin:servers.field.url/ }), { target: { value: 'https://upstream.example.test' } })
    fireEvent.change(within(createDialog).getByLabelText('admin:servers.field.api_token'), { target: { value: 'upstream-api-token' } })
    fireEvent.click(within(createDialog).getByRole('button', { name: 'common:actions.ok' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(api.post).toHaveBeenCalledWith('/admin/servers', expect.objectContaining({
      name: 'upstream', panel_type: panelType, url: 'https://upstream.example.test', api_token: 'upstream-api-token', auth_method: 'token',
    }))
    expect(api.post).toHaveBeenCalledTimes(1)
    expect(api.get.mock.calls.some(([url]) => String(url).includes('/node-installation'))).toBe(false)
  })

  it.each([false, true])('does not reopen a closed installation or overwrite a new target after delayed rotation (switch=%s)', async switchToAnother => {
    const secondServer: Server = { ...nativeServer, id: 8, name: 'second-native', url: 'psp://agt_8' }
    const secondProvisioning: NativeServerProvisioning = { ...provisioning, server: secondServer, agent_id: 'agt_8', credential: 'second-node-secret' }
    let rotated = false
    const rotatedProvisioning: NativeServerProvisioning = { ...provisioning, credential: 'rotated-node-secret' }
    let finishRotation!: (response: { data: NativeServerProvisioning }) => void
    const delayedRotation = new Promise<{ data: NativeServerProvisioning }>(resolve => { finishRotation = resolve })
    api.get.mockImplementation(async (url: string) => {
      if (url === '/admin/servers') return { data: list([nativeServer, secondServer]) }
      if (url.endsWith('/node-agent-status')) return { data: waiting }
      if (url === '/admin/servers/8/node-installation') return { data: secondProvisioning }
      if (url === '/admin/servers/7/node-installation') {
        if (rotated) return { data: rotatedProvisioning }
        throw { response: { status: 409, data: { code: 'node_credential_unavailable' } } }
      }
      throw new Error(`Unexpected GET ${url}`)
    })
    api.post.mockImplementation(async (url: string) => {
      if (url === '/admin/servers/7/rotate-node-credential') return delayedRotation
      return { data: {} }
    })
    mount(<><ServersView /><ConfirmHost /></>)

    await openInstallation(nativeServer)
    await screen.findByText('admin:servers.native.legacy_credential_hint')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.action.rotate_node_credential' }))
    const confirmation = await screen.findByRole('dialog', { name: 'admin:servers.native.rotate_title' })
    fireEvent.click(within(confirmation).getByRole('button', { name: 'admin:servers.native.rotate_confirm' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/servers/7/rotate-node-credential'))
    fireEvent.click(await screen.findByRole('button', { name: 'common:actions.close' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    if (switchToAnother) {
      await openInstallation(secondServer)
      await screen.findByLabelText('admin:servers.native.credential')
      expect((screen.getByLabelText('admin:servers.native.agent_id') as HTMLInputElement).value).toBe('agt_8')
    }

    // This simulates an already committed server response. Closing/switching
    // must not undo it, but its secret must not reenter the abandoned dialog.
    await act(async () => { rotated = true; finishRotation({ data: rotatedProvisioning }) })
    if (switchToAnother) {
      expect((screen.getByLabelText('admin:servers.native.agent_id') as HTMLInputElement).value).toBe('agt_8')
      expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(secondProvisioning.credential)
      fireEvent.click(screen.getByRole('button', { name: 'common:actions.close' }))
    }
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    // An explicit later GET recovers the committed credential on the same ID.
    await openInstallation(nativeServer)
    await screen.findByLabelText('admin:servers.native.credential')
    expect((screen.getByLabelText('admin:servers.native.agent_id') as HTMLInputElement).value).toBe('agt_7')
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(rotatedProvisioning.credential)
    expect(api.post.mock.calls.filter(([url]) => url === '/admin/servers/7/rotate-node-credential')).toHaveLength(1)
  })

  it('imports the original credential for a legacy node and rereads installation without rotating', async () => {
    let imported = false
    api.get.mockImplementation(async (url: string) => {
      if (url.endsWith('/node-agent-status')) return { data: waiting }
      if (!imported) throw { response: { status: 409, data: { code: 'node_credential_unavailable', error: 'unavailable' } } }
      return { data: provisioning }
    })
    api.post.mockImplementation(async () => { imported = true; return { data: { ok: true } } })
    const onRotate = vi.fn()
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={null} onClose={vi.fn()} onRotate={onRotate} />)
    await screen.findByText('admin:servers.native.legacy_credential_hint')
    expect(screen.queryByLabelText('admin:servers.native.agent_version')).toBeNull()
    fireEvent.change(screen.getByLabelText('admin:servers.native.old_credential'), { target: { value: provisioning.credential } })
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.import_credential' }))
    await screen.findByLabelText('admin:servers.native.agent_version')
    expect(api.post).toHaveBeenCalledWith('/admin/servers/7/node-credential', { credential: provisioning.credential }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
    expect(onRotate).not.toHaveBeenCalled()
    expect(screen.queryByLabelText('admin:servers.native.old_credential')).toBeNull()
  })

  it('requires an exact release version, copies the private script, and never puts credentials in commands or URLs', async () => {
    reads({ state: 'unconfigured', configured_nodes: 0, last_seen: '2026-09-12T08:00:00Z' })
    api.post.mockResolvedValue({ data: `#!/bin/sh\n# ${provisioning.credential}\n` })
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await screen.findByText('admin:servers.native.agent_status.unconfigured')
    expect(screen.queryByText('admin:servers.native.agent_status.running')).toBeNull()
    expect(copyScript().disabled).toBe(true)
    expect(screen.queryByRole('textbox', { name: 'admin:servers.native.agent_version' })).toBeNull()
    expect(api.post).not.toHaveBeenCalled()
    await selectVersion('v1.2.3-beta.1')
    fireEvent.click(copyScript())
    await waitFor(() => expect(copy).toHaveBeenCalledWith(`#!/bin/sh\n# ${provisioning.credential}\n`))
    expect(api.post).toHaveBeenCalledWith('/admin/servers/7/node-install-script', { version: 'v1.2.3-beta.1' }, expect.objectContaining({ responseType: 'text' }))
    expect(screen.queryByLabelText('admin:servers.native.start_command')).toBeNull()
    const run = (screen.getByLabelText('admin:servers.native.run_script_command') as HTMLTextAreaElement).value
    expect(run).toContain('sudo bash ./passwall-node-install-agt_7.sh')
    expect(run).not.toContain(provisioning.credential)
  })

  it('preserves identity and private credential but requires version confirmation after changing installation methods', async () => {
    reads()
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectVersion('v1.2.3-beta.1')
    for (const method of ['docker', 'manual', 'linux'] as const) {
      await selectMethod(method)
      expect((screen.getByLabelText('admin:servers.native.agent_id') as HTMLInputElement).value).toBe(provisioning.agent_id)
      expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(provisioning.credential)
      expect(versionInput().value).toBe('')
      expect(screen.getByRole('combobox', { name: 'admin:servers.native.method_label' }).textContent).toBe(`admin:servers.native.method.${method}`)
    }
    expect(api.post).not.toHaveBeenCalled()
    expect(api.get.mock.calls.some(([url]) => String(url).endsWith('/node-installation'))).toBe(false)
  })

  it('aborts a pending Linux script on method change and never copies its obsolete secret response', async () => {
    reads()
    let finish!: (response: { data: string }) => void
    api.post.mockReturnValue(new Promise<{ data: string }>(resolve => { finish = resolve }))
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectVersion('v1.2.3')
    fireEvent.click(copyScript())
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/servers/7/node-install-script', { version: 'v1.2.3' }, expect.objectContaining({ signal: expect.any(AbortSignal) })))
    const request = api.post.mock.calls.find(([url]) => url === '/admin/servers/7/node-install-script')!
    await selectMethod('manual')
    expect(request[2].signal.aborted).toBe(true)
    await act(async () => { finish({ data: `#!/bin/sh\n# obsolete ${provisioning.credential}\n` }) })
    expect(copy).not.toHaveBeenCalled()
    expect(versionInput().value).toBe('')
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(provisioning.credential)
    await selectMethod('linux')
    expect(copyScript().disabled).toBe(true)
  })

  it('aborts a pending private script when switching release channels without rotating the node identity', async () => {
    reads()
    let finish!: (response: { data: string }) => void
    api.post.mockReturnValue(new Promise<{ data: string }>(resolve => { finish = resolve }))
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectVersion('v1.2.3-beta.1')
    fireEvent.click(copyScript())
    await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
    const request = api.post.mock.calls[0][2]
    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }))
    fireEvent.click(await screen.findByRole('option', { name: 'admin:servers.native.release_stable' }))
    expect(request.signal.aborted).toBe(true)
    expect(versionInput().value).toBe('')
    expect(copyScript().disabled).toBe(true)
    await act(async () => { finish({ data: `#!/bin/sh\n# obsolete ${provisioning.credential}\n` }) })
    expect(copy).not.toHaveBeenCalled()
    expect((screen.getByLabelText('admin:servers.native.agent_id') as HTMLInputElement).value).toBe(provisioning.agent_id)
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(provisioning.credential)
    expect(api.post.mock.calls.some(([url]) => String(url).includes('rotate-node-credential'))).toBe(false)
  })

  it.each(['docker', 'manual'] as const)('shows and copies the exact %s private files and secret-free startup steps', async method => {
    reads()
    const materials = generatedFiles(method)
    api.post.mockResolvedValue({ data: materials })
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod(method)
    await selectVersion('v1.2.3-beta.1')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }))
    await screen.findAllByLabelText('admin:servers.native.file_content')
    expect(api.post).toHaveBeenCalledWith('/admin/servers/7/node-installation-files', {
      version: 'v1.2.3-beta.1', method, ...(method === 'manual' ? { os: 'linux', arch: 'amd64' } : {}),
    }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
    expect(materialContents()).toEqual(materialPreviews(materials))
    const privateFile = within(screen.getByRole('region', { name: 'credential' })).getByLabelText('admin:servers.native.file_content') as HTMLInputElement
    expect(privateFile.type).toBe('password')
    expect(privateFile.readOnly).toBe(true)
    const commands = screen.getAllByLabelText('admin:servers.native.command_label').map(input => (input as HTMLTextAreaElement).value)
    expect(commands).toEqual(materials.steps.map(step => step.commands!.join('\n')))
    expect(commands.join('\n')).not.toContain(provisioning.credential)
    expect([...document.querySelectorAll('a[href]')].every(link => !link.getAttribute('href')!.includes(provisioning.credential))).toBe(true)
    fireEvent.click(screen.getAllByRole('button', { name: 'admin:servers.native.copy_file' })[0])
    await waitFor(() => expect(copy).toHaveBeenCalledWith(materials.files[0].content))
    expect(api.post.mock.calls.filter(([url]) => String(url).endsWith('/node-installation-files'))).toHaveLength(1)
    expect(api.post.mock.calls.some(([url]) => String(url).includes('rotate-node-credential'))).toBe(false)
  })

  it('downloads a generated private file with exact bytes and revokes its temporary blob URL', async () => {
    reads()
    const materials = generatedFiles('docker')
    api.post.mockResolvedValue({ data: materials })
    const createURL = vi.fn().mockReturnValue('blob:private-install-file')
    const revokeURL = vi.fn()
    const OriginalURL = URL
    vi.stubGlobal('URL', class extends OriginalURL {
      static createObjectURL = createURL
      static revokeObjectURL = revokeURL
    })
    const downloads: { file: string; url: string }[] = []
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
      downloads.push({ file: this.download, url: this.href })
    })
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod('docker')
    await selectVersion('v1.2.3-beta.1')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }))
    await screen.findAllByLabelText('admin:servers.native.file_content')
    fireEvent.click(screen.getAllByRole('button', { name: 'admin:servers.native.download_file' })[0])
    await waitFor(() => expect(downloads).toEqual([{ file: 'credential', url: 'blob:private-install-file' }]))
    const content = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader()
      reader.onload = () => resolve(String(reader.result))
      reader.onerror = () => reject(reader.error)
      reader.readAsText(createURL.mock.calls[0][0] as Blob)
    })
    expect(content).toBe(materials.files[0].content)
    expect(revokeURL).toHaveBeenCalledWith('blob:private-install-file')
    expect(document.querySelector('a[download]')).toBeNull()
  })

  it('keeps new manual materials when an aborted Docker generation resolves after switching methods', async () => {
    reads()
    let finishDocker!: (response: { data: NativeInstallationFiles }) => void
    const manual = generatedFiles('manual')
    const obsolete = { ...generatedFiles('docker'), files: [{ name: 'obsolete.yaml', content: 'obsolete private response' }] }
    api.post.mockImplementation((_url: string, body: { method: string }) => body.method === 'docker'
      ? new Promise<{ data: NativeInstallationFiles }>(resolve => { finishDocker = resolve })
      : Promise.resolve({ data: manual }))
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod('docker')
    await selectVersion('v1.2.3-beta.1')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
    const oldRequest = api.post.mock.calls[0][2]
    await selectMethod('manual')
    expect(oldRequest.signal.aborted).toBe(true)
    await selectVersion('v1.2.3-beta.1')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }))
    await screen.findAllByLabelText('admin:servers.native.file_content')
    await act(async () => { finishDocker({ data: obsolete }) })
    expect(materialContents()).toEqual(materialPreviews(manual))
    expect(screen.queryByText('obsolete.yaml')).toBeNull()
    expect((screen.getByLabelText('admin:servers.native.agent_id') as HTMLInputElement).value).toBe(provisioning.agent_id)
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(provisioning.credential)
    expect(copy).not.toHaveBeenCalled()
  })

  it('aborts file generation on close and does not put its delayed private response into another server dialog', async () => {
    const secondServer: Server = { ...nativeServer, id: 8, name: 'second-native', url: 'psp://agt_8' }
    const secondProvisioning: NativeServerProvisioning = { ...provisioning, server: secondServer, agent_id: 'agt_8', credential: 'second-node-secret' }
    installReads({
      '/admin/servers': list([nativeServer, secondServer]),
      '/admin/servers/7/node-installation': provisioning,
      '/admin/servers/8/node-installation': secondProvisioning,
      '/admin/servers/7/node-agent-status': waiting,
      '/admin/servers/8/node-agent-status': waiting,
    })
    let finish!: (response: { data: NativeInstallationFiles }) => void
    api.post.mockImplementation((url: string) => url.endsWith('/node-installation-files')
      ? new Promise<{ data: NativeInstallationFiles }>(resolve => { finish = resolve })
      : Promise.resolve({ data: { ok: true } }))
    mount(<ServersView />)
    await openInstallation(nativeServer)
    await screen.findByLabelText('admin:servers.native.credential')
    await selectMethod('docker')
    await selectVersion('v1.2.3-beta.1')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }))
    await waitFor(() => expect(api.post.mock.calls.filter(([url]) => String(url).endsWith('/node-installation-files'))).toHaveLength(1))
    const oldRequest = api.post.mock.calls.find(([url]) => String(url).endsWith('/node-installation-files'))![2]
    fireEvent.click(screen.getByRole('button', { name: 'common:actions.close' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(oldRequest.signal.aborted).toBe(true)
    await openInstallation(secondServer)
    await screen.findByLabelText('admin:servers.native.credential')
    await selectMethod('docker')
    await act(async () => { finish({ data: generatedFiles('docker') }) })
    expect((screen.getByLabelText('admin:servers.native.agent_id') as HTMLInputElement).value).toBe(secondProvisioning.agent_id)
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(secondProvisioning.credential)
    expect(versionInput().value).toBe('')
    expect(screen.queryByLabelText('admin:servers.native.file_content')).toBeNull()
    expect(copy).not.toHaveBeenCalled()
  })

  it.each(['linux', 'darwin', 'windows'] as const)('generates manual files for the selected %s arm64 target', async os => {
    reads()
    const materials: NativeInstallationFiles = { ...generatedFiles('manual'), os, arch: 'arm64' }
    api.post.mockResolvedValue({ data: materials })
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod('manual')
    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'admin:servers.native.platform_label' }))
    fireEvent.click(await screen.findByRole('option', { name: `admin:servers.native.platform.${os}` }))
    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'admin:servers.native.architecture' }))
    fireEvent.click(await screen.findByRole('option', { name: /^arm64\b/ }))
    await selectVersion('v1.2.3-beta.1')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }))
    await screen.findAllByLabelText('admin:servers.native.file_content')
    expect(api.post).toHaveBeenCalledWith('/admin/servers/7/node-installation-files', {
      version: 'v1.2.3-beta.1', method: 'manual', os, arch: 'arm64',
    }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
    expect((screen.getByLabelText('admin:servers.native.agent_id') as HTMLInputElement).value).toBe(provisioning.agent_id)
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(provisioning.credential)
  })

  it('invalidates displayed files after a release change instead of allowing obsolete files to be copied', async () => {
    reads()
    api.post.mockResolvedValue({ data: generatedFiles('docker') })
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod('docker')
    await selectVersion('v1.2.3-beta.1')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }))
    await screen.findAllByLabelText('admin:servers.native.file_content')
    await selectVersion('v1.2.3-beta.2')
    expect(screen.queryByLabelText('admin:servers.native.file_content')).toBeNull()
    expect(screen.queryByRole('button', { name: 'admin:servers.native.copy_file' })).toBeNull()
    expect(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }).getAttribute('disabled')).toBeNull()
    expect(api.post).toHaveBeenCalledTimes(1)
    expect(copy).not.toHaveBeenCalled()
  })

  it('keeps a generation failure local and retryable without rendering or copying an error as a private file', async () => {
    reads()
    api.post.mockRejectedValue({ response: { status: 400, data: { error: 'Chosen release unavailable' } } })
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod('docker')
    await selectVersion('v1.2.3-beta.1')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }))
    await screen.findByText('Chosen release unavailable')
    expect(screen.queryByLabelText('admin:servers.native.file_content')).toBeNull()
    expect(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }).getAttribute('disabled')).toBeNull()
    expect(copy).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog')).toBeTruthy()
  })

  it.each([
    ['manual', 'os'],
    ['manual', 'arch'],
    ['docker', 'os'],
  ] as const)('rejects a successful %s response with the wrong %s before exposing private files', async (method, field) => {
    reads()
    const materials = generatedFiles(method)
    if (field === 'os') materials.os = 'windows'
    else materials.arch = 'arm64'
    api.post.mockResolvedValue({ data: materials })
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod(method)
    await selectVersion('v1.2.3-beta.1')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }))
    await screen.findByText('admin:servers.native.files_failed')
    expect(screen.queryByLabelText('admin:servers.native.file_content')).toBeNull()
    expect(screen.queryByRole('button', { name: 'admin:servers.native.copy_file' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'admin:servers.native.download_file' })).toBeNull()
    expect(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }).getAttribute('disabled')).toBeNull()
    expect(copy).not.toHaveBeenCalled()
    expect((screen.getByLabelText('admin:servers.native.agent_id') as HTMLInputElement).value).toBe(provisioning.agent_id)
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(provisioning.credential)
  })

  it('copies an individual step command verbatim rather than trimming or joining another command', async () => {
    reads()
    const materials = generatedFiles('docker')
    const command = '  printf "%s\\n" "$NODE_AGENT_ID"\n'
    materials.steps[0].commands = [command, 'docker compose ps']
    api.post.mockResolvedValue({ data: materials })
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod('docker')
    await selectVersion('v1.2.3-beta.1')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }))
    const step = await screen.findByRole('region', { name: 'Private files' })
    fireEvent.click(within(step).getAllByRole('button', { name: 'admin:servers.native.copy_step' })[0])
    await waitFor(() => expect(copy).toHaveBeenCalledWith(command))
    expect(copy).toHaveBeenCalledTimes(1)
    expect(api.post).toHaveBeenCalledTimes(1)
  })

  it('polls runtime transitions without closing the dialog and cancels all polling on unmount', async () => {
    vi.useFakeTimers()
    let status: NativeAgentStatus = waiting
    api.get.mockImplementation(async () => ({ data: status }))
    const onClose = vi.fn()
    const view = mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={onClose} onRotate={vi.fn()} />)
    await act(async () => {})
    expect(screen.getByText('admin:servers.native.agent_status.waiting')).toBeTruthy()
    status = { state: 'applying', configured_nodes: 1, last_seen: '2026-09-12T08:00:00Z' }
    await act(async () => { vi.advanceTimersByTime(4000) })
    expect(screen.getByText('admin:servers.native.agent_status.applying')).toBeTruthy()
    status = { state: 'running', configured_nodes: 1, core_state: 'running' }
    await act(async () => { vi.advanceTimersByTime(4000) })
    expect(screen.getByText('admin:servers.native.agent_status.running')).toBeTruthy()
    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getByRole('link', { name: 'admin:servers.native.configure_nodes' }).getAttribute('href')).toBe('/admin/nodes')
    view.unmount()
    const requests = api.get.mock.calls.length
    expect(api.get.mock.calls.every(([, config]) => config.signal.aborted)).toBe(true)
    await act(async () => { vi.advanceTimersByTime(8000) })
    expect(api.get).toHaveBeenCalledTimes(requests)
  })

  it('downloads the script as a private file and revokes its temporary blob URL', async () => {
    reads()
    api.post.mockResolvedValue({ data: '#!/bin/sh\n# private credential file\n' })
    const createURL = vi.fn().mockReturnValue('blob:private-install-script')
    const revokeURL = vi.fn()
    const OriginalURL = URL
    vi.stubGlobal('URL', class extends OriginalURL {
      static createObjectURL = createURL
      static revokeObjectURL = revokeURL
    })
    const downloads: { file: string; url: string }[] = []
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
      downloads.push({ file: this.download, url: this.href })
    })
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectVersion('v1.2.3')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.download_script' }))
    await waitFor(() => expect(downloads).toEqual([{ file: 'passwall-node-install-agt_7.sh', url: 'blob:private-install-script' }]))
    expect(createURL).toHaveBeenCalledWith(expect.any(Blob))
    expect(revokeURL).toHaveBeenCalledWith('blob:private-install-script')
    expect(document.querySelector('a[download]')).toBeNull()
  })

  it('renders a plain-text API failure locally without downloading or copying it', async () => {
    reads()
    api.post.mockRejectedValue({ response: { status: 400, data: '{"error":"Release has no installation assets"}' } })
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectVersion('v1.2.3')
    fireEvent.click(copyScript())
    await screen.findByText('Release has no installation assets')
    expect(copy).not.toHaveBeenCalled()
    expect(copyScript().disabled).toBe(false)
    expect(screen.getByRole('dialog')).toBeTruthy()
  })
})

describe('native installation inputs', () => {
  it.each(['', 'latest', '1.2.3', 'v01.2.3', 'v1.2.3-beta.01', 'https://example.test/v1.2.3'])('rejects noncanonical release version %s', version => {
    expect(isNodeReleaseVersion(version)).toBe(false)
  })

  it.each(['v0.1.0', 'v1.2.3', 'v1.2.3-beta.1', 'v1.2.3-rc-1'])('accepts canonical release version %s', version => {
    expect(isNodeReleaseVersion(version)).toBe(true)
  })

  it('does not treat a PSP agent identity as a proxy hostname', () => {
    expect(hostFromURL('psp://agt_7')).toBe('')
    expect(hostFromURL('PSP://agt_7')).toBe('')
    expect(hostFromURL('https://proxy.example.test:8443/admin')).toBe('proxy.example.test')
    expect(hostFromURL('proxy.example.test:8443')).toBe('proxy.example.test')
  })
})
