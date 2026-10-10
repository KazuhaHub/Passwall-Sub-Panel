import { useQuery } from '@tanstack/react-query'
import { getDestinationHits } from '@/api/destinationHits'
import { accessControlKeys } from '@/query/keys'
import { freshness, policies } from '@/query/policies'
import { useQueryScope } from '@/query/useQueryScope'
import { recordsRequest, type RecordsFilters } from './recordsParams'

export function useDestinationHits(filters: RecordsFilters, keyword: string, rangeReady = true) {
  const scope = useQueryScope(), valid = rangeReady && recordsRequest(filters, keyword, Date.now()) !== null
  return { valid, ...useQuery({
    queryKey: [...accessControlKeys.hits(scope), filters, keyword.trim()],
    queryFn: ({ signal }) => {
      // Resolve relative windows on each actual request, not every render or
      // drawer navigation. Instants must not continually change the cache key.
      const input = recordsRequest(filters, keyword, Date.now())
      if (!input) throw new Error('dest_audit_query_invalid')
      return getDestinationHits(input, { signal, silent: true })
    },
    enabled: valid, ...freshness(policies.destHits), retry: false, refetchOnWindowFocus: false,
  }) }
}
