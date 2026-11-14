import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Alert, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, Skeleton, Typography, useMediaQuery, useTheme,
} from '@mui/material'
import { useTranslation } from 'react-i18next'

import { destErrorOf, isDestUnavailable, type DestSettings } from '@/api/accessControl'
import { AsyncButton } from '@/components/AsyncButton'
import PolicyField, { outOfRange, type FieldSpec } from '@/components/PolicyField'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useDestSettings, useSaveDestSettings } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import type { SettingsFocus } from './accessParams'
import { discardChangesConfirm, shortenRetentionConfirm } from './confirmCopy'
import { destErrorText } from './destErrors'
import RuleDataPanel from './RuleDataPanel'

const S = 'admin:access_control.settings.'

// The accepted ranges are the server's (internal/ports/access_control_settings.go:
// DestPolicyApplyMinSeconds 30–3600, DestListRefreshHours 6–168). The server
// refuses a value outside them, and the defaults come from the served
// `defaults`, never from here.
const FIELDS: (FieldSpec & { key: 'dest_policy_apply_min_seconds' | 'dest_list_refresh_hours' | 'dest_hit_retention_days'; focus: SettingsFocus })[] = [
  { key: 'dest_policy_apply_min_seconds', kind: 'number', label: `${S}apply_seconds`, hint: `${S}apply_seconds_hint`, min: 30, max: 3600, tail: true, focus: 'apply' },
  { key: 'dest_list_refresh_hours', kind: 'number', label: `${S}refresh_hours`, hint: `${S}refresh_hours_hint`, min: 6, max: 168, tail: true, focus: 'refresh' },
  // DestHitRetentionDays 1–365. The allowlist trial's own retention (1–30) is a later phase's field.
  { key: 'dest_hit_retention_days', kind: 'number', label: `${S}hit_retention_days`, hint: `${S}hit_retention_days_hint`, min: 1, max: 365, tail: true, focus: 'retention' },
]

export interface AccessSettingsDialogProps {
  open: boolean
  focus?: SettingsFocus
  onClose: () => void
}

/**
 * DATA AND APPLY SETTINGS (S18, batch 1): the apply wait, the remote-list
 * refresh interval and the rule-data update mode (with the rule-data version
 * block). Only keys that changed are sent. A blank number is the default and is
 * never shown as 0.
 */
