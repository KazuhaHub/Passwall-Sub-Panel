import { useState, type ReactNode } from 'react'
import {
  Alert, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, LinearProgress, Typography, useMediaQuery, useTheme,
} from '@mui/material'
import CancelOutlinedIcon from '@mui/icons-material/CancelOutlined'
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutlineOutlined'
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import { useTranslation } from 'react-i18next'

import { destErrorOf, type DestRef } from '@/api/accessControl'
import { AsyncButton } from '@/components/AsyncButton'
import { amber } from '@/components/ToneBadge'
import { pushSnack } from '@/components/SnackbarHost'
import { useDestGroup, useDestGroupPreview, useDestStatus } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes } from '@/utils/accessControl'
import { minutesWords } from '../confirmCopy'
import { destErrorText } from '../destErrors'
import { blockedByLists } from './allowlistState'

const K = 'admin:access_control.allowlist.turn_on.'

type Tone = 'ok' | 'failing' | 'attention' | 'quiet'

function Check({ id, tone, children, action }: { id: string; tone: Tone; children: ReactNode; action?: ReactNode }) {
  const theme = useTheme()
  const md = theme.palette.md
  const icon = {
    ok: <CheckCircleOutlineIcon aria-hidden sx={{ fontSize: 20, color: theme.palette.success.main }} />,
    failing: <CancelOutlinedIcon aria-hidden sx={{ fontSize: 20, color: md.error }} />,
    attention: <WarningAmberIcon aria-hidden sx={{ fontSize: 20, color: amber(theme).fg }} />,
    quiet: <InfoOutlinedIcon aria-hidden sx={{ fontSize: 20, color: md.onSurfaceVariant }} />,
  }[tone]
  return (
    <Box component="li" data-testid={`check-${id}`} data-tone={tone} sx={{ display: 'flex', gap: 1, alignItems: 'flex-start', flexWrap: 'wrap' }}>
      <Box sx={{ mt: '1px' }}>{icon}</Box>
      <Typography variant="body2" sx={{ flex: '1 1 200px', minWidth: 0, overflowWrap: 'anywhere', color: tone === 'quiet' ? md.onSurfaceVariant : md.onSurface }}>{children}</Typography>
      {action}
    </Box>
  )
}

export interface TurnOnDialogProps {
  open: boolean
  group: { id: number; name: string; members: number }
  /** The lists the allowlist will be made of (the chosen ones, saved or not). */
  listIds: readonly number[]
  /** The lists as saved, when the lists being turned on differ from them (an edit): what is saved no longer decides. Default: the same. */
  savedListIds?: readonly number[]
  /** The base hosts about to be saved with a group's first allowlist; with none yet the saved row says. */
  baseCount?: number
  /** The group already has an allowlist (a report to look at); a first one has none. */
  hasRow?: boolean
  /** Which day of report-only the group is on; null when it never was (no data to look at). */
  reportDay: number | null
  /** Makes the one write. A rejection is explained here. */
  onConfirm: () => Promise<void>
  onClose: () => void
  onViewReport: () => void
  onViewNodes: () => void
}

/**
 * TURNING AN ALLOWLIST ON (S11). Turning on is the one change that can leave
 * members unable to reach anything, so it shows a checklist first. Only a list
 * that has not been downloaded stops it (the server refuses it too, 409
 * dest_list_not_ready); everything else is a note the admin may weigh (D3).
 * The write itself is the caller's: this dialog asks once and says why a
 * refusal came back.
 */
