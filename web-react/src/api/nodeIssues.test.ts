import { beforeEach, describe, expect, it, vi } from 'vitest'

const http = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('./client', () => ({ client: http }))

import { acknowledgeNodeIssue, listNodeIssues } from './nodeIssues'

beforeEach(() => vi.resetAllMocks())

describe('Node issues API', () => {
  it('keeps ordinary single-record review requests unchanged', async () => {
    http.post.mockResolvedValueOnce({ status: 204 })
    await expect(acknowledgeNodeIssue(407)).resolves.toBeUndefined()
    expect(http.post).toHaveBeenCalledExactlyOnceWith('/admin/node-issues/407/acknowledge')
    expect(http.get).not.toHaveBeenCalled()
  })

  it.each([{}, { quiet: false }])('keeps non-quiet options on the ordinary request path: %j', async (options) => {
    http.post.mockResolvedValueOnce({ status: 204 })
    await acknowledgeNodeIssue(17, options)
    expect(http.post).toHaveBeenCalledExactlyOnceWith('/admin/node-issues/17/acknowledge')
  })

  it('uses the same per-record endpoint without a body for quiet bulk review', async () => {
    http.post.mockResolvedValueOnce({ status: 204 })
    await expect(acknowledgeNodeIssue(406, { quiet: true })).resolves.toBeUndefined()
    expect(http.post).toHaveBeenCalledExactlyOnceWith('/admin/node-issues/406/acknowledge', undefined, { _skipErrorToast: true })
    expect(http.get).not.toHaveBeenCalled()
  })

  it.each([undefined, { quiet: true }])('propagates per-record failures to the caller: %j', async (options) => {
    const error = new Error('review failed')
    http.post.mockRejectedValueOnce(error)
    await expect(acknowledgeNodeIssue(405, options)).rejects.toBe(error)
    expect(http.post).toHaveBeenCalledTimes(1)
  })

  it('preserves all list filters and the original cancellation signal', async () => {
    const params = {
      page: 2, page_size: 50, keyword: 'Canada %_!',
      agent_id: 'agt-test', code: 'object_pending_timeout', acknowledged: false,
    }
    const signal = new AbortController().signal
    const result = { items: [], total: 52 }
    http.get.mockResolvedValueOnce({ data: result })
    await expect(listNodeIssues(params, signal)).resolves.toBe(result)
    expect(http.get).toHaveBeenCalledExactlyOnceWith('/admin/node-issues', { params, signal })
    expect(http.post).not.toHaveBeenCalled()
  })

  it('keeps an omitted list query empty and propagates lookup failures', async () => {
    const error = new Error('lookup failed')
    http.get.mockRejectedValueOnce(error)
    await expect(listNodeIssues()).rejects.toBe(error)
    expect(http.get).toHaveBeenCalledExactlyOnceWith('/admin/node-issues', { params: {}, signal: undefined })
  })
})
