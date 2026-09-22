import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect } from 'vitest'
import type { NodeReleaseCatalog, NodeReleaseChannel } from '@/api/nodeReleases'
import type { NativeAgentStatus, NativeInstallationFiles, NativeServerProvisioning, Server } from '@/api/servers'
import { installReads, list, mount } from '@/test/adminSaveHarness'
import { releaseTag } from '@/utils/productVersion'

// THE SHARED FIXTURES AND HELPERS FOR THE PASSWALL NODE INSTALLATION FLOWS.
//
// ONE FILE OF 83 TESTS WAS THIS SUITE'S CRITICAL PATH BY ITSELF: vitest runs one worker
// per file, so the slowest file sets the wall clock however many cores are free, and
// measured locally that file was 24.3s of a 25.9s run. The tests are unchanged and still
// named the same; they are split by what they drive, and this is the part all four need.
//
// THE MOCKS ARE NOT HERE, deliberately: `vi.mock` is registered in the file that declares
// its hoisted handle, and vitest refuses to export a hoisted variable — so each test file
// declares the two mocks it needs, exactly as the single file did.

export const releaseCatalog: NodeReleaseCatalog = {
  checked_at: '2026-09-12T13:00:00Z',
  releases: ['4.1.0', '4.1.2', '4.1.1'].map(version => ({
    version,
    // A CANDIDATE IS A CHANNEL, NOT A HYPHEN. This used to read the version text,
    // which worked while a beta spelled itself; a product version does not, so the
    // fixture states the channel. The arrangement is the one the old fixture had:
    // the release is stable and the two above it are testing candidates.
    channel: version === '4.1.0' ? 'stable' : 'testing',
    published_at: '2026-09-12T12:00:00Z', notes: 'Reviewed contract fixture',
    release_url: `https://github.com/KazuhaHub/Passwall-Node/releases/tag/${releaseTag(version)}`,
    methods: ['linux', 'docker', 'manual'],
    platforms: (['linux', 'darwin', 'windows'] as const).flatMap(os =>
      (['amd64', 'arm64'] as const).map(arch => ({ os, arch }))),
  })),
}

export const nativeServer: Server = {
  id: 7, name: 'test-native', panel_type: 'psp', url: 'psp://agt_7', capabilities: [],
  auth_method: '', has_api_token: false, has_password: false, insecure_https: false,
}
export const provisioning: NativeServerProvisioning = {
  server: nativeServer, agent_id: 'agt_7', credential: 'existing-long-lived-secret', endpoint: 'https://panel.test/v1/node/sync',
}
export const waiting: NativeAgentStatus = { state: 'waiting', configured_nodes: 0 }

export function reads(status: NativeAgentStatus = waiting) {
  installReads({
    '/admin/servers': list([nativeServer]),
    '/admin/servers/7/node-installation': provisioning,
    '/admin/servers/7/node-agent-status': status,
  })
}

export function versionInput(): HTMLInputElement {
  return screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' }).parentElement!.querySelector('input')!
}

export async function selectVersion(version: string, publishedAs?: NodeReleaseChannel) {
  // THE CHANNEL COMES FROM THE CATALOG, not from the version text. A product
  // version does not spell one, and deriving it from a hyphen made every candidate
  // look stable — so the two testing releases were never selectable and every case
  // that names one failed to find the option. A case that serves its OWN catalog
  // says which channel it published the release to.
  let channel = publishedAs
  if (!channel) {
    const entry = releaseCatalog.releases.find(release => release.version === version)
    if (!entry) throw new Error(`the fixture publishes no ${version}`)
    channel = entry.channel
  }
  const channelSelect = await screen.findByRole('combobox', { name: 'admin:servers.native.release_channel' })
  fireEvent.mouseDown(channelSelect)
  fireEvent.click(await screen.findByRole('option', { name: `admin:servers.native.release_${channel}` }))
  const versionSelect = screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' })
  await waitFor(() => expect(versionSelect.getAttribute('aria-disabled')).not.toBe('true'))
  fireEvent.mouseDown(versionSelect)
  fireEvent.click(await screen.findByRole('option', { name: version }))
}

