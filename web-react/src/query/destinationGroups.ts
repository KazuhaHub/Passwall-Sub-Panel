import { queryOptions, useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'
import { createDestinationGroupException, getDestinationGroup, getDestinationGroups, getDestinationGroupTrialReport, previewDestinationGroup, putDestinationGroup, type DestinationGroupInput, type DestinationGroupSummary } from '@/api/destinationGroups'
import { patchDestinationListEntries } from '@/api/accessControl'
import { accessControlKeys, groupKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

const admin = (scope: QueryScope) => scope.userId > 0 && scope.role === 'admin'
const positive = (id: number) => Number.isSafeInteger(id) && id > 0
const outstanding = (group: DestinationGroupSummary) => group.mode === 'allowlist' && group.eligible_nodes > group.applied_nodes

export function destinationGroupsQuery(scope: QueryScope, enabled = true) {
  return queryOptions({ queryKey: accessControlKeys.destinationGroups(scope), queryFn: ({ signal }) => getDestinationGroups({ signal, silent: true }),
    ...freshness(policies.destStatus), enabled: enabled && admin(scope), retry: false, refetchIntervalInBackground: false,
    refetchInterval: q => q.state.status !== 'error' && q.state.data?.items.some(outstanding) ? policies.destStatus.refetchInterval : false })
}
export function useDestinationGroups(scope: QueryScope, enabled = true) { return useQuery(destinationGroupsQuery(scope, enabled)) }

export function destinationGroupDetailQuery(scope: QueryScope, id: number, enabled = true) {
  return queryOptions({ queryKey: accessControlKeys.destinationGroup(scope, id), queryFn: ({ signal }) => getDestinationGroup(id, { signal, silent: true }),
    ...freshness(policies.destStatus), enabled: enabled && admin(scope) && positive(id), retry: false, refetchOnMount: 'always', refetchIntervalInBackground: false,
    refetchInterval: q => q.state.status !== 'error' && q.state.data && outstanding(q.state.data) ? policies.destStatus.refetchInterval : false })
}
export function useDestinationGroup(scope: QueryScope, id: number, enabled = true) { return useQuery(destinationGroupDetailQuery(scope, id, enabled)) }

export function destinationGroupPreviewQuery(scope: QueryScope, id: number, listIDs: number[], enabled = true) {
  const selected = [...new Set(listIDs)].sort((a, b) => a - b)
  return queryOptions({ queryKey: accessControlKeys.destinationGroupPreview(scope, id, selected), queryFn: ({ signal }) => previewDestinationGroup(id, selected, { signal, silent: true }),
    ...freshness(policies.destStatus), enabled: enabled && admin(scope) && positive(id) && selected.every(positive), retry: false, refetchOnWindowFocus: false, refetchInterval: false })
}
export function useDestinationGroupPreview(scope: QueryScope, id: number, listIDs: number[], enabled = true) { return useQuery(destinationGroupPreviewQuery(scope, id, listIDs, enabled)) }

export function destinationGroupTrialQuery(scope: QueryScope, id: number, days: number, enabled = true) {
  return queryOptions({ queryKey: accessControlKeys.destinationGroupTrial(scope, id, days), queryFn: ({ signal }) => getDestinationGroupTrialReport(id, days, { signal, silent: true }),
    ...freshness(policies.destHits), enabled: enabled && admin(scope) && positive(id) && positive(days), retry: false, refetchOnWindowFocus: false, refetchInterval: false })
}
export function useDestinationGroupTrial(scope: QueryScope, id: number, days: number, enabled = true) { return useQuery(destinationGroupTrialQuery(scope, id, days, enabled)) }

export function invalidateDestinationGroups(client: QueryClient, scope: QueryScope) {
  return Promise.all([
    client.invalidateQueries({ queryKey: accessControlKeys.destinationGroups(scope) }),
    client.invalidateQueries({ queryKey: accessControlKeys.destinationGroupDetails(scope) }),
    client.invalidateQueries({ queryKey: accessControlKeys.destinationGroupTrials(scope) }),
    client.invalidateQueries({ queryKey: accessControlKeys.destinationGroupPreviews(scope), refetchType: 'none' }),
  ])
}
function useInvalidateGroupWrite(scope: QueryScope) {
  const client = useQueryClient()
  return () => Promise.all([
    invalidateDestinationGroups(client, scope),
    ...[accessControlKeys.lists(scope), accessControlKeys.listDetails(scope), accessControlKeys.policies(scope), accessControlKeys.status(scope), accessControlKeys.userAccessRoot(scope), groupKeys.all(scope)].map(queryKey => client.invalidateQueries({ queryKey })),
    ...[accessControlKeys.listPreviews(scope), accessControlKeys.policyPreviews(scope)].map(queryKey => client.invalidateQueries({ queryKey, refetchType: 'none' })),
  ])
}
export function useSaveDestinationGroup(scope: QueryScope) {
  return useMutation({ mutationFn: ({ groupID, input }: { groupID: number; input: DestinationGroupInput }) => putDestinationGroup(groupID, input), onSettled: useInvalidateGroupWrite(scope) })
}
export function useCreateDestinationGroupException(scope: QueryScope) {
  return useMutation({ mutationFn: ({ groupID, input }: { groupID: number; input: { target: string; match: 'site' | 'host' } }) => createDestinationGroupException(groupID, input), onSettled: useInvalidateGroupWrite(scope) })
}
export function useAddDestinationGroupSites(scope: QueryScope) {
  return useMutation({ mutationFn: ({ listID, sites }: { listID: number; sites: string[] }) => {
    if (!positive(listID) || !Array.isArray(sites) || sites.length === 0 || !sites.every(site => typeof site === 'string' && /^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$/.test(site))) throw new Error('Invalid whole-site entries')
    return patchDestinationListEntries(listID, [...new Set(sites)].map(site => `domain:${site}`))
  }, onSettled: useInvalidateGroupWrite(scope) })
}
