// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { NativeAgentStatus, NativeServerProvisioning, Server } from '@/api/servers'
import { api, installReads, list, mount } from '@/test/adminSaveHarness'
import ConfirmHost from '@/components/ConfirmHost'
import ServersView, { isNodeReleaseVersion, NativeInstallationDialog } from './ServersView'
import { hostFromURL } from './NodesView'

const copy = vi.hoisted(() => vi.fn().mockResolvedValue(true))
vi.mock('@/utils/clipboard', () => ({ copyToClipboard: copy }))

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
  return screen.getByLabelText('admin:servers.native.agent_version')
}

function copyScript(): HTMLButtonElement {
  return screen.getByRole('button', { name: 'admin:servers.native.copy_script' })
}

afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('PSP Node installation', () => {
  it('opens Install/Reinstall even without upgrade capabilities and never rotates on reopen', async () => {
    reads()
    mount(<ServersView />)
    const row = (await screen.findByText(nativeServer.name)).closest('tr')!
    fireEvent.click(within(row).getByRole('button', { name: 'admin:servers.action.more' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'admin:servers.action.install_node' }))
    await screen.findByLabelText('admin:servers.native.agent_version')
    expect(versionInput().value).toBe('')
    expect(copyScript().disabled).toBe(true)
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(provisioning.credential)
    expect(api.get).toHaveBeenCalledWith('/admin/servers/7/node-installation', expect.objectContaining({ signal: expect.any(AbortSignal) }))
    expect(api.post.mock.calls.some(([url]) => String(url).includes('rotate-node-credential'))).toBe(false)
    expect(screen.getByText('admin:servers.native.private_warning')).toBeTruthy()
  })

  it('opens the default Linux installation workflow immediately after creating a native server', async () => {
    installReads({ '/admin/servers': list([]), '/admin/servers/7/node-agent-status': waiting })
    api.post.mockResolvedValue({ data: provisioning })
    mount(<ServersView />)
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.create' }))
    const createDialog = await screen.findByRole('dialog')
    fireEvent.mouseDown(within(createDialog).getByRole('combobox', { name: 'admin:servers.field.panel_type' }))
    fireEvent.click(await screen.findByRole('option', { name: 'PSP Node' }))
    fireEvent.change(within(createDialog).getByRole('textbox', { name: /admin:servers.field.name/ }), { target: { value: nativeServer.name } })
    fireEvent.click(within(createDialog).getByRole('button', { name: 'common:actions.ok' }))
    await screen.findByLabelText('admin:servers.native.agent_version')
    expect(api.post).toHaveBeenCalledWith('/admin/servers', { name: nativeServer.name, panel_type: 'psp', remark: undefined })
    expect(screen.getByText('admin:servers.native.install_method')).toBeTruthy()
    expect(versionInput().value).toBe('')
    expect(api.get.mock.calls.some(([url]) => String(url).endsWith('/node-installation'))).toBe(false)
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

    async function openInstallation(server: Server) {
      const row = (await screen.findByText(server.name)).closest('tr')!
      fireEvent.click(await within(row).findByRole('button', { name: 'admin:servers.action.more' }))
      fireEvent.click(await screen.findByRole('menuitem', { name: 'admin:servers.action.install_node' }))
    }

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
    api.post.mockImplementation(async () => { imported = true; return { data: { credential: provisioning.credential } } })
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
    for (const version of ['', 'latest', '1.2.3', 'v01.2.3', 'v1.2.3-beta.01', 'https://example.test/v1.2.3']) {
      fireEvent.change(versionInput(), { target: { value: version } })
      expect(copyScript().disabled).toBe(true)
    }
    expect(api.post).not.toHaveBeenCalled()
    fireEvent.change(versionInput(), { target: { value: 'v1.2.3-beta.1' } })
    fireEvent.click(copyScript())
    await waitFor(() => expect(copy).toHaveBeenCalledWith(`#!/bin/sh\n# ${provisioning.credential}\n`))
    expect(api.post).toHaveBeenCalledWith('/admin/servers/7/node-install-script', { version: 'v1.2.3-beta.1' }, expect.objectContaining({ responseType: 'text' }))
    const start = (screen.getByLabelText('admin:servers.native.start_command') as HTMLTextAreaElement).value
    expect(start).toMatch(/^sudo systemctl stop passwall-node &&\n/)
    expect(start).toContain('--credential-file /opt/passwall-node/config/credential')
    expect(start).not.toContain(provisioning.credential)
    expect(screen.getByRole('link', { name: 'admin:servers.native.manual_docs' }).getAttribute('href')).toBe('https://github.com/KazuhaHub/Passwall-Node/blob/main/README.md#run-the-production-daemon')
    const run = (screen.getByLabelText('admin:servers.native.run_script_command') as HTMLTextAreaElement).value
    expect(run).toContain('sudo bash ./passwall-node-install-agt_7.sh')
    expect(run).not.toContain(provisioning.credential)
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
    fireEvent.change(versionInput(), { target: { value: 'v1.2.3' } })
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
    fireEvent.change(versionInput(), { target: { value: 'v1.2.3' } })
    fireEvent.click(copyScript())
    await screen.findByText('Release has no installation assets')
    expect(copy).not.toHaveBeenCalled()
    expect(copyScript().disabled).toBe(false)
    expect(screen.getByRole('dialog')).toBeTruthy()
  })
})

describe('native installation inputs', () => {
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
