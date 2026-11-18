import { AxiosError } from 'axios'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const http = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('./client', () => ({ client: http }))

import * as api from './routes'

beforeEach(() => vi.clearAllMocks())

const input: api.RouteInput = { target: { kind: 'local' }, list_ids: [1], domains: [], cidrs: [], ports: '', network: '' }

describe('routes api', () => {
  it('reads the routes of one node quietly and treats null lists as empty', async () => {
    http.get.mockResolvedValue({ data: { supported: true, routes: [{ id: 'r1', target: { kind: 'local' }, list_ids: null, domains: null, cidrs: null, notes: null }], warnings: null, default_exit: { kind: 'local' }, sent: false, running: false } })
    const view = await api.getRoutes(7)
    expect(http.get).toHaveBeenCalledWith('/admin/nodes/7/routes', expect.objectContaining({ _skipErrorToast: true }))
    expect(view.warnings).toEqual([])
    expect(view.routes[0]).toMatchObject({ list_ids: [], domains: [], cidrs: [], notes: [], ports: '', network: '' })
    http.get.mockResolvedValue({ data: { supported: false, reason: 'no_agent', routes: null, default_exit: { kind: 'local' } } })
    expect((await api.getRoutes(7)).routes).toEqual([])
  })

  it('saves the whole list with PUT and checks with a POST that writes nothing', async () => {
    http.put.mockResolvedValue({ data: { supported: true, routes: [], warnings: [] } })
    await api.saveRoutes(7, [input])
    expect(http.put).toHaveBeenCalledWith('/admin/nodes/7/routes', { routes: [input] }, expect.objectContaining({ _skipErrorToast: true }))
    http.post.mockResolvedValue({ data: { supported: true, routes: [], warnings: [] } })
    const signal = new AbortController().signal
    await api.checkRoutes(7, [input], { signal })
    expect(http.post).toHaveBeenCalledWith('/admin/nodes/7/routes/check', { routes: [input] }, expect.objectContaining({ signal, _skipErrorToast: true }))
    expect(http.put).toHaveBeenCalledTimes(1)
  })

  it('reads a refusal into its code and parameters', () => {
    const err = new AxiosError('bad', '400', undefined, undefined, {
      status: 400, statusText: '', headers: {}, config: {} as never,
      data: { error: 'regexp not allowed', code: 'regexp_in_route', params: { route: 'r2', entry: 'regexp:a' } },
    })
    expect(api.routeErrorOf(err)).toEqual({ status: 400, code: 'regexp_in_route', params: { route: 'r2', entry: 'regexp:a' }, message: 'regexp not allowed' })
    expect(api.routeErrorOf(new Error('x'))).toBeNull()
  })
})
