// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, installReads, list, mount } from '@/test/adminSaveHarness'
import { useAuthStore } from '@/stores/auth'
import type { NodeReleaseCatalog } from '@/api/nodeReleases'
import type { NodeMigrationPreview, Server } from '@/api/servers'
import { NodeMigrationPreviewDialog } from './NodeMigrationPreviewDialog'
import ServersView from './ServersView'

const copy = vi.hoisted(() => vi.fn().mockResolvedValue(true))
vi.mock('@/utils/clipboard', () => ({ copyToClipboard: copy }))
const releaseReads = vi.hoisted(() => vi.fn())
vi.mock('@/api/nodeReleases', () => ({ listNodeReleases: releaseReads }))
const server: Server = {
  id: 7, name: 'existing-3xui', panel_type: '3xui', url: 'https://xui.example.test', capabilities: [],
  auth_method: 'token', has_api_token: true, has_password: false, insecure_https: false,
}
const preview: NodeMigrationPreview = {
  server_id: 7, server_name: server.name, core_version: '26.6.27', recommended_core_version: '26.6.27',
  core_requires_ack: false, allow_restricted_reality: false, fingerprint: 'a'.repeat(64),
  node_count: 3, client_count: 5, blockers: [], warnings: [{ code: 'managed_scope' }], can_migrate: true,
}
const catalog: NodeReleaseCatalog = {
  checked_at: '2026-09-12T13:00:00Z',
  releases: ['v0.0.1', 'v0.0.1-beta3', 'v0.0.1-beta2'].map(version => ({
    version, channel: version.includes('-') ? 'testing' : 'stable',
    published_at: '2026-09-12T12:00:00Z', notes: 'Reviewed release fixture',
    release_url: `https://github.com/KazuhaHub/Passwall-Node/releases/tag/${version}`,
    methods: ['linux'], platforms: [{ os: 'linux', arch: 'amd64' }, { os: 'linux', arch: 'arm64' }],
  })),
}
const endpoint = '/admin/servers/7/node-migration-preview'
const commandEndpoint = '/admin/servers/7/node-migration-command'
const generated = () => ({ server_id: 7, command: 'curl -fsSL https://panel.test/private-ticket | sudo bash',
  expires_at: new Date(Date.now() + 15 * 60_000).toISOString() })
beforeEach(() => {
  releaseReads.mockResolvedValue(catalog)
  api.post.mockImplementation(async () => ({ data: generated() }))
})
afterEach(() => vi.restoreAllMocks())
function reads(value = preview, servers = [server]) {
  installReads({ '/admin/servers': list(servers), [endpoint]: value })
}
async function openMenu(target: Server) {
  const row = (await screen.findByText(target.name)).closest('tr')!
  fireEvent.click(within(row).getByRole('button', { name: 'admin:servers.action.more' }))
}
async function chooseBackend(backend: string) {
  fireEvent.mouseDown(screen.getByRole('combobox', { name: 'admin:servers.install_reinstall.backend' }))
  fireEvent.click(await screen.findByRole('option', { name: backend }))
}
function command() { return screen.queryByLabelText('admin:servers.native.install_command') as HTMLTextAreaElement | null }
function generateButton() { return screen.getByRole('button', { name: 'admin:servers.migration.generate_node_command' }) as HTMLButtonElement }
async function selectVersion(version = 'v0.0.1') {
  const channel = version.includes('-') ? 'testing' : 'stable'
  fireEvent.mouseDown(await screen.findByRole('combobox', { name: 'admin:servers.native.release_channel' }))
  fireEvent.click(await screen.findByRole('option', { name: `admin:servers.native.release_${channel}` }))
  const field = screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' })
  await waitFor(() => expect(field.getAttribute('aria-disabled')).not.toBe('true'))
  fireEvent.mouseDown(field)
  fireEvent.click(await screen.findByRole('option', { name: version }))
}
async function confirmAndSelect(version = 'v0.0.1') {
  await screen.findByText('admin:servers.migration.ready')
  await selectVersion(version)
  fireEvent.click(screen.getByRole('checkbox', { name: 'admin:servers.migration.ack_managed_only' }))
  fireEvent.click(screen.getByRole('checkbox', { name: 'admin:servers.migration.single_instance_confirmation' }))
  await waitFor(() => expect(generateButton().disabled).toBe(false))
}

