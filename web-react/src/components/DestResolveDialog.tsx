import { useEffect, useState } from 'react'
import {
  Alert, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, Radio, RadioGroup, Typography, useMediaQuery, useTheme,
} from '@mui/material'
import { useTranslation } from 'react-i18next'

import type { DestNodeStatus, DestResolve } from '@/api/accessControl'
import { AsyncButton } from '@/components/AsyncButton'
import { ResolveUnavailableBadge } from '@/components/NodePolicyStatusRow'
import { pushSnack } from '@/components/SnackbarHost'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useDestStatus, useSetDestResolve } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes, nodeResolve } from '@/utils/accessControl'
import { discardChangesConfirm } from '@/views/admin/accessControl/confirmCopy'
import { destErrorText } from '@/views/admin/accessControl/destErrors'

const A = 'admin:access_control.dest_resolve.'

/**
 * The third row is the engine's IPIfNonMatch. It is listed so an admin who knows
 * the Xray setting finds it and reads why it is absent; it can never be chosen,
 * and the API refuses it.
 */
type Row = DestResolve | 'if_non_match'
const ROWS: Row[] = ['as_is', 'on_demand', 'if_non_match']
const CAVEATS = ['fail_open_closed', 'dial_again', 'route_only'] as const

/**
 * "How do the IP rules treat a host name?" (per native node): the one place a
 * server's hostname-matching mode is written, shared by the coverage sheet and
 * the server's access dialog. The restart sentence beside Save is the warning,
 * so there is no confirmation after it. Where a node set to on_demand runs
 * as_is anyway (too old, or an inbound that sniffs with routeOnly), the reason
 * is said here, not left for the admin to infer.
 */
export default function DestResolveDialog({ node, onClose }: {
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
  const save = useSetDestResolve(scope)
  const view = node ? nodeResolve(node) : null
  const stored: DestResolve = view?.mode ?? 'as_is'
  const [mode, setMode] = useState<DestResolve>(stored)
  const [error, setError] = useState<string | null>(null)
  const dirty = open && mode !== stored

  // Seeded on each open: a node's mode read while the dialog is open never overwrites a choice in progress.
  useEffect(() => { if (open) { setMode(stored); setError(null) } }, [open, node?.panel_id]) // eslint-disable-line react-hooks/exhaustive-deps

  const guard = useDirtyClose(dirty, discardChangesConfirm(t))
  const minutes = applyMinutes(status.data)

  const submit = async () => {
    if (!node) return
    setError(null)
    try {
      await save.mutateAsync({ serverId: node.panel_id, resolve: mode })
      pushSnack(minutes === null
        ? t(`${A}saved_unknown`, { node: node.panel_name })
        : t(`${A}saved`, { node: node.panel_name, count: minutes }), 'success')
      onClose()
    } catch (err) {
      setError(destErrorText(t, err))
    }
  }

  const option = (value: Row) => {
    const unoffered = value === 'if_non_match'
    return (
      <FormControlLabel key={value} value={value} disabled={unoffered || save.isPending}
        sx={{ alignItems: 'flex-start', m: 0, p: 1, borderRadius: 2, border: `1px solid ${mode === value ? md.primary : md.outlineVariant}` }}
        control={<Radio />}
        label={<Typography variant="body2" sx={{ pt: 0.75, color: unoffered ? md.onSurfaceVariant : md.onSurface }}>{t(`${A}option.${value}`)}</Typography>} />
    )
  }

  return (
    <Dialog open={open} onClose={() => guard(onClose)} fullWidth maxWidth="sm" fullScreen={fullScreen}
      slotProps={{ paper: { sx: { bgcolor: md.surfaceContainerHigh, borderRadius: fullScreen ? 0 : 3 } } }}>
      <DialogTitle>{t(`${A}title`)}</DialogTitle>
      <DialogContent sx={{ display: 'grid', gap: 1.5 }}>
        {node && <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{node.panel_name}</Typography>}
        {view?.unavailable && <Box><ResolveUnavailableBadge reason={view.unavailable} /></Box>}
        {error && <Alert severity="error">{error}</Alert>}
        <RadioGroup value={mode} onChange={(_, v) => { if (v === 'as_is' || v === 'on_demand') setMode(v) }} aria-label={t(`${A}label`)} sx={{ gap: 1 }}>
          {ROWS.map(option)}
        </RadioGroup>
        <Box data-testid="resolve-cost" component="ul" sx={{ m: 0, pl: 2.5, display: 'grid', gap: 0.5, color: md.onSurfaceVariant }}>
          {(['xray', 'singbox'] as const).map(e => <Typography key={e} component="li" variant="body2">{t(`${A}cost.${e}`)}</Typography>)}
        </Box>
        <Box data-testid="resolve-caveats" component="ul" sx={{ m: 0, pl: 2.5, display: 'grid', gap: 0.5, color: md.onSurfaceVariant }}>
          {CAVEATS.map(c => <Typography key={c} component="li" variant="body2">{t(`${A}caveat.${c}`)}</Typography>)}
        </Box>
      </DialogContent>
      <DialogActions sx={{ flexDirection: 'column', alignItems: 'stretch', gap: 1, p: 2 }}>
        <Typography variant="caption" sx={{ color: md.onSurfaceVariant }}>{t(`${A}restart`)}</Typography>
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 1 }}>
          <Button onClick={() => guard(onClose)}>{t(`${A}cancel`)}</Button>
          <AsyncButton variant="contained" disabled={!dirty} onClick={submit}>{t(`${A}save`)}</AsyncButton>
        </Box>
      </DialogActions>
    </Dialog>
  )
}
