import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getAccessControlSettings, putAccessControlSettings, getDestinationStatus, getDestinationPolicies, getDestinationLists, publishDestinationPolicies, putDestinationPause, retryDestinationPolicy, createDestinationPolicy, putDestinationPolicy, deleteDestinationPolicy, orderDestinationPolicies, type DestinationPolicyAction, type DestinationPolicyInput } from '@/api/accessControl'
import { accessControlKeys, settingsKeys } from './keys'
import type { QueryScope } from './session'
import { freshness, policies } from './policies'
import { statusNeedsPolling } from '@/utils/accessControl'
import { categoryRefreshState } from '@/utils/destinationCategories'
import { getDestinationList, getDestinationCategories, createDestinationList, putDestinationList, deleteDestinationList, refreshDestinationList, refreshDestinationCategories, type DestinationListInput } from '@/api/accessControl'
import { groupKeys } from './keys'
import { getDestinationExemptions, getDestinationUserAccess, createDestinationExemption, putDestinationExemption, deleteDestinationExemption, type DestinationExemptionInput } from '@/api/accessControl'
import { createDestinationGlobalException } from '@/api/accessControl'

export function useAccessControlSettings(scope: QueryScope, enabled: boolean) {
  return useQuery({
    queryKey: accessControlKeys.settings(scope),
    queryFn: ({ signal }) => getAccessControlSettings({ signal, silent: true }),
    enabled, ...freshness(policies.destSettings), refetchOnWindowFocus: false,
  })
}

export function useSaveAccessControlSettings(scope: QueryScope) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: putAccessControlSettings,
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: accessControlKeys.settings(scope) })
      void client.invalidateQueries({ queryKey: accessControlKeys.status(scope) })
      void client.invalidateQueries({ queryKey: settingsKeys.ui(scope) })
      void client.invalidateQueries({ queryKey: accessControlKeys.lists(scope) })
    },
  })
}

