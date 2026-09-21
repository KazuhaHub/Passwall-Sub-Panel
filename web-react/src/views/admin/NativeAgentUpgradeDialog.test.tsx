// @vitest-environment jsdom
import { useState } from 'react'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, expect, it } from 'vitest'
import { api, installReads, mount } from '@/test/adminSaveHarness'
import { NativeAgentUpgradeDialog } from './NativeAgentUpgradeDialog'
import type { NativeAgentUpgrade, Server } from '@/api/servers'
import { releaseTag } from '@/utils/productVersion'

const server: Server = { id: 7, name: 'native', panel_type: 'psp', url: 'psp://agt_7', panel_version: '4.0.1 (abcdef0)', capabilities: [], auth_method: '', has_api_token: false, has_password: false, insecure_https: false }
const queued: NativeAgentUpgrade = { task_id: 'upgrade-test', agent_id: 'agt_7', version: '4.0.2', expected_version: '4.0.1', status: 'queued', upgrade_state: 'queued', not_after_ms: 10000, dispatch_closed: false }
const catalog = { releases: [{ version: queued.version, channel: 'testing', published_at: '2026-09-12T12:00:00Z',
  release_url: `https://github.com/KazuhaHub/Passwall-Node/releases/tag/${releaseTag(queued.version)}`, notes: 'Reviewed release fixture',
  methods: ['linux'], platforms: [{ os: 'linux', arch: 'amd64' }, { os: 'linux', arch: 'arm64' }] }], checked_at: '' }
beforeEach(() => installReads({ '/admin/servers/node-releases': catalog,
  '/admin/servers/7/node-agent-upgrades/upgrade-test': queued }))

it('preselects the newest reviewed release on the upgrade page', async () => {
  mount(<NativeAgentUpgradeDialog server={{ ...server, update_channel: 'beta' }} onClose={() => {}} />)
  const field = screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' })
  await waitFor(() => expect(field.textContent).toContain(queued.version))
  expect((screen.getByRole('button', { name: 'admin:servers.agent_upgrade.confirm' }) as HTMLButtonElement).disabled).toBe(false)
  expect(api.post).not.toHaveBeenCalled()
})

async function selectRelease(savedBeta = false) {
  if (!savedBeta) {
    // The empty-state text is used as a SYNCHRONISATION point — wait until the
    // catalog has loaded and rendered nothing — not as a claim about the
    // wording. The key changed because this dialog now asks the upgrade
    // question, whose empty answer is "nothing applies to this node" rather
    // than "this channel has no releases"; the wait means the same thing.
    await screen.findByText('admin:servers.native.release_no_target_for_node')
    fireEvent.mouseDown(screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }))
    fireEvent.click(await screen.findByRole('option', { name: 'admin:servers.native.release_testing' }))
  }
  const field = screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' })
  await waitFor(() => expect(field.getAttribute('aria-disabled')).not.toBe('true'))
  fireEvent.mouseDown(field)
  fireEvent.click(await screen.findByRole('option', { name: queued.version }))
}

it('requires explicit confirmation and displays queued rather than a success toast', async () => {
  api.post.mockResolvedValue({ data: queued })
  mount(<NativeAgentUpgradeDialog server={server} onClose={() => {}} />)
  expect((screen.getByLabelText('admin:servers.agent_upgrade.current') as HTMLInputElement).value).toBe('4.0.1')
  const button = screen.getByRole('button', { name: 'admin:servers.agent_upgrade.confirm' }) as HTMLButtonElement
  expect(button.disabled).toBe(true)
  await screen.findByText('admin:servers.native.release_no_target_for_node')
  expect(button.disabled).toBe(true)
  await selectRelease()
  expect(api.post).not.toHaveBeenCalled()
  fireEvent.click(button)
  await screen.findByText('admin:servers.agent_upgrade.state.queued')
  expect(screen.queryByText('admin:servers.agent_upgrade.state.verified')).toBeNull()
  expect(api.post).toHaveBeenCalledWith('/admin/servers/7/upgrade-node-agent', { version: queued.version, expected_version: queued.expected_version }, expect.objectContaining({ headers: { 'Idempotency-Key': expect.any(String) }, signal: expect.any(AbortSignal) }))
})

