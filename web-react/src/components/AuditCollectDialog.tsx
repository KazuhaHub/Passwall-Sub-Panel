import { useEffect, useState } from 'react'
import {
  Alert, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, Radio, RadioGroup, Typography, useMediaQuery, useTheme,
} from '@mui/material'
import { useTranslation } from 'react-i18next'

import type { AuditCollect, DestNodeStatus } from '@/api/accessControl'
import { AsyncButton } from '@/components/AsyncButton'
import { pushSnack } from '@/components/SnackbarHost'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useDestSettings, useDestStatus, useSetAuditCollect } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes } from '@/utils/accessControl'
import { discardChangesConfirm } from '@/views/admin/accessControl/confirmCopy'
import { destErrorText } from '@/views/admin/accessControl/destErrors'

const A = 'admin:access_control.audit_collect.'

/** What this build offers: not recording, or the rule matches. The per-site usage mode is a later phase's. */
const MODES: AuditCollect[] = ['off', 'hits']

/**
 * "What does this node record?" (S14/S15): the one place a native server's
 * recording mode is written. It is shared by the coverage sheet and the
 * server's access dialog. The restart sentence beside Save is the warning, so
 * there is no confirmation after it (S20). A node that cannot record (too old)
 * may only be set to not recording; a sing-box node only enforces, and its
 * dialog is read-only. The days a mode keeps its records are the retention
 * setting, read, never written in.
 */
export default function AuditCollectDialog({ node, onClose }: {
  /** The node being set, or null (closed). */
  node: DestNodeStatus | null
  onClose: () => void
}) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const fullScreen = useMediaQuery(theme.breakpoints.down('sm'))
  const scope = useQueryScope()
  const open = node !== null
  const status = useDestStatus(scope, { enabled: open })
  const settings = useDestSettings(scope, { enabled: open })
  const save = useSetAuditCollect(scope)
  // The server answers the normalised value (unset is the default, hits); the usage mode is shown as what it is, not as hits.
  const stored: AuditCollect = node?.collect === 'off' ? 'off' : node?.collect === 'hits_and_usage' ? 'hits_and_usage' : 'hits'
  const [mode, setMode] = useState<AuditCollect>(stored)
  const [error, setError] = useState<string | null>(null)
  const execOnly = node?.engine === 'sing-box'
  const tooOld = !!node && !node.supports.hits && !execOnly
  const dirty = open && mode !== stored

  // Seeded on each open: a node's mode read while the dialog is open never overwrites a choice in progress.
  useEffect(() => { if (open) { setMode(stored); setError(null) } }, [open, node?.panel_id]) // eslint-disable-line react-hooks/exhaustive-deps

  const guard = useDirtyClose(dirty, discardChangesConfirm(t))
  const days = settings.data?.effective.dest_hit_retention_days
  const minutes = applyMinutes(status.data)

  const submit = async () => {
    if (!node) return
    setError(null)
    try {
      await save.mutateAsync({ serverId: node.panel_id, collect: mode })
      pushSnack(minutes === null
        ? t(`${A}saved_unknown`, { node: node.panel_name })
        : t(`${A}saved`, { node: node.panel_name, count: minutes }), 'success')
      onClose()
    } catch (err) {
      setError(destErrorText(t, err))
    }
  }

  const option = (value: AuditCollect) => {
    const disabled = execOnly || (tooOld && value !== 'off') || save.isPending || value === 'hits_and_usage'
    return (
      <FormControlLabel key={value} value={value} disabled={disabled} sx={{ alignItems: 'flex-start', m: 0, p: 1, borderRadius: 2, border: `1px solid ${mode === value ? md.primary : md.outlineVariant}` }}
        control={<Radio />}
        label={(
          <Box sx={{ pt: 0.75 }}>
            <Typography sx={{ fontWeight: 600, fontSize: 14 }}>{t(`${A}mode.${value}`)}</Typography>
            <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${A}mode_help.${value}`, { days: days ?? '…' })}</Typography>
            {value === 'hits' && tooOld && <Typography variant="body2" sx={{ color: md.onSurface }}>{t(`${A}too_old`)}</Typography>}
          </Box>
        )} />
    )
  }

  return (
    <Dialog open={open} onClose={() => guard(onClose)} fullWidth maxWidth="sm" fullScreen={fullScreen}
      slotProps={{ paper: { sx: { bgcolor: md.surfaceContainerHigh, borderRadius: fullScreen ? 0 : 3 } } }}>
      <DialogTitle>{t(`${A}title`, { name: node?.panel_name ?? '' })}</DialogTitle>
      <DialogContent sx={{ display: 'grid', gap: 1.5 }}>
        {execOnly && <Typography variant="body2">{t(`${A}exec_only`)}</Typography>}
        {error && <Alert severity="error">{error}</Alert>}
        <RadioGroup value={mode} onChange={(_, v) => setMode(v as AuditCollect)} aria-label={t(`${A}label`)} sx={{ gap: 1 }}>
          {/* A node already set to the usage mode (which this build cannot offer) shows it, so it is not mistaken for "hits". */}
          {(stored === 'hits_and_usage' ? [...MODES, 'hits_and_usage' as const] : MODES).map(option)}
        </RadioGroup>
      </DialogContent>
      <DialogActions sx={{ flexDirection: 'column', alignItems: 'stretch', gap: 1, p: 2 }}>
        {!execOnly && <Typography variant="caption" sx={{ color: md.onSurfaceVariant }}>{t(`${A}restart`)}</Typography>}
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 1 }}>
          <Button onClick={() => guard(onClose)}>{t(`${A}cancel`)}</Button>
          {!execOnly && <AsyncButton variant="contained" disabled={!dirty} onClick={submit}>{t(`${A}save`)}</AsyncButton>}
        </Box>
      </DialogActions>
    </Dialog>
  )
}
