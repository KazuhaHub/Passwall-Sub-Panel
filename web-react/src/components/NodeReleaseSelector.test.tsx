// @vitest-environment jsdom
import { useState } from 'react'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, installReads, mount } from '@/test/adminSaveHarness'
import type { NodeRelease, NodeReleaseCatalog, NodeReleaseChannel } from '@/api/nodeReleases'
import type { NativeInstallationSelection } from '@/api/servers'
import NodeReleaseSelector from './NodeReleaseSelector'

const endpoint = '/admin/servers/node-releases'
const linux: NativeInstallationSelection = { method: 'linux', os: 'linux', arch: 'amd64' }
const platforms: NodeRelease['platforms'] = [
  { os: 'linux', arch: 'amd64' }, { os: 'linux', arch: 'arm64' },
  { os: 'darwin', arch: 'amd64' }, { os: 'darwin', arch: 'arm64' },
  { os: 'windows', arch: 'amd64' }, { os: 'windows', arch: 'arm64' },
]
const stable: NodeRelease = {
  version: 'v1.2.3', channel: 'stable', published_at: '2026-09-12T12:36:16Z',
  release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/v1.2.3',
  notes: 'Reviewed protocol compatibility; install exactly this tag.', methods: ['linux', 'docker', 'manual'], platforms,
}
const testing: NodeRelease = {
  ...stable, version: 'v1.2.4-beta.1', channel: 'testing',
  release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/v1.2.4-beta.1',
}

function reads(releases: NodeRelease[]) {
  installReads({ [endpoint]: { releases, checked_at: '2026-09-12T13:00:00Z' } satisfies NodeReleaseCatalog })
}

function Controlled({ enabled = true, selection = linux, disabled = false, initialChannel = 'stable', compact = false }: {
  enabled?: boolean; selection?: NativeInstallationSelection; disabled?: boolean; initialChannel?: NodeReleaseChannel; compact?: boolean
}) {
  const [version, setVersion] = useState('')
  const [, refresh] = useState(0)
  return <>
    <NodeReleaseSelector enabled={enabled} selection={selection} value={version} onChange={setVersion} disabled={disabled} initialChannel={initialChannel} compact={compact} />
    <span data-testid="selected-version">{version}</span>
    <button onClick={() => refresh(value => value + 1)}>refresh selector parent</button>
  </>
}

function selected() { return screen.getByTestId('selected-version').textContent }

async function choose(label: string, option: string) {
  const field = screen.getByRole('combobox', { name: label })
  await waitFor(() => expect(field.getAttribute('aria-disabled')).not.toBe('true'))
  fireEvent.mouseDown(field)
  fireEvent.click(await screen.findByRole('option', { name: option }))
}

const chooseVersion = (version: string) => choose('admin:servers.native.agent_version', version)
const chooseTesting = () => choose('admin:servers.native.release_channel', 'admin:servers.native.release_testing')

