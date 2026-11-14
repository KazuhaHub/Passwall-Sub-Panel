import { useMemo } from 'react'
import {
  Alert, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, Skeleton, Typography, useMediaQuery, useTheme,
} from '@mui/material'
import { useTranslation } from 'react-i18next'

import type { DestPolicy } from '@/api/accessControl'
import { AsyncButton } from '@/components/AsyncButton'
import KpiTile, { KpiGrid } from '@/components/KpiTile'
import { useDestHits } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'

const C = 'admin:access_control.convert.'
const DAY = 86_400_000

/**
 * S4's "turn into a block" for a report-only policy: what it would have
 * blocked in the window, and why the real effect may be larger, then one
 * button. Switching report to on is a plain state change (the policy is
 * written whole, with the updated_at it was read at), so there is no second
 * confirmation after this one.
 *
 * The numbers are THIS policy's: its would-block count comes from the policy
 * list; the accounts and the top destinations from two reads of the hit
 * records filtered to its source (the response's own `summary` ignores the
 * source filter, so it cannot give "accounts involved"). When no node records
 * there is nothing to estimate from: the dialog says so, shows no 0, and does
 * not ask. A failed read leaves the button working.
 */
export default function ConvertToBlockDialog({ policy, days, onClose, onConfirm, onViewRecords }: {
  /** The report-only policy, or null (closed). */
  policy: DestPolicy | null
  /** The window of the figures (`hit_window_days`). */
  days: number
  onClose: () => void
  onConfirm: (policy: DestPolicy) => Promise<void>
  onViewRecords: (policy: DestPolicy) => void
}) {
  const { t, i18n } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const phone = useMediaQuery(theme.breakpoints.down('sm'))
  const scope = useQueryScope()
  const num = new Intl.NumberFormat(i18n.language)
  const id = policy?.id ?? 0
  const estimable = policy !== null && policy.would_block_recent !== null
  // One instant per opening: the reads are keyed by it, so it must not move on a re-render.
  const since = useMemo(() => Date.now() - days * DAY, [id, days]) // eslint-disable-line react-hooks/exhaustive-deps
  const base = { source: `p${id}`, action: 'observe' as const, since }
  const sites = useDestHits(scope, { ...base, group_by: 'site', page_size: 3 }, { enabled: estimable })
  const users = useDestHits(scope, { ...base, group_by: 'user', page_size: 1 }, { enabled: estimable })

  const loading = estimable && (sites.isPending || users.isPending)
  const failed = estimable && (sites.isError || users.isError)
  const would = policy?.would_block_recent ?? 0
  const top = (sites.data?.items ?? []).filter(i => i.dest).map(i => `${i.dest} ${num.format(i.count)}`).join(' · ')

  return (
    <Dialog open={policy !== null} onClose={onClose} fullWidth maxWidth="sm" fullScreen={phone}
      slotProps={{ paper: { sx: { bgcolor: md.surfaceContainerHigh, borderRadius: phone ? 0 : 3 } } }}>
      <DialogTitle>{t(`${C}title`, { name: policy?.name ?? '' })}</DialogTitle>
      <DialogContent sx={{ display: 'grid', gap: 1.5 }}>
        {!estimable && (
          <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${C}no_collect`)}</Typography>
        )}
        {loading && (
          <Box data-testid="convert-loading"><Skeleton variant="rounded" height={64} /></Box>
        )}
        {failed && <Alert severity="error">{t(`${C}read_failed`)}</Alert>}
        {estimable && !loading && !failed && (would === 0
          ? <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${C}no_hits`, { days })}</Typography>
          : (
            <>
              <Box data-testid="convert-numbers">
                <KpiGrid>
                  <KpiTile label={t(`${C}hits`, { days })} value={t(`${C}at_least`, { n: num.format(would) })} />
                  <KpiTile label={t(`${C}users`)} value={num.format(users.data?.total ?? 0)} />
                </KpiGrid>
              </Box>
              {top && <Typography variant="body2" data-testid="convert-top" sx={{ overflowWrap: 'anywhere' }}>{t(`${C}top`, { list: top })}</Typography>}
            </>
          ))}
        <Typography variant="body2">{t(`${C}body`)}</Typography>
        {policy?.counts_as_risk === true && <Typography variant="body2">{t(`${C}counts_as_risk`)}</Typography>}
      </DialogContent>
      <DialogActions sx={{
        flexWrap: 'wrap', gap: 1, px: phone ? 2 : undefined, pb: phone ? 2 : undefined,
        // The gap spaces the buttons; MUI's own left margin on top of it would double it on a wrapped row.
        '& > :not(style) ~ :not(style)': { ml: 0 },
        ...(phone ? { flexDirection: 'column-reverse', alignItems: 'stretch', '& .MuiButton-root': { minHeight: 44 } } : {}),
      }}>
        {estimable && would > 0 && (
          <Button size="small" onClick={() => policy && onViewRecords(policy)} sx={{ mr: phone ? 0 : 'auto' }}>{t(`${C}view_records`)}</Button>
        )}
        <Button onClick={onClose}>{t(`${C}cancel`)}</Button>
        <AsyncButton variant="contained" onClick={async () => { if (policy) await onConfirm(policy) }}>{t(`${C}action`)}</AsyncButton>
      </DialogActions>
    </Dialog>
  )
}
