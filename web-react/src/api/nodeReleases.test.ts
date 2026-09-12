import { beforeEach, describe, expect, it, vi } from 'vitest'

const http = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('./client', () => ({ client: http }))

import { listNodeReleases } from './nodeReleases'

beforeEach(() => vi.clearAllMocks())

describe('Passwall Node release catalog API', () => {
  it('fetches only non-secret release metadata with cancellation and local error handling', async () => {
    const catalog = { releases: [], checked_at: '2026-09-12T12:36:16Z' }
    const signal = new AbortController().signal
    http.get.mockResolvedValueOnce({ data: catalog })
    await expect(listNodeReleases(signal)).resolves.toBe(catalog)
    expect(http.get).toHaveBeenCalledExactlyOnceWith('/admin/servers/node-releases', { signal, _skipErrorToast: true })
    expect(http.post).not.toHaveBeenCalled()
  })

  it('does not turn a failed lookup into a successful empty catalog', async () => {
    const error = new Error('GitHub unavailable')
    http.get.mockRejectedValueOnce(error)
    await expect(listNodeReleases()).rejects.toBe(error)
  })
})
