import { keepPreviousData, queryOptions, useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import {
  addDestException, createDestExemption, createDestList, createDestPolicy, deleteDestExemption, deleteDestList, deleteDestPolicy,
  editDestListEntries, getDestCatalog, getDestExemptions, getDestList, getDestLists, getDestPolicies, getDestSettings,
  getDestSourceRule, getDestSourceRules, getDestStatus, getDestUser, getGeositeCategories, getGeositeStatus,
  instantiateDestTemplate, pauseDest, publishDest, putDestSettings, putDestSourceRule, refreshDestList,
  reorderDestPolicies, retryDestAgent, rollbackGeosite, testDestination, updateDestExemption, updateDestList,
  updateDestPolicy, updateGeosite,
  type DestAction, type DestExemptionInput, type DestListInput, type DestPolicyInput, type DestSettings,
  type DestSourceRuleInput, type DestState, type DestTestInput,
} from '@/api/accessControl'
import { destStatusActive, listsRefreshing } from '@/utils/accessControl'
import { accessControlKeys, riskCenterKeys, settingsKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

// ACCESS-CONTROL READS AND WRITES. Every read takes the session scope and
// builds its key from accessControlKeys; every write names, by hand, the keys
// it makes stale (plan §7.3). Writes never invalidate the prefix: the status
// read is polled while nodes apply, and a list edit must not restart that.

type Part =
  | 'status' | 'policies' | 'lists' | 'listDetails' | 'catalog' | 'exemptions' | 'sourceRules'
  | 'geosite' | 'settings' | 'uiSettings'

function invalidate(qc: QueryClient, scope: QueryScope, parts: Part[], extra: readonly (readonly unknown[])[] = []) {
  const k = accessControlKeys
  const keys: Record<Part, (readonly unknown[])[]> = {
    status: [k.status(scope)],
    policies: [k.policies(scope)],
    lists: [k.lists(scope)],
    listDetails: [k.listDetails(scope)],
    catalog: [k.catalog(scope)],
    exemptions: [k.exemptions(scope)],
    sourceRules: [k.sourceRules(scope)],
    geosite: [k.geositeStatus(scope), k.geositeCategories(scope)],
    settings: [k.settings(scope)],
    uiSettings: [settingsKeys.ui(scope)],
  }
  return Promise.all([...parts.flatMap(p => keys[p]), ...extra].map(queryKey => qc.invalidateQueries({ queryKey })))
}

// ---- reads ----

export function destStatusQuery(scope: QueryScope, enabled = true) {
  return queryOptions({
    queryKey: accessControlKeys.status(scope),
    queryFn: ({ signal }) => getDestStatus({ signal }),
    enabled,
    // The policy's interval is the ceiling; the condition is the data's.
    refetchInterval: q => (destStatusActive(q.state.data) ? policies.destStatus.refetchInterval : false),
    staleTime: policies.destStatus.staleTime,
    gcTime: policies.destStatus.gcTime,
  })
}

export function useDestStatus(scope: QueryScope, { enabled = true }: { enabled?: boolean } = {}) {
  return useQuery(destStatusQuery(scope, enabled))
}

export function destPoliciesQuery(scope: QueryScope, enabled = true) {
  return queryOptions({
    queryKey: accessControlKeys.policies(scope),
    queryFn: ({ signal }) => getDestPolicies({ signal }),
    enabled,
    ...freshness(policies.destDefinitions),
  })
}

export function useDestPolicies(scope: QueryScope, { enabled = true }: { enabled?: boolean } = {}) {
  return useQuery(destPoliciesQuery(scope, enabled))
}

export function destListsQuery(scope: QueryScope, enabled = true) {
  return queryOptions({
    queryKey: accessControlKeys.lists(scope),
    queryFn: ({ signal }) => getDestLists({ signal }),
    enabled,
    placeholderData: keepPreviousData,
    refetchInterval: q => (listsRefreshing(q.state.data) ? policies.destListRefreshing.refetchInterval : false),
    staleTime: policies.destDefinitions.staleTime,
    gcTime: policies.destDefinitions.gcTime,
  })
}

export function useDestLists(scope: QueryScope, { enabled = true }: { enabled?: boolean } = {}) {
  return useQuery(destListsQuery(scope, enabled))
}

/** One list's statistics and first 200 entries; `text` adds a custom list's pasted source. */
export function useDestList(scope: QueryScope, id: number | null, { text = false, enabled = true }: { text?: boolean; enabled?: boolean } = {}) {
  return useQuery(queryOptions({
    queryKey: accessControlKeys.list(scope, id ?? 0, text),
    queryFn: ({ signal }) => getDestList(id as number, text, { signal }),
    enabled: enabled && id !== null && id > 0,
    // The editor seeds a draft from this read: it is fetched fresh each time one opens.
    staleTime: text ? 0 : policies.destDefinitions.staleTime,
    gcTime: policies.destListRefreshing.gcTime,
  }))
}

export function useDestCatalog(scope: QueryScope) {
  return useQuery(queryOptions({
    queryKey: accessControlKeys.catalog(scope),
    queryFn: ({ signal }) => getDestCatalog({ signal }),
    ...freshness(policies.destDefinitions),
  }))
}

export function useDestExemptions(scope: QueryScope, { enabled = true }: { enabled?: boolean } = {}) {
  return useQuery(queryOptions({
    queryKey: accessControlKeys.exemptions(scope),
    queryFn: ({ signal }) => getDestExemptions({ signal }),
    enabled,
    ...freshness(policies.destDefinitions),
  }))
}

export function useDestSourceRules(scope: QueryScope) {
  return useQuery(queryOptions({
    queryKey: accessControlKeys.sourceRules(scope),
    queryFn: ({ signal }) => getDestSourceRules({ signal }),
    ...freshness(policies.destDefinitions),
  }))
}

/** One inbound's rule, with whether it is on a native node and whether a GeoIP database exists. */
export function useDestSourceRule(scope: QueryScope, nodeId: number | null, { enabled = true }: { enabled?: boolean } = {}) {
  return useQuery(queryOptions({
    queryKey: accessControlKeys.sourceRule(scope, nodeId ?? 0),
    queryFn: ({ signal }) => getDestSourceRule(nodeId as number, { signal }),
    enabled: enabled && nodeId !== null && nodeId > 0,
    // The edit dialog seeds a draft from it.
    staleTime: 0,
    gcTime: policies.destListRefreshing.gcTime,
  }))
}

export function useGeositeStatus(scope: QueryScope, { enabled = true }: { enabled?: boolean } = {}) {
  return useQuery(queryOptions({
    queryKey: accessControlKeys.geositeStatus(scope),
    queryFn: ({ signal }) => getGeositeStatus({ signal }),
    enabled,
    ...freshness(policies.destDefinitions),
  }))
}

/** 503 dest_geosite_unavailable means "no community data downloaded yet". */
export function useGeositeCategories(scope: QueryScope, { enabled = true }: { enabled?: boolean } = {}) {
  return useQuery(queryOptions({
    queryKey: accessControlKeys.geositeCategories(scope),
    queryFn: ({ signal }) => getGeositeCategories({ signal }),
    enabled,
    ...freshness(policies.destDefinitions),
  }))
}

export function useDestUser(scope: QueryScope, userId: number, { enabled = true }: { enabled?: boolean } = {}) {
  return useQuery(queryOptions({
    queryKey: accessControlKeys.user(scope, userId),
    queryFn: ({ signal }) => getDestUser(userId, { signal }),
    enabled: enabled && userId > 0,
    ...freshness(policies.destUser),
  }))
}

export function useDestSettings(scope: QueryScope, { enabled = true }: { enabled?: boolean } = {}) {
  return useQuery(queryOptions({
    queryKey: accessControlKeys.settings(scope),
    queryFn: ({ signal }) => getDestSettings({ signal }),
    enabled,
    ...freshness(policies.destDefinitions),
  }))
}

// ---- writes ----

/** What a policy write makes stale: the policies, the status the verdict reads, the lists' reference
 *  counts and the catalog's "added" marks. */
const POLICY_WRITE: Part[] = ['policies', 'status', 'lists', 'catalog']

export function useCreateDestPolicy(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: DestPolicyInput) => createDestPolicy(input),
    onSettled: () => invalidate(qc, scope, POLICY_WRITE),
  })
}

