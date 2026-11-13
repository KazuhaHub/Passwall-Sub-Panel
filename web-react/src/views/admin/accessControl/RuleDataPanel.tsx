import { useState } from 'react'
import {
  Alert, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, LinearProgress, Skeleton, ToggleButton,
  ToggleButtonGroup, Typography, useMediaQuery, useTheme,
} from '@mui/material'
import { useTranslation } from 'react-i18next'

import type { GeositeRejection, GeositeUpdateResult } from '@/api/accessControl'
import { AsyncButton } from '@/components/AsyncButton'
import FieldHint from '@/components/FieldHint'
import { pushSnack } from '@/components/SnackbarHost'
import ToneBadge from '@/components/ToneBadge'
import { useGeositeStatus, useRollbackGeosite, useUpdateGeosite } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { accessTone } from '@/utils/accessControl'
import { destErrorOf } from '@/api/accessControl'
import { destErrorText } from './destErrors'

const R = 'admin:access_control.rule_data.'

type Op = 'update' | 'rollback'

/** Why a release cannot be applied, in words; the list name rides along. */
function rejectionText(t: (k: string, o?: Record<string, unknown>) => string, r: GeositeRejection): string {
  const known = ['broad_entry', 'dest_list_too_large', 'category_gone', 'policy_over_limit'] as const
  const key = (known as readonly string[]).includes(r.reason) ? r.reason : 'other'
  return t(`${R}rejection.${key}`, { list: r.list ?? '', entry: r.entry ?? '', used: r.used ?? '', limit: r.limit ?? '' })
}

/**
 * THE RULE-DATA VERSION: which v2fly release the community categories come
 * from, how old it is, what the upstream offers, and the update / roll back
 * pair. An update is always previewed first (a dry run downloads and verifies a
 * release and changes nothing) and applied by naming the tag that was reviewed.
 * The auto / manual mode is the dialog's own draft (it is saved with the other
 * settings), so it comes in as props.
 */
