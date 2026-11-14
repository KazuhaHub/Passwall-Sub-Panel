// @vitest-environment jsdom
import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import * as api from '@/api/accessControl'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import {
  useCreateDestPolicy, useDeleteDestExemption, useDeleteDestList, useDeleteDestPolicy, useDestLists, useDestStatus,
  useEditDestListEntries, useInstantiateDestTemplate, usePauseDest, usePublishDest, usePutSourceRule,
  useReorderDestPolicies, useRetryDestAgent, useRollbackGeosite, useSaveDestExemption, useSaveDestList,
  useSaveDestSettings, useUpdateDestPolicy, useUpdateGeosite, useTestDestination, useAddDestException,
  useDestHits, useDestPolicies, useDestUser, useSetAuditCollect,
} from './accessControl'
import { accessControlKeys, riskCenterKeys, serverKeys, settingsKeys } from './keys'
import { policies } from './policies'
import type { QueryScope } from './session'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})

const scope: QueryScope = { apiBase: '/api', userId: 1, role: 'admin', authEpoch: 1 }
const ac = accessControlKeys

// WRITE → INVALIDATE (plan §7.3). One test per mutation hook, asserting the
// exact keys it invalidates: a write that forgets one leaves a screen showing
// the old truth, and one that invalidates too much re-reads the polled status.
function spy() {
  const client = newClient()
  const inv = vi.spyOn(client, 'invalidateQueries')
  const keys = () => inv.mock.calls.map(c => JSON.stringify((c[0] as { queryKey: unknown }).queryKey))
  return { client, keys, expectKeys: (expected: readonly (readonly unknown[])[]) => {
    expect(keys().sort()).toEqual(expected.map(k => JSON.stringify(k)).sort())
  } }
}

beforeEach(() => { vi.clearAllMocks() })

// Unmount every hook and drop every client after each test. A polled query
// keeps its refetchInterval timer alive while mounted; left running, it fires
// after the jsdom environment is torn down and React throws "window is not
// defined" outside any test.
const clients: ReturnType<typeof makeTestQueryClient>[] = []
function newClient() {
  const c = makeTestQueryClient()
  clients.push(c)
  return c
}
afterEach(() => {
  cleanup()
  for (const c of clients.splice(0)) c.clear()
})

async function run<T>(hook: () => { mutateAsync: (v: never) => Promise<unknown> } & T, client: ReturnType<typeof makeTestQueryClient>, arg: unknown) {
  const { result } = renderHook(hook, { wrapper: queryWrapper(client) })
  await act(async () => { await result.current.mutateAsync(arg as never) })
}

