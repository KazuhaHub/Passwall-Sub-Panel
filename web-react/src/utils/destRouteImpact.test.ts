import { AxiosError } from 'axios'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const confirm = vi.hoisted(() => vi.fn())
const http = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('@/components/ConfirmHost', () => ({ confirm }))
vi.mock('@/api/client', () => ({ client: http }))
vi.mock('@/i18n', () => ({ default: { t: (key: string, o?: { nodes?: string }) => `${key}|${o?.nodes ?? ''}` } }))

import { editDestListEntries, updateDestList } from '@/api/accessControl'
import { confirmingRouteImpact, isRouteImpactCancelled } from './destRouteImpact'

const impact = () => new AxiosError('conflict', '409', undefined, undefined, {
  status: 409, statusText: '', headers: {}, config: {} as never,
  data: { error: 'impact', code: 'dest_list_route_impact', used_by: [{ kind: 'route', id: 4, name: 'hk-entry' }, { kind: 'route', id: 5, name: '' }] },
})
const written = { list: { id: 1, name: 'x', used_by: [] }, report: null }

beforeEach(() => { vi.clearAllMocks() })

describe('route impact confirmation', () => {
  it('names the nodes, and on yes sends the same write again with the go-ahead', async () => {
    http.put.mockRejectedValueOnce(impact()).mockResolvedValueOnce({ data: written })
    confirm.mockResolvedValue(true)
    const out = await confirmingRouteImpact((...c) => updateDestList(1, { kind: 'custom', text: 'a' }, ...c))
    expect(out.list.id).toBe(1)
    expect(confirm.mock.calls[0][0].message).toBe('admin:access_control.confirm.route_impact.message|hk-entry, #5')
    expect(http.put).toHaveBeenCalledTimes(2)
    expect(http.put.mock.calls[0][2].params).toBeUndefined()
    expect(http.put.mock.calls[1][2].params).toEqual({ confirm_routes: 'true' })
  })

  it('changes nothing when declined', async () => {
    http.post.mockRejectedValueOnce(impact())
    confirm.mockResolvedValue(false)
    const err = await confirmingRouteImpact((...c) => editDestListEntries(1, { remove: ['domain:a.com'] }, ...c)).catch(e => e)
    expect(isRouteImpactCancelled(err)).toBe(true)
    expect(http.post).toHaveBeenCalledTimes(1)
  })

  it('does not ask for any other refusal, and does not ask when the write goes through', async () => {
    const other = new AxiosError('x', '409', undefined, undefined, { status: 409, statusText: '', headers: {}, config: {} as never, data: { code: 'dest_list_stale' } })
    http.put.mockRejectedValueOnce(other)
    await expect(confirmingRouteImpact((...c) => updateDestList(1, { kind: 'custom' }, ...c))).rejects.toBe(other)
    http.put.mockResolvedValueOnce({ data: written })
    await confirmingRouteImpact((...c) => updateDestList(1, { kind: 'custom' }, ...c))
    expect(confirm).not.toHaveBeenCalled()
  })
})