export default function RuleDataPanel({ mode, onModeChange, disabled }: {
  mode: 'manual' | 'auto'
  onModeChange: (m: 'manual' | 'auto') => void
  disabled?: boolean
}) {
  const { t, i18n } = useTranslation(['admin'])
  const theme = useTheme()
  const fullScreen = useMediaQuery(theme.breakpoints.down('sm'))
  const md = theme.palette.md
  const scope = useQueryScope()
  const status = useGeositeStatus(scope)
  const update = useUpdateGeosite(scope)
  const rollback = useRollbackGeosite(scope)
  const [review, setReview] = useState<{ op: Op; result: GeositeUpdateResult } | null>(null)
  const [error, setError] = useState('')

  const date = (iso?: string) => (iso ? new Date(iso).toLocaleDateString(i18n.language) : '')

  if (status.isPending) return <Skeleton variant="rounded" height={96} />
  // A failed REFRESH keeps the data on screen (and the review dialog with its message); only a read that never succeeded replaces it.
  if (!status.data) {
    return (
      <Alert severity="error" action={<Button color="inherit" size="small" onClick={() => void status.refetch()}>{t('admin:access_control.retry')}</Button>}>
        {t(`${R}read_failed`)}
      </Alert>
    )
  }
  const s = status.data
  const busy = update.isPending || rollback.isPending

  const preview = async (op: Op) => {
    setError('')
    try {
      const result = op === 'update' ? await update.mutateAsync({ dryRun: true }) : await rollback.mutateAsync({ dryRun: true })
      setReview({ op, result })
    } catch (err) {
      setError(destErrorText(t, err))
    }
  }

  const apply = async () => {
    if (!review) return
    try {
      // The apply names the release the dry run reviewed, so one that lands in between changes nothing.
      if (review.op === 'update') await update.mutateAsync({ tag: review.result.to.tag })
      else {
        // The rollback API takes no tag: it goes to whatever is "previous" now. So the reviewed
        // version is re-checked first, and the apply is abandoned if it moved.
        const fresh = await status.refetch()
        if (fresh.isError || fresh.error) {
          // Without a fresh read the reviewed version cannot be confirmed: do not apply.
          setReview(null)
          setError(t(`${R}rollback_recheck_failed`))
          return
        }
        if (fresh.data?.previous?.tag !== review.result.to.tag) {
          setReview(null)
          setError(t(`${R}rollback_changed`))
          return
        }
        await rollback.mutateAsync({})
      }
      pushSnack(t(`${R}applied_snack`, { tag: review.result.to.tag }), 'success')
      setReview(null)
    } catch (err) {
      const e = destErrorOf(err)
      const rejection = e?.code === 'dest_geosite_update_rejected' ? e.body.rejection as GeositeRejection | undefined : undefined
      setReview(null)
      setError(rejection ? rejectionText(t, rejection) : e?.code === 'dest_geosite_update_rejected' ? t(`${R}rejection.refused`) : destErrorText(t, err))
    }
  }

  const rej = review?.result.rejection
  const diff = review?.result
  const age = s.current_age_days

  return (
    <Box data-testid="rule-data-panel">
      {!s.current ? (
        <Typography sx={{ color: md.onSurfaceVariant }}>{t(`${R}none`)}</Typography>
      ) : (
        <Box sx={{ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr)', gap: 0.5 }}>
          <Typography sx={{ fontSize: 14 }}>
            {t(`${R}current`, { tag: s.current.tag })}
            {s.current_released_at ? ` · ${t(`${R}released`, { date: date(s.current_released_at) })}` : ''}
            {age !== undefined ? ` · ${t(`${R}age`, { count: age })}` : ''}
          </Typography>
        </Box>
      )}
      <Typography sx={{ fontSize: 14, mt: 0.5 }}>
        {s.latest.tag ? t(`${R}latest`, { tag: s.latest.tag }) : t(`${R}latest_unknown`)}
        {s.update_available && s.behind_days !== undefined ? ` · ${t(`${R}behind`, { count: s.behind_days })}` : ''}
      </Typography>
      {s.update_available && (
        <Box sx={{ mt: 0.75 }}><ToneBadge tone={accessTone(theme, 'attention')} label={t(`${R}update_available`)} /></Box>
      )}
      {s.latest.error && (
        <Typography variant="body2" sx={{ color: md.onSurfaceVariant, mt: 0.5 }}>{t(`${R}latest_error`)}</Typography>
      )}
      {(s.last_update_error || s.data_error) && (
        <Box sx={{ mt: 1 }}>
          <FieldHint tone="amber" summary={s.data_error ? t(`${R}data_error.summary`) : t(`${R}last_update_error.summary`)}
            detail={s.data_error ? t(`${R}data_error.detail`) : t(`${R}last_update_error.detail`)} />
        </Box>
      )}

      <Box sx={{ display: 'flex', gap: 1, mt: 1.5, flexWrap: 'wrap' }}>
        <AsyncButton variant="outlined" size="small" disabled={disabled || busy} onClick={() => preview('update')}>{t(`${R}check_update`)}</AsyncButton>
        {s.previous && (
          <AsyncButton variant="text" size="small" disabled={disabled || busy} onClick={() => preview('rollback')}>{t(`${R}rollback`, { tag: s.previous.tag })}</AsyncButton>
        )}
      </Box>
      {busy && <LinearProgress sx={{ mt: 1 }} aria-label={t(`${R}working`)} />}
      {error && <Alert severity="error" sx={{ mt: 1 }} onClose={() => setError('')}>{error}</Alert>}

      <Box sx={{ mt: 2 }}>
        <Typography sx={{ fontSize: 14, fontWeight: 600, mb: 0.5 }} id="rules-mode-label">{t(`${R}mode.title`)}</Typography>
        <ToggleButtonGroup exclusive size="small" value={mode} disabled={disabled} aria-labelledby="rules-mode-label"
          onChange={(_, v: 'manual' | 'auto' | null) => { if (v) onModeChange(v) }}>
          <ToggleButton value="manual">{t(`${R}mode.manual`)}</ToggleButton>
          <ToggleButton value="auto">{t(`${R}mode.auto`)}</ToggleButton>
        </ToggleButtonGroup>
        <Typography variant="body2" sx={{ color: md.onSurfaceVariant, mt: 0.5 }}>{t(`${R}mode.${mode}_hint`)}</Typography>
      </Box>

      <Dialog open={review !== null} onClose={() => !busy && setReview(null)} fullScreen={fullScreen} maxWidth="sm" fullWidth
        slotProps={{ paper: { sx: { borderRadius: fullScreen ? 0 : 3, bgcolor: md.surfaceContainerHigh } } }}>
        <DialogTitle>{review?.op === 'rollback' ? t(`${R}review.title_rollback`) : t(`${R}review.title_update`)}</DialogTitle>
        <DialogContent>
          {diff && (
            <>
              <Typography sx={{ fontSize: 14, mb: 1 }}>
                {diff.from ? t(`${R}review.from_to`, { from: diff.from.tag, to: diff.to.tag }) : t(`${R}review.to`, { to: diff.to.tag })}
              </Typography>
              {rej && <Alert severity="error" sx={{ mb: 1 }}>{rejectionText(t, rej)}</Alert>}
              {diff.unchanged ? (
                <Typography sx={{ color: md.onSurfaceVariant }}>{t(`${R}review.unchanged`)}</Typography>
              ) : (
                <Box component="table" sx={{ width: '100%', borderCollapse: 'collapse', fontSize: 13, fontVariantNumeric: 'tabular-nums' }}>
                  <thead>
                    <tr>
                      <Box component="th" sx={{ textAlign: 'left', py: 0.5 }}>{t(`${R}review.category`)}</Box>
                      <Box component="th" sx={{ textAlign: 'right' }}>{t(`${R}review.entries`)}</Box>
                      <Box component="th" sx={{ textAlign: 'right' }}>{t(`${R}review.change`)}</Box>
                    </tr>
                  </thead>
                  <tbody>
                    {diff.categories.map(c => (
                      <tr key={c.name}>
                        <Box component="td" sx={{ py: 0.5, overflowWrap: 'anywhere' }}>
                          {c.name}
                          {c.in_use && <Box component="span" sx={{ ml: 1 }}><ToneBadge tone={accessTone(theme, 'measuring')} label={t(`${R}review.in_use`)} /></Box>}
                        </Box>
                        <Box component="td" sx={{ textAlign: 'right' }}>{c.old} → {c.new}</Box>
                        <Box component="td" sx={{ textAlign: 'right' }}>{`+${c.added} −${c.removed}`}</Box>
                      </tr>
                    ))}
                  </tbody>
                </Box>
              )}
              {!diff.unchanged && !rej && (
                <Typography variant="body2" sx={{ color: md.onSurfaceVariant, mt: 1.5 }}>
                  {diff.lists_changed > 0 ? t(`${R}review.restart`) : t(`${R}review.no_restart`)}
                </Typography>
              )}
            </>
          )}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setReview(null)} disabled={busy}>{t(`${R}review.cancel`)}</Button>
          <AsyncButton variant="contained" disabled={!!rej || !!diff?.unchanged} onClick={apply}>
            {review?.op === 'rollback' ? t(`${R}review.apply_rollback`) : t(`${R}review.apply_update`)}
          </AsyncButton>
        </DialogActions>
      </Dialog>
    </Box>
  )
}