it('retries an uncertain POST with exactly the same idempotency key', async () => {
  api.post.mockRejectedValueOnce(new Error('lost response')).mockResolvedValueOnce({ data: queued })
  mount(<NativeAgentUpgradeDialog server={server} onClose={() => {}} />)
  await selectRelease()
  fireEvent.click(screen.getByRole('button', { name: 'admin:servers.agent_upgrade.confirm' }))
  await screen.findByText('admin:servers.agent_upgrade.request_failed')
  fireEvent.click(screen.getByRole('button', { name: 'admin:servers.agent_upgrade.retry' }))
  await screen.findByText('admin:servers.agent_upgrade.state.queued')
  const keys = api.post.mock.calls.map(call => call[2].headers['Idempotency-Key'])
  expect(keys).toHaveLength(2); expect(keys[0]).toBe(keys[1])
})

it('requires the verified status and checksum returned by the original task lookup', async () => {
  api.post.mockResolvedValue({ data: queued })
  installReads({ '/admin/servers/node-releases': catalog, '/admin/servers/7/node-agent-upgrades/upgrade-test':
    { ...queued, status: 'succeeded', upgrade_state: 'verified', binary_sha256: 'a'.repeat(64) } })
  mount(<NativeAgentUpgradeDialog server={server} onClose={() => {}} />)
  await selectRelease()
  fireEvent.click(screen.getByRole('button', { name: 'admin:servers.agent_upgrade.confirm' }))
  await waitFor(() => expect(screen.getByText('admin:servers.agent_upgrade.state.verified')).toBeTruthy(), { timeout: 3000 })
  expect(screen.getByText(`SHA-256: ${'a'.repeat(64)}`)).toBeTruthy()
  expect(api.get).toHaveBeenCalledWith('/admin/servers/7/node-agent-upgrades/upgrade-test', expect.objectContaining({ signal: expect.any(AbortSignal) }))
  expect(api.post).toHaveBeenCalledTimes(1)
})

it('uses the saved beta preference but still requires an exact reviewed version and confirmation; temporary changes never save it', async () => {
  const saved = { ...server, update_channel: 'beta' as const }
  function Parent() {
    const [current, setCurrent] = useState<Server | null>(saved)
    return <><NativeAgentUpgradeDialog server={current} onClose={() => setCurrent(null)} />
      <button onClick={() => setCurrent(current ? { ...current } : saved)}>refresh upgrade parent</button></>
  }
  mount(<Parent />)
  expect(screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }).textContent).toBe('admin:servers.native.release_testing')
  const button = screen.getByRole('button', { name: 'admin:servers.agent_upgrade.confirm' }) as HTMLButtonElement
  expect(button.disabled).toBe(true)
  await selectRelease(true)
  expect(button.disabled).toBe(false)
  fireEvent.mouseDown(screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }))
  fireEvent.click(await screen.findByRole('option', { name: 'admin:servers.native.release_stable' }))
  expect(button.disabled).toBe(true)
  fireEvent.click(screen.getByText('refresh upgrade parent'))
  expect(screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }).textContent).toBe('admin:servers.native.release_stable')
  expect(api.post).not.toHaveBeenCalled()
  expect(api.put).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.close' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  fireEvent.click(screen.getByText('refresh upgrade parent'))
  expect(screen.getByRole('combobox', { name: 'admin:servers.native.release_channel' }).textContent).toBe('admin:servers.native.release_testing')
  expect((screen.getByRole('button', { name: 'admin:servers.agent_upgrade.confirm' }) as HTMLButtonElement).disabled).toBe(true)
})

