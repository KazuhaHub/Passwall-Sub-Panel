import { useState } from 'react'
import { Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, Typography, useMediaQuery, useTheme } from '@mui/material'
import ChevronRightIcon from '@mui/icons-material/ChevronRight'
import { Link as RouterLink, useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'

import type { DestNodeStatus } from '@/api/accessControl'
import AuditCollectDialog from '@/components/AuditCollectDialog'
import DestResolveDialog from '@/components/DestResolveDialog'
import KpiTile, { KpiGrid } from '@/components/KpiTile'
import NodePolicyStatusRow, { ResolveUnavailableBadge } from '@/components/NodePolicyStatusRow'
import { useDestSettings, useRetryDestAgent } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { nodeRecording, nodeResolve } from '@/utils/accessControl'
import { reportDestError, reportRetry } from './accessControl/destErrors'
import { recordsHref } from './accessControl/records/recordsParams'

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
  const theme = useTheme()
  const fullScreen = useMediaQuery(theme.breakpoints.down('sm'))
  const scope = useQueryScope()
  const navigate = useNavigate()
  const retry = useRetryDestAgent(scope)
  const settings = useDestSettings(scope, { enabled: node !== null })
  const [recordingFor, setRecordingFor] = useState<DestNodeStatus | null>(null)
  const [resolveFor, setResolveFor] = useState<DestNodeStatus | null>(null)
  const rec = node ? nodeRecording(node) : null
  const res = node ? nodeResolve(node) : null
  const days = settings.data?.effective.dest_hit_retention_days
  const num = new Intl.NumberFormat()
  const R = 'admin:servers.access.'

  return (
    <Dialog open={node !== null} onClose={onClose} fullWidth maxWidth="sm" fullScreen={fullScreen}
      slotProps={{ paper: { sx: { bgcolor: theme.palette.md.surfaceContainerHigh, borderRadius: fullScreen ? 0 : 3 } } }}>
      <DialogTitle>{t('admin:servers.access.dialog_title', { name: serverName })}</DialogTitle>
      <DialogContent>
        {node && (
          <Box>
            <NodePolicyStatusRow node={node} variant="block"
              onOpenInbound={id => { onClose(); void navigate(`/admin/nodes?inbound=${id}`) }}
              onRetry={async id => { try { reportRetry(t, await retry.mutateAsync(id)) } catch (err) { reportDestError(t, err) } }}
              onUpgrade={onClose} onOpenLists={() => { onClose(); void navigate('/admin/access-control?tab=lists') }} />
            {(rec || res) && (
              <KpiGrid>
                {rec && <Box data-testid="server-recording">
                  <KpiTile label={t(`${R}recording`)}
                    value={rec.mode === 'exec_only' ? t(`${R}recording_exec_only`) : rec.mode === 'too_old' ? t(`${R}recording_too_old`) : t(`${R}recording_${rec.mode}`)}
                    caption={(
                      <Box component="span" sx={{ display: 'flex', alignItems: 'center', gap: 0.5, flexWrap: 'wrap' }}>
                        {rec.mode === 'hits' && days !== undefined && <span>{t(`${R}retention`, { days })}</span>}
                        <Button size="small" onClick={() => setRecordingFor(node)}>{t(`${R}change`)}</Button>
                      </Box>
                    )} />
                </Box>}
                {res && (
                  <Box data-testid="server-resolve">
                    <KpiTile label={t(`${R}resolve`)} value={t(`admin:access_control.node_resolve.mode.${res.effective}`)}
                      caption={(
                        <Box component="span" sx={{ display: 'flex', alignItems: 'center', gap: 0.5, flexWrap: 'wrap' }}>
                          {res.unavailable && <ResolveUnavailableBadge reason={res.unavailable} />}
                          <Button size="small" onClick={() => setResolveFor(node)}>{t(`${R}change`)}</Button>
                        </Box>
                      )} />
                  </Box>
                )}
                {rec && rec.mode === 'hits' && (
                  <Box data-testid="server-hits">
                    <KpiTile label={t(`${R}hits_24h`)} value={rec.hits24h === null ? '—' : num.format(rec.hits24h)}
                      caption={<Button size="small" endIcon={<ChevronRightIcon />} onClick={() => { onClose(); void navigate(`/admin/access-control${recordsHref({ panel_id: node.panel_id })}`) }}>{t(`${R}view_records`)}</Button>} />
                  </Box>
                )}
              </KpiGrid>
            )}
            {rec && rec.mode === 'hits' && rec.hits24h === null && (
              <Typography variant="caption" sx={{ color: theme.palette.md.onSurfaceVariant }}>{t(`${R}hits_none_note`)}</Typography>
            )}
          </Box>
        )}
      </DialogContent>
      <DialogActions>
        <Button component={RouterLink} to="/admin/access-control?sheet=nodes" endIcon={<ChevronRightIcon />} onClick={onClose}>
          {t('admin:servers.access.open_page')}
        </Button>
        <Button onClick={onClose}>{t('admin:servers.access.close')}</Button>
      </DialogActions>
      <AuditCollectDialog node={recordingFor} onClose={() => setRecordingFor(null)} />
      <DestResolveDialog node={resolveFor} onClose={() => setResolveFor(null)} />
    </Dialog>
  )
}
