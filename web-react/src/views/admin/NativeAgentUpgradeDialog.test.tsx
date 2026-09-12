// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { expect, it } from 'vitest'
import { api, mount } from '@/test/adminSaveHarness'
import { NativeAgentUpgradeDialog } from './NativeAgentUpgradeDialog'
import type { NativeAgentUpgrade, Server } from '@/api/servers'

const server: Server = { id: 7, name: 'native', panel_type: 'psp', url: 'psp://agt_7', panel_version: 'v0.0.1-beta2 (abcdef0)', capabilities: [], auth_method: '', has_api_token: false, has_password: false, insecure_https: false }
const queued: NativeAgentUpgrade = { task_id: 'upgrade-test', agent_id: 'agt_7', version: 'v0.0.1-beta3', expected_version: 'v0.0.1-beta2', status: 'queued', upgrade_state: 'queued', not_after_ms: 10000, dispatch_closed: false }

it('requires explicit confirmation and displays queued rather than a success toast', async () => {
  api.post.mockResolvedValue({ data: queued })
  api.get.mockResolvedValue({ data: queued })
  mount(<NativeAgentUpgradeDialog server={server} onClose={() => {}} />)
  expect((screen.getByLabelText('admin:servers.agent_upgrade.current') as HTMLInputElement).value).toBe('v0.0.1-beta2')
  const button = screen.getByRole('button', { name: 'admin:servers.agent_upgrade.confirm' }) as HTMLButtonElement
  expect(button.disabled).toBe(true)
  fireEvent.change(screen.getByLabelText('admin:servers.agent_upgrade.target'), { target: { value: 'latest' } })
  expect(button.disabled).toBe(true)
  fireEvent.change(screen.getByLabelText('admin:servers.agent_upgrade.target'), { target: { value: queued.version } })
  expect(api.post).not.toHaveBeenCalled()
  fireEvent.click(button)
  await screen.findByText('admin:servers.agent_upgrade.state.queued')
  expect(screen.queryByText('admin:servers.agent_upgrade.state.verified')).toBeNull()
  expect(api.post).toHaveBeenCalledWith('/admin/servers/7/upgrade-node-agent', { version: queued.version, expected_version: queued.expected_version }, expect.objectContaining({ headers: { 'Idempotency-Key': expect.any(String) }, signal: expect.any(AbortSignal) }))
})

it('retries an uncertain POST with exactly the same idempotency key', async () => {
  api.post.mockRejectedValueOnce(new Error('lost response')).mockResolvedValueOnce({ data: queued })
  api.get.mockResolvedValue({ data: queued })
  mount(<NativeAgentUpgradeDialog server={server} onClose={() => {}} />)
  fireEvent.change(screen.getByLabelText('admin:servers.agent_upgrade.target'), { target: { value: queued.version } })
  fireEvent.click(screen.getByRole('button', { name: 'admin:servers.agent_upgrade.confirm' }))
  await screen.findByText('admin:servers.agent_upgrade.request_failed')
  fireEvent.click(screen.getByRole('button', { name: 'admin:servers.agent_upgrade.retry' }))
  await screen.findByText('admin:servers.agent_upgrade.state.queued')
  const keys = api.post.mock.calls.map(call => call[2].headers['Idempotency-Key'])
  expect(keys).toHaveLength(2); expect(keys[0]).toBe(keys[1])
})

it('requires the verified status and checksum returned by the original task lookup', async () => {
  api.post.mockResolvedValue({ data: queued })
  api.get.mockResolvedValue({ data: { ...queued, status: 'succeeded', upgrade_state: 'verified', binary_sha256: 'a'.repeat(64) } })
  mount(<NativeAgentUpgradeDialog server={server} onClose={() => {}} />)
  fireEvent.change(screen.getByLabelText('admin:servers.agent_upgrade.target'), { target: { value: queued.version } })
  fireEvent.click(screen.getByRole('button', { name: 'admin:servers.agent_upgrade.confirm' }))
  await waitFor(() => expect(screen.getByText('admin:servers.agent_upgrade.state.verified')).toBeTruthy(), { timeout: 3000 })
  expect(screen.getByText(`SHA-256: ${'a'.repeat(64)}`)).toBeTruthy()
  expect(api.get).toHaveBeenCalledWith('/admin/servers/7/node-agent-upgrades/upgrade-test', expect.objectContaining({ signal: expect.any(AbortSignal) }))
  expect(api.post).toHaveBeenCalledTimes(1)
})
