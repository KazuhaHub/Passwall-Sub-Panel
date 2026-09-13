// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, list, mount } from '@/test/adminSaveHarness'
import { useAuthStore } from '@/stores/auth'
import type { NodeMigrationPreview, Server } from '@/api/servers'
import { NodeMigrationPreviewDialog } from './NodeMigrationPreviewDialog'
import ServersView from './ServersView'

const server: Server = {
  id: 7, name: 'existing-3xui', panel_type: '3xui', url: 'https://xui.example.test', capabilities: [],
  auth_method: 'token', has_api_token: true, has_password: false, insecure_https: false,
}
const preview: NodeMigrationPreview = {
  server_id: 7, server_name: server.name, core_version: '26.6.27', recommended_core_version: '26.6.27',
  core_requires_ack: false, allow_restricted_reality: false, fingerprint: 'a'.repeat(64),
  node_count: 3, client_count: 5, blockers: [], warnings: [{ code: 'managed_scope' }], can_migrate: true,
}
const endpoint = '/admin/servers/7/node-migration-preview'

function reads(value = preview, servers = [server]) {
  installReads({ '/admin/servers': list(servers), [endpoint]: value })
}

async function openMenu(target: Server) {
  const row = (await screen.findByText(target.name)).closest('tr')!
  fireEvent.click(within(row).getByRole('button', { name: 'admin:servers.action.more' }))
}

function cli() { return screen.queryByLabelText('admin:servers.migration.cli_command') as HTMLTextAreaElement | null }