export function destinationStatusQuery(scope: QueryScope, enabled = true) {
  return queryOptions({ queryKey: accessControlKeys.status(scope), queryFn: ({ signal }) => getDestinationStatus({ signal, silent: true }),
    ...freshness(policies.destStatus), enabled, refetchIntervalInBackground: false,
    refetchInterval: q => statusNeedsPolling(q.state.data) ? policies.destStatus.refetchInterval : false })
}
export function useDestinationStatus(scope: QueryScope, enabled = true) { return useQuery(destinationStatusQuery(scope, enabled)) }
export function useDestinationPolicies(scope: QueryScope) {
  return useQuery({ queryKey: accessControlKeys.policies(scope), queryFn: ({ signal }) => getDestinationPolicies({ signal, silent: true }), ...freshness(policies.destDefinitions) })
}
export function destinationListsQuery(scope: QueryScope, enabled = true) {
  return queryOptions({ queryKey: accessControlKeys.lists(scope), queryFn: ({ signal }) => getDestinationLists({ signal, silent: true }), ...freshness(policies.destListRefreshing),
    enabled, staleTime: q => q.state.data?.items?.some(list => list.state === 'refreshing') ? 0 : policies.destDefinitions.staleTime,
    refetchIntervalInBackground: false, refetchInterval: q => q.state.data?.items?.some(list => list.state === 'refreshing') ? policies.destListRefreshing.refetchInterval : false })
}
export function useDestinationLists(scope: QueryScope, enabled = true) { return useQuery(destinationListsQuery(scope, enabled)) }
export function useDestinationList(scope: QueryScope, id: number, text = false) {
  return useQuery({ queryKey: accessControlKeys.listDetail(scope, id, text), queryFn: ({ signal }) => getDestinationList(id, text, { signal, silent: true }), ...freshness(policies.destDefinitions) })
}
export function destinationCategoriesQuery(scope: QueryScope, enabled = true) {
  return queryOptions({ queryKey: accessControlKeys.categories(scope), queryFn: ({ signal }) => getDestinationCategories({ signal, silent: true }), enabled, ...freshness(policies.destDefinitions), retry: false,
    refetchIntervalInBackground: false,
    staleTime: q => categoryRefreshState(q.state.data, q.state.error).refreshing ? 0 : policies.destDefinitions.staleTime,
    refetchInterval: q => categoryRefreshState(q.state.data, q.state.error).refreshing ? policies.destListRefreshing.refetchInterval : false })
}
export function useDestinationCategories(scope: QueryScope, enabled: boolean) { return useQuery(destinationCategoriesQuery(scope, enabled)) }
function useInvalidateLists(scope: QueryScope) {
  const client = useQueryClient()
  return () => Promise.all([accessControlKeys.lists(scope), accessControlKeys.listDetails(scope), accessControlKeys.listPreviews(scope), accessControlKeys.policyPreviews(scope), accessControlKeys.policies(scope), accessControlKeys.status(scope), groupKeys.all(scope)]
    .map(queryKey => client.invalidateQueries({ queryKey, refetchType: queryKey.some(key => key === 'list-preview' || key === 'policy-preview') ? 'none' : 'active' })))
}
export function useSaveDestinationList(scope: QueryScope) {
  const invalidate = useInvalidateLists(scope)
  return useMutation({ mutationFn: ({ input, existing }: { input: DestinationListInput; existing?: { id: number; updated_at: number } }) => existing
    ? putDestinationList(existing.id, { ...input, updated_at: existing.updated_at }) : createDestinationList(input), onSettled: invalidate })
}
export function useDeleteDestinationList(scope: QueryScope) {
  return useMutation({ mutationFn: deleteDestinationList, onSettled: useInvalidateLists(scope) })
}
export function useRefreshDestinationList(scope: QueryScope) {
  return useMutation({ mutationFn: refreshDestinationList, onSettled: useInvalidateLists(scope) })
}
export function useRefreshDestinationCategories(scope: QueryScope) {
  const client = useQueryClient(), invalidate = useInvalidateLists(scope)
  return useMutation({ mutationFn: refreshDestinationCategories, onSettled: () => Promise.all([invalidate(), client.invalidateQueries({ queryKey: accessControlKeys.categories(scope) })]) })
}
function useInvalidatePolicies(scope: QueryScope) {
  const client = useQueryClient()
  return () => Promise.all([accessControlKeys.policies(scope), accessControlKeys.status(scope), accessControlKeys.lists(scope), accessControlKeys.policyPreviews(scope)]
    .map(queryKey => client.invalidateQueries({ queryKey })))
}
export function useSaveDestinationPolicy(scope: QueryScope) {
  const invalidate = useInvalidatePolicies(scope)
  return useMutation({ mutationFn: ({ input, existing }: { input: DestinationPolicyInput; existing?: { id: number; updated_at: number } }) => existing
    ? putDestinationPolicy(existing.id, { ...input, updated_at: existing.updated_at }) : createDestinationPolicy(input), onSettled: invalidate })
}
export function useDeleteDestinationPolicy(scope: QueryScope) {
  const invalidate = useInvalidatePolicies(scope)
  return useMutation({ mutationFn: deleteDestinationPolicy, onSettled: invalidate })
}
export function useOrderDestinationPolicies(scope: QueryScope) {
  const invalidate = useInvalidatePolicies(scope)
  return useMutation({ mutationFn: ({ action, ids }: { action: DestinationPolicyAction; ids: number[] }) => orderDestinationPolicies(action, ids), onSettled: invalidate })
}
export function useDestinationPublication(scope: QueryScope) {
  const client = useQueryClient()
  return useMutation({ mutationFn: ({ paused }: { paused?: boolean }) => paused === undefined ? publishDestinationPolicies() : putDestinationPause(paused),
    onSettled: () => Promise.all([accessControlKeys.status(scope), accessControlKeys.policies(scope)].map(queryKey => client.invalidateQueries({ queryKey }))) })
}
export function useRetryDestinationPolicy(scope: QueryScope) {
  const client = useQueryClient()
  return useMutation({ mutationFn: retryDestinationPolicy, onSettled: () => client.invalidateQueries({ queryKey: accessControlKeys.status(scope) }) })
}

export function useDestinationExemptions(scope: QueryScope, enabled = true) {
  return useQuery({ queryKey: accessControlKeys.exemptions(scope), queryFn: ({ signal }) => getDestinationExemptions({ signal, silent: true }), enabled, ...freshness(policies.destDefinitions) })
}
export function useDestinationUserAccess(scope: QueryScope, id: number, enabled = true) {
  return useQuery({ queryKey: accessControlKeys.userAccess(scope, id), queryFn: ({ signal }) => getDestinationUserAccess(id, { signal, silent: true }), enabled: enabled && id > 0, ...freshness(policies.destDefinitions) })
}
function useInvalidateExemptions(scope: QueryScope) {
  const client = useQueryClient()
  return () => Promise.all([accessControlKeys.exemptions(scope), accessControlKeys.userAccessRoot(scope), accessControlKeys.policies(scope), accessControlKeys.status(scope), accessControlKeys.policyPreviews(scope)]
    .map(queryKey => client.invalidateQueries({ queryKey })))
}
export function useSaveDestinationExemption(scope: QueryScope) {
  return useMutation({ mutationFn: ({ userId, input, existing }: { userId: number; input: DestinationExemptionInput; existing?: boolean }) => existing
    ? putDestinationExemption(userId, input) : createDestinationExemption({ ...input, user_id: userId }), onSettled: useInvalidateExemptions(scope) })
}
export function useDeleteDestinationExemption(scope: QueryScope) {
  return useMutation({ mutationFn: deleteDestinationExemption, onSettled: useInvalidateExemptions(scope) })
}
export function useCreateDestinationException(scope: QueryScope) {
  return useMutation({ mutationFn: createDestinationGlobalException, onSettled: useInvalidateLists(scope) })
}
