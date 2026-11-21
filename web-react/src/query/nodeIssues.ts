import { queryOptions } from '@tanstack/react-query'
import { listNodeIssues, type NodeIssueListParams } from '@/api/nodeIssues'
import type { ListResponse, NodeAgentIssue } from '@/api/types'
import { nodeIssueKeys } from './keys'
import { freshness, policies } from './policies'
import type { QueryScope } from './session'

/**
 * The agents' issue reports. Read on demand like the logs (focus revalidation, no polling): they are
 * reports of what happened, not a live health check, and the page says so. A newer key cancels the
 * request for the older one through the signal, so a slow answer can never overwrite a newer filter.
 */
export function nodeIssuesQuery(scope: QueryScope, params: NodeIssueListParams) {
  return queryOptions({
    queryKey: nodeIssueKeys.list(scope, params),
    queryFn: ({ signal }): Promise<ListResponse<NodeAgentIssue>> => listNodeIssues(params, signal),
    ...freshness(policies.logList),
  })
}
