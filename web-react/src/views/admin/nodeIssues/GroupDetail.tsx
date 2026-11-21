import { AsyncButton } from '@/components/common/AsyncButton'
import { IconCheck } from '@/lib/icons'
import { CodeSection, DetailList, DetailRow, TimeCell, useAdminT } from '../logs/shared'
import type { NodeAgentIssue } from '@/api/types'
import type { NodeIssueGroup } from '@/utils/nodeIssueGroups'
import { suggestedAction, type IssueView } from './model'

/**
 * Everything about one group of reports. The people-facing part is first (what to do, which records,
 * review state); the stable code, agent identity, object key and unabridged diagnostic are in a fold
 * below, so they are one click away for a bug report and out of the way for everyone else.
 */
export function GroupDetail({ group, view, canOperate, busyId, locked, onAcknowledge }: {
  group: NodeIssueGroup
  view: IssueView
  canOperate: boolean
  /** The record whose single review is in flight. */
  busyId: number | null
  /** Another review (or a refresh) is running; every action waits. */
  locked: boolean
  onAcknowledge: (issue: NodeAgentIssue) => void
}) {
  const t = useAdminT()
  const mixedCodes = group.issues.some(i => i.code !== group.code)
  const codes = [...new Set(group.issues.map(i => i.code))]
  return (
    <>
      <p className="text-sm text-muted-foreground">{suggestedAction(group, view, t)}</p>
      <p className="mt-2 text-xs text-muted-foreground">
        {t('node_issues.detail_scope', { records: t('node_issues.record_count', { count: group.issues.length }) })}
      </p>
      <ul className="mt-4 divide-y rounded-md border">
        {group.issues.map(issue => (
          <li key={issue.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2">
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium">{t('node_issues.record_id', { id: issue.id })}</div>
              <div className="text-xs text-muted-foreground">
                {view === 'diagnostic' ? t('node_issues.startup_title') : t(`node_issues.code_titles.${issue.code}`, { defaultValue: t('node_issues.unknown_title') })}
                {' · '}
                {t(`node_issues.state.${issue.acknowledged_at ? 'acknowledged' : 'unacknowledged'}`)}
              </div>
            </div>
            {!issue.acknowledged_at && canOperate && (
              <AsyncButton variant="outline" size="sm" icon={<IconCheck />} disabled={locked && busyId !== issue.id} pending={busyId === issue.id} onClick={() => onAcknowledge(issue)}>
                {t('node_issues.acknowledge')}
              </AsyncButton>
            )}
          </li>
        ))}
      </ul>
      <details className="group mt-5 rounded-md border">
        <summary className="cursor-pointer list-none px-3 py-2 text-sm font-medium outline-none focus-visible:ring-3 focus-visible:ring-ring/50 [&::-webkit-details-marker]:hidden">
          {t('node_issues.technical_details')}
        </summary>
        <div className="border-t px-3 pb-3">
          <DetailList className="mt-3">
            <DetailRow label={t('node_issues.table.code')} mono>{codes.join('\n')}</DetailRow>
            <DetailRow label={t('node_issues.table.agent')} mono>{group.agentID}</DetailRow>
          </DetailList>
          {group.issues.map(issue => (
            <div key={issue.id} className="mt-4 border-t pt-3">
              <div className="text-xs text-muted-foreground">#{issue.id}</div>
              <DetailList className="mt-2">
                <DetailRow label={t('node_issues.table.code')} mono hidden={!mixedCodes}>{issue.code}</DetailRow>
                <DetailRow label={t('node_issues.table.key')} mono>{issue.key || '—'}</DetailRow>
                <DetailRow label={t('node_issues.table.first_seen')}><TimeCell value={issue.first_seen_at} /></DetailRow>
                <DetailRow label={t('node_issues.table.last_seen')}><TimeCell value={issue.last_seen_at} /></DetailRow>
                <DetailRow label={t('node_issues.reviewed_at')} hidden={!issue.acknowledged_at}><TimeCell value={issue.acknowledged_at} /></DetailRow>
              </DetailList>
              <CodeSection title={t('node_issues.table.detail')} text={issue.detail || '—'} name={t('node_issues.table.detail')} />
            </div>
          ))}
        </div>
      </details>
    </>
  )
}