describe('3X-UI to Passwall Node node-host migration command', () => {
  it('opens one unified selector with the original backend, then prechecks an explicitly chosen Passwall Node migration', async () => {
    reads()
    mount(<ServersView />)
    await openMenu(server)
    expect(screen.getAllByRole('menuitem', { name: 'admin:servers.install_reinstall.action' })).toHaveLength(1)
    expect(screen.queryByRole('menuitem', { name: 'admin:servers.passwall_node_install.action' })).toBeNull()
    fireEvent.click(screen.getByRole('menuitem', { name: 'admin:servers.install_reinstall.action' }))
    expect(screen.getByRole('combobox', { name: 'admin:servers.install_reinstall.backend' }).textContent).toBe('3X-UI')
    expect(api.get.mock.calls.some(([url]) => String(url).includes('node-migration'))).toBe(false)
    await chooseBackend('Passwall Node')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.install_reinstall.precheck' }))
    await screen.findByText('admin:servers.migration.ready')
    expect(screen.getByRole('heading', { name: 'admin:servers.install_reinstall.title' })).toBeTruthy()
    expect(screen.getByText('admin:servers.migration.online_hint')).toBeTruthy()
    expect(screen.getByText('admin:servers.migration.supported_deployment_hint')).toBeTruthy()
    expect(screen.getByText('admin:servers.migration.preserved')).toBeTruthy()
    expect(screen.queryByLabelText('admin:servers.native.credential')).toBeNull()
    expect(screen.queryByLabelText('admin:servers.migration.cli_command')).toBeNull()
    expect(screen.queryByLabelText('admin:servers.migration.docker_commands')).toBeNull()
    expect(command()).toBeNull()
    expect(generateButton().disabled).toBe(true)
    expect(api.post.mock.calls.every(([url]) => url === '/admin/servers/probe')).toBe(true)
    expect(api.put).not.toHaveBeenCalled()
    expect(api.delete).not.toHaveBeenCalled()
  })
  it('uses the same entry for native reinstall and keeps S-UI switching explicitly unsupported', async () => {
    const native = { ...server, id: 8, name: 'native-node', panel_type: 'psp' as const }
    const sui = { ...server, id: 9, name: 'sui-node', panel_type: 'sui' as const }
    reads(preview, [native, sui])
    mount(<ServersView />)
    await openMenu(native)
    fireEvent.click(screen.getByRole('menuitem', { name: 'admin:servers.install_reinstall.action' }))
    expect(screen.getByRole('combobox', { name: 'admin:servers.install_reinstall.backend' }).textContent).toBe('Passwall Node')
    expect((screen.getByRole('button', { name: 'admin:servers.install_reinstall.continue' }) as HTMLButtonElement).disabled).toBe(false)
    fireEvent.click(screen.getByRole('button', { name: 'common:actions.close' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await openMenu(sui)
    fireEvent.click(screen.getByRole('menuitem', { name: 'admin:servers.install_reinstall.action' }))
    expect(screen.getByRole('combobox', { name: 'admin:servers.install_reinstall.backend' }).textContent).toBe('S-UI')
    expect(screen.getByText('admin:servers.install_reinstall.manual_unverified')).toBeTruthy()
    await chooseBackend('Passwall Node')
    expect(screen.getByText('admin:servers.install_reinstall.switch_unavailable')).toBeTruthy()
    expect((screen.getByRole('button', { name: 'admin:servers.install_reinstall.continue' }) as HTMLButtonElement).disabled).toBe(true)
    expect(api.get.mock.calls.some(([url]) => String(url).includes('node-migration') || String(url).endsWith('/node-installation'))).toBe(false)
  })
  it('hides the unified reinstall menu from operators', async () => {
    reads(preview, [{ ...server, capabilities: ['panel.upgrade'] }])
    useAuthStore.setState({ role: 'operator' })
    mount(<ServersView />)
    await openMenu(server)
    expect(screen.queryByRole('menuitem', { name: 'admin:servers.install_reinstall.action' })).toBeNull()
    expect(api.get.mock.calls.some(([url]) => String(url).includes('node-migration'))).toBe(false)
  })
  it('requires a catalog version and both confirmations before issuing or copying the command', async () => {
    reads()
    mount(<NodeMigrationPreviewDialog server={server} onClose={vi.fn()} />)
    await screen.findByText('admin:servers.migration.ready')
    expect(generateButton().disabled).toBe(true)
    expect(command()).toBeNull()
    fireEvent.click(generateButton())
    expect(api.post).not.toHaveBeenCalled()
    await selectVersion('v0.0.1-beta3')
    expect(generateButton().disabled).toBe(true)
    fireEvent.click(screen.getByRole('checkbox', { name: 'admin:servers.migration.ack_managed_only' }))
    expect(generateButton().disabled).toBe(true)
    fireEvent.click(screen.getByRole('checkbox', { name: 'admin:servers.migration.single_instance_confirmation' }))
    fireEvent.click(generateButton())
    await waitFor(() => expect(command()?.value).toBe(generated().command))
    expect(command()?.readOnly).toBe(true)
    expect(api.post).toHaveBeenCalledWith(commandEndpoint, {
      version: 'v0.0.1-beta3', fingerprint: 'a'.repeat(64), core_version: '26.6.27',
      allow_restricted_reality: false, managed_only: true, confirm_single_instance: true,
    }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.copy_command' }))
    await waitFor(() => expect(copy).toHaveBeenCalledWith(generated().command))
    expect(api.post).toHaveBeenCalledTimes(1)
    expect(api.put).not.toHaveBeenCalled()
    expect(api.delete).not.toHaveBeenCalled()
  })
  it('does not silently select testing releases when no stable release exists', async () => {
    reads()
    releaseReads.mockResolvedValue({ ...catalog, releases: catalog.releases.filter(release => release.channel === 'testing') })
    mount(<NodeMigrationPreviewDialog server={server} onClose={vi.fn()} />)
    await screen.findByText('admin:servers.native.release_no_stable')
    expect(screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }).textContent).toBe('admin:servers.native.release_stable')
    expect(generateButton().disabled).toBe(true)
    expect(api.post).not.toHaveBeenCalled()
  })
  it('never accepts an arbitrary version injected into the select input', async () => {
    reads()
    mount(<NodeMigrationPreviewDialog server={server} onClose={vi.fn()} />)
    await confirmAndSelect()
    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }))
    fireEvent.click(screen.getByRole('option', { name: 'admin:servers.native.release_testing' }))
    const input = screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' }).parentElement!.querySelector('input')!
    fireEvent.change(input, { target: { value: 'v99.99.99' } })
    expect(input.value).toBe('')
    expect(generateButton().disabled).toBe(true)
    expect(api.post).not.toHaveBeenCalled()
  })
  it('never generates with blockers, even if the preview incorrectly says can_migrate', async () => {
    reads({ ...preview, blockers: [{ code: 'missing_snapshot', node_id: 3 }, { code: 'credential_mismatch', client_id: 5 }], can_migrate: true })
    mount(<NodeMigrationPreviewDialog server={server} onClose={vi.fn()} />)
    await screen.findByText('admin:servers.migration.blockers')
    expect(screen.getByText(/admin:servers.migration.issue.missing_snapshot/)).toBeTruthy()
    expect(screen.getByText(/admin:servers.migration.issue.credential_mismatch/)).toBeTruthy()
    await selectVersion()
    expect(generateButton().disabled).toBe(true)
    expect(command()).toBeNull()
    expect(api.post).not.toHaveBeenCalled()
  })
  it('localizes all current policy codes without displaying an unknown fallback', async () => {
    const codes = [
      'source_not_3xui', 'legacy_ownership_pending', 'missing_snapshot', 'duplicate_node', 'duplicate_inbound', 'duplicate_listener_binding',
      'cross_panel_attachment', 'config_not_synced', 'endpoint_not_confirmed', 'unsupported_protocol',
      'inbound_expiry_unsupported', 'unsupported_flow', 'invalid_config', 'snapshot_contains_clients',
      'missing_client', 'duplicate_client', 'lifecycle_not_minted', 'invalid_credentials', 'duplicate_username',
      'missing_attachment', 'orphan_attachment', 'duplicate_attachment', 'credential_not_confirmed', 'flow_conflict',
      'external_file_dependency', 'global_config_dependency', 'local_fallback_dependency',
      'fallback_environment_dependency', 'socket_environment_dependency', 'connection_limits_not_enforced',
      'core_not_verified', 'core_ack_required', 'restricted_core', 'reality_compatibility_normalization',
      'core_version_changed', 'managed_scope', 'unsafe_reality_finalmask_tcp',
    ]
    reads({ ...preview, blockers: codes.map(code => ({ code })), can_migrate: false })
    mount(<NodeMigrationPreviewDialog server={server} onClose={vi.fn()} />)
    await screen.findByText('admin:servers.migration.blockers')
    expect(screen.queryByText('admin:servers.migration.issue.unknown')).toBeNull()
    expect(screen.getByText('admin:servers.migration.issue.unsafe_reality_finalmask_tcp')).toBeTruthy()
    expect(generateButton().disabled).toBe(true)
  })
  it('rechecks restricted-core acknowledgement and binds it to the request; withdrawing it clears the ticket', async () => {
    const restricted = { ...preview, core_version: '26.7.28', core_requires_ack: true, blockers: [{ code: 'core_ack_required' }], can_migrate: false }
    api.get.mockImplementation(async (_url: string, options: { params: { allow_restricted_reality?: boolean } }) => ({
      data: options.params.allow_restricted_reality ? { ...restricted, blockers: [], allow_restricted_reality: true, can_migrate: true } : restricted,
    }))
    mount(<NodeMigrationPreviewDialog server={server} onClose={vi.fn()} />)
    await screen.findByText('admin:servers.migration.blockers')
    expect(generateButton().disabled).toBe(true)
    fireEvent.click(screen.getByRole('checkbox', { name: 'admin:servers.migration.ack_restricted_core' }))
    await confirmAndSelect()
    fireEvent.click(generateButton())
    await screen.findByLabelText('admin:servers.native.install_command')
    expect(api.get).toHaveBeenCalledWith(endpoint, expect.objectContaining({ params: { allow_restricted_reality: true } }))
    expect(api.post).toHaveBeenCalledWith(commandEndpoint, expect.objectContaining({ core_version: '26.7.28', allow_restricted_reality: true }), expect.anything())
    fireEvent.click(screen.getByRole('checkbox', { name: 'admin:servers.migration.ack_restricted_core' }))
    expect(command()).toBeNull()
    await screen.findByText('admin:servers.migration.blockers')
    expect(generateButton().disabled).toBe(true)
  })
  it('uses the recommended verified core only after explicit choice and a matching fresh preview', async () => {
    const blocked = { ...preview, core_version: '26.9.99', blockers: [{ code: 'core_not_verified' }], can_migrate: false }
    api.get.mockImplementation(async (_url: string, options: { params: { core_version?: string } }) => ({
      data: options.params.core_version ? { ...preview, warnings: [{ code: 'core_version_changed' }] } : blocked,
    }))
    mount(<NodeMigrationPreviewDialog server={server} onClose={vi.fn()} />)
    await screen.findByText('admin:servers.migration.blockers')
    expect(api.get.mock.calls.every(([, options]) => !options.params.core_version)).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.migration.use_recommended_core' }))
    await confirmAndSelect()
    expect(screen.getByText('admin:servers.migration.issue.core_version_changed')).toBeTruthy()
    fireEvent.click(generateButton())
    await screen.findByLabelText('admin:servers.native.install_command')
    expect(api.get).toHaveBeenCalledWith(endpoint, expect.objectContaining({ params: { core_version: '26.6.27' } }))
    expect(api.post.mock.calls[0][1].core_version).toBe('26.6.27')
  })
  it.each([{ core_version: '26.6.27; false' }, { fingerprint: 'a'.repeat(64) + '; false' }])('rejects malformed preview tokens: %j', async invalid => {
    reads({ ...preview, ...invalid })
    mount(<NodeMigrationPreviewDialog server={server} onClose={vi.fn()} />)
    await screen.findByText('admin:servers.migration.invalid_command')
    expect(generateButton().disabled).toBe(true)
    expect(api.post).not.toHaveBeenCalled()
  })
  it('rejects a preview for another identity or the wrong explicitly selected core', async () => {
    reads({ ...preview, server_id: 8 })
    mount(<NodeMigrationPreviewDialog server={server} onClose={vi.fn()} />)
    await screen.findByText('admin:servers.migration.failed')
    expect(generateButton().disabled).toBe(true)
    reads({ ...preview, core_version: '26.9.99', can_migrate: false, blockers: [{ code: 'core_not_verified' }] })
    fireEvent.click(screen.getByRole('button', { name: 'common:actions.retry' }))
    await screen.findByText('admin:servers.migration.blockers')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.migration.use_recommended_core' }))
    await screen.findByText('admin:servers.migration.failed')
    expect(generateButton().disabled).toBe(true)
    expect(api.post).not.toHaveBeenCalled()
  })
  it('handles access denial and retry without exposing raw error contents', async () => {
    api.get.mockRejectedValue({ response: { status: 403, data: { error: 'sensitive detail' } } })
    mount(<NodeMigrationPreviewDialog server={server} onClose={vi.fn()} />)
    await screen.findByText('admin:servers.migration.forbidden')
    expect(screen.queryByText('sensitive detail')).toBeNull()
    expect(command()).toBeNull()
    reads()
    fireEvent.click(screen.getByRole('button', { name: 'common:actions.retry' }))
    await screen.findByText('admin:servers.migration.ready')
    expect(generateButton().disabled).toBe(true)
  })
  it('does not fetch previews or releases for operators or unsupported sources', async () => {
    useAuthStore.setState({ role: 'operator' })
    const view = mount(<NodeMigrationPreviewDialog server={server} onClose={vi.fn()} />)
    await screen.findByText('admin:servers.migration.forbidden')
    expect(api.get).not.toHaveBeenCalled()
    expect(releaseReads).not.toHaveBeenCalled()
    useAuthStore.setState({ role: 'admin' })
    view.rerender(<NodeMigrationPreviewDialog server={{ ...server, panel_type: 'psp' }} onClose={vi.fn()} />)
    await screen.findByText('admin:servers.migration.unsupported_server')
    expect(api.get).not.toHaveBeenCalled()
    expect(releaseReads).not.toHaveBeenCalled()
  })
  it.each([{ server_id: 8 }, { command: '' }, { expires_at: 'bad-date' }, { expires_at: '2020-01-01T00:00:00Z' }])('rejects invalid issued command material: %j', async invalid => {
    reads()
    api.post.mockResolvedValue({ data: { ...generated(), ...invalid } })
    mount(<NodeMigrationPreviewDialog server={server} onClose={vi.fn()} />)
    await confirmAndSelect()
    fireEvent.click(generateButton())
    await screen.findByText('admin:servers.migration.command_failed')
    expect(command()).toBeNull()
    expect(copy).not.toHaveBeenCalled()
  })
  it('clears expired material and disables copy without changing the server or credential', async () => {
    reads()
    let issued!: ReturnType<typeof generated>
    api.post.mockImplementation(async () => ({ data: issued = { ...generated(), expires_at: new Date(Date.now() + 500).toISOString() } }))
    mount(<NodeMigrationPreviewDialog server={server} onClose={vi.fn()} />)
    await confirmAndSelect()
    fireEvent.click(generateButton())
    await screen.findByLabelText('admin:servers.native.install_command')
    await screen.findByText('admin:servers.native.command_expired')
    expect(command()).toBeNull()
    const button = screen.getByRole('button', { name: 'admin:servers.native.copy_command' }) as HTMLButtonElement
    expect(button.disabled).toBe(true)
    fireEvent.click(button)
    expect(copy).not.toHaveBeenCalled()
    expect(document.body.textContent).not.toContain(issued.command)
    expect(api.post).toHaveBeenCalledTimes(1)
    expect(api.put).not.toHaveBeenCalled()
  })
  it('checks the actual deadline again on copy even when the timer has not fired yet', async () => {
    reads()
    const value = generated()
    api.post.mockResolvedValue({ data: value })
    mount(<NodeMigrationPreviewDialog server={server} onClose={vi.fn()} />)
    await confirmAndSelect()
    fireEvent.click(generateButton())
    await screen.findByLabelText('admin:servers.native.install_command')
    vi.spyOn(Date, 'now').mockReturnValue(Date.parse(value.expires_at) + 1)
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.copy_command' }))
    expect(command()).toBeNull()
    expect(copy).not.toHaveBeenCalled()
  })
  it('clears a displayed ticket when a confirmation or channel is changed', async () => {
    reads()
    mount(<NodeMigrationPreviewDialog server={server} onClose={vi.fn()} />)
    await confirmAndSelect()
    fireEvent.click(generateButton())
    await screen.findByLabelText('admin:servers.native.install_command')
    fireEvent.click(screen.getByRole('checkbox', { name: 'admin:servers.migration.ack_managed_only' }))
    expect(command()).toBeNull()
    expect(generateButton().disabled).toBe(true)
    fireEvent.click(screen.getByRole('checkbox', { name: 'admin:servers.migration.ack_managed_only' }))
    fireEvent.click(generateButton())
    await screen.findByLabelText('admin:servers.native.install_command')
    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }))
    fireEvent.click(screen.getByRole('option', { name: 'admin:servers.native.release_testing' }))
    expect(command()).toBeNull()
    expect(generateButton().disabled).toBe(true)
  })
  it('aborts old ticket requests and ignores delayed secrets after a PN version change', async () => {
    reads()
    const requests: { signal: AbortSignal; resolve: (value: { data: ReturnType<typeof generated> }) => void }[] = []
    api.post.mockImplementation((_url: string, _body: unknown, options: { signal: AbortSignal }) => new Promise(resolve => {
      requests.push({ signal: options.signal, resolve })
    }))
    mount(<NodeMigrationPreviewDialog server={server} onClose={vi.fn()} />)
    await confirmAndSelect('v0.0.1-beta3')
    fireEvent.click(generateButton())
    const old = requests[0]
    await selectVersion('v0.0.1-beta2')
    expect(old.signal.aborted).toBe(true)
    await act(async () => old.resolve({ data: { ...generated(), command: 'old-secret-material' } }))
    expect(command()).toBeNull()
    expect(document.body.textContent).not.toContain('old-secret-material')
    fireEvent.click(generateButton())
    await act(async () => requests[1].resolve({ data: { ...generated(), command: 'new-version-material' } }))
    await waitFor(() => expect(command()?.value).toBe('new-version-material'))
    expect(api.post.mock.calls[1][1].version).toBe('v0.0.1-beta2')
  })
  it('aborts pending previews on close and ignores late responses', async () => {
    const requests: { signal: AbortSignal; resolve: (value: { data: NodeMigrationPreview }) => void }[] = []
    api.get.mockImplementation((_url: string, options: { signal: AbortSignal }) => new Promise(resolve => {
      requests.push({ signal: options.signal, resolve })
    }))
    const onClose = vi.fn()
    mount(<NodeMigrationPreviewDialog server={server} onClose={onClose} />)
    fireEvent.click(screen.getByRole('button', { name: 'common:actions.close' }))
    expect(requests.every(request => request.signal.aborted)).toBe(true)
    expect(onClose).toHaveBeenCalledOnce()
    await act(async () => requests.forEach(request => request.resolve({ data: preview })))
    expect(screen.queryByText('admin:servers.migration.ready')).toBeNull()
    expect(command()).toBeNull()
  })
  it('aborts pending tickets on close and ignores late secrets', async () => {
    reads()
    let signal!: AbortSignal
    let resolve!: (value: { data: ReturnType<typeof generated> }) => void
    api.post.mockImplementation((_url: string, _body: unknown, options: { signal: AbortSignal }) => {
      signal = options.signal
      return new Promise(done => { resolve = done })
    })
    const onClose = vi.fn()
    mount(<NodeMigrationPreviewDialog server={server} onClose={onClose} />)
    await confirmAndSelect()
    fireEvent.click(generateButton())
    fireEvent.click(screen.getByRole('button', { name: 'common:actions.close' }))
    expect(signal.aborted).toBe(true)
    await act(async () => resolve({ data: { ...generated(), command: 'closed-dialog-secret' } }))
    expect(command()).toBeNull()
    expect(document.body.textContent).not.toContain('closed-dialog-secret')
  })
  it('aborts old previews when servers change and never accepts their delayed response', async () => {
    const requests: { signal: AbortSignal; resolve: (value: { data: NodeMigrationPreview }) => void }[] = []
    api.get.mockImplementation((_url: string, options: { signal: AbortSignal }) => new Promise(resolve => {
      requests.push({ signal: options.signal, resolve })
    }))
    const view = mount(<NodeMigrationPreviewDialog server={server} onClose={vi.fn()} />)
    const old = [...requests]
    const next = { ...server, id: 8, name: 'second-3xui' }
    view.rerender(<NodeMigrationPreviewDialog server={next} onClose={vi.fn()} />)
    expect(old.every(request => request.signal.aborted)).toBe(true)
    const current = requests.filter(request => !request.signal.aborted).at(-1)!
    await act(async () => {
      current.resolve({ data: { ...preview, server_id: 8, server_name: next.name } })
      old.forEach(request => request.resolve({ data: preview }))
    })
    await screen.findByText('admin:servers.migration.ready')
    expect(generateButton().disabled).toBe(true)
    expect(command()).toBeNull()
    view.unmount()
    expect(current.signal.aborted).toBe(true)
  })
  it('clears commands and resets releases/confirmations when the server identity changes', async () => {
    reads()
    const view = mount(<NodeMigrationPreviewDialog server={server} onClose={vi.fn()} />)
    await confirmAndSelect()
    fireEvent.click(generateButton())
    await screen.findByLabelText('admin:servers.native.install_command')
    const signal = api.post.mock.calls[0][2].signal as AbortSignal
    const next = { ...server, id: 8, name: 'new-server' }
    installReads({ '/admin/servers/8/node-migration-preview': { ...preview, server_id: 8, server_name: next.name } })
    view.rerender(<NodeMigrationPreviewDialog server={next} onClose={vi.fn()} />)
    expect(command()).toBeNull()
    expect(signal.aborted).toBe(true)
    await screen.findByText('admin:servers.migration.ready')
    expect((screen.getByRole('checkbox', { name: 'admin:servers.migration.single_instance_confirmation' }) as HTMLInputElement).checked).toBe(false)
    expect((screen.getByRole('checkbox', { name: 'admin:servers.migration.ack_managed_only' }) as HTMLInputElement).checked).toBe(false)
    expect(generateButton().disabled).toBe(true)
    expect(api.post).toHaveBeenCalledTimes(1)
  })
  it('clears old commands before a retried preview can provide a new fingerprint', async () => {
    reads()
    mount(<NodeMigrationPreviewDialog server={server} onClose={vi.fn()} />)
    await confirmAndSelect()
    fireEvent.click(generateButton())
    await screen.findByLabelText('admin:servers.native.install_command')
    let resolve!: (value: { data: NodeMigrationPreview }) => void
    api.get.mockImplementation(() => new Promise(done => { resolve = done }))
    fireEvent.click(screen.getByRole('button', { name: 'common:actions.retry' }))
    expect(command()).toBeNull()
    expect(generateButton().disabled).toBe(true)
    await act(async () => resolve({ data: { ...preview, fingerprint: 'b'.repeat(64) } }))
    await waitFor(() => expect(generateButton().disabled).toBe(false))
    fireEvent.click(generateButton())
    await screen.findByLabelText('admin:servers.native.install_command')
    expect(api.post.mock.calls[1][1].fingerprint).toBe('b'.repeat(64))
  })
})
