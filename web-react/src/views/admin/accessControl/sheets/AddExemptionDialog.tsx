import { useRef, useState } from 'react'
import { Alert, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, IconButton, Stack, TextField, ToggleButton, ToggleButtonGroup, Typography, useMediaQuery, useTheme } from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import { useTranslation } from 'react-i18next'
import type { DestinationExemptionView } from '@/api/accessControl'
import UserAutocomplete from '@/components/UserAutocomplete'
import FieldHint from '@/components/FieldHint'
import { pushSnack } from '@/components/SnackbarHost'
import { useQueryScope } from '@/query/useQueryScope'
import { useDestinationUserAccess, useSaveDestinationExemption } from '@/query/accessControl'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { discardSettingsCopy } from '../confirmCopy'
import { destinationError } from '../errors'
import { exemptionExpiry, localExpiry, validateExemption, type ExemptionExpiry } from './exemptionDraft'
const P = 'admin:access_control.exemptions.'
export default function AddExemptionDialog({ userId: lockedId, upn, existing, etaMs, accessWarning, onClose, onSaved }: { userId?: number; upn?: string; existing?: DestinationExemptionView; etaMs?: number; accessWarning?: boolean; onClose: () => void; onSaved?: () => void }) {
  const { t } = useTranslation(['admin', 'common']), theme = useTheme(), mobile = useMediaQuery(theme.breakpoints.down('sm'))
  const scope = useQueryScope(), save = useSaveDestinationExemption(scope)
  const [userId, setUserId] = useState<number | null>(existing?.user_id ?? lockedId ?? null)
  const [reason, setReason] = useState(existing?.reason ?? '')
  const initialMode: ExemptionExpiry = existing ? existing.expires_at === null ? 'never' : 'custom' : 'day'
  const initialCustom = existing?.expires_at ? localExpiry(existing.expires_at) : ''
  const [mode, setMode] = useState<ExemptionExpiry>(initialMode), [custom, setCustom] = useState(initialCustom)
  const [error, setError] = useState<{ error: string; field?: string }>({ error: '' }), [busy, setBusy] = useState(false)
  const admission = useRef(false)
  const access = useDestinationUserAccess(scope, userId ?? 0)
  const dirty = userId !== (existing?.user_id ?? lockedId ?? null) || reason !== (existing?.reason ?? '') || mode !== initialMode || custom !== initialCustom
  const closeCheck = useDirtyClose(dirty, discardSettingsCopy(t))
  useLeaveGuard(dirty, discardSettingsCopy(t), (next, current) => next.pathname !== current.pathname || next.search !== current.search, busy)
  const close = () => { if (!admission.current) void closeCheck().then(ok => { if (ok) onClose() }) }
  const now = Date.now(), expiresAt = exemptionExpiry(mode, custom, now, existing)
  const invalid = validateExemption(userId, reason, expiresAt, now), valid = !Object.values(invalid).some(Boolean)
  const duplicate = error.error === 'dest_exemption_exists'
  const submit = async () => {
    if (admission.current || !valid || duplicate || !!existing && !dirty) return
    // Resolve relative expiries at the actual save time, not the last render.
    const at = Date.now(), expiry = exemptionExpiry(mode, custom, at, existing)
    if (Object.values(validateExemption(userId, reason, expiry, at)).some(Boolean)) { setError({ error: 'dest_policy_invalid', field: 'expires_at' }); return }
    admission.current = true; setBusy(true)
    try {
      const result = await save.mutateAsync({ userId: userId!, existing: !!existing, input: { reason: reason.trim(), expires_at: expiry } })
      pushSnack(t(`${P}saved`, { upn: result.upn ?? upn ?? `#${userId}` }), 'success'); (onSaved ?? onClose)()
    } catch (err) { setError(destinationError(err)) } finally { admission.current = false; setBusy(false) }
  }
  return <Dialog open fullWidth maxWidth="sm" fullScreen={mobile} onClose={close} aria-labelledby="exemption-editor-title">
    <DialogTitle id="exemption-editor-title" sx={{ display: 'flex', alignItems: 'center' }}><Box component="span" sx={{ flex: 1 }}>{t(`${P}${existing ? 'edit' : 'add'}`)}</Box><IconButton disabled={busy} aria-label={t('common:actions.close')} onClick={close}><CloseIcon /></IconButton></DialogTitle>
    <DialogContent dividers><Stack spacing={2.5}>
      {error.error && !duplicate && <Alert severity="error">{t(`${P}save_failed`, { error: error.error })}</Alert>}
      <Box component="fieldset" disabled={busy} sx={{ p: 0, m: 0, border: 0, minWidth: 0 }}><Stack spacing={2.5}>
        {lockedId || existing ? <TextField label={t(`${P}account`)} value={existing?.upn ?? upn ?? `#${userId}`} slotProps={{ input: { readOnly: true } }} /> : <UserAutocomplete value={userId} onChange={id => { setUserId(id); setError({ error: '' }) }} label={t(`${P}account`)} />}
        {duplicate && <Alert severity="error">{t(`${P}dest_exemption_exists`)}</Alert>}
        {accessWarning && <FieldHint tone="amber" summary={t('admin:access_control.exception.account_summary')} detail={t('admin:access_control.exception.account_detail')} />}
        {access.data?.group?.mode === 'allowlist' && <FieldHint tone="amber" summary={t(`${P}allowlist_summary`, { group: access.data.group.name })} detail={t(`${P}allowlist_detail`)} />}
        <TextField autoFocus label={t(`${P}reason`)} multiline minRows={2} value={reason} onChange={e => { setReason(e.target.value); setError(old => old.error === 'dest_exemption_exists' ? old : { error: '' }) }} error={invalid.reason && !!reason || error.field === 'reason'} helperText={t(`${P}reason_count`, { count: Array.from(reason).length, limit: 255 })} />
        <Typography variant="subtitle2">{t(`${P}expiry`)}</Typography>
        <ToggleButtonGroup exclusive value={mode} aria-label={t(`${P}expiry`)} sx={{ width: '100%', flexWrap: 'wrap' }} onChange={(_, value: ExemptionExpiry | null) => { if (value) { setMode(value); setError(old => old.error === 'dest_exemption_exists' ? old : { error: '' }) } }}>{(['never', 'day', 'week', 'custom'] as const).map(value => <ToggleButton key={value} value={value} sx={{ flex: 1 }}>{t(`${P}expiry_${value}`)}</ToggleButton>)}</ToggleButtonGroup>
        {mode === 'custom' && <TextField type="datetime-local" label={t(`${P}custom_time`)} value={custom} slotProps={{ inputLabel: { shrink: true } }} onChange={e => { setCustom(e.target.value); setError(old => old.error === 'dest_exemption_exists' ? old : { error: '' }) }} error={invalid.expiry || error.field === 'expires_at'} helperText={t(`${P}${invalid.expiry ? 'expiry_invalid' : 'browser_time'}`)} />}
        <Typography variant="caption" color="text.secondary">{t(`${P}expiry_delay`)}</Typography>
      </Stack></Box>
    </Stack></DialogContent>
    <DialogActions sx={{ flexWrap: 'wrap', px: 3, py: 2 }}><Typography variant="caption" sx={{ flex: '1 1 100%' }}>{t(`${P}apply_hint`, { eta: etaMs ? t('admin:access_control.confirm.eta_minutes', { minutes: Math.ceil(etaMs / 60000) }) : t('admin:access_control.confirm.eta_unknown') })}</Typography><Button disabled={busy} onClick={close}>{t('common:actions.cancel')}</Button><Button variant="contained" disabled={busy || !valid || duplicate || !!existing && !dirty} onClick={() => void submit()}>{t(busy ? 'admin:access_control.settings.saving' : existing ? 'common:actions.save' : `${P}add`)}</Button></DialogActions>
  </Dialog>
}
