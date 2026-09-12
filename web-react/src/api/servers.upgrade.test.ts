import { beforeEach, expect, it, vi } from 'vitest'

const http = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('./client', () => ({ client: http }))
import { getNativeAgentUpgrade, requestNativeAgentUpgrade } from './servers'

beforeEach(() => { vi.clearAllMocks(); http.post.mockResolvedValue({ data: { task_id: 'task-test' } }); http.get.mockResolvedValue({ data: { upgrade_state: 'queued' } }) })

it('requests an exact release with a stable idempotency header and no secret URL', async () => {
  const signal = new AbortController().signal
  await expect(requestNativeAgentUpgrade(7, 'v0.0.1-beta3', 'v0.0.1-beta2', 'same-request-identity', signal)).resolves.toEqual({ task_id: 'task-test' })
  expect(http.post).toHaveBeenCalledWith('/admin/servers/7/upgrade-node-agent', { version: 'v0.0.1-beta3', expected_version: 'v0.0.1-beta2' },
    { headers: { 'Idempotency-Key': 'same-request-identity' }, signal, _skipErrorToast: true })
})

it('queries only the original task under its own server and supports cancellation', async () => {
  const signal = new AbortController().signal
  await expect(getNativeAgentUpgrade(7, 'task:test', signal)).resolves.toEqual({ upgrade_state: 'queued' })
  expect(http.get).toHaveBeenCalledWith('/admin/servers/7/node-agent-upgrades/task%3Atest', { signal, _skipErrorToast: true })
  expect(http.post).not.toHaveBeenCalled()
})
