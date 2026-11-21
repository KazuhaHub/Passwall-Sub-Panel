import { StatusBadge } from '@/components/common/StatusBadge'
import type { NodeIssueGroup } from '@/utils/nodeIssueGroups'
import { useAdminT } from '../logs/shared'
import { REVIEW_TONE, reviewOf, serverNameOf, suggestedAction, issueTitle, type IssueView } from './model'

export function ReviewBadge({ group }: { group: NodeIssueGroup }) {
  const t = useAdminT()
  const r = reviewOf(group)
  return <StatusBadge tone={REVIEW_TONE[r]} label={t(`node_issues.state.${r}`)} />
}

/** What went wrong in plain words, and what to do first. The stable code and the agent id are in the sheet. */
export function ProblemCell({ group, view, showState }: { group: NodeIssueGroup; view: IssueView; showState: boolean }) {
  const t = useAdminT()
  return (
    <div className="min-w-0 whitespace-normal">
      <div className="text-sm font-medium break-words">{issueTitle(group, view, t)}</div>
      <div className="mt-0.5 text-xs break-words text-muted-foreground">{suggestedAction(group, view, t)}</div>
      {showState && <div className="mt-1"><ReviewBadge group={group} /></div>}
    </div>
  )
}

export function ServerCell({ group }: { group: NodeIssueGroup }) {
  const t = useAdminT()
  const serverId = group.issues.find(i => i.server_name)?.server_id
  return (
    <div className="min-w-0 whitespace-normal">
      <div className="text-sm break-words">{serverNameOf(group, t)}</div>
      <div className="mt-0.5 text-xs text-muted-foreground">
        {serverId ? t('node_issues.server_id', { id: serverId }) : t('node_issues.unlinked_hint')}
      </div>
    </div>
  )
}
