import { useEffect, useState } from 'react'
import {
  Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, FormHelperText, TextField, ToggleButton, ToggleButtonGroup,
  Typography, useMediaQuery, useTheme,
} from '@mui/material'
import { useTranslation } from 'react-i18next'

import { destErrorOf, type DestExemption } from '@/api/accessControl'
import { AsyncButton } from '@/components/AsyncButton'
import { pushSnack } from '@/components/SnackbarHost'
import UserAutocomplete from '@/components/UserAutocomplete'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useDestStatus, useSaveDestExemption } from '@/query/accessControl'
import { useUserDetail } from '@/query/users'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes } from '@/utils/accessControl'
import { discardChangesConfirm } from '../confirmCopy'
import { reportDestError } from '../destErrors'

type Expiry = 'forever' | 'h24' | 'd7' | 'custom'
const HOUR = 3_600_000
const D = 'admin:access_control.exemptions.dialog.'
const MAX_REASON = 255

/** datetime-local wants local wall time without a zone. */
function toLocalInput(ms: number): string {
  const d = new Date(ms - new Date(ms).getTimezoneOffset() * 60_000)
  return d.toISOString().slice(0, 16)
}

/**
 * S5's add / edit dialog, shared by the exemptions sheet and the account
 * drawer's access tab. `lockedUserId` pins the account (the drawer); `existing`
 * turns it into an edit of that exemption's reason and expiry. No first-publish
 * confirmation: an exemption only ever loosens.
 */
export default function AddExemptionDialog({ open, onClose, lockedUserId, existing }: {
  open: boolean
  onClose: () => void
  lockedUserId?: number
  existing?: DestExemption
}) {
  const { t } = useTranslation(['admin'])
  const theme = useTheme()
  const full = useMediaQuery(theme.breakpoints.down('sm'))
  const scope = useQueryScope()
  const save = useSaveDestExemption(scope)
  const status = useDestStatus(scope, { enabled: open })
  const [userId, setUserId] = useState<number | null>(lockedUserId ?? existing?.user_id ?? null)
  const [reason, setReason] = useState(existing?.reason ?? '')
  const [expiry, setExpiry] = useState<Expiry>(existing?.expires_at ? 'custom' : 'forever')
  const [custom, setCustom] = useState(existing?.expires_at ? toLocalInput(existing.expires_at) : '')
  const [exists, setExists] = useState(false)
  const [touched, setTouched] = useState(false)
  const picked = useUserDetail(scope, open && !existing ? userId ?? 0 : 0)

  // A dialog that closes and reopens starts from what it is given.
  useEffect(() => {
    if (!open) return
    setUserId(lockedUserId ?? existing?.user_id ?? null)
    setReason(existing?.reason ?? '')
    setExpiry(existing?.expires_at ? 'custom' : 'forever')
    setCustom(existing?.expires_at ? toLocalInput(existing.expires_at) : '')
    setExists(false)
    setTouched(false)
  }, [open, lockedUserId, existing])

  const dirty = open && (reason !== (existing?.reason ?? '') || expiry !== (existing?.expires_at ? 'custom' : 'forever')
    || (!existing && lockedUserId === undefined && userId !== null))
  const guard = useDirtyClose(dirty, discardChangesConfirm(t))
  const close = () => guard(onClose)

  const customMs = custom ? new Date(custom).getTime() : NaN
  const customBad = expiry === 'custom' && (!Number.isFinite(customMs) || customMs <= Date.now())
  const reasonBad = reason.trim() === ''
  const userBad = userId === null
  const invalid = reasonBad || userBad || customBad

  const expiresAt = (): number | null => {
    switch (expiry) {
      case 'h24': return Date.now() + 24 * HOUR
      case 'd7': return Date.now() + 7 * 24 * HOUR
      case 'custom': return customMs
      default: return null
    }
  }

  const submit = async () => {
    setTouched(true)
    if (invalid || userId === null) return
    try {
      await save.mutateAsync({ userId, create: !existing, reason: reason.trim(), expires_at: expiresAt() })
      const upn = existing?.upn ?? picked.data?.upn ?? `#${userId}`
      pushSnack(t(existing ? `${D}updated_snack` : `${D}added_snack`, { upn }), 'success')
      onClose()
    } catch (err) {
      if (destErrorOf(err)?.code === 'dest_exemption_exists') setExists(true)
      else reportDestError(t, err)
    }
  }

  const minutes = applyMinutes(status.data)
  const minutesText = minutes === null ? t('admin:access_control.confirm.minutes_unknown') : t('admin:access_control.confirm.minutes', { count: minutes })

  return (
    <Dialog open={open} onClose={close} fullWidth maxWidth="sm" fullScreen={full}
      slotProps={{ paper: { sx: { bgcolor: theme.palette.md.surfaceContainerHigh, borderRadius: full ? 0 : 3 } } }}>
      <DialogTitle>{t(existing ? `${D}title_edit` : `${D}title_add`)}</DialogTitle>
      <DialogContent sx={{ display: 'grid', gap: 2, pt: '8px !important' }}>
        <Box>
          {existing || lockedUserId !== undefined
            ? <Typography sx={{ fontWeight: 600 }}>{existing?.upn ?? picked.data?.upn ?? `#${lockedUserId}`}</Typography>
            : (
              <UserAutocomplete value={userId} onChange={id => { setUserId(id); setExists(false) }} label={t(`${D}user`)} width={400} />
            )}
          {(exists || (touched && userBad)) && (
            <FormHelperText error>{exists ? t(`${D}exists`) : t(`${D}user_required`)}</FormHelperText>
          )}
        </Box>
        <TextField label={t(`${D}reason`)} value={reason} required fullWidth
          onChange={e => setReason(e.target.value.slice(0, MAX_REASON))}
          error={touched && reasonBad} helperText={touched && reasonBad ? t(`${D}reason_required`) : `${reason.length} / ${MAX_REASON}`} />
        <Box>
          <Typography variant="body2" sx={{ mb: 0.5 }}>{t(`${D}expiry`)}</Typography>
          <ToggleButtonGroup exclusive size="small" value={expiry} fullWidth={full} aria-label={t(`${D}expiry`)}
            onChange={(_, v: Expiry | null) => { if (v) setExpiry(v) }}>
            <ToggleButton value="forever">{t(`${D}forever`)}</ToggleButton>
            <ToggleButton value="h24">{t(`${D}h24`)}</ToggleButton>
            <ToggleButton value="d7">{t(`${D}d7`)}</ToggleButton>
            <ToggleButton value="custom">{t(`${D}custom`)}</ToggleButton>
          </ToggleButtonGroup>
          {expiry === 'h24' && <FormHelperText>{t(`${D}h24_note`)}</FormHelperText>}
          {expiry === 'custom' && (
            <TextField type="datetime-local" sx={{ mt: 1 }} fullWidth label={t(`${D}custom_label`)} value={custom}
              onChange={e => setCustom(e.target.value)} slotProps={{ inputLabel: { shrink: true } }}
              error={customBad && (touched || custom !== '')} helperText={customBad && custom !== '' ? t(`${D}custom_past`) : undefined} />
          )}
        </Box>
        <Typography variant="body2" sx={{ color: theme.palette.md.onSurfaceVariant }}>{t(`${D}apply_note`, { minutes: minutesText })}</Typography>
      </DialogContent>
      <DialogActions>
        <Button onClick={close}>{t(`${D}cancel`)}</Button>
        <AsyncButton variant="contained" onClick={submit}>{t(existing ? `${D}save_edit` : `${D}save`)}</AsyncButton>
      </DialogActions>
    </Dialog>
  )
}
