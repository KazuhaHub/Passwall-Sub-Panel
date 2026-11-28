import { queryOptions, useQuery } from '@tanstack/react-query'
import { getNodeMetricsCurrent } from '@/api/nodeMetrics'
import { nodeMetricsKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

export function nodeMetricsCurrentQuery(scope: QueryScope, serverId: number) {
  return queryOptions({
    queryKey: nodeMetricsKeys.current(scope, serverId),
    queryFn: ({ signal }) => getNodeMetricsCurrent(serverId, signal),
    ...freshness(policies.nodeMetricsCurrent),
  })
}

export const useNodeMetricsCurrent = (scope: QueryScope, serverId: number, enabled = true) =>
  useQuery({ ...nodeMetricsCurrentQuery(scope, serverId), enabled })
