import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'

import type { DestNodeStatus } from '@/api/accessControl'
import AuditCollectDialog from '@/components/AuditCollectDialog'
import { KpiGrid, KpiTile } from '@/components/common/KpiTile'
import { MoreMenu, type MoreMenuItem } from '@/components/common/MoreMenu'
import { DetailDialog } from '@/components/data/DetailDialog'
import DestResolveDialog from '@/components/DestResolveDialog'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import NodePolicyStatusRow from '@/components/NodePolicyStatusRow'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { IconInfo } from '@/lib/icons'
import { useDestSettings, useDestStatus, useRetryDestAgent } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { isNativeKind, NODE_FILTERS, NODE_STATE_FILTER, nodeStateKey } from '@/utils/accessControl'
import { reportDestError, reportRetry } from '../destErrors'
import type { NodeFilter } from '../accessParams'

export interface CoverageSheetProps {
  open: boolean
  onClose: () => void
  /** The `node_state` filter, from the URL. */
  filter: NodeFilter | null
  onFilterChange: (next: NodeFilter | null) => void
  onOpenSettings: (focus?: 'apply' | 'refresh' | 'rules') => void
}

const C = 'admin:access_control.coverage.'

/**
 * S14: the whole fleet, one row per node, with where it stands, what to do next
 * and what it records. The rows are NodePolicyStatusRow, the one node-state
 * component; the recording mode is set in AuditCollectDialog and how IP rules
 * treat host names in DestResolveDialog (both shared with the servers page)
 * from a row's menu.
 */
