/** @vitest-environment jsdom */
import type { ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { accessControlKeys, groupKeys } from './keys'
import { sessionScope } from './session'
import { destinationGroupDetailQuery, destinationGroupPreviewQuery, destinationGroupsQuery, destinationGroupTrialQuery, invalidateDestinationGroups, useSaveDestinationGroup, useCreateDestinationGroupException, useAddDestinationGroupSites } from './destinationGroups'
import type { DestinationGroupSummary } from '@/api/destinationGroups'

const api = vi.hoisted(() => ({ getDestinationGroups: vi.fn(), getDestinationGroup: vi.fn(), previewDestinationGroup: vi.fn(), getDestinationGroupTrialReport: vi.fn(), putDestinationGroup: vi.fn(), createDestinationGroupException: vi.fn(), entries: vi.fn() }))
vi.mock('@/api/destinationGroups', () => api)
vi.mock('@/api/accessControl', () => ({ patchDestinationListEntries: api.entries }))
const scope = sessionScope({ userId: 1, role: 'admin', authEpoch: 3 })
const sibling = sessionScope({ userId: 1, role: 'admin', authEpoch: 4 })
const trial: DestinationGroupSummary = { group_id: 7, group_name: 'Trial', mode: 'allowlist', stage: 'trial', list_ids: [], base_list_id: 10, extra_list_id: 11, updated_at: 1000, stage_changed_at: 1000, total_nodes: 1, eligible_nodes: 1, applied_nodes: 0, ineligible: [], members: 2, would_deny_recent: { days: 7, sites: 0, count: 0 }, denied_24h: 0 }
beforeEach(() => { vi.clearAllMocks(); api.putDestinationGroup.mockResolvedValue({ ...trial, stage: 'enforce' }); api.createDestinationGroupException.mockResolvedValue({ group_id: 7, list_id: 11, entry: 'domain:example.test' }) })
afterEach(cleanup)

it('separates sessions, canonicalizes preview lists without changing the draft, and gates every read to admins', async () => {
  const ids = [5, 1, 5], preview = destinationGroupPreviewQuery(scope, 7, ids)
  expect(preview.queryKey).toEqual(destinationGroupPreviewQuery(scope, 7, [1, 5]).queryKey)
  expect(ids).toEqual([5, 1, 5])
  expect(preview.queryKey).not.toEqual(destinationGroupPreviewQuery(sibling, 7, ids).queryKey)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  api.previewDestinationGroup.mockResolvedValue({ members: 2 })
  await client.fetchQuery(preview)
  expect(api.previewDestinationGroup).toHaveBeenCalledWith(7, [1, 5], { signal: expect.any(AbortSignal), silent: true })
  for (const role of ['operator', 'user', 'anon']) {
    const denied = { ...scope, role }
    for (const opts of [destinationGroupsQuery(denied), destinationGroupDetailQuery(denied, 7), destinationGroupPreviewQuery(denied, 7, []), destinationGroupTrialQuery(denied, 7, 7)]) expect(opts.enabled).toBe(false)
  }
  expect(destinationGroupDetailQuery(scope, 0).enabled).toBe(false)
  expect(destinationGroupPreviewQuery(scope, 7, [-1]).enabled).toBe(false)
  expect(destinationGroupTrialQuery(scope, 7, 0).enabled).toBe(false)
  client.clear()
})

it('polls only outstanding receipts, stops on settlement or read errors, and never polls reports or hidden pages', () => {
  const client = new QueryClient(), opts = destinationGroupsQuery(scope)
  const query = client.getQueryCache().build<{ items: DestinationGroupSummary[] }, Error, { items: DestinationGroupSummary[] }, ReturnType<typeof accessControlKeys.destinationGroups>>(client, { queryKey: accessControlKeys.destinationGroups(scope) })
  if (typeof opts.refetchInterval !== 'function') throw new Error('Expected receipt-dependent polling')
  query.setData({ items: [trial] })
  expect(opts.refetchInterval(query)).toBe(10000)
  query.setData({ items: [{ ...trial, applied_nodes: 1 }] })
  expect(opts.refetchInterval(query)).toBe(false)
  query.setData({ items: [trial] }); query.setState({ status: 'error', error: new Error('unavailable') })
  expect(opts.refetchInterval(query)).toBe(false)
  expect(opts.refetchIntervalInBackground).toBe(false)
  const report = destinationGroupTrialQuery(scope, 7, 31)
  expect(report.refetchInterval).toBe(false)
  expect(report.refetchOnWindowFocus).toBe(false)
  client.clear()
})

it('cancels a departed report, preserves server effective days, and keys each requested interval separately', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  let signal: AbortSignal | undefined
  api.getDestinationGroupTrialReport.mockImplementation((_id, _days, opts) => { signal = opts.signal; return new Promise(() => {}) })
  const pending = destinationGroupTrialQuery(scope, 7, 31)
  const result = client.fetchQuery(pending).catch(() => undefined)
  await waitFor(() => expect(signal).toBeDefined())
  await client.cancelQueries({ queryKey: pending.queryKey })
  expect(signal?.aborted).toBe(true)
  await result
  const payload = { effective_days: 1, items: [], dropped_in_range: 0, losses: { rows: 0, events: 0, unmatched: 0, scope: 'panel', complete: false } }
  api.getDestinationGroupTrialReport.mockResolvedValue(payload)
  expect(await client.fetchQuery(destinationGroupTrialQuery(scope, 7, 7))).toEqual(payload)
  expect(api.getDestinationGroupTrialReport).toHaveBeenLastCalledWith(7, 7, { signal: expect.any(AbortSignal), silent: true })
  expect(destinationGroupTrialQuery(scope, 7, 1).queryKey).not.toEqual(destinationGroupTrialQuery(scope, 7, 7).queryKey)
  client.clear()
})

