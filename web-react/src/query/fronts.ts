import { queryOptions, useQuery } from '@tanstack/react-query'
import { getFrontOptions, getFronts, getServerPorts } from '@/api/fronts'
import { frontKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

export function frontsQuery(scope: QueryScope, panelId: number) {
  return queryOptions({
    queryKey: frontKeys.list(scope, panelId),
    queryFn: ({ signal }) => getFronts(panelId, { signal }),
    ...freshness(policies.fronts),
  })
}

export function frontOptionsQuery(scope: QueryScope, panelId: number) {
  return queryOptions({
    queryKey: frontKeys.options(scope, panelId),
    queryFn: ({ signal }) => getFrontOptions(panelId, { signal }),
    ...freshness(policies.fronts),
  })
}

export function serverPortsQuery(scope: QueryScope, panelId: number) {
  return queryOptions({
    queryKey: frontKeys.ports(scope, panelId),
    queryFn: ({ signal }) => getServerPorts(panelId, { signal }),
    ...freshness(policies.fronts),
  })
}

export const useFronts = (scope: QueryScope, panelId: number) => useQuery(frontsQuery(scope, panelId))
export const useFrontOptions = (scope: QueryScope, panelId: number, enabled = true) =>
  useQuery({ ...frontOptionsQuery(scope, panelId), enabled })
export const useServerPorts = (scope: QueryScope, panelId: number, enabled = true) =>
  useQuery({ ...serverPortsQuery(scope, panelId), enabled })
