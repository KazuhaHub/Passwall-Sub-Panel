import { beforeEach, expect, it, vi } from 'vitest'

const http = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('./client', () => ({ client: http }))
import { getNodeMigrationPreview } from './servers'

beforeEach(() => vi.clearAllMocks())

it('requests only abortable read-only migration metadata and returns the preview directly', async () => {
  const signal = new AbortController().signal
  const preview = { server_id: 7, fingerprint: 'a'.repeat(64), can_migrate: false, blockers: [], warnings: [] }
  http.get.mockResolvedValueOnce({ data: preview })
  await expect(getNodeMigrationPreview(7, signal)).resolves.toBe(preview)
  expect(http.get).toHaveBeenCalledExactlyOnceWith('/admin/servers/7/node-migration-preview', { params: {}, signal, _skipErrorToast: true })
  expect(http.post).not.toHaveBeenCalled()
})

it('includes only explicit core-selection and compatibility-acknowledgement query options', async () => {
  http.get.mockResolvedValueOnce({ data: {} })
  const signal = new AbortController().signal
  await getNodeMigrationPreview(7, signal, { core_version: '26.7.28', allow_restricted_reality: true })
  expect(http.get).toHaveBeenCalledWith('/admin/servers/7/node-migration-preview', {
    params: { core_version: '26.7.28', allow_restricted_reality: true }, signal, _skipErrorToast: true,
  })
  expect(http.post).not.toHaveBeenCalled()
})