describe('Passwall Node release selection', () => {
  it('folds publication notes behind an accessible summary only on the compact installation surface', async () => {
    reads([stable])
    mount(<Controlled compact />)
    await chooseVersion(stable.version)
    expect(screen.queryByText(stable.notes)).toBeNull()
    expect(screen.queryByRole('link', { name: 'admin:servers.native.release_details' })).toBeNull()
    const summary = screen.getByRole('button', { name: 'admin:servers.native.release_review' })
    expect(summary.tagName).toBe('BUTTON')
    expect(summary.getAttribute('aria-expanded')).toBe('false')
    expect(summary.getAttribute('tabindex')).not.toBe('-1')
    fireEvent.click(summary)
    expect(summary.getAttribute('aria-expanded')).toBe('true')
    expect(await screen.findByText(stable.notes)).toBeTruthy()
    expect(screen.getByRole('link', { name: 'admin:servers.native.release_details' }).getAttribute('href')).toBe(stable.release_url)
    expect(selected()).toBe(stable.version)
    expect(api.post).not.toHaveBeenCalled()
  })
  it('opens the saved testing preference without preselecting a version or making any secret/write request', async () => {
    reads([testing, stable])
    const view = mount(<Controlled initialChannel="testing" />)
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull())
    expect(screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }).textContent).toBe('admin:servers.native.release_testing')
    expect(selected()).toBe('')
    await choose('admin:servers.native.release_channel', 'admin:servers.native.release_stable')
    await chooseVersion(stable.version)
    fireEvent.click(screen.getByRole('button', { name: 'refresh selector parent' }))
    expect(selected()).toBe(stable.version)
    expect(screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }).textContent).toBe('admin:servers.native.release_stable')
    view.rerender(<Controlled enabled={false} initialChannel="testing" />)
    view.rerender(<Controlled initialChannel="testing" />)
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull())
    expect(selected()).toBe('')
    expect(screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }).textContent).toBe('admin:servers.native.release_testing')
    expect(api.get.mock.calls.every(([url]) => url === endpoint)).toBe(true)
    expect(api.post).not.toHaveBeenCalled()
    expect(api.put).not.toHaveBeenCalled()
  })

  it('starts stable and does not silently fall back when only testing releases exist', async () => {
    reads([testing])
    mount(<Controlled />)
    await screen.findByText('admin:servers.native.release_no_stable')
    expect(selected()).toBe('')
    expect(screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' }).getAttribute('aria-disabled')).toBe('true')
    await chooseTesting()
    expect(selected()).toBe('')
    await chooseVersion(testing.version)
    expect(selected()).toBe(testing.version)
    expect(api.get.mock.calls.every(([url]) => url === endpoint)).toBe(true)
    expect(api.post).not.toHaveBeenCalled()
  })

  it('requires an exact version choice and displays the official link, date, and plain-text compatibility notes', async () => {
    reads([{ ...stable, notes: '<script>alert("not HTML")</script>\nReviewed contract.' }])
    mount(<Controlled />)
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull())
    expect(selected()).toBe('')
    await chooseVersion(stable.version)
    expect(selected()).toBe(stable.version)
    expect(screen.getByText(/<script>alert/).querySelector('script')).toBeNull()
    const link = screen.getByRole('link', { name: 'admin:servers.native.release_details' })
    expect(link.getAttribute('href')).toBe(stable.release_url)
    expect(link.getAttribute('rel')).toBe('noopener noreferrer')
    expect(screen.getByText('admin:servers.native.release_published')).toBeTruthy()
  })

  it('clears the exact selection when changing channels and does not auto-select the other channel', async () => {
    reads([testing, stable])
    mount(<Controlled />)
    await chooseVersion(stable.version)
    await chooseTesting()
    expect(selected()).toBe('')
    await chooseVersion(testing.version)
    expect(selected()).toBe(testing.version)
  })

  it('clears a selected version on platform changes even when that version supports both platforms', async () => {
    reads([stable])
    const view = mount(<Controlled selection={{ ...linux, method: 'manual' }} />)
    await chooseVersion(stable.version)
    view.rerender(<Controlled selection={{ method: 'manual', os: 'windows', arch: 'arm64' }} />)
    await waitFor(() => expect(selected()).toBe(''))
    await chooseVersion(stable.version)
    expect(selected()).toBe(stable.version)
  })

  it('filters manual versions by exact OS and architecture', async () => {
    reads([{ ...stable, platforms: [{ os: 'linux', arch: 'amd64' }] }])
    mount(<Controlled selection={{ method: 'manual', os: 'windows', arch: 'arm64' }} />)
    await screen.findByText('admin:servers.native.release_no_stable')
    expect(selected()).toBe('')
  })

  it('requires the selected recipe and both Linux architectures for auto-detecting installations', async () => {
    reads([{ ...stable, platforms: [{ os: 'linux', arch: 'amd64' }] }])
    const view = mount(<Controlled />)
    await screen.findByText('admin:servers.native.release_no_stable')
    view.rerender(<Controlled selection={{ ...linux, method: 'docker' }} />)
    await screen.findByText('admin:servers.native.release_no_stable')
    expect(selected()).toBe('')
    reads([{ ...stable, methods: ['manual'] }])
    view.rerender(<Controlled enabled={false} />)
    view.rerender(<Controlled selection={{ ...linux, method: 'docker' }} />)
    await screen.findByText('admin:servers.native.release_no_stable')
  })

  it('distinguishes a lookup failure from an empty channel and lets the administrator retry', async () => {
    api.get.mockRejectedValue(new Error('Rate limit'))
    mount(<Controlled />)
    await screen.findByText('admin:servers.native.release_failed')
    expect(screen.queryByText('admin:servers.native.release_no_stable')).toBeNull()
    reads([stable])
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.release_retry' }))
    await chooseVersion(stable.version)
    expect(selected()).toBe(stable.version)
    expect(screen.queryByText('admin:servers.native.release_failed')).toBeNull()
  })

  it('aborts closed requests and ignores late responses after the selector is enabled again', async () => {
    const requests: { signal: AbortSignal; resolve: (value: { data: NodeReleaseCatalog }) => void }[] = []
    api.get.mockImplementation((_url: string, options: { signal: AbortSignal }) => new Promise(resolve => {
      requests.push({ signal: options.signal, resolve })
    }))
    const view = mount(<Controlled />)
    expect(screen.getByRole('status')).toBeTruthy()
    const oldRequests = [...requests]
    view.rerender(<Controlled enabled={false} />)
    expect(oldRequests.every(request => request.signal.aborted)).toBe(true)
    view.rerender(<Controlled />)
    const current = requests.filter(request => !request.signal.aborted).at(-1)!
    await act(async () => {
      current.resolve({ data: { releases: [testing], checked_at: '' } })
      oldRequests.forEach(request => request.resolve({ data: { releases: [stable], checked_at: '' } }))
    })
    await screen.findByText('admin:servers.native.release_no_stable')
    expect(selected()).toBe('')
    view.unmount()
    expect(current.signal.aborted).toBe(true)
  })

  it('does not fetch any metadata or secrets while disabled by lifecycle', async () => {
    mount(<Controlled enabled={false} />)
    expect(api.get).not.toHaveBeenCalled()
    expect(api.post).not.toHaveBeenCalled()
    expect(screen.queryByRole('combobox')).toBeNull()
  })

  it('does not expose versions with untrusted external release links', async () => {
    reads([{ ...stable, release_url: 'javascript:alert(1)' }])
    mount(<Controlled />)
    await screen.findByText('admin:servers.native.release_no_stable')
    expect(screen.queryByRole('link')).toBeNull()
    expect(selected()).toBe('')
  })

  it('prevents changing channel and version while an installation operation is disabled', async () => {
    reads([stable])
    mount(<Controlled disabled />)
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull())
    expect(screen.getAllByRole('combobox').every(field => field.getAttribute('aria-disabled') === 'true')).toBe(true)
    expect(selected()).toBe('')
  })
})
