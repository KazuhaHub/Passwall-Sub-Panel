// @vitest-environment jsdom
import { useState } from 'react'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, expect, it } from 'vitest'
import { api, installReads, mount } from '@/test/adminSaveHarness'
import { NativeAgentUpgradeDialog } from './NativeAgentUpgradeDialog'
import type { NativeAgentUpgrade, Server } from '@/api/servers'

const server: Server = { id: 7, name: 'native', panel_type: 'psp', url: 'psp://agt_7', panel_version: 'v0.0.1-beta2 (abcdef0)', capabilities: [], auth_method: '', has_api_token: false, has_password: false, insecure_https: false }
const queued: NativeAgentUpgrade = { task_id: 'upgrade-test', agent_id: 'agt_7', version: 'v0.0.1-beta3', expected_version: 'v0.0.1-beta2', status: 'queued', upgrade_state: 'queued', not_after_ms: 10000, dispatch_closed: false }
const catalog = { releases: [{ version: queued.version, channel: 'testing', published_at: '2026-09-12T12:00:00Z',
  release_url: `https://github.com/KazuhaHub/Passwall-Node/releases/tag/${queued.version}`, notes: 'Reviewed release fixture',
  methods: ['linux'], platforms: [{ os: 'linux', arch: 'amd64' }, { os: 'linux', arch: 'arm64' }] }], checked_at: '' }
beforeEach(() => installReads({ '/admin/servers/node-releases': catalog,
  '/admin/servers/7/node-agent-upgrades/upgrade-test': queued }))

async function selectRelease(savedBeta = false) {
  if (!savedBeta) {
    await screen.findByText('admin:servers.native.release_no_stable')
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
  expect((screen.getByLabelText('admin:servers.agent_upgrade.current') as HTMLInputElement).value).toBe('v0.0.1-beta2')
  const button = screen.getByRole('button', { name: 'admin:servers.agent_upgrade.confirm' }) as HTMLButtonElement
  expect(button.disabled).toBe(true)
  await screen.findByText('admin:servers.native.release_no_stable')
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