describe('write → invalidate', () => {
  it('a policy write: policies, status, lists (the reference count), catalog (the "added" mark), and the hit records and account views that name policies', async () => {
    vi.mocked(api.createDestPolicy).mockResolvedValue({ id: 1 } as api.DestPolicy)
    const s = spy()
    await run(() => useCreateDestPolicy(scope), s.client, { name: 'x' })
    s.expectKeys([ac.policies(scope), ac.status(scope), ac.lists(scope), ac.catalog(scope), ac.hitsAll(scope), ac.usersAll(scope)])
  })

  it.each([
    ['update', () => useUpdateDestPolicy(scope), { id: 1, input: {} }, 'updateDestPolicy'],
    ['delete', () => useDeleteDestPolicy(scope), 1, 'deleteDestPolicy'],
  ] as const)('a policy %s likewise', async (_n, hook, arg, fn) => {
    vi.mocked(api[fn] as (...a: unknown[]) => Promise<unknown>).mockResolvedValue({})
    const s = spy()
    await run(hook as never, s.client, arg)
    s.expectKeys([ac.policies(scope), ac.status(scope), ac.lists(scope), ac.catalog(scope), ac.hitsAll(scope), ac.usersAll(scope)])
  })

  it('reordering moves the policies and what the status reads of them', async () => {
    vi.mocked(api.reorderDestPolicies).mockResolvedValue()
    const s = spy()
    await run(() => useReorderDestPolicies(scope), s.client, { action: 'block', ids: [2, 1] })
    s.expectKeys([ac.policies(scope), ac.status(scope)])
  })

  it('a template instantiation creates lists and a policy: everything those read', async () => {
    vi.mocked(api.instantiateDestTemplate).mockResolvedValue({ id: 1 } as api.DestPolicy)
    const s = spy()
    await run(() => useInstantiateDestTemplate(scope), s.client, { key: 'bittorrent', state: 'report', locale: 'zh-CN' })
    s.expectKeys([ac.policies(scope), ac.status(scope), ac.lists(scope), ac.catalog(scope), ac.hitsAll(scope), ac.usersAll(scope)])
  })

  it.each([
    ['save', () => useSaveDestList(scope), { id: 1, input: {} }, 'updateDestList'],
    ['delete', () => useDeleteDestList(scope), 1, 'deleteDestList'],
    ['entries', () => useEditDestListEntries(scope), { id: 1, add: ['a.com'] }, 'editDestListEntries'],
  ] as const)('a list %s: lists, every list detail, policies (the lists a policy names), status', async (_n, hook, arg, fn) => {
    vi.mocked(api[fn] as (...a: unknown[]) => Promise<unknown>).mockResolvedValue({})
    const s = spy()
    await run(hook as never, s.client, arg)
    s.expectKeys([ac.lists(scope), ac.listDetails(scope), ac.policies(scope), ac.status(scope)])
  })

  it('an exemption: exemptions, policies (the count), status, the risk drawer and the access tab of that account', async () => {
    vi.mocked(api.createDestExemption).mockResolvedValue({} as api.DestExemption)
    const s = spy()
    await run(() => useSaveDestExemption(scope), s.client, { userId: 7, reason: 'r', expires_at: null, create: true })
    s.expectKeys([ac.exemptions(scope), ac.policies(scope), ac.status(scope), riskCenterKeys.user(scope, 7), ac.user(scope, 7)])
  })

  it('cancelling an exemption invalidates the same', async () => {
    vi.mocked(api.deleteDestExemption).mockResolvedValue()
    const s = spy()
    await run(() => useDeleteDestExemption(scope), s.client, 7)
    s.expectKeys([ac.exemptions(scope), ac.policies(scope), ac.status(scope), riskCenterKeys.user(scope, 7), ac.user(scope, 7)])
  })

  it('a source rule: source rules, that inbound, status', async () => {
    vi.mocked(api.putDestSourceRule).mockResolvedValue({ rule: {} as api.DestSourceRule, geoip_available: true })
    const s = spy()
    await run(() => usePutSourceRule(scope), s.client, { nodeId: 4, input: {} })
    s.expectKeys([ac.sourceRules(scope), ac.sourceRule(scope, 4), ac.status(scope)])
  })

  it('settings: settings, status, lists (the refresh interval they quote), the policies and the hit and account views (the retention window they quote), the system settings record', async () => {
    vi.mocked(api.putDestSettings).mockResolvedValue({} as api.DestSettingsView)
    const s = spy()
    await run(() => useSaveDestSettings(scope), s.client, { dest_list_refresh_hours: 12 })
    s.expectKeys([ac.settings(scope), ac.status(scope), ac.lists(scope), ac.policies(scope), ac.hitsAll(scope), ac.usersAll(scope), settingsKeys.ui(scope)])
  })

  it('a recording mode: the status, the servers list that carries audit_collect, and what is shown only while a node records (policy counters, hit records, account hits)', async () => {
    vi.mocked(api.setAuditCollect).mockResolvedValue()
    const s = spy()
    await run(() => useSetAuditCollect(scope), s.client, { serverId: 5, collect: 'off' })
    s.expectKeys([ac.status(scope), serverKeys.lists(scope), ac.policies(scope), ac.hitsAll(scope), ac.usersAll(scope)])
  })

  it('pause, publish-now and a retry: status and policies', async () => {
    vi.mocked(api.pauseDest).mockResolvedValue()
    vi.mocked(api.publishDest).mockResolvedValue({ published_generation: 2, generation: 2, unpublished: false })
    vi.mocked(api.retryDestAgent).mockResolvedValue({ retried: true })
    for (const [hook, arg] of [[() => usePauseDest(scope), true], [() => usePublishDest(scope), undefined], [() => useRetryDestAgent(scope), 'a1']] as const) {
      const s = spy()
      await run(hook as never, s.client, arg)
      s.expectKeys([ac.status(scope), ac.policies(scope)])
    }
  })

  it('applying a rule-data update or a rollback: the status of the data, lists, policies, status, catalog; a dry run invalidates nothing', async () => {
    vi.mocked(api.updateGeosite).mockResolvedValue({ applied: true, dry_run: false } as api.GeositeUpdateResult)
    vi.mocked(api.rollbackGeosite).mockResolvedValue({ applied: true, dry_run: false } as api.GeositeUpdateResult)
    for (const hook of [() => useUpdateGeosite(scope), () => useRollbackGeosite(scope)]) {
      const s = spy()
      await run(hook as never, s.client, { dryRun: false })
      s.expectKeys([ac.geositeStatus(scope), ac.geositeCategories(scope), ac.lists(scope), ac.listDetails(scope), ac.policies(scope), ac.status(scope), ac.catalog(scope)])
    }
    vi.mocked(api.updateGeosite).mockResolvedValue({ applied: false, dry_run: true } as api.GeositeUpdateResult)
    const dry = spy()
    await run(() => useUpdateGeosite(scope), dry.client, { dryRun: true })
    dry.expectKeys([])
  })

  it('an allow exception creates or appends to a list and a policy: lists, details, policies, status', async () => {
    vi.mocked(api.addDestException).mockResolvedValue({ entry: 'a.com', created: null })
    const s = spy()
    await run(() => useAddDestException(scope), s.client, { target: 'a.com', match: 'site' })
    s.expectKeys([ac.lists(scope), ac.listDetails(scope), ac.policies(scope), ac.status(scope)])
  })

  it('the test tool is a pure read of the published policy: it invalidates nothing', async () => {
    vi.mocked(api.testDestination).mockResolvedValue({} as api.DestTestResult)
    const s = spy()
    await run(() => useTestDestination(scope), s.client, { target: 'a.com' })
    s.expectKeys([])
  })

  it('never touches the servers list key (the status line there reads the status read)', () => {
    expect(serverKeys.all(scope)).not.toEqual(ac.all(scope))
  })
})

