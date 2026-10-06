import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getAccessControlSettings, putAccessControlSettings, getDestinationStatus, getDestinationPolicies, getDestinationLists, publishDestinationPolicies, putDestinationPause, retryDestinationPolicy, createDestinationPolicy, putDestinationPolicy, deleteDestinationPolicy, orderDestinationPolicies, type DestinationPolicyAction, type DestinationPolicyInput } from '@/api/accessControl'
import { accessControlKeys, settingsKeys } from './keys'
import type { QueryScope } from './session'
import { freshness, policies } from './policies'
import { statusNeedsPolling } from '@/utils/accessControl'

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
export function useDestinationLists(scope: QueryScope) {
  return useQuery({ queryKey: accessControlKeys.lists(scope), queryFn: ({ signal }) => getDestinationLists({ signal, silent: true }), ...freshness(policies.destDefinitions) })
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
