import type { NodeAgentIssue } from '@/api/types'

export type NodeIssueCategory = 'statistics' | 'sync' | 'tasks' | 'other'

const categories: Readonly<Record<string, NodeIssueCategory>> = {
  core_telemetry_failed: 'statistics',
  core_convergence_failed: 'sync',
  object_pending_timeout: 'sync',
  object_rejected_timeout: 'sync',
  segment_rejected: 'sync',
  roster_ahead_of_config: 'sync',
  attachment_unknown_listener: 'sync',
  directives_ahead_of_roster: 'sync',
  directive_unknown_client: 'sync',
  report_missing_object: 'sync',
  task_identity_conflict: 'tasks',
  task_replay_fenced: 'tasks',
  legacy_task_result_quarantined: 'tasks',
}

export interface NodeIssueGroup {
  id: string
  agentID: string
  code: string
  category: NodeIssueCategory
  issues: NodeAgentIssue[]
  lastSeenAt: string
  unacknowledgedCount: number
}

/** Presentation groups for one API page; every durable issue remains intact. */
export function groupNodeIssues(items: readonly NodeAgentIssue[]): NodeIssueGroup[] {
  const groups = new Map<string, NodeIssueGroup>()
  const latestTimestamps = new Map<string, number>()

  for (const issue of items) {
    // Unknown codes remain visible; no raw diagnostic is used as display copy.
    const category = Object.hasOwn(categories, issue.code) ? categories[issue.code] : 'other'
    const id = JSON.stringify([issue.agent_id, category])
    const timestamp = Date.parse(issue.last_seen_at)
    let group = groups.get(id)

    if (!group) {
      group = {
        id,
        agentID: issue.agent_id,
        code: issue.code,
        category,
        issues: [],
        lastSeenAt: issue.last_seen_at,
        unacknowledgedCount: 0,
      }
      groups.set(id, group)
      latestTimestamps.set(id, Number.isFinite(timestamp) ? timestamp : -Infinity)
    } else if (Number.isFinite(timestamp) && timestamp > latestTimestamps.get(id)!) {
      group.lastSeenAt = issue.last_seen_at
      latestTimestamps.set(id, timestamp)
    }

    group.issues.push(issue)
    if (!issue.acknowledged_at) group.unacknowledgedCount += 1
  }

  // If no timestamp parses, retain the group's first value without inventing a time.
  return [...groups.values()]
}
