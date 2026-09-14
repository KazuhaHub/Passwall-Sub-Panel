import { client } from './client'
import type { ListResponse, NodeAgentIssue } from './types'

export interface NodeIssueListParams {
  page?: number
  page_size?: number
  keyword?: string
  agent_id?: string
  code?: string
  acknowledged?: boolean
  view?: 'attention' | 'diagnostic' | 'all'
}

export async function listNodeIssues(params: NodeIssueListParams = {}, signal?: AbortSignal) {
  const { data } = await client.get<ListResponse<NodeAgentIssue>>('/admin/node-issues', { params, signal })
  return data
}

export async function acknowledgeNodeIssue(id: number, options?: { quiet?: boolean }) {
  const url = `/admin/node-issues/${id}/acknowledge`
  // Bulk review owns aggregate feedback; individual failures must still reject.
  if (options?.quiet) {
    await client.post(url, undefined, { _skipErrorToast: true })
  } else {
    await client.post(url)
  }
}
