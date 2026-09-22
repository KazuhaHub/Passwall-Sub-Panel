// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { releaseCatalog, nativeServer, provisioning, waiting, selectVersion, mountExpanded } from '@/test/installationHarness'
import { api } from '@/test/adminSaveHarness'
import { isNodeReleaseVersion, NativeInstallationDialog } from './ServersView'
import { hostFromURL } from './NodesView'

// WHAT THIS FILE DRIVES: the dialog's own inputs, and the two flows that replace
// what is already installed — an in-place upgrade and an identity replacement.

vi.mock('@/utils/clipboard', () => ({ copyToClipboard: vi.fn().mockResolvedValue(true) }))
const releaseReads = vi.hoisted(() => vi.fn())
vi.mock('@/api/nodeReleases', () => ({ listNodeReleases: releaseReads }))

beforeEach(() => releaseReads.mockResolvedValue(releaseCatalog))
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('native installation inputs', () => {
  it.each([
    '', 'latest', 'https://example.test/4.1.0',
    // `4.1.0` USED TO BE IN THIS LIST, and that was the defect rather than the
    // rule: it is exactly what this scheme stamps, so a released product version
    // left the install action disabled with nothing said about why. It is
    // asserted as accepted below, and the near misses stay here — a short form,
    // too many segments, a prerelease, a tag, a leading zero and a zero build.
    '4.1', '4.1.0.4.5', '04.1.0', '4.1.0-rc.1', 'release/4.1.0',
    '01.2.3', '4.1.0.0', '4.1.0-beta.01', '4.1.0.1.2',
  ])('rejects noncanonical release version %s', version => {
    expect(isNodeReleaseVersion(version)).toBe(false)
  })

  it.each([
    '1.0.0', '4.1.0', '4.1.1', '4.1.4',
    // The optional BUILD component, which is part of the identity.
    '4.1.0.4',
    // The forms this project publishes.
    '1.2.3', '4.0.0', '102.1.0',
  ])('accepts canonical release version %s', version => {
    expect(isNodeReleaseVersion(version)).toBe(true)
  })

  it('does not treat a PSP agent identity as a proxy hostname', () => {
    expect(hostFromURL('psp://agt_7')).toBe('')
    expect(hostFromURL('PSP://agt_7')).toBe('')
    expect(hostFromURL('https://proxy.example.test:8443/admin')).toBe('proxy.example.test')
    expect(hostFromURL('proxy.example.test:8443')).toBe('proxy.example.test')
  })
})

// REPLACING THE RELEASE IS THE OPERATOR'S DECISION, AND THE PANEL SAYS SO WHEN IT
// IS MISSING.
//
// A node on another release is the case an in-place upgrade exists for, and the
// installer refuses without being told — with a message about identity that names
// nothing about the version. The panel knows the node's reported version, so it can
// say what is about to happen before the operator runs anything, without deciding
// for them: the switch is off until they turn it on.
describe('Passwall Node in-place upgrade', () => {
  it('warns when the node reports another release and sends the mode only once asked', async () => {
    api.get.mockImplementation((url: string) => Promise.resolve(url.includes('node-installation')
      ? { data: provisioning }
      : { data: waiting }))
    api.post.mockResolvedValue({ data: { server_id: 7, command: 'curl -fsSL https://panel.test/private-once | sudo bash',
      expires_at: new Date(Date.now() + 15 * 60_000).toISOString() } })

    const installed = { ...nativeServer, panel_version: '4.0.0 (abcdef1)' }
    mountExpanded(<NativeInstallationDialog server={installed}
      initialProvisioning={{ ...provisioning, server: installed }} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectVersion('4.1.0')

    // The switch is off and the panel says what that means here.
    const toggle = screen.getByRole('checkbox', { name: 'admin:servers.native.upgrade_in_place' }) as HTMLInputElement
    expect(toggle.checked).toBe(false)
    expect(screen.getByText('admin:servers.native.upgrade_in_place_needed')).toBeTruthy()

    fireEvent.click(toggle)
    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_command' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/servers/7/node-install-command',
      { version: '4.1.0', mode: 'upgrade' }, expect.objectContaining({ signal: expect.any(AbortSignal) })))
  })

  it('says nothing about replacing a release when the node already reports the selected one', async () => {
    api.get.mockImplementation((url: string) => Promise.resolve(url.includes('node-installation')
      ? { data: provisioning }
      : { data: waiting }))
    const installed = { ...nativeServer, panel_version: '4.1.0 (abcdef1)' }
    mountExpanded(<NativeInstallationDialog server={installed}
      initialProvisioning={{ ...provisioning, server: installed }} onClose={vi.fn()} onRotate={vi.fn()} />)
    await selectVersion('4.1.0')
    expect(screen.queryByText('admin:servers.native.upgrade_in_place_needed')).toBeNull()
  })})

// TAKING THE HOST OVER IS THE OTHER ANSWER TO THE SAME QUESTION, and it is about the
// identity rather than the version: it exists for a machine that has a node this panel
// does not know — one another panel installed, or one left behind. It is never
// inferred, and what it costs is stated where it is chosen rather than afterwards.
describe('Passwall Node identity replacement', () => {
  function mountReplaceable(panelVersion: string) {
    api.get.mockImplementation((url: string) => Promise.resolve(url.includes('node-installation')
      ? { data: provisioning }
      : { data: waiting }))
    api.post.mockResolvedValue({ data: { server_id: 7, command: 'curl -fsSL https://panel.test/private-once | sudo bash',
      expires_at: new Date(Date.now() + 15 * 60_000).toISOString() } })
    const installed = { ...nativeServer, panel_version: panelVersion }
    mountExpanded(<NativeInstallationDialog server={installed}
      initialProvisioning={{ ...provisioning, server: installed }} onClose={vi.fn()} onRotate={vi.fn()} />)
  }

  it('sends the replace mode, with its consequences, and only once asked', async () => {
    mountReplaceable('4.0.0 (abcdef1)')
    await selectVersion('4.1.0')

    const toggle = screen.getByRole('checkbox', { name: 'admin:servers.native.replace_identity' }) as HTMLInputElement
    expect(toggle.checked).toBe(false)
    expect(screen.queryByText('admin:servers.native.replace_identity_warning')).toBeNull()

    fireEvent.click(toggle)
    // THE COST IS SAID WHERE THE CHOICE IS, and the warning about the reported version
    // is gone: this answer covers it, because the identity is not being kept.
    expect(screen.getByText('admin:servers.native.replace_identity_warning')).toBeTruthy()
    expect(screen.queryByText('admin:servers.native.upgrade_in_place_needed')).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: 'admin:servers.native.generate_command' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/servers/7/node-install-command',
      { version: '4.1.0', mode: 'replace' }, expect.objectContaining({ signal: expect.any(AbortSignal) })))
  })

  it('keeps the two answers to one question mutually exclusive', async () => {
    mountReplaceable('4.1.0 (abcdef1)')
    await selectVersion('4.1.0')
    const upgrade = screen.getByRole('checkbox', { name: 'admin:servers.native.upgrade_in_place' }) as HTMLInputElement
    const replace = screen.getByRole('checkbox', { name: 'admin:servers.native.replace_identity' }) as HTMLInputElement

    fireEvent.click(replace)
    expect(replace.checked).toBe(true)
    fireEvent.click(upgrade)
    expect(upgrade.checked).toBe(true)
    expect(replace.checked).toBe(false)
    expect(screen.queryByText('admin:servers.native.replace_identity_warning')).toBeNull()
  })
})
