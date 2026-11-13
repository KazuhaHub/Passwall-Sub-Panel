import { useState, type ReactNode } from 'react'
import { Alert, Box, Button, Skeleton, Tooltip, Typography, useTheme } from '@mui/material'
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined'
import { useTranslation } from 'react-i18next'

import type { DestExemption } from '@/api/accessControl'
import { AsyncButton } from '@/components/AsyncButton'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import ToneBadge from '@/components/ToneBadge'
import { useDeleteDestExemption, useDestStatus, useDestUser } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { accessTone, applyMinutes } from '@/utils/accessControl'
import { formatMsDualTz } from '@/utils/datetime'
import { formatRelativeTimeShort } from '@/utils/relativeTime'
import { timeLeft } from '../../accessControl/timeLeft'
import { removeExemptionConfirm } from '../../accessControl/confirmCopy'
import AddExemptionDialog from '../../accessControl/sheets/AddExemptionDialog'
import { reportDestError } from '../../accessControl/destErrors'

const D = 'admin:access_control.drawer.'

function Row({ label, children, action, tinted }: { label: string; children: ReactNode; action?: ReactNode; tinted?: boolean }) {
  const md = useTheme().palette.md
  return (
    <Box sx={{
      display: 'flex', alignItems: 'center', gap: 1.5, flexWrap: 'wrap', p: 1.5, borderRadius: 3,
      bgcolor: tinted ? md.surfaceContainerHigh : md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}`,
    }}>
      <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant, minWidth: 48 }}>{label}</Typography>
      <Box sx={{ flex: 1, minWidth: 0, fontSize: 14, overflowWrap: 'anywhere' }}>{children}</Box>
      {action}
    </Box>
  )
}

/**
 * The drawer's access tab (batch 1: the account's group and its exemption
 * only; hits and per-site usage come with later batches). Read only while the
 * tab is open — the parent mounts it on selection — and the exemption can be
 * added or lifted in place, through the same mutations the exemptions sheet uses.
 */
export default function AccessTab({ userId }: { userId: number }) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const scope = useQueryScope()
  const panelTz = useSiteStore(s => s.timezone)
  const q = useDestUser(scope, userId)
  const status = useDestStatus(scope)
  const remove = useDeleteDestExemption(scope)
  const [adding, setAdding] = useState(false)

  if (q.isPending) {
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.25 }} data-testid="access-tab-loading">
        <Skeleton variant="rounded" height={56} /><Skeleton variant="rounded" height={56} />
      </Box>
    )
  }
  if (q.isError || !q.data) {
    return (
      <Alert severity="error" action={<Button color="inherit" size="small" onClick={() => void q.refetch()}>{t(`${D}retry`)}</Button>}>
        {t(`${D}load_failed`)}
      </Alert>
    )
  }

  const { group, exemption } = q.data
  const now = Date.now()
  const expiry = (e: DestExemption) => e.expires_at === null
    ? t(`${D}permanent`)
    : t(`${D}expires_in`, { left: timeLeft(t, Math.max(0, e.expires_at - now)) })

  const lift = async (e: DestExemption) => {
    const ok = await confirm(removeExemptionConfirm({ t, minutes: applyMinutes(status.data) }, e.upn))
    if (!ok) return
    try {
      await remove.mutateAsync(e.user_id)
      pushSnack(t(`${D}removed_snack`, { upn: e.upn }), 'success')
    } catch (err) {
      reportDestError(t, err)
    }
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.25 }}>
      <Row label={t(`${D}group`)}>
        {group
          ? (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
              <span>{group.name}</span>
              {group.mode === 'open' && <ToneBadge tone={accessTone(theme, 'none')} label={t(`${D}open`)} testId="group-mode" data="open" />}
            </Box>
          )
          : <Typography component="span" sx={{ color: md.onSurfaceVariant, fontSize: 14 }}>{t(`${D}no_group`)}</Typography>}
      </Row>
      {exemption
        ? (
          <>
            <Row label={t(`${D}exemption`)} tinted
              action={<AsyncButton size="small" onClick={() => lift(exemption)}>{t(`${D}remove_exemption`)}</AsyncButton>}>
              <Tooltip title={exemption.expires_at === null ? '' : formatMsDualTz(exemption.expires_at, panelTz)}>
                <Box component="span">
                {t(`${D}exemption_line`, {
                  reason: exemption.reason, by: exemption.created_by_upn || '—',
                  ago: formatRelativeTimeShort(now - exemption.created_at, t), expiry: expiry(exemption),
                })}
                </Box>
              </Tooltip>
            </Row>
            <Box sx={{ display: 'flex', gap: 0.75, alignItems: 'center', px: 1, color: md.onSurfaceVariant, borderRadius: 2, bgcolor: md.surfaceContainerHigh, py: 0.75 }}>
              <InfoOutlinedIcon aria-hidden sx={{ fontSize: 16 }} />
              <Typography sx={{ fontSize: 12.5 }}>{t(`${D}exempt_hint`)}</Typography>
            </Box>
          </>
        )
        : (
          <Row label={t(`${D}exemption`)} action={<Button size="small" onClick={() => setAdding(true)}>{t(`${D}add_exemption`)}</Button>}>
            <span>{t(`${D}not_exempt`)}</span>
          </Row>
        )}
      <AddExemptionDialog open={adding} onClose={() => setAdding(false)} lockedUserId={userId} />
    </Box>
  )
}