// ONE PREDICATE, NOT TWO. The list used to exclude a target that no VERIFIED EDGE
// reached, because a request along an unwalked path was refused — so a node on a
// version no edge started from had an empty dialog, and the remedy was a policy
// THE INSTANCE'S ANSWER IS THE WHOLE ANSWER.
//
// This list used to be filtered twice on the way in — by whether a verified edge
// reached the release, then by whether a signed policy offered it — and each
// filter could empty the dialog for a node the operator was looking at, with the
// remedy a document they had no reason to know about. Nothing is filtered here
// now: the panel reports the releases it can see are published, and an operator
// choosing among them is making the decision those gates were making for them.
it('offers exactly the targets the instance reports', async () => {
  const further = { ...catalog.releases[0], version: '4.0.6',
    release_url: `https://github.com/KazuhaHub/Passwall-Node/releases/tag/${releaseTag('4.0.6')}` }
  const unreported = { ...catalog.releases[0], version: '4.0.7',
    release_url: `https://github.com/KazuhaHub/Passwall-Node/releases/tag/${releaseTag('4.0.7')}` }
  installReads({
    '/admin/servers/node-releases': { ...catalog, releases: [catalog.releases[0], further, unreported] },
    '/admin/servers/7/node-agent-upgrades/upgrade-test': queued,
    '/admin/servers/7/upgrade-options': {
      component: 'agent', state: 'ready', target_pinnable: true, reason_codes: ['compatible'],
      targets: [{ version: queued.version }, { version: further.version }],
    },
  })
  mount(<NativeAgentUpgradeDialog server={{ ...server, update_channel: 'beta' }} onClose={() => {}} />)

  const field = screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' })
  await waitFor(() => expect(field.getAttribute('aria-disabled')).not.toBe('true'))
  fireEvent.mouseDown(field)
  expect(await screen.findByRole('option', { name: queued.version })).toBeTruthy()
  expect(await screen.findByRole('option', { name: '4.0.6' })).toBeTruthy()
  // IN THE CATALOG BUT NOT IN THE ANSWER, so it is not offered — the instance's
  // list is the restriction, and it is the panel's own statement rather than a
  // second document's opinion.
  expect(screen.queryByRole('option', { name: '4.0.7' })).toBeNull()
})

// A control-plane blip must not remove an action the operator was using, and the
// write path still protects the fire — so a failed read falls back to the weaker
// "strictly newer" filter rather than emptying the list.
it('falls back to offering what is newer when the instance cannot answer', async () => {
  const further = { ...catalog.releases[0], version: '4.0.6',
    release_url: `https://github.com/KazuhaHub/Passwall-Node/releases/tag/${releaseTag('4.0.6')}` }
  installReads({
    '/admin/servers/node-releases': { ...catalog, releases: [catalog.releases[0], further] },
    '/admin/servers/7/node-agent-upgrades/upgrade-test': queued,
  })
  mount(<NativeAgentUpgradeDialog server={{ ...server, update_channel: 'beta' }} onClose={() => {}} />)

  const field = screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' })
  await waitFor(() => expect(field.getAttribute('aria-disabled')).not.toBe('true'))
  fireEvent.mouseDown(field)
  expect(await screen.findByRole('option', { name: '4.0.6' })).toBeTruthy()
})

// A NODE THAT REPORTS A PRODUCT-SCHEME VERSION.
//
// `panel_version` is a VERSION — the product scheme stamps three integers with no
// prefix — and this dialog required a v prefix on it before it would enable
// Confirm. So the operator could open the upgrade page, see the node's version,
// pick a target, and find the action permanently disabled with nothing said
// about why. `exact()` is used three times: to gate submit, to gate the button,
// and to decide whether the node's own version can be passed as `newerThan`.
it('upgrades a node that reports a product-scheme version', async () => {
  const modernServer: Server = { ...server, panel_version: '4.0.0 (dc5270c)' }
  installReads({
    '/admin/servers/node-releases': {
      checked_at: '',
      releases: [{
        version: '4.0.1', channel: 'stable', published_at: '2026-09-12T12:00:00Z',
        release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/release/4.0.1',
        notes: 'Reviewed release fixture', methods: ['linux'],
        platforms: [{ os: 'linux', arch: 'amd64' }, { os: 'linux', arch: 'arm64' }],
      }],
    },
  })
  mount(<NativeAgentUpgradeDialog server={modernServer} onClose={() => {}} />)
  expect((screen.getByLabelText('admin:servers.agent_upgrade.current') as HTMLInputElement).value).toBe('4.0.0')
  const button = screen.getByRole('button', { name: 'admin:servers.agent_upgrade.confirm' }) as HTMLButtonElement
  expect(button.disabled).toBe(true)
  const field = screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' })
  await waitFor(() => expect(field.getAttribute('aria-disabled')).not.toBe('true'))
  fireEvent.mouseDown(field)
  fireEvent.click(await screen.findByRole('option', { name: '4.0.1' }))
  await waitFor(() => expect(button.disabled).toBe(false))
})