/** PUT of the WHOLE policy and the `updated_at` it was read at (no PATCH, R18). */
export function useUpdateDestPolicy(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, input }: { id: number; input: DestPolicyInput }) => updateDestPolicy(id, input),
    onSettled: () => invalidate(qc, scope, POLICY_WRITE),
  })
}

export function useDeleteDestPolicy(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => deleteDestPolicy(id),
    onSettled: () => invalidate(qc, scope, POLICY_WRITE),
  })
}

export function useReorderDestPolicies(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ action, ids }: { action: DestAction; ids: number[] }) => reorderDestPolicies(action, ids),
    // Settled either way: a 409 means the order on screen was stale.
    onSettled: () => invalidate(qc, scope, ['policies', 'status']),
  })
}

export function useInstantiateDestTemplate(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ key, state, locale }: { key: string; state: DestState; locale: string }) => instantiateDestTemplate(key, state, locale),
    onSettled: () => invalidate(qc, scope, POLICY_WRITE),
  })
}

/** Whatever a list write makes stale: the lists, their details, the policies that name them, the status. */
const LIST_WRITE: Part[] = ['lists', 'listDetails', 'policies', 'status']

export function useCreateDestList(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: DestListInput) => createDestList(input),
    onSettled: () => invalidate(qc, scope, LIST_WRITE),
  })
}

