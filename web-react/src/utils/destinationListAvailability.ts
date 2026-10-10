import type { DestinationListSummary, DestinationPolicyOverviewItem } from '@/api/accessControl'

/** Refresh state never invalidates previously downloaded entries. */
export function destinationListAvailable(list: Pick<DestinationListSummary, 'kind' | 'entry_count' | 'last_fetched_at'>): boolean {
  return list.entry_count > 0 && (list.kind === 'custom' || list.last_fetched_at !== null)
}

/** Older overview DTOs lack the independent availability flag. */
export function policyListAvailable(list: DestinationPolicyOverviewItem['list_states'][number]): boolean {
  return list.available ?? list.state === 'ready'
}