export default function CoverageSheet({ open, onClose, filter, onFilterChange, onOpenSettings }: CoverageSheetProps) {
  const { t } = useTranslation(['admin'])
  const navigate = useNavigate()
  const scope = useQueryScope()
  const status = useDestStatus(scope, { enabled: open })
  const settings = useDestSettings(scope, { enabled: open })
  const retry = useRetryDestAgent(scope)
  const [recordingFor, setRecordingFor] = useState<DestNodeStatus | null>(null)
  const [resolveFor, setResolveFor] = useState<DestNodeStatus | null>(null)
  const readAt = status.dataUpdatedAt
  const now = useMemo(() => Date.now(), [readAt]) // eslint-disable-line react-hooks/exhaustive-deps

  const nodes = useMemo(() => status.data?.nodes ?? [], [status.data])
  const counts = useMemo(() => {
    const c: Record<NodeFilter, number> = { applied: 0, pending: 0, problem: 0, upgrade: 0, inactive: 0 }
    for (const n of nodes) c[NODE_STATE_FILTER[nodeStateKey(n, now)]]++
    return c
  }, [nodes, now])
  const shown = filter ? nodes.filter(n => NODE_STATE_FILTER[nodeStateKey(n, now)] === filter) : nodes

  const doRetry = async (agentId: string) => {
    try {
      reportRetry(t, await retry.mutateAsync(agentId))
    } catch (err) {
      reportDestError(t, err)
    }
  }
  const toServer = (n: DestNodeStatus) => void navigate(`/admin/servers?q=${encodeURIComponent(n.panel_name)}`)
  const release = status.data?.geosite_release
  const usageCount = status.data ? status.data.nodes.filter(n => n.collecting_usage).length : 0
  // A node on the usage mode is counted on its own line, not as one that records rule matches only.
  const recordingCount = status.data ? status.data.nodes.filter(n => n.collecting && !n.collecting_usage).length : 0

  const menuOf = (n: DestNodeStatus): MoreMenuItem[] => {
    const settable = isNativeKind(n.kind) && n.state !== 'unsupported_kind'
    return [
      ...(settable ? [
        { id: 'recording', label: t(`${C}set_recording`), onSelect: () => setRecordingFor(n) },
        { id: 'resolve', label: t(`${C}set_resolve`), onSelect: () => setResolveFor(n) },
      ] : []),
      { id: 'server', label: t('admin:access_control.node_action.open_server'), disabled: n.kind !== 'psp', onSelect: () => toServer(n) },
      { id: 'issues', label: t('admin:access_control.node_action.node_issues'), onSelect: () => void navigate(`/admin/node-issues?agent=${encodeURIComponent(n.agent_id)}`) },
    ]
  }

  return (
    <>
      <DetailDialog open={open} onClose={onClose} size="lg"
        title={t(`${C}title`, { n: status.data ? nodes.length : '…' })}>
        {status.isPending && <div className="grid gap-2">{[0, 1, 2, 3, 4, 5].map(i => <Skeleton key={i} className="h-16 w-full" />)}</div>}
        {status.isError && <ErrorState compact title={t('admin:access_control.verdict.read_failed')} reason={status.error} retry={() => status.refetch()} />}
        {status.data && (
          <>
            <div data-testid="recording-note" className="mb-2 flex items-start gap-2 rounded-lg bg-muted p-3">
              <IconInfo aria-hidden className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
              <div className="grid gap-1 text-sm">
                <p>
                  {recordingCount > 0
                    ? t(`${C}recording_note`, { count: recordingCount, days: settings.data?.effective.dest_hit_retention_days ?? '…' })
                    : usageCount > 0 ? t(`${C}recording_raw_log`) : t(`${C}recording_none`)}
                </p>
                {usageCount > 0 && (
                  <p data-testid="recording-usage-note">
                    {t(`${C}usage_note`, { count: usageCount, days: settings.data?.effective.dest_usage_retention_days ?? '…' })}
                  </p>
                )}
              </div>
            </div>
            <KpiGrid>
              {NODE_FILTERS.map(f => (
                <KpiTile key={f} label={t(`${C}filter.${f}`)} value={counts[f]} pressed={filter === f}
                  onToggle={() => onFilterChange(filter === f ? null : f)} />
              ))}
            </KpiGrid>
            {shown.length === 0 ? (
              <EmptyState compact title={t(`${C}empty`)}
                action={filter ? <Button size="sm" variant="outline" onClick={() => onFilterChange(null)}>{t(`${C}clear_filter`)}</Button> : undefined} />
            ) : (
              <ul className="divide-y rounded-lg border px-3">
                {shown.map(n => {
                  const oldData = !!n.rules_data_version && !!release && n.rules_data_version !== release
                  return (
                    <li key={n.agent_id || n.panel_id} className="flex items-start gap-1">
                      <div className="min-w-0 flex-1">
                        <NodePolicyStatusRow node={n} variant="row" now={now}
                          onRetry={doRetry}
                          onOpenInbound={id => void navigate(`/admin/nodes?inbound=${id}`)}
                          onUpgrade={() => toServer(n)}
                          onOpenLists={() => onClose()}
                          extra={(oldData || n.geoip_unavailable) ? (
                            <div className="mt-0.5 text-sm">
                              {oldData && <p>{t(`${C}old_data`, { version: n.rules_data_version })}</p>}
                              {n.geoip_unavailable && <p>{t(`${C}geoip_unavailable`)}</p>}
                            </div>
                          ) : undefined} />
                      </div>
                      <div className="mt-2"><MoreMenu label={t(`${C}row_menu`, { name: n.panel_name })} items={menuOf(n)} /></div>
                    </li>
                  )
                })}
              </ul>
            )}
            <p className="mt-4 text-sm text-muted-foreground">
              {t(`${C}apply_line`, { seconds: settings.data?.effective.dest_policy_apply_min_seconds ?? '…' })}{' '}
              <Button variant="link" size="sm" className="h-auto p-0 align-baseline" onClick={() => onOpenSettings('apply')}>{t(`${C}edit`)}</Button>
            </p>
          </>
        )}
      </DetailDialog>
      <AuditCollectDialog node={recordingFor} onClose={() => setRecordingFor(null)} />
      <DestResolveDialog node={resolveFor} onClose={() => setResolveFor(null)} />
    </>
  )
}