describe('hit records and the account view are queries, not polls', () => {
  it('the hit read is keyed by its filters, never refetches on a timer and uses the destHits policy', async () => {
    const client = newClient()
    vi.mocked(api.getDestHits).mockResolvedValue({ items: [], sources: [], summary: { block: 0, deny: 0, observe: 0, users: 0 } } as unknown as api.DestHits)
    const params = { user_id: 7, source: 'p3', group_by: 'site' } as const
    const { result } = renderHook(() => useDestHits(scope, params), { wrapper: queryWrapper(client) })
    await waitFor(() => expect(result.current.data).toBeTruthy())
    const q = client.getQueryCache().find({ queryKey: ac.hits(scope, params) })!
    expect(q).toBeTruthy()
    expect((q.options as { staleTime?: number }).staleTime).toBe(policies.destHits.staleTime)
    expect((q.options as { gcTime?: number }).gcTime).toBe(policies.destHits.gcTime)
    expect(policies.destHits.refetchInterval).toBe(false)
    expect(api.getDestHits).toHaveBeenCalledWith(params, expect.anything())
  })

  it('the keyword is part of the key and of the request only: an empty keyword is the same read as none', async () => {
    expect(ac.hits(scope, { q: '' })).toEqual(ac.hits(scope, {}))
  })

  it('the policy read carries hit counters, so its own policy keeps them under a minute-class window', () => {
    expect(policies.destPolicies.staleTime).toBeLessThanOrEqual(60_000)
    expect(policies.destPolicies.refetchInterval).toBe(false)
    expect(policies.destPolicies.note.length).toBeGreaterThan(20)
  })

  it('the account read and the policy read are keyed under the access-control prefix', async () => {
    const client = newClient()
    vi.mocked(api.getDestPolicies).mockResolvedValue({ allow: [], block: [], observe: [] } as unknown as api.DestPolicies)
    vi.mocked(api.getDestUser).mockResolvedValue({ user_id: 7 } as api.DestUserView)
    renderHook(() => { useDestPolicies(scope); useDestUser(scope, 7) }, { wrapper: queryWrapper(client) })
    await waitFor(() => expect(client.getQueryCache().find({ queryKey: ac.user(scope, 7) })).toBeTruthy())
    expect(ac.user(scope, 7).slice(0, ac.usersAll(scope).length)).toEqual([...ac.usersAll(scope)])
  })
})

describe('polling is conditional', () => {
  it('the status poll is the policy ceiling while a node applies, and off otherwise', async () => {
    const client = newClient()
    const status = (state: string) => ({
      generation: 1, published_generation: 1, paused: false, publish_error: null, nodes: [{ state }], policy_notes: {}, totals: {},
    }) as unknown as api.DestStatus
    vi.mocked(api.getDestStatus).mockResolvedValue(status('pending'))
    const { result } = renderHook(() => useDestStatus(scope), { wrapper: queryWrapper(client) })
    await waitFor(() => expect(result.current.data).toBeTruthy())
    const q = client.getQueryCache().find({ queryKey: ac.status(scope) })!
    const interval = (q.options as unknown as { refetchInterval: (q: unknown) => number | false }).refetchInterval
    expect(interval(q)).toBe(policies.destStatus.refetchInterval)
    vi.mocked(api.getDestStatus).mockResolvedValue(status('applied'))
    await act(async () => { await client.invalidateQueries({ queryKey: ac.status(scope) }) })
    expect(interval(q)).toBe(false)
  })

  it('the list read polls at the refreshing cadence only while a list is refreshing', async () => {
    const client = newClient()
    const lists = (state: string) => ({ items: [{ id: 1, state }], refresh_hours: 24, budget: {} }) as unknown as api.DestListsView
    vi.mocked(api.getDestLists).mockResolvedValue(lists('refreshing'))
    const { result } = renderHook(() => useDestLists(scope), { wrapper: queryWrapper(client) })
    await waitFor(() => expect(result.current.data).toBeTruthy())
    const q = client.getQueryCache().find({ queryKey: ac.lists(scope) })!
    const interval = (q.options as unknown as { refetchInterval: (q: unknown) => number | false }).refetchInterval
    expect(interval(q)).toBe(policies.destListRefreshing.refetchInterval)
    vi.mocked(api.getDestLists).mockResolvedValue(lists('ready'))
    await act(async () => { await client.invalidateQueries({ queryKey: ac.lists(scope) }) })
    expect(interval(q)).toBe(false)
  })
})