function keys(s = scope) {
  return [accessControlKeys.destinationGroups(s), accessControlKeys.destinationGroup(s, 7), accessControlKeys.destinationGroupPreview(s, 7, [1]), accessControlKeys.destinationGroupTrial(s, 7, 7)]
}
it('invalidates current group facts and reports without automatically refetching a preview or touching another session', async () => {
  const client = new QueryClient(), preview = destinationGroupPreviewQuery(scope, 7, [1])
  for (const key of [...keys(), ...keys(sibling)]) client.setQueryData(key, { fixture: true })
  const refetch = vi.fn().mockResolvedValue({ fixture: true })
  const { QueryObserver } = await import('@tanstack/react-query')
  const observer = new QueryObserver(client, { ...preview, queryFn: refetch, staleTime: Infinity })
  const stop = observer.subscribe(() => {})
  await invalidateDestinationGroups(client, scope)
  for (const key of keys()) expect(client.getQueryState(key)?.isInvalidated).toBe(true)
  for (const key of keys(sibling)) expect(client.getQueryState(key)?.isInvalidated).toBe(false)
  expect(refetch).not.toHaveBeenCalled()
  stop(); client.clear()
})

it.each([true, false])('refreshes mode-dependent caches after a save or conflict while preserving the complete CAS input (success=%s)', async success => {
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  const extraKeys = [accessControlKeys.lists(scope), accessControlKeys.listDetail(scope, 11, true), accessControlKeys.policies(scope), accessControlKeys.status(scope), accessControlKeys.userAccess(scope, 2), groupKeys.catalogue(scope)]
  for (const key of [...keys(), ...extraKeys, ...keys(sibling)]) client.setQueryData(key, { fixture: true })
  if (!success) api.putDestinationGroup.mockRejectedValue({ response: { status: 409 } })
  const { result } = renderHook(() => useSaveDestinationGroup(scope), { wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> })
  const input = { mode: 'allowlist' as const, stage: 'enforce' as const, list_ids: [5], updated_at: 1000 }
  await act(async () => { try { await result.current.mutateAsync({ groupID: 7, input }) } catch { /* Verify conflict invalidation. */ } })
  expect(api.putDestinationGroup).toHaveBeenCalledWith(7, input)
  for (const key of [...keys(), ...extraKeys]) expect(client.getQueryState(key)?.isInvalidated).toBe(true)
  for (const key of keys(sibling)) expect(client.getQueryState(key)?.isInvalidated).toBe(false)
  client.clear()
})

it('refreshes whole-site coverage and list editors after adding a group exception', async () => {
  const client = new QueryClient()
  for (const key of keys()) client.setQueryData(key, { fixture: true })
  const { result } = renderHook(() => useCreateDestinationGroupException(scope), { wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> })
  await act(async () => { await result.current.mutateAsync({ groupID: 7, input: { target: 'example.test', match: 'site' } }) })
  expect(api.createDestinationGroupException).toHaveBeenCalledWith(7, { target: 'example.test', match: 'site' })
  for (const key of keys()) expect(client.getQueryState(key)?.isInvalidated).toBe(true)
  client.clear()
})
it('adds selected whole sites in one atomic entries request, rejects IP labels and refreshes actual coverage', async () => {
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  for (const key of keys()) client.setQueryData(key, { fixture: true })
  api.entries.mockResolvedValue({ id: 11 })
  const { result } = renderHook(() => useAddDestinationGroupSites(scope), { wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> })
  await act(async () => { await result.current.mutateAsync({ listID: 11, sites: ['a.test', 'b.test', 'a.test'] }) })
  expect(api.entries).toHaveBeenCalledExactlyOnceWith(11, ['domain:a.test', 'domain:b.test'])
  for (const key of keys()) expect(client.getQueryState(key)?.isInvalidated).toBe(true)
  await act(async () => { await expect(result.current.mutateAsync({ listID: 11, sites: ['(ip)'] })).rejects.toThrow() })
  expect(api.entries).toHaveBeenCalledOnce()
  client.clear()
})
