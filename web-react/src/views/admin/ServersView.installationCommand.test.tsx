// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import type { NativeServerProvisioning, Server } from '@/api/servers'
import { releaseCatalog, nativeServer, provisioning, waiting, reads, versionInput, selectVersion, copyScript, showIdentity, mountExpanded, selectMethod, openInstallation, generatedFiles, materialContents, materialPreviews } from '@/test/installationHarness'
import { api, installReads, list, mount } from '@/test/adminSaveHarness'
import { useAuthStore } from '@/stores/auth'
import ConfirmHost from '@/components/ConfirmHost'
import ServersView, { NativeInstallationDialog, PUBLIC_NODE_INSTALL_COMMAND } from './ServersView'

// WHAT THIS FILE DRIVES: the one-click command and its lifecycle — generation, expiry,
// rotation, and every way a late or wrong response is refused.

const copy = vi.hoisted(() => vi.fn().mockResolvedValue(true))
vi.mock('@/utils/clipboard', () => ({ copyToClipboard: copy }))
const releaseReads = vi.hoisted(() => vi.fn())
vi.mock('@/api/nodeReleases', () => ({ listNodeReleases: releaseReads }))

beforeEach(() => releaseReads.mockResolvedValue(releaseCatalog))
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('Passwall Node installation', () => {
  it.each(['wrong-server', 'expired', 'empty'] as const)('rejects an invalid %s command response instead of exposing it', async invalid => {
    reads()
    api.post.mockResolvedValue({ data: { server_id: invalid === 'wrong-server' ? 8 : 7,
      command: invalid === 'empty' ? '' : 'private command',
      expires_at: new Date(Date.now() + (invalid === 'expired' ? -60_000 : 15 * 60_000)).toISOString() } })
    mountExpanded(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectVersion('4.1.0')
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
    // THE VALUE IS WRITTEN BY AN EFFECT, so its existence and its value are two
    // different moments: the field is on screen before the selection lands, and
    // reading it straight after the await raced the effect. That is a flake with a
    // rate of about one run in thirty — enough to fail CI on a branch whose diff
    // cannot cause it, which is how it was found: a Go-only pull request was red on
    // the web job.
    await waitFor(() => expect(versionInput().value).toBe('4.1.0'))
    expect(copyScript().disabled).toBe(false)
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
    await showIdentity()
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
    expect(api.post).toHaveBeenCalledWith('/admin/servers', { name: nativeServer.name, panel_type: 'psp', remark: undefined, update_channel: 'stable' })
    expect(api.post.mock.calls.filter(([url]) => url === '/admin/servers')).toHaveLength(1)
    expect(screen.getByRole('combobox', { name: 'admin:servers.native.method_label' }).textContent).toBe('admin:servers.native.method.linux')
    expect(versionInput().value).toBe('')
    expect(screen.getByRole('heading', { name: /^admin:servers\.passwall_node_install\.title/ })).toBeTruthy()
    expect(screen.queryByText('admin:servers.passwall_node_install.existing_hint')).toBeNull()
    expect(api.get.mock.calls.some(([url]) => String(url).endsWith('/node-installation'))).toBe(false)
  })
  it.each(['docker', 'github', 'manual'] as const)('hands the selected %s installation method into step two without recreating or rereading credentials', async method => {
    installReads({ '/admin/servers': list([]), '/admin/servers/7/node-agent-status': waiting })
    api.post.mockResolvedValue({ data: provisioning })
    mount(<ServersView />)
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.create' }))
    const createDialog = await screen.findByRole('dialog')
    await selectMethod(method, createDialog)
    fireEvent.change(within(createDialog).getByRole('textbox', { name: /admin:servers.field.name/ }), { target: { value: nativeServer.name } })
    fireEvent.click(within(createDialog).getByRole('button', { name: 'admin:servers.native.create_continue' }))
    await showIdentity()
    expect(screen.getByRole('dialog')).toBe(createDialog)
    expect(screen.queryByRole('button', { name: 'admin:servers.native.create_continue' })).toBeNull()
    expect(screen.getByRole('combobox', { name: 'admin:servers.native.method_label' }).textContent).toBe(`admin:servers.native.method.${method}`)
    if (method === 'github') {
      expect((screen.getByLabelText('admin:servers.native.github_install_command') as HTMLTextAreaElement).value).toBe(PUBLIC_NODE_INSTALL_COMMAND)
      expect(screen.queryByRole('combobox', { name: 'admin:servers.native.release_channel' })).toBeNull()
      expect(screen.queryByRole('combobox', { name: 'admin:servers.native.platform_label' })).toBeNull()
      expect(screen.queryByRole('combobox', { name: 'admin:servers.native.architecture' })).toBeNull()
    } else if (method === 'manual') {
      expect(screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' })).toBeTruthy()
      expect(screen.getByRole('combobox', { name: 'admin:servers.native.platform_label' })).toBeTruthy()
      expect(screen.getByRole('combobox', { name: 'admin:servers.native.architecture' })).toBeTruthy()
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
      await showIdentity()
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
    await showIdentity()
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
    mountExpanded(<NativeInstallationDialog server={nativeServer} initialProvisioning={null} onClose={vi.fn()} onRotate={onRotate} />)
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
    mountExpanded(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await screen.findByText('admin:servers.native.agent_status.unconfigured')
    expect(screen.queryByText('admin:servers.native.agent_status.running')).toBeNull()
    expect(versionInput().value).toBe('4.1.0')
    expect(copyScript().disabled).toBe(false)
    expect(screen.queryByRole('textbox', { name: 'admin:servers.native.agent_version' })).toBeNull()
    expect(api.post).not.toHaveBeenCalled()
    await selectVersion('4.1.1')
    fireEvent.click(copyScript())
    await waitFor(() => expect(copy).toHaveBeenCalledWith(`#!/bin/sh\n# ${provisioning.credential}\n`))
    expect(api.post).toHaveBeenCalledWith('/admin/servers/7/node-install-script', { version: '4.1.1', mode: 'install' }, expect.objectContaining({ responseType: 'text' }))
    expect(screen.queryByLabelText('admin:servers.native.start_command')).toBeNull()
    const run = (screen.getByLabelText('admin:servers.native.run_script_command') as HTMLTextAreaElement).value
    expect(run).toContain('sudo bash ./passwall-node-install-agt_7.sh')
    expect(run).not.toContain(provisioning.credential)
  })
  it('preserves identity and private credential while Docker follows the selected release channel', async () => {
    reads()
    mountExpanded(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectVersion('4.1.1')
    for (const method of ['docker', 'github', 'manual', 'linux'] as const) {
      await selectMethod(method)
      expect((screen.getByLabelText('admin:servers.native.agent_id') as HTMLInputElement).value).toBe(provisioning.agent_id)
      expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(provisioning.credential)
      if (method === 'github') {
        expect(screen.queryByLabelText('admin:servers.native.agent_version')).toBeNull()
        expect((screen.getByLabelText('admin:servers.native.github_install_command') as HTMLTextAreaElement).value).toBe(PUBLIC_NODE_INSTALL_COMMAND)
      } else {
        await waitFor(() => expect(versionInput().value).toBe(method === 'docker' ? 'beta' : '4.1.0'))
      }
      expect(screen.getByRole('combobox', { name: 'admin:servers.native.method_label' }).textContent).toBe(`admin:servers.native.method.${method}`)
    }
    expect(api.post).not.toHaveBeenCalled()
    expect(api.get.mock.calls.some(([url]) => String(url).endsWith('/node-installation'))).toBe(false)
  })
  it('aborts a pending Linux script on method change and never copies its obsolete secret response', async () => {
    reads()
    let finish!: (response: { data: string }) => void
    api.post.mockReturnValue(new Promise<{ data: string }>(resolve => { finish = resolve }))
    mountExpanded(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectVersion('4.1.0')
    fireEvent.click(copyScript())
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/servers/7/node-install-script', { version: '4.1.0', mode: 'install' }, expect.objectContaining({ signal: expect.any(AbortSignal) })))
    const request = api.post.mock.calls.find(([url]) => url === '/admin/servers/7/node-install-script')!
    await selectMethod('github')
    expect(request[2].signal.aborted).toBe(true)
    await act(async () => { finish({ data: `#!/bin/sh\n# obsolete ${provisioning.credential}\n` }) })
    expect(copy).not.toHaveBeenCalled()
    expect(screen.queryByLabelText('admin:servers.native.agent_version')).toBeNull()
    expect((screen.getByLabelText('admin:servers.native.github_install_command') as HTMLTextAreaElement).value).toBe(PUBLIC_NODE_INSTALL_COMMAND)
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(provisioning.credential)
    await selectMethod('linux')
    expect(copyScript().disabled).toBe(true)
  })
  it('aborts a pending private script when switching release channels without rotating the node identity', async () => {
    reads()
    let finish!: (response: { data: string }) => void
    api.post.mockReturnValue(new Promise<{ data: string }>(resolve => { finish = resolve }))
    mountExpanded(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectVersion('4.1.1')
    fireEvent.click(copyScript())
    await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
    const request = api.post.mock.calls[0][2]
    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }))
    fireEvent.click(await screen.findByRole('option', { name: 'admin:servers.native.release_stable' }))
    expect(request.signal.aborted).toBe(true)
    expect(versionInput().value).toBe('4.1.0')
    expect(copyScript().disabled).toBe(false)
    await act(async () => { finish({ data: `#!/bin/sh\n# obsolete ${provisioning.credential}\n` }) })
    expect(copy).not.toHaveBeenCalled()
    expect((screen.getByLabelText('admin:servers.native.agent_id') as HTMLInputElement).value).toBe(provisioning.agent_id)
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(provisioning.credential)
    expect(api.post.mock.calls.some(([url]) => String(url).includes('rotate-node-credential'))).toBe(false)
  })
  it('shows and copies the exact Docker private files and secret-free startup steps', async () => {
    reads()
    const materials = generatedFiles()
    api.post.mockResolvedValue({ data: materials })
    mountExpanded(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod('docker')
    await selectVersion('4.1.1')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }))
    await screen.findAllByLabelText('admin:servers.native.file_content')
    expect(api.post).toHaveBeenCalledWith('/admin/servers/7/node-installation-files', {
      version: '4.1.1', method: 'docker', docker_remote_upgrade: true,
    }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
    expect(materialContents()).toEqual(materialPreviews(materials))
    const privateFile = within(screen.getByRole('region', { name: 'config/node-credential.txt' })).getByLabelText('admin:servers.native.file_content') as HTMLInputElement
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
    const materials = generatedFiles()
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
    mountExpanded(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod('docker')
    await selectVersion('4.1.1')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }))
    await screen.findAllByLabelText('admin:servers.native.file_content')
    fireEvent.click(screen.getAllByRole('button', { name: 'admin:servers.native.download_file' })[0])
    await waitFor(() => expect(downloads).toEqual([{ file: 'node-credential.txt', url: 'blob:private-install-file' }]))
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
})