// A NODE ON THE REPLACED SCHEME IS STILL UPGRADABLE, AND THAT IS THE WHOLE POINT.
//
// The dialog used to require the node's OWN reported version to be a product
// version before it would enable Confirm. A node still reporting `v0.0.1-beta9`
// therefore could not be moved at all: the operator opened the page, picked a
// target, and found the action permanently disabled. But that version is the
// NODE'S OWN RECORD of itself — the node is what compares it against the request
// — so this panel has no business parsing it.
it('upgrades a node that reports a version from the replaced scheme', async () => {
  const legacyServer: Server = { ...server, panel_version: 'v0.0.1-beta9' }
  installReads({
    '/admin/servers/node-releases': {
      checked_at: '',
      releases: [{
        version: '4.0.1', channel: 'stable', published_at: '2026-09-12T12:00:00Z',
        release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/release/4.0.1',
        notes: 'Reviewed release fixture', methods: ['linux'],
        platforms: [{ os: 'linux', arch: 'amd64' }, { os: 'linux', arch: 'arm64' }],
      }],
    },
  })
  mount(<NativeAgentUpgradeDialog server={legacyServer} onClose={() => {}} />)
  expect((screen.getByLabelText('admin:servers.agent_upgrade.current') as HTMLInputElement).value).toBe('v0.0.1-beta9')
  const button = screen.getByRole('button', { name: 'admin:servers.agent_upgrade.confirm' }) as HTMLButtonElement
  expect(button.disabled).toBe(true)
  const field = screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' })
  await waitFor(() => expect(field.getAttribute('aria-disabled')).not.toBe('true'))
  fireEvent.mouseDown(field)
  fireEvent.click(await screen.findByRole('option', { name: '4.0.1' }))
  await waitFor(() => expect(button.disabled).toBe(false))
})

// THE NODE'S VERSION IS OPAQUE, SO THE PANEL DOES NOT JUDGE IT.
//
// This case used to be its opposite: a reported version that is not a release
// version disabled Confirm. That made the panel the arbiter of a string it does
// not own, and it is why a node from before the scheme change could not be
// upgraded. What still gates the action is the TARGET — it must name a release,
// because that is what the node fetches.
it('accepts whatever version the node reports about itself', async () => {
  for (const panel_version of ['v0.0.1-beta9', 'v3.9.2', 'v0.0.1-beta12 (abcdef0)', 'dev']) {
    installReads({
      '/admin/servers/node-releases': {
        checked_at: '',
        releases: [{
          version: '4.0.1', channel: 'stable', published_at: '2026-09-12T12:00:00Z',
          release_url: 'https://github.com/KazuhaHub/Passwall-Node/releases/tag/release/4.0.1',
          notes: 'Reviewed release fixture', methods: ['linux'],
          platforms: [{ os: 'linux', arch: 'amd64' }, { os: 'linux', arch: 'arm64' }],
        }],
      },
    })
    const view = mount(<NativeAgentUpgradeDialog server={{ ...server, panel_version }} onClose={() => {}} />)
    const field = screen.getByRole('combobox', { name: 'admin:servers.native.agent_version' })
    await waitFor(() => expect(field.getAttribute('aria-disabled')).not.toBe('true'))
    fireEvent.mouseDown(field)
    fireEvent.click(await screen.findByRole('option', { name: '4.0.1' }))
    const button = screen.getByRole('button', { name: 'admin:servers.agent_upgrade.confirm' }) as HTMLButtonElement
    await waitFor(() => expect(button.disabled, panel_version).toBe(false))
    view.unmount()
  }
})