export function useSaveDestList(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, input }: { id: number; input: DestListInput }) => updateDestList(id, input),
    onSettled: () => invalidate(qc, scope, LIST_WRITE),
  })
}

export function useDeleteDestList(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => deleteDestList(id),
    onSettled: () => invalidate(qc, scope, LIST_WRITE),
  })
}

/** Lines added to and removed from a custom list in one write (also the allow exception). */
export function useEditDestListEntries(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, add, remove }: { id: number; add?: string[]; remove?: string[] }) => editDestListEntries(id, { add, remove }),
    onSettled: () => invalidate(qc, scope, LIST_WRITE),
  })
}

/** 202: the refresh runs in the background. The lists read then polls (destListRefreshing) until it ends. */
export function useRefreshDestList(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => refreshDestList(id),
    onSettled: () => invalidate(qc, scope, ['lists']),
  })
}

export function useSaveDestExemption(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ userId, create, ...input }: { userId: number; create: boolean } & DestExemptionInput) =>
      create ? createDestExemption({ ...input, user_id: userId }) : updateDestExemption(userId, input),
    onSettled: (_d, _e, v) => invalidate(qc, scope, ['exemptions', 'policies', 'status'], [riskCenterKeys.user(scope, v.userId), accessControlKeys.user(scope, v.userId)]),
  })
}

export function useDeleteDestExemption(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (userId: number) => deleteDestExemption(userId),
    onSettled: (_d, _e, userId) => invalidate(qc, scope, ['exemptions', 'policies', 'status'], [riskCenterKeys.user(scope, userId), accessControlKeys.user(scope, userId)]),
  })
}

export function usePutSourceRule(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ nodeId, input }: { nodeId: number; input: DestSourceRuleInput }) => putDestSourceRule(nodeId, input),
    onSettled: (_d, _e, v) => invalidate(qc, scope, ['sourceRules', 'status'], [accessControlKeys.sourceRule(scope, v.nodeId)]),
  })
}

export function useSaveDestSettings(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (changed: Partial<DestSettings>) => putDestSettings(changed),
    // The refresh interval is quoted on the lists, and the system settings record holds the same keys.
    onSettled: () => invalidate(qc, scope, ['settings', 'status', 'lists', 'uiSettings']),
  })
}

export function usePauseDest(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (paused: boolean) => pauseDest(paused),
    onSettled: () => invalidate(qc, scope, ['status', 'policies']),
  })
}

export function usePublishDest(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => publishDest(),
    // Settled either way: a refused publish leaves `publish_error` on the status.
    onSettled: () => invalidate(qc, scope, ['status', 'policies']),
  })
}

export function useRetryDestAgent(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (agentId: string) => retryDestAgent(agentId),
    onSettled: () => invalidate(qc, scope, ['status', 'policies']),
  })
}

/**
 * Apply a rule-data update. A dry run changes nothing and invalidates nothing;
 * the apply moves the data's own status, every list derived from it, the
 * policies that name those lists and the status.
 */
export function useUpdateGeosite(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { tag?: string; dryRun?: boolean }) => updateGeosite(v),
    onSettled: (_d, _e, v) => v.dryRun ? undefined : invalidate(qc, scope, ['geosite', 'lists', 'listDetails', 'policies', 'status', 'catalog']),
  })
}

export function useRollbackGeosite(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { dryRun?: boolean }) => rollbackGeosite(v.dryRun === true),
    onSettled: (_d, _e, v) => v.dryRun ? undefined : invalidate(qc, scope, ['geosite', 'lists', 'listDetails', 'policies', 'status', 'catalog']),
  })
}

/** An allow exception: the first call creates the exceptions list and the allow policy over it, later calls append. */
export function useAddDestException(scope: QueryScope) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: { target: string; match: 'site' | 'host' }) => addDestException(input),
    onSettled: () => invalidate(qc, scope, ['lists', 'listDetails', 'policies', 'status']),
  })
}

/** The test tool is a read of the published policy that happens to be a POST (and audited): nothing goes stale. */
export function useTestDestination(_scope: QueryScope) {
  return useMutation({ mutationFn: (input: DestTestInput) => testDestination(input) })
}
