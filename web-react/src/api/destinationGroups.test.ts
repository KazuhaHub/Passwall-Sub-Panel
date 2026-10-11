import { beforeEach, expect, it, vi } from 'vitest'
import { getDestinationGroups, getDestinationGroup, previewDestinationGroup, getDestinationGroupTrialReport, putDestinationGroup, createDestinationGroupException } from './destinationGroups'

const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }))
vi.mock('./client', () => ({ client: api }))
const mode = { group_id: 7, mode: 'allowlist', stage: 'trial', updated_at: 1800000000000, stage_changed_at: 1800000000000, list_ids: [1], base_list_id: 2, extra_list_id: 3 }
const summary = { ...mode, group_name: 'Guests', members: 4, total_nodes: 2, eligible_nodes: 1, applied_nodes: 0,
  ineligible: [{ panel_id: 2, name: 'Legacy', reason: 'capability' }], would_deny_recent: { days: 3, sites: 2, count: 9 }, denied_24h: 1 }
const losses = { rows: 0, events: 0, unmatched: 0, scope: 'panel', complete: false }
const readiness = { lists_ready: true, not_ready_lists: [], unapplied_nodes: [{ panel_id: 1, name: 'Native' }], base_nonempty: false, remaining_sites_24h: 0, top_sites: [], trial_losses: losses }
const budget = Object.fromEntries(['rules', 'domains', 'regexps', 'cidrs', 'subjects', 'bytes'].map(key => [key, { used: 0, limit: 10 }]))
beforeEach(() => vi.resetAllMocks())

it('forwards cancellation and uses server-bounded counters without inventing zeros', async () => {
  const controller = new AbortController()
  api.get.mockResolvedValueOnce({ data: { items: [summary] } })
  expect(await getDestinationGroups({ signal: controller.signal, silent: true })).toEqual({ items: [summary] })
  expect(api.get).toHaveBeenCalledWith('/admin/dest/groups', { signal: controller.signal, _skipErrorToast: true })
  for (const data of ['<html>old router</html>', { items: [{ ...summary, eligible_nodes: 3 }] }, { items: [{ ...summary, ineligible: [] }] }, { items: [{ ...summary, mode: 'unknown' }] }]) {
    api.get.mockResolvedValueOnce({ data })
    await expect(getDestinationGroups()).rejects.toThrow()
  }
  const error = new DOMException('Aborted', 'AbortError')
  api.get.mockRejectedValueOnce(error)
  await expect(getDestinationGroups({ signal: controller.signal })).rejects.toBe(error)
})

it('checks readiness identity and counts without treating an empty base as invalid', async () => {
  api.get.mockResolvedValueOnce({ data: { ...summary, readiness } })
  expect((await getDestinationGroup(7)).readiness.base_nonempty).toBe(false)
  for (const data of [{ ...summary, group_id: 8, readiness }, { ...summary, readiness: { ...readiness, unapplied_nodes: [] } }, { ...summary, readiness: { ...readiness, trial_losses: { ...losses, complete: true } } }]) {
    api.get.mockResolvedValueOnce({ data })
    await expect(getDestinationGroup(7)).rejects.toThrow()
  }
})

it('previews normalized list selection with explicit manual-DNS fallback and abort support', async () => {
  const controller = new AbortController(), ids = [5, 1, 5]
  const preview = { members: 4, total_nodes: 0, eligible_nodes: 0, ineligible: [], base_hosts: [], base_hosts_available: false, base_list_id: 0, extra_list_id: 0, budget }
  api.get.mockResolvedValueOnce({ data: preview })
  expect(await previewDestinationGroup(7, ids, { signal: controller.signal })).toEqual(preview)
  expect(ids).toEqual([5, 1, 5])
  expect(api.get).toHaveBeenCalledWith('/admin/dest/groups/7/preview', { params: { list_ids: '1,5' }, signal: controller.signal, _skipErrorToast: undefined })
  await expect(previewDestinationGroup(0, [])).rejects.toThrow()
  await expect(previewDestinationGroup(7, [0])).rejects.toThrow()
  expect(api.get).toHaveBeenCalledOnce()
})

it('keeps every anonymous trial site and rejects sample-host or invalid loss payloads', async () => {
  const items = Array.from({ length: 30 }, (_, i) => ({ site: `site-${i}.test`, count: i+1, last_at: 1800000000000, added: false }))
  const report = { effective_days: 1, items, dropped_in_range: 0, losses }
  api.get.mockResolvedValueOnce({ data: report })
  expect((await getDestinationGroupTrialReport(7, 31)).items).toHaveLength(30)
  expect(api.get.mock.calls[0][1].params).toEqual({ days: 31 })
  for (const data of [{ ...report, items: [{ ...items[0], sample_host: 'private-host.test' }] }, { ...report, losses: { ...losses, complete: true } }, { ...report, dropped_in_range: 2 }, { ...report, items: [items[0], items[0]] }]) {
    api.get.mockResolvedValueOnce({ data })
    await expect(getDestinationGroupTrialReport(7, 7)).rejects.toThrow()
  }
})

it('sends the complete CAS mode and group exception scope while preserving backend conflicts', async () => {
  const input = { mode: 'allowlist', stage: 'trial', list_ids: [1], base_hosts: ['dns.example.test'], updated_at: 0 } as const
  api.put.mockResolvedValueOnce({ data: mode })
  expect(await putDestinationGroup(7, { ...input, list_ids: [...input.list_ids], base_hosts: [...input.base_hosts] })).toEqual(mode)
  expect(api.put.mock.calls[0]).toEqual(['/admin/dest/groups/7', input])
  const conflict = { response: { status: 409, data: { error: 'dest_mode_stale' } } }
  api.put.mockRejectedValueOnce(conflict)
  await expect(putDestinationGroup(7, { mode: 'open', stage: '', list_ids: [], updated_at: mode.updated_at })).rejects.toBe(conflict)
  api.post.mockResolvedValueOnce({ data: { group_id: 7, list_id: 3, entry: 'domain:example.test' } })
  await createDestinationGroupException(7, { target: 'example.test', match: 'site' })
  expect(api.post).toHaveBeenCalledWith('/admin/dest/exceptions', { target: 'example.test', match: 'site', scope: 'group', group_id: 7 })
})
