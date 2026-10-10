import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getDestinationHits } from './destinationHits'
import { parseRecordsParams, recordsRequest } from '@/views/admin/accessControl/records/recordsParams'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('./client', () => ({ client: { get } }))

const now = Date.parse('2026-10-10T01:00:00Z')
function request() {
  const input = recordsRequest(parseRecordsParams(new URLSearchParams('rec_action=deny&rec_panel=7&rec_group_by=site'), 30), ' private%_host.test ', now)
  if (!input) throw new Error('valid filter fixture rejected')
  return input
}

describe('destination hits transport', () => {
  beforeEach(() => vi.resetAllMocks())

  it('uses the assembled records query with UTC milliseconds, deny semantics and cancellation', async () => {
    const controller = new AbortController()
    get.mockResolvedValue({ data: { group_by: 'site', items: [], total: 0 } })
    await getDestinationHits(request(), { signal: controller.signal, silent: true })
    expect(get).toHaveBeenCalledWith('/admin/dest/hits', {
      params: { panel_id: 7, action: 'block', source_kind: 'group', since: now - 86400000, until: now,
        group_by: 'site', include_trial: 0, page: 1, page_size: 50, q: 'private%_host.test' },
      signal: controller.signal, _skipErrorToast: true,
    })
  })

  it('retains deleted display names, frozen actions and separate incomplete loss units', async () => {
    const data = { group_by: 'none', items: [{ source: 'p12', source_name: null, user_id: 5, user_upn: null,
      panel_id: 7, panel_name: 'Node', hour: now - 3600000, action: 'block', dest: 'example.test', port: 443,
      count: 7, first_at: now - 3500000, last_at: now - 3400000 }], total: 1, page: 1, page_size: 50,
      summary: { block: 7, deny: 0, observe: 0, users: 1 }, sources: [{ source: 'p12', name: null }],
      dropped_in_range: 2, losses: { rows: 2, events: 3, unmatched: 4, scope: 'panel', complete: false } }
    get.mockResolvedValue({ data })
    expect(await getDestinationHits({ ...request(), group_by: 'none', include_trial: true })).toBe(data)
    expect(get.mock.calls[0][1].params.include_trial).toBe(1)
  })

  it('does not turn failed or cancelled reads into a complete zero', async () => {
    for (const error of [new Error('unavailable'), new DOMException('Aborted', 'AbortError')]) {
      get.mockRejectedValueOnce(error)
      await expect(getDestinationHits(request())).rejects.toBe(error)
    }
  })

  it('does not forward page-link or component fields accidentally attached at runtime', async () => {
    get.mockResolvedValue({ data: { group_by: 'site', items: [], total: 0 } })
    await getDestinationHits({ ...request(), rec_q: 'stale-search', drawer: 'private-drawer', sort: 'dest' } as ReturnType<typeof request>)
    const params = get.mock.calls[0][1].params
    expect(params).not.toHaveProperty('rec_q')
    expect(params).not.toHaveProperty('drawer')
    expect(params).not.toHaveProperty('sort')
  })
})