describe('3X-UI to Passwall Node read-only migration preview', () => {
  it('opens from an existing 3X-UI server without requiring upgrade capabilities and shows offline instructions only', async () => {
    reads()
    mount(<ServersView />)
    await openMenu(server)
    expect(screen.getAllByRole('menuitem', { name: 'admin:servers.passwall_node_install.action' })).toHaveLength(1)
    fireEvent.click(await screen.findByRole('menuitem', { name: 'admin:servers.passwall_node_install.action' }))
    await screen.findByText('admin:servers.migration.ready')
    expect(screen.getByRole('heading', { name: 'admin:servers.passwall_node_install.title' })).toBeTruthy()
    expect(screen.getByText('admin:servers.migration.preview_only')).toBeTruthy()
    expect(screen.queryByLabelText('admin:servers.native.credential')).toBeNull()
    expect(cli()?.value).toBe(`psp migrate-server --server-id 7 --core-version 26.6.27 --expected-fingerprint ${'a'.repeat(64)} --all-psp-stopped --old-xray-stopped --managed-only --apply`)
    const docker = screen.getByLabelText('admin:servers.migration.docker_commands') as HTMLTextAreaElement
    expect(docker.value).toContain('docker compose stop YOUR_PSP_SERVICE')
    expect(docker.value).toContain('docker compose run --rm --no-deps YOUR_PSP_SERVICE migrate-server --server-id 7')
    expect(docker.value).toContain('docker compose start YOUR_PSP_SERVICE')
    expect(docker.value).toBe(`docker compose stop YOUR_PSP_SERVICE &&\ndocker compose run --rm --no-deps YOUR_PSP_SERVICE ${cli()!.value.slice(4)} &&\ndocker compose start YOUR_PSP_SERVICE`)
    expect(docker.value.split(' &&\n')).toHaveLength(3)
    expect(cli()?.readOnly).toBe(true)
    expect(screen.getByText('admin:servers.migration.preserved')).toBeTruthy()
    expect(screen.getByText('admin:servers.migration.scope')).toBeTruthy()
    expect(screen.getByText('admin:servers.migration.maintenance')).toBeTruthy()
    expect(screen.getByText('admin:servers.migration.after_restart')).toBeTruthy()
    expect(api.post.mock.calls.every(([url]) => url === '/admin/servers/probe')).toBe(true)
    expect(api.put).not.toHaveBeenCalled()
    expect(api.delete).not.toHaveBeenCalled()
  })

  it('uses the same entry for Passwall Node and explicitly disables unsupported S-UI installation', async () => {
    const native: Server = { ...server, id: 8, name: 'native-node', panel_type: 'psp', capabilities: [] }
    const sui: Server = { ...server, id: 9, name: 'sui-node', panel_type: 'sui', capabilities: [] }
    reads(preview, [native, sui])
    mount(<ServersView />)
    await openMenu(native)
    expect(screen.getByRole('menuitem', { name: 'admin:servers.passwall_node_install.action' }).getAttribute('aria-disabled')).not.toBe('true')
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' })
    await openMenu(sui)
    const unsupported = screen.getByRole('menuitem', { name: 'admin:servers.passwall_node_install.action' })
    expect(unsupported.getAttribute('aria-disabled')).toBe('true')
    expect(screen.getByText('admin:servers.passwall_node_install.sui_unsupported')).toBeTruthy()
    fireEvent.click(unsupported)
    expect(api.get.mock.calls.some(([url]) => String(url).includes('node-migration-preview'))).toBe(false)
    expect(api.get.mock.calls.some(([url]) => String(url).endsWith('/node-installation'))).toBe(false)
    expect(api.post.mock.calls.every(([url]) => url === '/admin/servers/probe')).toBe(true)
  })

  it('hides the migration menu from operators even when other server actions exist', async () => {
    reads(preview, [{ ...server, capabilities: ['panel.upgrade'] }])
    useAuthStore.setState({ role: 'operator' })
    mount(<ServersView />)
    await openMenu(server)
    expect(screen.queryByRole('menuitem', { name: 'admin:servers.passwall_node_install.action' })).toBeNull()
    expect(api.get.mock.calls.some(([url]) => String(url).includes('node-migration-preview'))).toBe(false)
  })

  it('shows localized blockers and suppresses every apply command even if the server wrongly marks it eligible', async () => {
    reads({ ...preview, blockers: [{ code: 'missing_snapshot', node_id: 3 }, { code: 'credential_mismatch', client_id: 5 }], can_migrate: true })
    mount(<NodeMigrationPreviewDialog server={server} onClose={() => {}} />)
    await screen.findByText('admin:servers.migration.blockers')
    expect(screen.getByText(/admin:servers.migration.issue.missing_snapshot/)).toBeTruthy()
    expect(screen.getByText(/admin:servers.migration.issue.credential_mismatch/)).toBeTruthy()
    expect(cli()).toBeNull()
    expect(screen.queryByLabelText('admin:servers.migration.docker_commands')).toBeNull()
    expect(api.post).not.toHaveBeenCalled()
  })

  it('localizes every currently emitted migration policy code without displaying an untranslated fallback', async () => {
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
    mount(<NodeMigrationPreviewDialog server={server} onClose={() => {}} />)
    await screen.findByText('admin:servers.migration.blockers')
    expect(screen.queryByText('admin:servers.migration.issue.unknown')).toBeNull()
    expect(screen.getByText('admin:servers.migration.issue.duplicate_listener_binding')).toBeTruthy()
    expect(screen.getByText('admin:servers.migration.issue.unsafe_reality_finalmask_tcp')).toBeTruthy()
    expect(screen.getByText('admin:servers.migration.issue.connection_limits_not_enforced')).toBeTruthy()
    expect(screen.getByText('admin:servers.migration.issue.reality_compatibility_normalization')).toBeTruthy()
    expect(cli()).toBeNull()
  })

  it('requires an explicit restricted-core acknowledgement and rechecks the preview before exposing the matching CLI flag', async () => {
    const restricted = { ...preview, core_version: '26.7.28', core_requires_ack: true, blockers: [{ code: 'core_ack_required' }], can_migrate: false }
    api.get.mockImplementation(async (_url: string, options: { params: { allow_restricted_reality?: boolean } }) => ({
      data: options.params.allow_restricted_reality
        ? { ...restricted, blockers: [], allow_restricted_reality: true, can_migrate: true }
        : restricted,
    }))
    mount(<NodeMigrationPreviewDialog server={server} onClose={() => {}} />)
    await screen.findByText('admin:servers.migration.blockers')
    expect(cli()).toBeNull()
    fireEvent.click(screen.getByRole('checkbox', { name: 'admin:servers.migration.ack_restricted_core' }))
    await screen.findByText('admin:servers.migration.ready')
    expect(cli()?.value).toContain('--allow-restricted-reality --all-psp-stopped')
    expect(api.get).toHaveBeenCalledWith(endpoint, expect.objectContaining({ params: { allow_restricted_reality: true } }))
    expect(api.post).not.toHaveBeenCalled()
  })

  it('changes to the recommended verified core only after the administrator chooses it', async () => {
    const blocked = { ...preview, core_version: '26.9.99', blockers: [{ code: 'core_not_verified' }], can_migrate: false }
    api.get.mockImplementation(async (_url: string, options: { params: { core_version?: string } }) => ({
      data: options.params.core_version ? { ...preview, warnings: [{ code: 'core_version_changed' }] } : blocked,
    }))
    mount(<NodeMigrationPreviewDialog server={server} onClose={() => {}} />)
    await screen.findByText('admin:servers.migration.blockers')
    expect(cli()).toBeNull()
    expect(api.get.mock.calls.every(([, options]) => !options.params.core_version)).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.migration.use_recommended_core' }))
    await screen.findByText('admin:servers.migration.ready')
    expect(cli()?.value).toContain('--core-version 26.6.27')
    expect(screen.getByText('admin:servers.migration.issue.core_version_changed')).toBeTruthy()
    expect(api.get).toHaveBeenCalledWith(endpoint, expect.objectContaining({ params: { core_version: '26.6.27' } }))
  })

  it('fails closed on unsafe fingerprint or version tokens', async () => {
    reads({ ...preview, core_version: '26.6.27; touch /tmp/not-allowed' })
    const view = mount(<NodeMigrationPreviewDialog server={server} onClose={() => {}} />)
    await screen.findByText('admin:servers.migration.invalid_command')
    expect(cli()).toBeNull()
    reads({ ...preview, fingerprint: 'a'.repeat(64) + '; false' })
    fireEvent.click(screen.getByRole('button', { name: 'common:actions.retry' }))
    await screen.findByText('admin:servers.migration.invalid_command')
    expect(cli()).toBeNull()
    view.unmount()
  })

  it('shows a local lookup error with retry and does not display an apply command until a successful new preview', async () => {
    api.get.mockRejectedValue(new Error('Database unavailable'))
    mount(<NodeMigrationPreviewDialog server={server} onClose={() => {}} />)
    await screen.findByText('admin:servers.migration.failed')
    expect(cli()).toBeNull()
    reads()
    fireEvent.click(screen.getByRole('button', { name: 'common:actions.retry' }))
    await screen.findByText('admin:servers.migration.ready')
    expect(cli()).not.toBeNull()
  })

  it('handles backend access denial without rendering commands or raw error contents', async () => {
    api.get.mockRejectedValue({ response: { status: 403, data: { error: 'sensitive detail' } } })
    mount(<NodeMigrationPreviewDialog server={server} onClose={() => {}} />)
    await screen.findByText('admin:servers.migration.forbidden')
    expect(cli()).toBeNull()
    expect(screen.queryByText('sensitive detail')).toBeNull()
  })

  it('does not issue a preview request for a non-administrator or native server', async () => {
    useAuthStore.setState({ role: 'operator' })
    const view = mount(<NodeMigrationPreviewDialog server={server} onClose={() => {}} />)
    await screen.findByText('admin:servers.migration.forbidden')
    expect(api.get).not.toHaveBeenCalled()
    useAuthStore.setState({ role: 'admin' })
    view.rerender(<NodeMigrationPreviewDialog server={{ ...server, panel_type: 'psp' }} onClose={() => {}} />)
    await screen.findByText('admin:servers.migration.unsupported_server')
    expect(api.get).not.toHaveBeenCalled()
  })

  it('aborts closed requests and ignores late previews when another server is selected', async () => {
    const requests: { signal: AbortSignal; resolve: (value: { data: NodeMigrationPreview }) => void; url: string }[] = []
    api.get.mockImplementation((url: string, options: { signal: AbortSignal }) => new Promise(resolve => {
      requests.push({ url, signal: options.signal, resolve })
    }))
    const view = mount(<NodeMigrationPreviewDialog server={server} onClose={() => {}} />)
    const old = [...requests]
    const nextServer = { ...server, id: 8, name: 'second-3xui' }
    view.rerender(<NodeMigrationPreviewDialog server={nextServer} onClose={() => {}} />)
    expect(old.every(request => request.signal.aborted)).toBe(true)
    const current = requests.filter(request => !request.signal.aborted).at(-1)!
    await act(async () => {
      current.resolve({ data: { ...preview, server_id: 8, server_name: nextServer.name } })
      old.forEach(request => request.resolve({ data: preview }))
    })
    await waitFor(() => expect(cli()?.value).toContain('--server-id 8'))
    expect(cli()?.value).not.toContain('--server-id 7')
    view.unmount()
    expect(current.signal.aborted).toBe(true)
  })
})
