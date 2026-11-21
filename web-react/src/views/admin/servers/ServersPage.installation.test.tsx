// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import type { Server } from '@/api/servers'
import { releaseCatalog, nativeServer, provisioning, waiting, reads, versionInput, selectVersion, showIdentity, mountExpanded, selectMethod, openInstallation, pickRowAction, generatedFiles } from './install/installationHarness'
import { api, installReads, list, mount } from './testHarness'
import ServersPage from './ServersPage'
import { NativeInstallationSheet } from './install/NativeInstallationSheet'
import { publicNodeInstallCommands, PUBLIC_NODE_INSTALL_COMMAND, PUBLIC_NODE_INSTALL_ONLY_COMMAND } from './install/installModel'

// WHAT THIS FILE DRIVES: creating and editing the installation record — metadata,
// channel preference, folded identity, and the entries the dialog opens from.

const copy = vi.hoisted(() => vi.fn().mockResolvedValue(true))
vi.mock('@/utils/clipboard', () => ({ copyToClipboard: copy }))
const releaseReads = vi.hoisted(() => vi.fn())
vi.mock('@/api/nodeReleases', () => ({ listNodeReleases: releaseReads }))

beforeEach(() => releaseReads.mockResolvedValue(releaseCatalog))
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('Passwall Node installation', () => {
  it('uses stable by default and requires an explicit beta channel in public commands', () => {
    expect(PUBLIC_NODE_INSTALL_COMMAND).not.toContain('--channel')
    expect(publicNodeInstallCommands('rc').install).toContain('--channel rc')
    expect(publicNodeInstallCommands('testing').install).toContain('--channel beta')
    expect(PUBLIC_NODE_INSTALL_COMMAND).toMatch(/\| sudo sh$/)
    expect(PUBLIC_NODE_INSTALL_ONLY_COMMAND).toMatch(/\| sudo sh -s -- --install-only$/)
    expect(publicNodeInstallCommands('beta')).toEqual({
      install: expect.stringContaining('--channel beta'),
      installOnly: expect.stringMatching(/--channel beta --install-only$/),
    })
  })
  it('creates a Passwall Node from one form: remark, channel and name, then continues into its installation', async () => {
    installReads({ '/admin/servers': list([]), '/admin/servers/7/node-agent-status': waiting })
    api.post.mockResolvedValue({ data: { ...provisioning, server: { ...nativeServer, remark: 'optional note', update_channel: 'beta' } } })
    mount(<ServersPage />)
    fireEvent.click(await screen.findByRole('button', { name: 'admin:servers.create' }))
    const sheet = await screen.findByRole('dialog')
    const name = within(sheet).getByRole('textbox', { name: /admin:servers.field.name/ }) as HTMLInputElement
    expect(name.getAttribute('aria-required')).toBe('true')
    expect(document.activeElement).toBe(name)
    expect((within(sheet).getByRole('combobox', { name: 'admin:servers.field.panel_type' }) as HTMLSelectElement).value).toBe('psp')
    expect((within(sheet).getByRole('combobox', { name: 'admin:servers.native.method_label' }) as HTMLSelectElement).value).toBe('linux')
    expect((within(sheet).getByRole('combobox', { name: 'admin:servers.field.update_channel' }) as HTMLSelectElement).value).toBe('stable')
    expect(within(sheet).getByText('admin:servers.native.create_record_hint')).toBeTruthy()
    fireEvent.change(within(sheet).getByRole('textbox', { name: 'admin:servers.field.remark' }), { target: { value: 'optional note' } })
    fireEvent.change(within(sheet).getByRole('combobox', { name: 'admin:servers.field.update_channel' }), { target: { value: 'beta' } })
    fireEvent.change(name, { target: { value: nativeServer.name } })
    fireEvent.click(within(sheet).getByRole('button', { name: 'admin:servers.native.create_continue' }))
    await screen.findByRole('combobox', { name: 'admin:servers.native.agent_version' })
    expect(api.post).toHaveBeenCalledWith('/admin/servers', { name: nativeServer.name, panel_type: 'psp', remark: 'optional note', update_channel: 'beta' })
    await waitFor(() => expect(versionInput().value).toBe('4.1.2'))
    expect(api.post).toHaveBeenCalledTimes(1)
  })
  it('keeps the native edit channel discoverable with a short helper and preserves an existing remark', async () => {
    const saved = { ...nativeServer, remark: 'original note', update_channel: 'beta' as const }
    installReads({ '/admin/servers': list([saved]) })
    api.put.mockResolvedValue({ data: saved })
    mount(<ServersPage />)
    await pickRowAction(nativeServer, 'admin:servers.action.edit')
    const sheet = await screen.findByRole('dialog')
    expect((within(sheet).getByRole('combobox', { name: 'admin:servers.field.update_channel' }) as HTMLSelectElement).value).toBe('beta')
    expect(within(sheet).getByText('admin:servers.hint.update_channel_short')).toBeTruthy()
    expect((within(sheet).getByRole('textbox', { name: 'admin:servers.field.remark' }) as HTMLInputElement).value).toBe('original note')
    expect(screen.queryByRole('combobox', { name: 'admin:servers.native.method_label' })).toBeNull()
    fireEvent.click(within(sheet).getByRole('button', { name: 'common:actions.ok' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(api.put).toHaveBeenCalledWith('/admin/servers/7', { name: nativeServer.name, remark: 'original note', update_channel: 'beta' })
    expect(api.post.mock.calls.every(([url]) => url === '/admin/servers/probe')).toBe(true)
  })
  it('closes an untouched form without asking, and shows the connection fields for 3X-UI and S-UI', async () => {
    installReads({ '/admin/servers': list([]) })
    mount(<ServersPage />)
    fireEvent.click(await screen.findByRole('button', { name: 'admin:servers.create' }))
    fireEvent.click(await screen.findByRole('button', { name: 'common:actions.cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    fireEvent.click(screen.getAllByRole('button', { name: 'admin:servers.create' })[0])
    for (const kind of ['3xui', 'sui']) {
      fireEvent.change(await screen.findByRole('combobox', { name: 'admin:servers.field.panel_type' }), { target: { value: kind } })
      expect(screen.getByRole('textbox', { name: /admin:servers.field.url/ })).toBeTruthy()
      expect(screen.getByLabelText(/admin:servers.field.api_token/)).toBeTruthy()
      expect(screen.getByRole('textbox', { name: 'admin:servers.field.remark' })).toBeTruthy()
      expect((screen.getByRole('combobox', { name: 'admin:servers.field.auth_method' }) as HTMLSelectElement).value).toBe('token')
      expect(screen.queryByRole('combobox', { name: 'admin:servers.field.update_channel' })).toBeNull()
    }
    expect(api.post).not.toHaveBeenCalled()
    expect(api.put).not.toHaveBeenCalled()
  })
  it('still rejects an invalid native name without creating a record or exposing installation materials', async () => {
    installReads({ '/admin/servers': list([]) })
    mount(<ServersPage />)
    fireEvent.click(await screen.findByRole('button', { name: 'admin:servers.create' }))
    const name = await screen.findByRole('textbox', { name: /admin:servers.field.name/ })
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.create_continue' }))
    await waitFor(() => expect(name.getAttribute('aria-invalid')).toBe('true'))
    expect(screen.queryByRole('combobox', { name: 'admin:servers.native.agent_version' })).toBeNull()
    expect(api.post).not.toHaveBeenCalled()
    expect(api.put).not.toHaveBeenCalled()
  })
  // The full flow includes an async clipboard assertion with its own 5s bound.
  // Keep the outer budget above that bound so a failure reports the assertion.
  it('keeps identity, credentials and full scripts folded by default, with a labeled advanced control and one-line command feedback', async () => {
    reads()
    const generated = { server_id: 7, command: 'curl -fsSL https://panel.test/bootstrap/private-ticket -o /tmp/install',
      expires_at: new Date(Date.now() + 15 * 60_000).toISOString() }
    api.post.mockResolvedValue({ data: generated })
    copy.mockResolvedValue(false)
    mount(<NativeInstallationSheet server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    expect(screen.queryByLabelText('admin:servers.native.credential')).toBeNull()
    expect(screen.queryByLabelText('admin:servers.native.agent_id')).toBeNull()
    expect(screen.queryByRole('button', { name: 'admin:servers.native.copy_script' })).toBeNull()
    expect(screen.queryByText('admin:servers.native.method_hint.linux')).toBeNull()
    expect(screen.getByText('admin:servers.native.reinstall_safety')).toBeTruthy()
    const advanced = screen.getByRole('button', { name: 'admin:servers.native.advanced' })
    expect(advanced.tagName).toBe('BUTTON')
    expect(advanced.getAttribute('aria-expanded')).toBe('false')
    expect(advanced.getAttribute('tabindex')).not.toBe('-1')
    await selectVersion('4.1.0')
    expect(screen.queryByText('Reviewed contract fixture')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_command' }))
    const field = await screen.findByRole('textbox', { name: 'admin:servers.native.install_command' }) as HTMLInputElement
    expect(field.tagName).toBe('INPUT')
    expect(field.readOnly).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.copy_command' }))
    await screen.findByText('admin:servers.native.copy_failed', undefined, { timeout: 5000 })
    expect(field.value).toBe(generated.command)
    fireEvent.click(advanced)
    expect(advanced.getAttribute('aria-expanded')).toBe('true')
    expect((await screen.findByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(provisioning.credential)
    expect(api.post).toHaveBeenCalledTimes(1)
    expect(api.put).not.toHaveBeenCalled()
  }, 10_000)
  it('keeps the installation secret masked until it is revealed', async () => {
    reads()
    mount(<NativeInstallationSheet server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.advanced' }))
    const credential = await screen.findByLabelText('admin:servers.native.credential') as HTMLInputElement
    expect(credential.type).toBe('password')
    expect(credential.readOnly).toBe(true)
    fireEvent.click(within(credential.closest('[data-slot="input-group"]') as HTMLElement).getByRole('button', { name: 'common:ui.form.secret_show' }))
    expect(credential.type).toBe('text')
  })
  it.each(['stable', 'beta'] as const)('saves a PN %s preference on the original record and reopens Edit with the saved response', async preference => {
    const saved = { ...nativeServer, update_channel: preference }
    reads()
    api.put.mockResolvedValue({ data: saved })
    mount(<ServersPage />)
    await pickRowAction(nativeServer, 'admin:servers.action.edit')
    const edit = await screen.findByRole('dialog')
    const channel = within(edit).getByRole('combobox', { name: 'admin:servers.field.update_channel' }) as HTMLSelectElement
    expect(channel.value).toBe('stable')
    fireEvent.change(channel, { target: { value: preference } })
    fireEvent.click(within(edit).getByRole('button', { name: 'common:actions.ok' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(api.put).toHaveBeenCalledWith('/admin/servers/7', { name: nativeServer.name, remark: '', update_channel: preference })
    await pickRowAction(nativeServer, 'admin:servers.action.edit')
    expect((screen.getByRole('combobox', { name: 'admin:servers.field.update_channel' }) as HTMLSelectElement).value).toBe(preference)
    expect(api.put).toHaveBeenCalledTimes(1)
    expect(api.post.mock.calls.every(([url]) => url === '/admin/servers/probe')).toBe(true)
  })
  it.each(['stable', 'beta'] as const)('opens the saved PN %s installation preference but never persists a temporary override or changes fixed identity', async preference => {
    reads()
    installReads({ '/admin/servers': list([{ ...nativeServer, update_channel: preference }]),
      '/admin/servers/7/node-installation': provisioning, '/admin/servers/7/node-agent-status': waiting })
    mount(<ServersPage />)
    await openInstallation(nativeServer)
    const channel = preference === 'beta' ? 'beta' : 'stable'
    await showIdentity()
    expect((screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }) as HTMLSelectElement).value).toBe(channel)
    expect(versionInput().value).toBe(preference === 'beta' ? '4.1.2' : '4.1.0')
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(provisioning.credential)
    expect((screen.getByLabelText('admin:servers.native.agent_id') as HTMLInputElement).value).toBe(provisioning.agent_id)
    fireEvent.change(screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }), { target: { value: channel === 'beta' ? 'stable' : 'beta' } })
    expect(api.put).not.toHaveBeenCalled()
    expect(api.post.mock.calls.every(([url]) => url === '/admin/servers/probe')).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: 'common:actions.close' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await openInstallation(nativeServer)
    await showIdentity()
    expect((screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }) as HTMLSelectElement).value).toBe(channel)
    expect(api.put).not.toHaveBeenCalled()
  })
  it('saves the explicit beta preference when creating PN, then opens beta without choosing or installing a version', async () => {
    installReads({ '/admin/servers': list([]), '/admin/servers/7/node-agent-status': waiting })
    api.post.mockResolvedValue({ data: { ...provisioning, server: { ...nativeServer, update_channel: 'beta' } } })
    mount(<ServersPage />)
    fireEvent.click(await screen.findByRole('button', { name: 'admin:servers.create' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.change(within(dialog).getByRole('combobox', { name: 'admin:servers.field.update_channel' }), { target: { value: 'beta' } })
    fireEvent.change(within(dialog).getByRole('textbox', { name: /admin:servers.field.name/ }), { target: { value: nativeServer.name } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'admin:servers.native.create_continue' }))
    await showIdentity()
    expect(api.post).toHaveBeenCalledWith('/admin/servers', { name: nativeServer.name, panel_type: 'psp', remark: undefined, update_channel: 'beta' })
    expect((screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }) as HTMLSelectElement).value).toBe('beta')
    expect(versionInput().value).toBe('4.1.2')
    expect(api.post).toHaveBeenCalledTimes(1)
  })
  it.each([
    ['stable', 'stable'],
    ['beta', 'beta'],
  ] as const)('generates Docker files that follow the saved %s channel by default', async (preference, imageTag) => {
    const server = { ...nativeServer, update_channel: preference }
    installReads({ '/admin/servers/7/node-agent-status': waiting })
    api.post.mockResolvedValue({ data: generatedFiles() })
    mount(<NativeInstallationSheet server={server} initialProvisioning={{ ...provisioning, server }}
      onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod('docker')
    await waitFor(() => expect(versionInput().value).toBe(imageTag))
    const generate = screen.getByRole('button', { name: 'admin:servers.native.generate_files' }) as HTMLButtonElement
    expect(generate.disabled).toBe(false)
    fireEvent.click(generate)
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/servers/7/node-installation-files', {
      version: imageTag, method: 'docker', docker_remote_upgrade: true,
    }, expect.objectContaining({ signal: expect.any(AbortSignal) })))
    await screen.findByText('admin:servers.native.files_ready')
  })
  it('enables the isolated Docker upgrade helper by default and allows opting out from Advanced', async () => {
    installReads({ '/admin/servers/7/node-agent-status': waiting })
    api.post.mockResolvedValue({ data: generatedFiles() })
    mount(<NativeInstallationSheet server={nativeServer} initialProvisioning={provisioning}
      onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod('docker')
    await waitFor(() => expect(versionInput().value).toBe('stable'))
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.advanced' }))
    const remoteUpgrade = screen.getByRole('switch', { name: 'admin:servers.native.docker_remote_upgrade' }) as HTMLInputElement
    expect(remoteUpgrade.getAttribute('aria-checked')).toBe('true')
    fireEvent.click(remoteUpgrade)
    expect(remoteUpgrade.getAttribute('aria-checked')).toBe('false')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/servers/7/node-installation-files', {
      version: 'stable', method: 'docker',
    }, expect.objectContaining({ signal: expect.any(AbortSignal) })))
  })
  it.each([['3xui', '3X-UI'], ['sui', 'S-UI']] as const)('opens one existing %s install/reinstall entry with its original backend and configures that same record', async (panelType, label) => {
    const upstream: Server = { ...nativeServer, panel_type: panelType, auth_method: 'token', url: 'https://upstream.test', has_api_token: true }
    installReads({ '/admin/servers': list([upstream]) })
    mount(<ServersPage />)
    await pickRowAction(upstream, 'admin:servers.install_reinstall.action')
    const backend = screen.getByRole('combobox', { name: 'admin:servers.install_reinstall.backend' }) as HTMLSelectElement
    expect(backend.value).toBe(panelType)
    expect(backend.selectedOptions[0].textContent).toBe(label)
    expect(screen.getByText('admin:servers.install_reinstall.manual_unverified')).toBeTruthy()
    expect(api.get.mock.calls.some(([url]) => String(url).includes('node-installation') || String(url).includes('node-migration'))).toBe(false)
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.install_reinstall.configure_original' }))
    expect(await screen.findByText('admin:servers.edit_title')).toBeTruthy()
    expect((screen.getByRole('textbox', { name: /admin:servers.field.name/ }) as HTMLInputElement).value).toBe(upstream.name)
    // Only a new server can choose its type; an existing one shows it, locked.
    expect((screen.getByRole('combobox', { name: 'admin:servers.field.panel_type' }) as HTMLSelectElement).disabled).toBe(true)
    expect(api.put).not.toHaveBeenCalled()
    expect(api.post.mock.calls.every(([url]) => url === '/admin/servers/probe')).toBe(true)
  })
  it('generates and copies a single-use node command only after an exact verified release, without changing the fixed identity', async () => {
    reads()
    const generated = { server_id: nativeServer.id, command: 'curl -fsSL https://panel.test/private-once | sudo bash',
      expires_at: new Date(Date.now() + 15 * 60_000).toISOString() }
    api.post.mockResolvedValue({ data: generated })
    mountExpanded(<NativeInstallationSheet server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    const button = screen.getByRole('button', { name: 'admin:servers.native.generate_command' }) as HTMLButtonElement
    expect(button.disabled).toBe(true)
    fireEvent.click(button)
    expect(api.post).not.toHaveBeenCalled()
    await selectVersion('4.1.0')
    fireEvent.click(button)
    const command = await screen.findByLabelText('admin:servers.native.install_command') as HTMLInputElement
    expect(command.value).toBe(generated.command)
    expect(command.readOnly).toBe(true)
    expect(screen.getByText('admin:servers.native.command_safety')).toBeTruthy()
    expect(api.post).toHaveBeenCalledWith('/admin/servers/7/node-install-command', { version: '4.1.0', mode: 'install' }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.copy_command' }))
    await waitFor(() => expect(copy).toHaveBeenCalledWith(generated.command))
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(provisioning.credential)
    expect((screen.getByLabelText('admin:servers.native.agent_id') as HTMLInputElement).value).toBe(provisioning.agent_id)
    expect(api.post).toHaveBeenCalledTimes(1)
    expect(api.put).not.toHaveBeenCalled()
  })
  it('does not replace the automatic reviewed version with an arbitrary unreviewed input', async () => {
    reads()
    mountExpanded(<NativeInstallationSheet server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' }).getAttribute('aria-disabled')).not.toBe('true'))
    fireEvent.change(versionInput(), { target: { value: '99.99.99' } })
    expect(versionInput().value).toBe('4.1.0')
    const button = screen.getByRole('button', { name: 'admin:servers.native.generate_command' }) as HTMLButtonElement
    expect(button.disabled).toBe(false)
    expect(api.post).not.toHaveBeenCalled()
  })
  it('does not request a command when the loaded provisioning belongs to another server identity', async () => {
    reads()
    mountExpanded(<NativeInstallationSheet server={nativeServer} initialProvisioning={{ ...provisioning,
      server: { ...nativeServer, id: 8 } }} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectVersion('4.1.0')
    const button = screen.getByRole('button', { name: 'admin:servers.native.generate_command' }) as HTMLButtonElement
    expect(button.disabled).toBe(true)
    fireEvent.click(button)
    expect(api.post).not.toHaveBeenCalled()
  })
  it('disables copying after a generated command expires without rotating the fixed credential', async () => {
    reads()
    // The clock is the test's: the wall clock stands still, so the response is never already expired when it
    // lands, and the expiry timer (the one 500 ms timeout) is captured and fired by hand rather than waited for.
    const t0 = Date.now()
    const now = vi.spyOn(Date, 'now').mockReturnValue(t0)
    const realSetTimeout = globalThis.setTimeout
    let expire: (() => void) | undefined
    const timer = vi.spyOn(globalThis, 'setTimeout').mockImplementation(((fn: () => void, ms?: number, ...args: unknown[]) => {
      if (ms === 500) { expire = fn; return 0 as unknown as ReturnType<typeof setTimeout> }
      return realSetTimeout(fn, ms, ...args)
    }) as typeof setTimeout)
    api.post.mockImplementation(async () => ({ data: { server_id: 7, command: 'short-lived command',
      expires_at: new Date(t0 + 500).toISOString() } }))
    mountExpanded(<NativeInstallationSheet server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectVersion('4.1.0')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_command' }))
    await screen.findByLabelText('admin:servers.native.install_command')
    expect(screen.queryByText('admin:servers.native.command_expired')).toBeNull()
    // The command can appear before React runs the passive effect that schedules expiration.
    await waitFor(() => expect(expire).toBeDefined())
    timer.mockRestore(); now.mockRestore()
    act(() => { expire!() })
    expect(screen.getByText('admin:servers.native.command_expired')).toBeTruthy()
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
    mount(<ServersPage />)
    await openInstallation(nativeServer)
    await selectVersion('4.1.0')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_command' }))
    await waitFor(() => expect(api.post.mock.calls.some(([url]) => url === '/admin/servers/7/node-install-command')).toBe(true))
    const request = api.post.mock.calls.find(([url]) => url === '/admin/servers/7/node-install-command')![2]
    if (change === 'version') await selectVersion('4.1.1')
    else if (change === 'method') await selectMethod('manual')
    else {
      fireEvent.click(screen.getByRole('button', { name: 'common:actions.close' }))
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
      await openInstallation(change === 'server' ? secondServer : nativeServer)
      await showIdentity()
    }
    expect(request.signal.aborted).toBe(true)
    await act(async () => { finish({ data: { server_id: 7, command: 'obsolete private command',
      expires_at: new Date(Date.now() + 15 * 60_000).toISOString() } }) })
    expect(screen.queryByLabelText('admin:servers.native.install_command')).toBeNull()
    expect(copy).not.toHaveBeenCalled()
    await showIdentity()
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value)
      .toBe(change === 'server' ? secondProvisioning.credential : provisioning.credential)
  // A LOADED RUNNER TAKES LONGER THAN THE DEFAULT FIVE SECONDS, and this case drives
  // four variants of one cancellation flow through a real dialog — it timed out once
  // on a shared runner and passed on the rerun. The timeout is not the assertion: what
  // it proves is that a late private response is ignored, not that it is quick.
  }, 30_000)
})
