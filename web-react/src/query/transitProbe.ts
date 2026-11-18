import { queryOptions, useQuery } from '@tanstack/react-query'
import { getTransitProbes, type TransitProbe } from '@/api/transitProbe'
import { probeKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'


export function transitProbesQuery(scope: QueryScope, panelId: number) {
  return queryOptions({
    queryKey: probeKeys.panel(scope, panelId),
    queryFn: ({ signal }): Promise<TransitProbe[]> => getTransitProbes(panelId, { signal }),
    ...freshness(policies.probes),
  })
}

export function useTransitProbes(scope: QueryScope, panelId: number, { enabled = true }: { enabled?: boolean } = {}) {
  return useQuery({ ...transitProbesQuery(scope, panelId), enabled: enabled && panelId > 0 })
}
