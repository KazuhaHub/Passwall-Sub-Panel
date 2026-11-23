import { useState } from 'react'
import { Link as RouterLink, useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'

import type { DestNodeStatus } from '@/api/accessControl'
import AuditCollectDialog from '@/components/AuditCollectDialog'
import DestResolveDialog from '@/components/DestResolveDialog'
import NodePolicyStatusRow, { ResolveUnavailableBadge } from '@/components/NodePolicyStatusRow'
import { KpiGrid, KpiTile } from '@/components/common/KpiTile'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { IconChevronRight } from '@/lib/icons'
import { useDestSettings, useRetryDestAgent } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { nodeRecording, nodeResolve } from '@/utils/accessControl'
import { reportDestError, reportRetry } from '../../accessControl/destErrors'
import { recordsHref } from '../../accessControl/records/recordsParams'

/**
 * One native node's access-control state, opened from the Servers page's
 * "Access control: …" line: the state line and its problems, then what the
 * node records and how many hits it reported in the last 24 hours. The state
 * is drawn by the one NodePolicyStatusRow the coverage sheet uses; the
 * recording mode is set in the one AuditCollectDialog and how IP rules treat
 * host names in the one DestResolveDialog.
 */
export default function ServerAccessDialog({ node, serverName, onClose }: {
  /** The node's delivery state, or null (closed). */
  node: DestNodeStatus | null
  serverName: string
  onClose: () => void
}) {
  const { t } = useTranslation(['admin'])
  const scope = useQueryScope()
  const navigate = useNavigate()
  const retry = useRetryDestAgent(scope)
  const settings = useDestSettings(scope, { enabled: node !== null })
  const [recordingFor, setRecordingFor] = useState<DestNodeStatus | null>(null)
  const [resolveFor, setResolveFor] = useState<DestNodeStatus | null>(null)
  const rec = node ? nodeRecording(node) : null
  const res = node ? nodeResolve(node) : null
  const days = settings.data?.effective.dest_hit_retention_days
  const usageDays = settings.data?.effective.dest_usage_retention_days
  const recordsHits = rec?.mode === 'hits' || rec?.mode === 'hits_and_usage'
  const num = new Intl.NumberFormat()
  const R = 'admin:servers.access.'
  // The two editors are still separate modal dialogs of their own. Two modals open over each other fight for focus
  // and count a click in the upper one as outside the lower one, so this dialog steps aside while one of them is
  // open and returns, unchanged, when it closes.
  const editing = recordingFor !== null || resolveFor !== null

  return (
    <>
      <Dialog open={node !== null && !editing} onOpenChange={open => { if (!open) onClose() }}>
        <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>{t('admin:servers.access.dialog_title', { name: serverName })}</DialogTitle>
          </DialogHeader>
          {node && (
            <div className="min-w-0">
              <NodePolicyStatusRow node={node} variant="block"
                onOpenInbound={id => { onClose(); void navigate(`/admin/nodes?inbound=${id}`) }}
                onRetry={async id => { try { reportRetry(t, await retry.mutateAsync(id)) } catch (err) { reportDestError(t, err) } }}
                onUpgrade={onClose} onOpenLists={() => { onClose(); void navigate('/admin/access-control?tab=lists') }} />
              {(rec || res) && (
                <KpiGrid>
                  {rec && <div data-testid="server-recording" className="min-w-0">
                    <KpiTile label={t(`${R}recording`)}
                      value={rec.mode === 'exec_only' ? t(`${R}recording_exec_only`) : rec.mode === 'too_old' ? t(`${R}recording_too_old`) : t(`${R}recording_${rec.mode}`)}
                      caption={(
                        <span className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
                          {recordsHits && days !== undefined && <span>{t(`${R}retention`, { days })}</span>}
                          {rec.mode === 'hits_and_usage' && usageDays !== undefined && <span>{t(`${R}usage_retention`, { days: usageDays })}</span>}
                          <Button variant="link" size="xs" className="h-auto p-0" onClick={() => setRecordingFor(node)}>{t(`${R}change`)}</Button>
                        </span>
                      )} />
                  </div>}
                  {res && (
                    <div data-testid="server-resolve" className="min-w-0">
                      <KpiTile label={t(`${R}resolve`)} value={t(`admin:access_control.node_resolve.mode.${res.effective}`)}
                        caption={(
                          <span className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
                            {res.unavailable && <ResolveUnavailableBadge reason={res.unavailable} />}
                            <Button variant="link" size="xs" className="h-auto p-0" onClick={() => setResolveFor(node)}>{t(`${R}change`)}</Button>
                          </span>
                        )} />
                    </div>
                  )}
                  {rec && recordsHits && (
                    <div data-testid="server-hits" className="min-w-0">
                      <KpiTile label={t(`${R}hits_24h`)} value={rec.hits24h === null ? '—' : num.format(rec.hits24h)}
                        caption={(
                          <Button variant="link" size="xs" className="h-auto p-0" onClick={() => { onClose(); void navigate(`/admin/access-control${recordsHref({ panel_id: node.panel_id })}`) }}>
                            {t(`${R}view_records`)}<IconChevronRight aria-hidden />
                          </Button>
                        )} />
                    </div>
                  )}
                </KpiGrid>
              )}
              {rec && recordsHits && rec.hits24h === null && (
                <p className="text-xs text-muted-foreground">{t(`${R}hits_none_note`)}</p>
              )}
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={onClose}>{t('admin:servers.access.close')}</Button>
            <Button render={<RouterLink to="/admin/access-control?sheet=nodes" onClick={onClose} />}>
              {t('admin:servers.access.open_page')}<IconChevronRight aria-hidden />
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <AuditCollectDialog node={recordingFor} onClose={() => setRecordingFor(null)} />
      <DestResolveDialog node={resolveFor} onClose={() => setResolveFor(null)} />
    </>
  )
}
