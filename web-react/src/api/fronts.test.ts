import { AxiosError } from 'axios'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const http = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('./client', () => ({ client: http }))

import * as api from './fronts'

beforeEach(() => vi.clearAllMocks())

describe('fronts api', () => {
  it('reads the fronts of one server quietly and treats null lists as empty', async () => {
    http.get.mockResolvedValue({ data: { support: { supported: true }, fronts: null, max: 16, defaults: {} } })
    const view = await api.getFronts(4)
    expect(http.get).toHaveBeenCalledWith('/admin/servers/4/fronts', expect.objectContaining({ _skipErrorToast: true }))
    expect(view.fronts).toEqual([])
    http.get.mockResolvedValue({ data: { ports: null } })
    expect(await api.getServerPorts(4)).toEqual([])
  })

  it('keeps the incoming tunnels among the target options and treats null lists as empty', async () => {
    const link = { link_id: 7, entry_panel_id: 2, port: 31443, server_name: 'tunnel.example' }
    http.get.mockResolvedValue({ data: { listeners: null, lines: null, links: [link] } })
    const options = await api.getFrontOptions(4)
    expect(http.get).toHaveBeenCalledWith('/admin/servers/4/fronts/options', expect.objectContaining({ _skipErrorToast: true }))
    expect(options).toEqual({ listeners: [], lines: [], links: [link] })
    http.get.mockResolvedValue({ data: { listeners: null, lines: null, links: null } })
    expect((await api.getFrontOptions(4)).links).toEqual([])
  })

  it('asks for suggestions as reads, never as writes', async () => {
    http.get.mockResolvedValue({ data: { port: 30012 } })
    expect(await api.suggestLoopbackPort(4)).toBe(30012)
    expect(http.get).toHaveBeenCalledWith('/admin/servers/4/fronts/suggest', expect.objectContaining({ params: { what: 'loopback' } }))
    http.get.mockResolvedValue({ data: { host: 'www.asus.com', port: 443 } })
    expect(await api.suggestCamouflage(4)).toEqual({ host: 'www.asus.com', port: 443 })
    http.get.mockResolvedValue({ data: { port: 41234 } })
    expect(await api.suggestPort(4)).toBe(41234)
    expect(http.get).toHaveBeenLastCalledWith('/admin/servers/4/fronts/suggest', expect.objectContaining({ params: { what: 'port' } }))
    expect(http.post).not.toHaveBeenCalled()
  })

  it('creates, updates and deletes through the server path', async () => {
    const input: api.FrontInput = { port: 443, listeners: [], routes: [], default: { host: 'a.example', port: 443 }, confirm_move_listeners: false }
    http.post.mockResolvedValue({ data: { front: { id: 9 }, warnings: [] } })
    await api.createFront(4, input)
    expect(http.post).toHaveBeenCalledWith('/admin/servers/4/fronts', input, expect.anything())
    http.put.mockResolvedValue({ data: { front: { id: 9 }, warnings: [] } })
    await api.updateFront(4, 9, input)
    expect(http.put).toHaveBeenCalledWith('/admin/servers/4/fronts/9', input, expect.anything())
    http.delete.mockResolvedValue({ data: {} })
    await api.deleteFront(4, 9)
    expect(http.delete).toHaveBeenCalledWith('/admin/servers/4/fronts/9', expect.anything())
  })

  it('reads a refusal into its code and parameters', () => {
    const err = new AxiosError('bad', '400', undefined, undefined, {
      status: 409, statusText: '', headers: {}, config: {} as never,
      data: { error: 'confirm', code: 'confirm_required', params: { port: 443 } },
    })
    expect(api.frontErrorOf(err)).toEqual({ status: 409, code: 'confirm_required', params: { port: 443 }, message: 'confirm' })
    expect(api.frontErrorOf(new Error('x'))).toBeNull()
  })

  it('splits typed server names on commas, spaces and new lines', () => {
    expect(api.parseServerNames('a.example.com, b.example.com\n c.example.com，')).toEqual(['a.example.com', 'b.example.com', 'c.example.com'])
    expect(api.parseServerNames('  ')).toEqual([])
  })
})