export default function TurnOnDialog({ open, group, listIds, savedListIds, baseCount, hasRow = true, reportDay, onConfirm, onClose, onViewReport, onViewNodes }: TurnOnDialogProps) {
  const { t, i18n } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const phone = useMediaQuery(theme.breakpoints.down('sm'))
  const scope = useQueryScope()
  const status = useDestStatus(scope, { enabled: open })
  const detail = useDestGroup(scope, group.id, { enabled: open })
  const preview = useDestGroupPreview(scope, group.id, 'on', listIds, { enabled: open })
  const [refusal, setRefusal] = useState<string | null>(null)
  const num = new Intl.NumberFormat(i18n.language)

  const r = detail.data?.readiness
  const reading = detail.isPending
  const failed = detail.isError
  // The saved readiness speaks of the saved lists; once the lists were edited only what is chosen counts.
  const sameLists = !savedListIds || (savedListIds.length === listIds.length && savedListIds.every(id => listIds.includes(id)))
  const notReady: DestRef[] = []
  for (const l of [...(sameLists ? r?.not_ready_lists ?? [] : []), ...(preview.data?.not_ready_lists ?? [])]) {
    if (!notReady.some(x => x.id === l.id)) notReady.push(l)
  }
  const blocked = blockedByLists(notReady)
  const names = notReady.map(l => l.name).join(t(`${K}list_separator`))
  const topSites = (r?.top_sites ?? []).filter(s => !s.added).slice(0, 3).map(s => s.site).join(t(`${K}list_separator`))

  const confirm = async () => {
    setRefusal(null)
    try {
      await onConfirm()
    } catch (err) {
      const e = destErrorOf(err)
      if (e?.code === 'dest_group_stale') {
        pushSnack(t(`${K}stale`), 'warning')
        onClose()
      } else if (e?.code === 'dest_list_not_ready') {
        const list = ((e.body.not_ready_lists as DestRef[] | undefined) ?? []).map(l => l.name).filter(Boolean).join(t(`${K}list_separator`)) || names
        setRefusal(t(`${K}not_ready`, { list }))
      } else {
        setRefusal(destErrorText(t, err))
      }
    }
  }

  return (
    <Dialog open={open} onClose={onClose} fullWidth maxWidth="sm" fullScreen={phone}
      slotProps={{ paper: { sx: { bgcolor: md.surfaceContainerHigh, borderRadius: phone ? 0 : 3 } } }}>
      <DialogTitle>{t(`${K}title`, { group: group.name })}</DialogTitle>
      <DialogContent>
        {reading && <LinearProgress aria-label={t(`${K}reading`)} sx={{ mb: 1.5 }} />}
        {failed && (
          <Alert severity="error" sx={{ mb: 1.5 }} action={<Button color="inherit" size="small" onClick={() => void detail.refetch()}>{t('admin:access_control.retry')}</Button>}>
            {t(`${K}read_failed`)}
          </Alert>
        )}
        {r && (
          <Box component="ul" sx={{ listStyle: 'none', m: 0, p: 0, display: 'grid', gap: 1, mb: 2 }}>
            {blocked
              ? <Check id="lists" tone="failing">{t(`${K}not_ready`, { list: names })}</Check>
              : <Check id="lists" tone="ok">{t(`${K}lists_ready`)}</Check>}
            {r.unapplied_nodes > 0 && (
              <Check id="unapplied" tone="attention"
                action={<Button size="small" onClick={onViewNodes}>{t(`${K}view`)}</Button>}>
                {t(`${K}unapplied`, { count: r.unapplied_nodes })}
              </Check>
            )}
            {(baseCount !== undefined ? baseCount > 0 : r.base_nonempty)
              ? <Check id="base" tone="ok">{t(`${K}base_ok`)}</Check>
              : <Check id="base" tone="attention">{t(`${K}base_empty`)}</Check>}
            {r.remaining_sites_24h > 0
              ? (
                <Check id="remaining" tone="attention" action={hasRow ? <Button size="small" onClick={onViewReport}>{t(`${K}view_report`)}</Button> : undefined}>
                  {t(`${K}remaining`, { count: r.remaining_sites_24h, top: topSites })}
                </Check>
              )
              : <Check id="remaining" tone="ok">{t(`${K}remaining_none`)}</Check>}
            {reportDay !== null
              ? <Check id="age" tone="quiet">{t(`${K}age`, { days: reportDay })}</Check>
              : <Check id="age" tone="quiet">{t(`${K}age_none`)}</Check>}
          </Box>
        )}
        <Box sx={{ p: 1.5, borderRadius: 2, bgcolor: md.errorContainer, color: md.onErrorContainer }}>
          <Typography variant="body2">{t(`${K}consequence`, { members: num.format(group.members), minutes: minutesWords(t, applyMinutes(status.data)) })}</Typography>
          {(preview.data?.nodes_lost.length ?? 0) > 0 && (
            <Typography variant="body2" sx={{ mt: 0.5 }}>{t(`${K}lost`, { count: preview.data?.nodes_lost.length })}</Typography>
          )}
        </Box>
        {refusal && <Alert severity="error" sx={{ mt: 1.5 }}>{refusal}</Alert>}
      </DialogContent>
      <DialogActions sx={{ ...(phone ? { position: 'sticky', bottom: 0, bgcolor: md.surfaceContainerHigh } : {}) }}>
        <Button onClick={onClose}>{t(`${K}cancel`)}</Button>
        <AsyncButton variant="contained" color="error" disabled={reading || failed || blocked} onClick={confirm}>{t(`${K}confirm`)}</AsyncButton>
      </DialogActions>
    </Dialog>
  )
}
