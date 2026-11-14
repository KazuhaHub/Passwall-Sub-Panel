import { useState, type ReactNode } from 'react'
import { Alert, Box, Button, Skeleton, Tooltip, Typography, useTheme } from '@mui/material'
import ChevronRightIcon from '@mui/icons-material/ChevronRight'
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined'
import { useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'

import type { DestExemption, DestUserHit } from '@/api/accessControl'
import { AsyncButton } from '@/components/AsyncButton'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import ToneBadge from '@/components/ToneBadge'
import { useDeleteDestExemption, useDestStatus, useDestUser } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { accessTone, applyMinutes } from '@/utils/accessControl'
import { recordsHref } from '../../accessControl/records/recordsParams'
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
 * The drawer's access tab: the account's group, its exemption and the hits it
 * had in the window (per-site usage is a later phase's). Read only while the
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
  const navigate = useNavigate()
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
      {q.data.recent_hits && (
        <HitsSection userId={userId} days={q.data.recent_hits.days} items={q.data.recent_hits.items} available={q.data.hits_available}
          onViewAll={() => void navigate(`/admin/access-control${recordsHref({ user_id: userId, since: q.data.recent_hits!.days <= 1 ? '24h' : `${q.data.recent_hits!.days}d` })}`)}
          onNodes={() => void navigate('/admin/access-control?sheet=nodes')} />
      )}
      <AddExemptionDialog open={adding} onClose={() => setAdding(false)} lockedUserId={userId} />
    </Box>
  )
}

/**
 * Their recent hits, one line per rule that was hit (S16). The window is what
 * the server answered (`days`, already held to the retention). Whether any
 * node of the account records is told apart from "nothing hit": an account no
 * recording node serves has no hits to show, which is not the same as none.
 */
function HitsSection({ userId, days, items, available, onViewAll, onNodes }: {
  userId: number
  days: number
  items: DestUserHit[]
  available: boolean | null
  onViewAll: () => void
  onNodes: () => void
}) {
  const { t, i18n } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const scope = useQueryScope()
  const status = useDestStatus(scope)
  const num = new Intl.NumberFormat(i18n.language)
  const nodeName = new Map((status.data?.nodes ?? []).map(n => [n.panel_id, n.panel_name]))
  const sep = t('admin:access_control.drawer.hits_list_separator')
  const name = (h: DestUserHit) => {
    if (h.source_name === null) return t(`admin:access_control.drawer.hits_deleted_${h.source.startsWith('s') ? 'source_rule' : 'policy'}`)
    return h.source.startsWith('s') ? t('admin:access_control.drawer.hits_source_rule', { name: h.source_name }) : h.source_name
  }
  return (
    <Box data-testid="access-hits" data-user={userId} sx={{ mt: 1, display: 'flex', flexDirection: 'column', gap: 1 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1, flexWrap: 'wrap' }}>
        <Typography component="h3" sx={{ fontSize: 14, fontWeight: 600 }}>{t('admin:access_control.drawer.hits_title', { days })}</Typography>
        {available !== false && items.length > 0 && (
          <Button size="small" endIcon={<ChevronRightIcon />} onClick={onViewAll}>{t('admin:access_control.drawer.hits_view_all')}</Button>
        )}
      </Box>
      {available === false
        ? (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap', p: 1.5, borderRadius: 2, bgcolor: md.surfaceContainerHigh }}>
            <Typography variant="body2" sx={{ flex: 1, minWidth: 0 }}>{t('admin:access_control.drawer.hits_not_recorded')}</Typography>
            <Button size="small" onClick={onNodes}>{t('admin:access_control.drawer.hits_nodes')}</Button>
          </Box>
        )
        : items.length === 0
          ? <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t('admin:access_control.drawer.hits_none', { days })}</Typography>
          : items.map(h => {
            const where = [...(h.top_dests ?? []), (h.panels ?? []).map(id => nodeName.get(id)).filter((n): n is string => !!n).join(sep)].filter(Boolean).join(' · ')
            return (
              <Box key={`${h.source}/${h.action}`} data-testid="access-hit" sx={{ p: 1.25, borderRadius: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}`, minWidth: 0 }}>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, minWidth: 0 }}>
                  {h.action === 'observe'
                    ? <ToneBadge tone={accessTone(theme, 'would_deny')} label={t('admin:access_control.records.rule.would_block')} data="observe" />
                    : <ToneBadge tone={accessTone(theme, 'block')} label={t('admin:access_control.records.rule.blocked')} data="block" />}
                  <Typography sx={{ flex: 1, minWidth: 0, fontSize: 14, overflowWrap: 'anywhere', fontStyle: h.source_name === null ? 'italic' : 'normal' }}>{name(h)}</Typography>
                  <Typography sx={{ fontVariantNumeric: 'tabular-nums', fontWeight: 600 }}>{num.format(h.count)}</Typography>
                </Box>
                {where && <Typography variant="body2" sx={{ color: md.onSurfaceVariant, overflowWrap: 'anywhere' }}>{where}</Typography>}
              </Box>
            )
          })}
    </Box>
  )
}
