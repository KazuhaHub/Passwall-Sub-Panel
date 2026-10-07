/** @vitest-environment jsdom */
import type { ReactNode } from 'react'
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'
import { act, cleanup, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { sessionScope } from './session'
import { accessControlKeys, groupKeys, settingsKeys } from './keys'
import { destinationCategoriesQuery, useDestinationCategories, destinationListsQuery, useSaveDestinationList, useDeleteDestinationList, useRefreshDestinationList, useRefreshDestinationCategories, useSaveDestinationPolicy, useDeleteDestinationPolicy, useOrderDestinationPolicies, useDestinationPublication, useRetryDestinationPolicy, useSaveAccessControlSettings, useSaveDestinationExemption, useDeleteDestinationExemption, useCreateDestinationException } from './accessControl'
import { destinationBudget, samplePolicy } from '@/test/accessControlFixtures'
import type { DestinationCategoriesView, DestinationListSummary, DestinationListsView } from '@/api/accessControl'
const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
const scope = sessionScope({ userId: 1, role: 'admin', authEpoch: 1 })
const sibling = sessionScope({ userId: 2, role: 'admin', authEpoch: 1 })
const list = { name: 'List', kind: 'custom' as const, text: 'example.com' }
const listKeys = ['lists', 'detail', 'listPreview', 'policyPreview', 'policies', 'status', 'groups']
const policyKeys = ['policies', 'status', 'lists', 'policyPreview']
const exemptionKeys = ['exemptions', 'userAccess', 'policies', 'status', 'policyPreview']
const cases: Array<{ name: string; keys: string[]; successOnly?: boolean; useRun: () => () => Promise<unknown> }> = [
  { name: 'global exception', keys: listKeys, useRun: () => { const m = useCreateDestinationException(scope); return () => m.mutateAsync({ target: 'example.test', match: 'site', scope: 'global' }) } },
  { name: 'create exemption', keys: exemptionKeys, useRun: () => { const m = useSaveDestinationExemption(scope); return () => m.mutateAsync({ userId: 7, input: { reason: 'Diagnostics', expires_at: null } }) } },
  { name: 'edit exemption', keys: exemptionKeys, useRun: () => { const m = useSaveDestinationExemption(scope); return () => m.mutateAsync({ userId: 7, existing: true, input: { reason: 'Updated', expires_at: 1900000000000 } }) } },
  { name: 'cancel exemption', keys: exemptionKeys, useRun: () => { const m = useDeleteDestinationExemption(scope); return () => m.mutateAsync(7) } },
  { name: 'create list', keys: listKeys, useRun: () => { const m = useSaveDestinationList(scope); return () => m.mutateAsync({ input: list }) } },
  { name: 'edit list', keys: listKeys, useRun: () => { const m = useSaveDestinationList(scope); return () => m.mutateAsync({ input: list, existing: { id: 7, updated_at: 3000 } }) } },
  { name: 'delete list', keys: listKeys, useRun: () => { const m = useDeleteDestinationList(scope); return () => m.mutateAsync(7) } },
  { name: 'refresh list', keys: listKeys, useRun: () => { const m = useRefreshDestinationList(scope); return () => m.mutateAsync(7) } },
  { name: 'refresh categories', keys: [...listKeys, 'categories'], useRun: () => { const m = useRefreshDestinationCategories(scope); return () => m.mutateAsync() } },
  { name: 'create policy', keys: policyKeys, useRun: () => { const m = useSaveDestinationPolicy(scope); return () => m.mutateAsync({ input: samplePolicy }) } },
  { name: 'edit policy', keys: policyKeys, useRun: () => { const m = useSaveDestinationPolicy(scope); return () => m.mutateAsync({ input: samplePolicy, existing: samplePolicy }) } },
  { name: 'delete policy', keys: policyKeys, useRun: () => { const m = useDeleteDestinationPolicy(scope); return () => m.mutateAsync(12) } },
  { name: 'order policies', keys: policyKeys, useRun: () => { const m = useOrderDestinationPolicies(scope); return () => m.mutateAsync({ action: 'block', ids: [12] }) } },
  { name: 'publish', keys: ['status', 'policies'], useRun: () => { const m = useDestinationPublication(scope); return () => m.mutateAsync({}) } },
  { name: 'pause', keys: ['status', 'policies'], useRun: () => { const m = useDestinationPublication(scope); return () => m.mutateAsync({ paused: true }) } },
  { name: 'resume', keys: ['status', 'policies'], useRun: () => { const m = useDestinationPublication(scope); return () => m.mutateAsync({ paused: false }) } },
  { name: 'retry agent', keys: ['status'], useRun: () => { const m = useRetryDestinationPolicy(scope); return () => m.mutateAsync('node-one') } },
  { name: 'settings', keys: ['settings', 'status', 'ui', 'lists'], successOnly: true, useRun: () => { const m = useSaveAccessControlSettings(scope); return () => m.mutateAsync({ dest_list_refresh_hours: 12 }) } },
]
function keys(s = scope) {
  return { exemptions: accessControlKeys.exemptions(s), userAccess: accessControlKeys.userAccess(s, 7), lists: accessControlKeys.lists(s), detail: accessControlKeys.listDetail(s, 7, true), listPreview: [...accessControlKeys.listPreviews(s), 'input'], policyPreview: [...accessControlKeys.policyPreviews(s), 'input'], policies: accessControlKeys.policies(s), status: accessControlKeys.status(s), groups: groupKeys.catalogue(s), categories: accessControlKeys.categories(s), settings: accessControlKeys.settings(s), ui: settingsKeys.ui(s) }
}
beforeEach(() => { vi.clearAllMocks(); for (const method of [api.post, api.put, api.delete]) method.mockResolvedValue({ data: {} }) })
afterEach(cleanup)
it.each([true, false])('follows HTTP 202 with delayed catalog reads and stops after settlement (success=%s)', async success => {
  vi.useFakeTimers()
  try {
    let queued = false, queuedReads = 0
    const missing = (refreshing: boolean, last_error = '') => Object.assign(new Error('missing'), { isAxiosError: true, response: { status: 503, data: { error: 'dest_geosite_unavailable', refreshing, last_error } } })
    api.get.mockImplementation(async () => {
      if (!queued) throw missing(false)
      if (++queuedReads === 1) throw missing(true)
      if (!success) throw missing(false, 'download_failed')
      return { data: { categories: [], updated_at: 4000, refreshing: false, last_error: '' } }
    })
    api.post.mockImplementation(async () => { queued = true; return { data: {} } })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const { result, unmount } = renderHook(() => ({ query: useDestinationCategories(scope, true), refresh: useRefreshDestinationCategories(scope) }), { wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> })
    await act(async () => { await vi.advanceTimersByTimeAsync(20) })
    expect(api.get).toHaveBeenCalledTimes(1)
    await act(async () => { await result.current.refresh.mutateAsync(); await vi.advanceTimersByTimeAsync(20) })
    expect(queuedReads).toBe(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(queuedReads).toBe(2)
    if (success) expect(result.current.query.data?.updated_at).toBe(4000)
    else expect(result.current.query.error).toMatchObject({ response: { data: { last_error: 'download_failed' } } })
    await act(async () => { await vi.advanceTimersByTimeAsync(15000) })
    expect(queuedReads).toBe(2)
    expect(api.post).toHaveBeenCalledTimes(1)
    unmount(); client.clear()
  } finally { vi.useRealTimers() }
})
it('polls queued catalog downloads on both 503 and cached 200, then stops on success or failure', () => {
  const options = destinationCategoriesQuery(scope), client = new QueryClient()
  const query = client.getQueryCache().build<DestinationCategoriesView, Error, DestinationCategoriesView, ReturnType<typeof accessControlKeys.categories>>(client, { queryKey: accessControlKeys.categories(scope) })
  const interval = options.refetchInterval
  if (typeof interval !== 'function') throw new Error('Catalog polling must depend on lifecycle status')
  const unavailable = (refreshing: boolean, last_error = '') => Object.assign(new Error('missing'), { isAxiosError: true, response: { status: 503, data: { refreshing, last_error } } })
  query.setState({ status: 'error', error: unavailable(false) }); expect(interval(query)).toBe(false)
  query.setState({ error: unavailable(true) }); expect(interval(query)).toBe(5000)
  expect(options.refetchIntervalInBackground).toBe(false)
  query.setData({ categories: [], updated_at: 1000, refreshing: true }); expect(interval(query)).toBe(5000)
  query.setState({ status: 'error', error: unavailable(false, 'download_failed') }); expect(interval(query)).toBe(false)
  query.setState({ error: new Error('connection lost') }); expect(interval(query)).toBe(false)
  query.setData({ categories: [], updated_at: 2000, refreshing: false, last_error: '' }); expect(interval(query)).toBe(false)
})
it.each(cases.flatMap(row => [true, false].map(success => ({ ...row, success }))))('$name updates precisely its dependent session caches (success=$success)', async row => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  for (const key of [...Object.values(keys()), ...Object.values(keys(sibling))]) client.setQueryData(key, { fixture: true })
  if (!row.success) for (const method of [api.post, api.put, api.delete]) method.mockRejectedValue({ response: { status: 409, data: { error: 'stale' } } })
  const { result } = renderHook(row.useRun, { wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> })
  await act(async () => { try { await result.current() } catch { /* Assert conflict invalidation below. */ } })
  const expected = !row.success && row.successOnly ? [] : row.keys
  for (const [name, key] of Object.entries(keys())) expect(client.getQueryState(key)?.isInvalidated, name).toBe(expected.includes(name))
  for (const key of Object.values(keys(sibling))) expect(client.getQueryState(key)?.isInvalidated).toBe(false)
})
it('polls a refreshing list every five seconds, stops when settled and pauses in the background', () => {
  const options = destinationListsQuery(scope), client = new QueryClient()
  const query = client.getQueryCache().build<DestinationListsView, Error, DestinationListsView, ReturnType<typeof accessControlKeys.lists>>(client, { queryKey: accessControlKeys.lists(scope) })
  const interval = options.refetchInterval
  if (typeof interval !== 'function') throw new Error('List polling must depend on refresh state')
  const item: DestinationListSummary = { id: 7, name: 'List', kind: 'remote', source_url: 'https://example.org/list', geosite_category: '', geosite_attrs: '', entry_count: 0, regexp_count: 0, state: 'refreshing', last_fetched_at: null, last_error: '', owner_group_id: 0, updated_at: 3000, parse_report_summary: null, used_by: [] }
  query.setData({ items: [item], budget: destinationBudget, refresh_hours: 12 })
  expect(interval(query)).toBe(5000)
  expect(options.refetchIntervalInBackground).toBe(false)
  query.setData({ items: [{ ...item, state: 'ready' }], budget: destinationBudget, refresh_hours: 12 })
  expect(interval(query)).toBe(false)
  query.setData({ items: [{ ...item, state: 'failed' }], budget: destinationBudget, refresh_hours: 12 })
  expect(interval(query)).toBe(false)
})
it('does not refetch an active network preview as a side effect of a list mutation', async () => {
  const client = new QueryClient(), key = [...accessControlKeys.listPreviews(scope), 'remote-input'], fetch = vi.fn()
  client.setQueryData(key, { fixture: true })
  const { result } = renderHook(() => { useQuery({ queryKey: key, queryFn: fetch, staleTime: Infinity }); const mutation = useRefreshDestinationCategories(scope); return () => mutation.mutateAsync() }, { wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> })
  await act(async () => { await result.current() })
  expect(client.getQueryState(key)?.isInvalidated).toBe(true)
  expect(fetch).not.toHaveBeenCalled()
})
