// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import type { NodeReleaseCatalog } from '@/api/nodeReleases'
import type { NativeAgentStatus, NativeInstallationFiles, NativeServerProvisioning, Server } from '@/api/servers'
import { releaseCatalog, nativeServer, provisioning, waiting, reads, versionInput, selectVersion, copyScript, showIdentity, mountExpanded, selectMethod, openInstallation, generatedFiles, generatedManualFiles } from '@/test/installationHarness'
import { api, installReads, list, mount } from '@/test/adminSaveHarness'
import ServersView, { NativeInstallationDialog, PUBLIC_NODE_INSTALL_COMMAND, PUBLIC_NODE_INSTALL_ONLY_COMMAND } from './ServersView'

// WHAT THIS FILE DRIVES: what an operator leaves with — the selected method, the private
// files and scripts, and the downloads and offline materials built from a release.

const copy = vi.hoisted(() => vi.fn().mockResolvedValue(true))
vi.mock('@/utils/clipboard', () => ({ copyToClipboard: copy }))
const releaseReads = vi.hoisted(() => vi.fn())
vi.mock('@/api/nodeReleases', () => ({ listNodeReleases: releaseReads }))

beforeEach(() => releaseReads.mockResolvedValue(releaseCatalog))
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('Passwall Node installation', () => {
  it('aborts pending Docker generation when switching to the static GitHub workflow and ignores the obsolete response', async () => {
    reads()
    let finishDocker!: (response: { data: NativeInstallationFiles }) => void
    const obsolete = { ...generatedFiles(), files: [{ name: 'obsolete.yaml', content: 'obsolete private response' }] }
    api.post.mockImplementation(() => new Promise<{ data: NativeInstallationFiles }>(resolve => { finishDocker = resolve }))
    mountExpanded(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod('docker')
    await selectVersion('4.1.1')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledTimes(1))
    const oldRequest = api.post.mock.calls[0][2]
    await selectMethod('github')
    expect(oldRequest.signal.aborted).toBe(true)
    expect((screen.getByLabelText('admin:servers.native.github_install_command') as HTMLTextAreaElement).value).toBe(PUBLIC_NODE_INSTALL_COMMAND)
    expect(screen.queryByRole('button', { name: 'admin:servers.native.generate_files' })).toBeNull()
    await act(async () => { finishDocker({ data: obsolete }) })
    expect(screen.queryByLabelText('admin:servers.native.file_content')).toBeNull()
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
    await showIdentity()
    await selectMethod('docker')
    await selectVersion('4.1.1')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }))
    await waitFor(() => expect(api.post.mock.calls.filter(([url]) => String(url).endsWith('/node-installation-files'))).toHaveLength(1))
    const oldRequest = api.post.mock.calls.find(([url]) => String(url).endsWith('/node-installation-files'))![2]
    fireEvent.click(screen.getByRole('button', { name: 'common:actions.close' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(oldRequest.signal.aborted).toBe(true)
    await openInstallation(secondServer)
    await showIdentity()
    await selectMethod('docker')
    await act(async () => { finish({ data: generatedFiles() }) })
    expect((screen.getByLabelText('admin:servers.native.agent_id') as HTMLInputElement).value).toBe(secondProvisioning.agent_id)
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(secondProvisioning.credential)
    expect(versionInput().value).toBe('latest')
    expect(screen.queryByLabelText('admin:servers.native.file_content')).toBeNull()
    expect(copy).not.toHaveBeenCalled()
  })
  it('uses the public GitHub installer without release, OS, architecture, or file-generation requests', async () => {
    reads()
    mountExpanded(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod('github')
    expect((screen.getByLabelText('admin:servers.native.github_install_command') as HTMLTextAreaElement).value).toBe(PUBLIC_NODE_INSTALL_COMMAND)
    expect((screen.getByLabelText('admin:servers.native.github_install_only_command') as HTMLTextAreaElement).value).toBe(PUBLIC_NODE_INSTALL_ONLY_COMMAND)
    expect(screen.queryByRole('combobox', { name: 'admin:servers.native.release_channel' })).toBeNull()
    expect(screen.queryByRole('combobox', { name: 'admin:servers.native.agent_version' })).toBeNull()
    expect(screen.queryByRole('combobox', { name: 'admin:servers.native.platform_label' })).toBeNull()
    expect(screen.queryByRole('combobox', { name: 'admin:servers.native.architecture' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'admin:servers.native.generate_files' })).toBeNull()
    expect(api.post).not.toHaveBeenCalled()
    expect((screen.getByLabelText('admin:servers.native.agent_id') as HTMLInputElement).value).toBe(provisioning.agent_id)
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(provisioning.credential)
  })
  it('copies the public command and each connection value separately without embedding the credential in the command', async () => {
    reads()
    mount(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod('github')
    expect(PUBLIC_NODE_INSTALL_COMMAND).not.toContain(provisioning.credential)
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.copy_github_command' }))
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.copy_github_install_only_command' }))
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.copy_endpoint' }))
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.copy_agent_id' }))
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.copy_credential' }))
    await waitFor(() => expect(copy.mock.calls.map(([value]) => value)).toEqual([
      PUBLIC_NODE_INSTALL_COMMAND,
      PUBLIC_NODE_INSTALL_ONLY_COMMAND,
      provisioning.endpoint,
      provisioning.agent_id,
      provisioning.credential,
    ]))
    expect(api.post).not.toHaveBeenCalled()
  })
  it('generates complete offline materials and exact public download links for the selected platform', async () => {
    reads()
    const materials = generatedManualFiles()
    api.post.mockResolvedValue({ data: materials })
    mountExpanded(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod('manual')
    await selectVersion('4.1.1')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }))
    await screen.findAllByLabelText('admin:servers.native.file_content')
    expect(api.post).toHaveBeenCalledWith('/admin/servers/7/node-installation-files', {
      version: '4.1.1', method: 'manual', os: 'linux', arch: 'amd64',
    }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
    for (const download of materials.downloads ?? []) {
      const matches = screen.getAllByRole('link', { name: 'admin:servers.native.download_release_file' })
      expect(matches.some(candidate => candidate.getAttribute('href') === download.url)).toBe(true)
    }
    expect((screen.getByLabelText('admin:servers.native.endpoint') as HTMLInputElement).value).toBe(provisioning.endpoint)
    expect((screen.getByLabelText('admin:servers.native.agent_id') as HTMLInputElement).value).toBe(provisioning.agent_id)
    expect((screen.getByLabelText('admin:servers.native.credential') as HTMLInputElement).value).toBe(provisioning.credential)
  })
  it('installs a product-scheme release, addressed by its tag and named by its version', async () => {
    const product: NodeReleaseCatalog = {
      checked_at: '2026-09-12T13:00:00Z',
      releases: [{
        version: '4.0.0', channel: 'stable', published_at: '2026-09-12T12:00:00Z', notes: 'Reviewed contract fixture',
        release_tag: 'release/4.0.0',
        release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/release/4.0.0',
        methods: ['linux', 'docker', 'manual'],
        platforms: (['linux', 'darwin', 'windows'] as const).flatMap(os =>
          (['amd64', 'arm64'] as const).map(arch => ({ os, arch }))),
      }],
    }
    releaseReads.mockResolvedValue(product)
    const materials = generatedManualFiles('4.0.0', 'release/4.0.0')
    api.post.mockResolvedValue({ data: materials })
    mountExpanded(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod('manual')
    await selectVersion('4.0.0', 'stable')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }))
    await screen.findAllByLabelText('admin:servers.native.file_content')
    expect(api.post).toHaveBeenCalledWith('/admin/servers/7/node-installation-files', {
      version: '4.0.0', method: 'manual', os: 'linux', arch: 'amd64',
    }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
    for (const download of materials.downloads ?? []) {
      const matches = screen.getAllByRole('link', { name: 'admin:servers.native.download_release_file' })
      expect(matches.some(candidate => candidate.getAttribute('href') === download.url)).toBe(true)
    }
  })
  it('rejects a product release whose download path is built from its version', async () => {
    const product: NodeReleaseCatalog = {
      checked_at: '2026-09-12T13:00:00Z',
      releases: [{
        version: '4.0.0', channel: 'stable', published_at: '2026-09-12T12:00:00Z', notes: 'Reviewed contract fixture',
        release_tag: 'release/4.0.0',
        release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/release/4.0.0',
        methods: ['linux', 'docker', 'manual'],
        platforms: (['linux', 'darwin', 'windows'] as const).flatMap(os =>
          (['amd64', 'arm64'] as const).map(arch => ({ os, arch }))),
      }],
    }
    releaseReads.mockResolvedValue(product)
    // Untagged path: what a URL built from the version looks like.
    api.post.mockResolvedValue({ data: generatedManualFiles('4.0.0', '4.0.0') })
    mountExpanded(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod('manual')
    await selectVersion('4.0.0', 'stable')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }))
    await screen.findByText('admin:servers.native.files_failed')
    expect(screen.queryByLabelText('admin:servers.native.file_content')).toBeNull()
  })
  it('invalidates displayed files after a release change instead of allowing obsolete files to be copied', async () => {
    reads()
    api.post.mockResolvedValue({ data: generatedFiles() })
    mountExpanded(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod('docker')
    await selectVersion('4.1.1')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }))
    await screen.findAllByLabelText('admin:servers.native.file_content')
    await selectVersion('4.1.2')
    expect(screen.queryByLabelText('admin:servers.native.file_content')).toBeNull()
    expect(screen.queryByRole('button', { name: 'admin:servers.native.copy_file' })).toBeNull()
    expect(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }).getAttribute('disabled')).toBeNull()
    expect(api.post).toHaveBeenCalledTimes(1)
    expect(copy).not.toHaveBeenCalled()
  })
  it('keeps a generation failure local and retryable without rendering or copying an error as a private file', async () => {
    reads()
    api.post.mockRejectedValue({ response: { status: 400, data: { error: 'Chosen release unavailable' } } })
    mountExpanded(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod('docker')
    await selectVersion('4.1.1')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }))
    await screen.findByText('Chosen release unavailable')
    expect(screen.queryByLabelText('admin:servers.native.file_content')).toBeNull()
    expect(screen.getByRole('button', { name: 'admin:servers.native.generate_files' }).getAttribute('disabled')).toBeNull()
    expect(copy).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog')).toBeTruthy()
  })
  it('rejects a successful Docker response with the wrong OS before exposing private files', async () => {
    reads()
    const materials = generatedFiles()
    materials.os = 'windows'
    api.post.mockResolvedValue({ data: materials })
    mountExpanded(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod('docker')
    await selectVersion('4.1.1')
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
    const materials = generatedFiles()
    const command = '  printf "%s\\n" "$NODE_AGENT_ID"\n'
    materials.steps[0].commands = [command, 'docker compose ps']
    api.post.mockResolvedValue({ data: materials })
    mountExpanded(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectMethod('docker')
    await selectVersion('4.1.1')
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
    const view = mountExpanded(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={onClose} onRotate={vi.fn()} />)
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
    mountExpanded(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectVersion('4.1.0')
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.download_script' }))
    await waitFor(() => expect(downloads).toEqual([{ file: 'passwall-node-install-agt_7.sh', url: 'blob:private-install-script' }]))
    expect(createURL).toHaveBeenCalledWith(expect.any(Blob))
    expect(revokeURL).toHaveBeenCalledWith('blob:private-install-script')
    expect(document.querySelector('a[download]')).toBeNull()
  })
  it('renders a plain-text API failure locally without downloading or copying it', async () => {
    reads()
    api.post.mockRejectedValue({ response: { status: 400, data: '{"error":"Release has no installation assets"}' } })
    mountExpanded(<NativeInstallationDialog server={nativeServer} initialProvisioning={provisioning} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectVersion('4.1.0')
    fireEvent.click(copyScript())
    await screen.findByText('Release has no installation assets')
    expect(copy).not.toHaveBeenCalled()
    expect(copyScript().disabled).toBe(false)
    expect(screen.getByRole('dialog')).toBeTruthy()
  })
})