export function copyScript(): HTMLButtonElement {
  const advanced = screen.queryByRole('button', { name: 'admin:servers.native.advanced' })
  if (advanced?.getAttribute('aria-expanded') === 'false') fireEvent.click(advanced)
  return screen.getByRole('button', { name: 'admin:servers.native.copy_script' })
}

export async function showIdentity() {
  const advanced = await screen.findByRole('button', { name: 'admin:servers.native.advanced' })
  if (advanced.getAttribute('aria-expanded') === 'false') fireEvent.click(advanced)
  return screen.findByLabelText('admin:servers.native.credential')
}

export function mountExpanded(component: Parameters<typeof mount>[0]) {
  const view = mount(component)
  const advanced = screen.queryByRole('button', { name: 'admin:servers.native.advanced' })
  if (advanced?.getAttribute('aria-expanded') === 'false') fireEvent.click(advanced)
  return view
}

export async function selectMethod(method: 'linux' | 'github' | 'docker' | 'manual', container: HTMLElement = document.body) {
  fireEvent.mouseDown(within(container).getByRole('combobox', { name: 'admin:servers.native.method_label' }))
  fireEvent.click(await screen.findByRole('option', { name: `admin:servers.native.method.${method}` }))
}

export async function openInstallation(server: Server) {
  const row = (await screen.findByText(server.name)).closest('tr')!
  fireEvent.click(await within(row).findByRole('button', { name: 'admin:servers.action.more' }))
  fireEvent.click(await screen.findByRole('menuitem', { name: 'admin:servers.install_reinstall.action' }))
  fireEvent.click(await screen.findByRole('button', { name: 'admin:servers.install_reinstall.continue' }))
}

export function generatedFiles(): NativeInstallationFiles {
  return {
    method: 'docker', os: 'linux',
    files: [
      { name: 'node-credential.txt', destination: 'config/node-credential.txt', content: `${provisioning.credential}\n`, sensitive: true },
      { name: 'compose.yaml', destination: 'compose.yaml', content: 'services:\n  node:\n    image: ghcr.io/kazuhahub/passwall-node:4.1.1\n    network_mode: host\n' },
    ],
    steps: [
      { title: 'Private files', commands: ['chmod 0600 ./config/node-credential.txt'] },
      { title: 'Start the node', commands: ['docker compose up -d'] },
    ],
  }
}

// `tag` defaults to the TAG the version is published under, which is not the
// version: a release is addressed as release/4.1.1 and named 4.1.1. A case that
// wants the wrong shape passes it explicitly — that is what one of them checks.
export function generatedManualFiles(version = '4.1.1', tag = releaseTag(version)): NativeInstallationFiles {
  const asset = `passwall-node_${version}_linux_amd64.tar.gz`
  return {
    method: 'manual', os: 'linux', arch: 'amd64',
    files: [
      { name: 'node-credential.txt', content: `${provisioning.credential}\n`, sensitive: true },
      { name: 'node-config.json', content: JSON.stringify({ endpoint: provisioning.endpoint, agent_id: provisioning.agent_id, version }) },
    ],
    downloads: [
      { name: asset, url: `https://github.com/KazuhaHub/Passwall-Node/releases/download/${tag}/${asset}` },
      { name: 'SHA256SUMS.txt', url: `https://github.com/KazuhaHub/Passwall-Node/releases/download/${tag}/SHA256SUMS.txt` },
    ],
    steps: [{ id: 'download_verify', title: 'Verify transferred files', commands: ['sha256sum --check selected.sha256'] }],
  }
}

export function materialContents(): string[] {
  return screen.getAllByLabelText('admin:servers.native.file_content').map(input => (input as HTMLTextAreaElement).value)
}

export function materialPreviews(materials: NativeInstallationFiles): string[] {
  // Sensitive single-line password inputs remove CR/LF for display. Copy and
  // download below must still use the original file bytes, including its LF.
  return materials.files.map(file => file.sensitive ? file.content.replace(/[\r\n]/g, '') : file.content)
}

