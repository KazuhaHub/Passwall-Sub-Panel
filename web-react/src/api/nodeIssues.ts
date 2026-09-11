import { client } from './client'
import type { ListResponse, NodeAgentIssue } from './types'

export interface NodeIssueListParams {
  page?: number
  page_size?: number
  keyword?: string
  agent_id?: string
  code?: string
  acknowledged?: boolean
}

export async function listNodeIssues(params: NodeIssueListParams = {}) {
  const { data } = await client.get<ListResponse<NodeAgentIssue>>('/admin/node-issues', { params })
  return data
}

export async function acknowledgeNodeIssue(id: number) {
  await client.post(`/admin/node-issues/${id}/acknowledge`)
}