export default function AccessSettingsDialog({ open, focus, onClose }: AccessSettingsDialogProps) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const fullScreen = useMediaQuery(theme.breakpoints.down('sm'))
  const scope = useQueryScope()
  const read = useDestSettings(scope, { enabled: open })
  const save = useSaveDestSettings(scope)
  const [draft, setDraft] = useState<DestSettings | null>(null)
  const [seeded, setSeeded] = useState(false)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const body = useRef<HTMLDivElement>(null)

  const stored = read.data?.settings
  const storedMode = (stored?.dest_rules_update_mode || read.data?.effective.dest_rules_update_mode || 'manual') as 'manual' | 'auto'

  // Seed the draft from the stored settings each time the dialog opens; a
  // refetch while it is open never overwrites what is being typed.
  useEffect(() => {
    if (!open) { setSeeded(false); setDraft(null); setErrors({}); return }
    if (stored && !seeded) {
      setDraft({ ...stored, dest_rules_update_mode: storedMode })
      setSeeded(true)
    }
  }, [open, stored, seeded, storedMode])

  useEffect(() => {
    if (!open || !draft || !focus) return
    const id = window.setTimeout(() => {
      const root = body.current
      if (!root) return
      if (focus === 'rules') root.querySelector('[data-focus="rules"]')?.scrollIntoView?.({ block: 'center' })
      else root.querySelector<HTMLInputElement>(`[data-focus="${focus}"] input`)?.focus()
    }, 0)
    return () => window.clearTimeout(id)
    // Once per open and focus request: seeding the draft is what makes the field exist.
  }, [open, focus, draft !== null]) // eslint-disable-line react-hooks/exhaustive-deps

  const changed = useMemo(() => {
    const out: Partial<DestSettings> = {}
    if (!draft || !stored) return out
    for (const f of FIELDS) {
      if ((draft[f.key] ?? 0) !== (stored[f.key] ?? 0)) out[f.key] = draft[f.key] ?? 0
    }
    if (draft.dest_rules_update_mode !== storedMode) out.dest_rules_update_mode = draft.dest_rules_update_mode
    return out
  }, [draft, stored, storedMode])
  const count = Object.keys(changed).length
  const invalid = !!draft && FIELDS.some(f => outOfRange(f, draft[f.key]))

  const guard = useDirtyClose(count > 0, discardChangesConfirm(t))
  const close = () => guard(onClose)

  const submit = async () => {
    setErrors({})
    // A shorter retention deletes records within the hour, for good: asked first (S20).
    if (read.data && 'dest_hit_retention_days' in changed) {
      const next = changed.dest_hit_retention_days || read.data.defaults.dest_hit_retention_days
      if (next && next < read.data.effective.dest_hit_retention_days && !(await confirm(shortenRetentionConfirm(t, next)))) return
    }
    try {
      await save.mutateAsync(changed)
      pushSnack(t(`${S}saved`), 'success')
      onClose()
    } catch (err) {
      const e = destErrorOf(err)
      if (e?.status === 400 && e.field) setErrors({ [e.field]: t(`${S}out_of_range`) })
      else setErrors({ _: destErrorText(t, err) })
    }
  }

  return (
    <Dialog open={open} onClose={close} fullScreen={fullScreen} maxWidth="sm" fullWidth
      slotProps={{ paper: { sx: { borderRadius: fullScreen ? 0 : 3, bgcolor: md.surfaceContainerHigh } } }}>
      <DialogTitle>{t(`${S}title`)}</DialogTitle>
      <DialogContent ref={body}>
        {read.isPending && open && <Skeleton variant="rounded" height={160} />}
        {read.isError && (isDestUnavailable(read.error)
          ? <Alert severity="info">{t(`${S}unwired`)}</Alert>
          : <Alert severity="error" action={<Button color="inherit" size="small" onClick={() => void read.refetch()}>{t('admin:access_control.retry')}</Button>}>{t(`${S}read_failed`)}</Alert>)}
        {draft && read.data && (
          <Box sx={{ display: 'grid', gap: 2.5, pt: 1 }}>
            {errors._ && <Alert severity="error">{errors._}</Alert>}
            <Box>
              <Typography component="h2" sx={{ fontSize: 16, fontWeight: 600, mb: 1.5 }}>{t(`${S}group_retention`)}</Typography>
              <Box data-focus="retention">
                <PolicyField spec={FIELDS[2]} value={draft.dest_hit_retention_days} defaults={read.data.defaults}
                  effective={read.data.effective.dest_hit_retention_days} error={errors.dest_hit_retention_days}
                  onChange={v => setDraft({ ...draft, dest_hit_retention_days: v as number })} />
                {errors.dest_hit_retention_days && <Typography variant="caption" role="alert" sx={{ color: md.error }}>{errors.dest_hit_retention_days}</Typography>}
              </Box>
            </Box>
            <Box>
              <Typography component="h2" sx={{ fontSize: 16, fontWeight: 600, mb: 1.5 }}>{t(`${S}group_refresh`)}</Typography>
              <Box data-focus="refresh">
                <PolicyField spec={FIELDS[1]} value={draft.dest_list_refresh_hours} defaults={read.data.defaults}
                  effective={read.data.effective.dest_list_refresh_hours} error={errors.dest_list_refresh_hours}
                  onChange={v => setDraft({ ...draft, dest_list_refresh_hours: v as number })} />
                {errors.dest_list_refresh_hours && <Typography variant="caption" role="alert" sx={{ color: md.error }}>{errors.dest_list_refresh_hours}</Typography>}
              </Box>
            </Box>
            <Box>
              <Typography component="h2" sx={{ fontSize: 16, fontWeight: 600, mb: 1.5 }}>{t(`${S}group_apply`)}</Typography>
              <Box data-focus="apply">
                <PolicyField spec={FIELDS[0]} value={draft.dest_policy_apply_min_seconds} defaults={read.data.defaults}
                  effective={read.data.effective.dest_policy_apply_min_seconds} error={errors.dest_policy_apply_min_seconds}
                  onChange={v => setDraft({ ...draft, dest_policy_apply_min_seconds: v as number })} />
                {errors.dest_policy_apply_min_seconds && <Typography variant="caption" role="alert" sx={{ color: md.error }}>{errors.dest_policy_apply_min_seconds}</Typography>}
              </Box>
            </Box>
            <Box data-focus="rules">
              <Typography component="h2" sx={{ fontSize: 16, fontWeight: 600, mb: 1.5 }}>{t(`${S}group_rules`)}</Typography>
              <RuleDataPanel mode={draft.dest_rules_update_mode === 'auto' ? 'auto' : 'manual'}
                onModeChange={m => setDraft({ ...draft, dest_rules_update_mode: m })} disabled={save.isPending} />
            </Box>
          </Box>
        )}
      </DialogContent>
      <DialogActions sx={{ position: 'sticky', bottom: 0, bgcolor: md.surfaceContainerHigh, justifyContent: 'space-between', px: 3 }}>
        <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>
          {count > 0 ? t(`${S}changed`, { count }) : ''}
        </Typography>
        <Box sx={{ display: 'flex', gap: 1 }}>
          <Button disabled={count === 0 || save.isPending} onClick={() => stored && setDraft({ ...stored, dest_rules_update_mode: storedMode })}>{t(`${S}discard`)}</Button>
          <AsyncButton variant="contained" disabled={count === 0 || invalid} onClick={submit}>{t(`${S}save`)}</AsyncButton>
        </Box>
      </DialogActions>
    </Dialog>
  )
}
