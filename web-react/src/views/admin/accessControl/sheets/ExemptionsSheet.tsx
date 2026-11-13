import { useMemo, useState } from 'react'
import { Alert, Box, Button, IconButton, Menu, MenuItem, Skeleton, Tooltip, Typography, useTheme } from '@mui/material'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import { useTranslation } from 'react-i18next'

import { type DestExemption } from '@/api/accessControl'
import ToneBadge from '@/components/ToneBadge'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { useDeleteDestExemption, useDestExemptions, useDestStatus } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { accessTone, applyMinutes, type AccessToneKind } from '@/utils/accessControl'
import { formatRelativeTimeShort } from '@/utils/relativeTime'
import { timeLeft } from '../timeLeft'
import { removeExemptionConfirm } from '../confirmCopy'
import { reportDestError } from '../destErrors'
import AddExemptionDialog from './AddExemptionDialog'
import SheetDrawer from './SheetDrawer'

export interface ExemptionsSheetProps {
  open: boolean
  onClose: () => void
  /** Hands over to the account drawer: replaces `sheet=exemptions` with `user=<id>`. */
  onOpenUser: (userId: number) => void
}

const X = 'admin:access_control.exemptions.'
const HOUR = 3_600_000

/** S5: every exemption in one place, each with who added it, why and when it ends. */
export default function ExemptionsSheet({ open, onClose, onOpenUser }: ExemptionsSheetProps) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const scope = useQueryScope()
  const q = useDestExemptions(scope, { enabled: open })
  const status = useDestStatus(scope, { enabled: open })
  const remove = useDeleteDestExemption(scope)
  const [menu, setMenu] = useState<{ el: HTMLElement; row: DestExemption } | null>(null)
  const [dialog, setDialog] = useState<{ open: boolean; existing?: DestExemption }>({ open: false })
  const now = Date.now()

  // Expired rows wait for the hourly cleanup: last, in the measuring tone.
  const rows = useMemo(() => [...(q.data ?? [])].sort((a, b) => Number(a.expired) - Number(b.expired)), [q.data])

  const tone = (r: DestExemption): { kind: AccessToneKind; label: string } => {
    if (r.expired) return { kind: 'measuring', label: t(`${X}expired`) }
    if (r.expires_at === null) return { kind: 'permanent', label: t(`${X}permanent`) }
    const ms = r.expires_at - now
    return { kind: ms < 24 * HOUR ? 'attention' : 'none', label: t(`${X}expires_in`, { left: timeLeft(t, ms) }) }
  }

  const doRemove = async (row: DestExemption) => {
    const ok = await confirm(removeExemptionConfirm({ t, minutes: applyMinutes(status.data) }, row.upn))
    if (!ok) return
    try {
      await remove.mutateAsync(row.user_id)
      pushSnack(t(`${X}removed_snack`, { upn: row.upn }), 'success')
    } catch (err) {
      reportDestError(t, err)
    }
  }

  return (
    <>
      <SheetDrawer open={open} onClose={onClose} title={t(`${X}title`)} headingId="exemptions-heading">
        <Typography variant="body2" sx={{ color: md.onSurfaceVariant, mb: 1.5 }}>{t(`${X}lead`)}</Typography>
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', mb: 1.5 }}>
          <Button variant="contained" size="small" onClick={() => setDialog({ open: true })}>{t(`${X}add`)}</Button>
        </Box>
        {q.isPending && [0, 1, 2].map(i => <Skeleton key={i} variant="rounded" height={64} sx={{ mb: 1 }} />)}
        {q.isError && (
          <Alert severity="error" action={<Button color="inherit" size="small" onClick={() => void q.refetch()}>{t('admin:access_control.retry')}</Button>}>
            {t(`${X}load_failed`)}
          </Alert>
        )}
        {q.isSuccess && rows.length === 0 && (
          <Box sx={{ p: 2, borderRadius: 2, bgcolor: md.surfaceContainerHigh }}>
            <Typography variant="body2">{t(`${X}empty`)}</Typography>
          </Box>
        )}
        {rows.length > 0 && (
          <Box sx={{ borderRadius: 3, border: `1px solid ${md.outlineVariant}`, bgcolor: md.surfaceContainerLow, overflow: 'hidden' }}>
            {rows.map((r, i) => {
              const tn = tone(r)
              return (
                <Box key={r.user_id} data-testid="exemption-row"
                  sx={{ p: 1.5, display: 'flex', gap: 1, alignItems: 'flex-start', borderTop: i ? `1px solid ${md.outlineVariant}` : 'none' }}>
                  <Box sx={{ flex: 1, minWidth: 0 }}>
                    <Typography sx={{ fontWeight: 600, overflowWrap: 'anywhere' }}>{r.upn || `#${r.user_id}`}</Typography>
                    <Typography variant="body2" sx={{ color: md.onSurfaceVariant, overflowWrap: 'anywhere' }}>
                      {t(`${X}reason_line`, { reason: r.reason, by: r.created_by_upn || '—', ago: formatRelativeTimeShort(now - r.created_at, t) })}
                    </Typography>
                  </Box>
                  <Tooltip title={t(`${X}expiry_tip`)}>
                    <Box><ToneBadge tone={accessTone(theme, tn.kind)} label={tn.label} data={tn.kind} /></Box>
                  </Tooltip>
                  <IconButton size="small" aria-label={t(`${X}row_menu`)} onClick={e => setMenu({ el: e.currentTarget, row: r })}
                    sx={{ minWidth: 44, minHeight: 44 }}>
                    <MoreVertIcon />
                  </IconButton>
                </Box>
              )
            })}
          </Box>
        )}
        <Menu anchorEl={menu?.el} open={menu !== null} onClose={() => setMenu(null)}>
          <MenuItem onClick={() => { const r = menu!.row; setMenu(null); onOpenUser(r.user_id) }}>{t(`${X}open_user`)}</MenuItem>
          <MenuItem onClick={() => { const r = menu!.row; setMenu(null); setDialog({ open: true, existing: r }) }}>{t(`${X}edit`)}</MenuItem>
          <MenuItem sx={{ color: md.error }} onClick={() => { const r = menu!.row; setMenu(null); void doRemove(r) }}>{t(`${X}cancel`)}</MenuItem>
        </Menu>
      </SheetDrawer>
      <AddExemptionDialog open={dialog.open} existing={dialog.existing} onClose={() => setDialog({ open: false })} />
    </>
  )
}
