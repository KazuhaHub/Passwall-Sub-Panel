import { useRef, useState } from 'react'
import { Alert, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, IconButton, Stack, TextField, ToggleButton, ToggleButtonGroup, Typography, useMediaQuery, useTheme } from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import { useTranslation } from 'react-i18next'
import { AsyncButton } from '@/components/AsyncButton'
import FieldHint from '@/components/FieldHint'
import { pushSnack } from '@/components/SnackbarHost'
import { useCreateDestinationException } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { discardSettingsCopy } from '../confirmCopy'
import { destinationError } from '../errors'
import AddExemptionDialog from './AddExemptionDialog'
import { testTarget } from './testDraft'
const P = 'admin:access_control.exception.'
export default function GlobalExceptionDialog({ target, userId, etaMs, onClose }: { target: string; userId?: number; etaMs?: number; onClose: () => void }) {
  const { t } = useTranslation(['admin', 'common']), theme = useTheme(), mobile = useMediaQuery(theme.breakpoints.down('sm')), save = useCreateDestinationException(useQueryScope())
  const normalized = testTarget(target), initial = normalized.ip ? 'host' : 'site'
  const [match, setMatch] = useState<'site' | 'host'>(initial), [accountOnly, setAccountOnly] = useState(false), [error, setError] = useState(''), [busy, setBusy] = useState(false), admission = useRef(false)
  const dirty = match !== initial, copy = discardSettingsCopy(t), closeCheck = useDirtyClose(dirty, copy)
  useLeaveGuard(dirty && !accountOnly, copy, (next, current) => next.pathname !== current.pathname || next.search !== current.search, busy)
  const close = () => { if (!admission.current) void closeCheck().then(ok => { if (ok) onClose() }) }
  const submit = async () => {
    if (admission.current || !normalized.target) return
    admission.current = true; setBusy(true); setError('')
    try {
      const response = await save.mutateAsync({ target: normalized.target, match, scope: 'global' })
      pushSnack(t(`${P}${response.created ? 'created' : 'saved'}`, { entry: response.entry }), 'success'); onClose()
    } catch (err) { setError(destinationError(err).error) } finally { admission.current = false; setBusy(false) }
  }
  if (accountOnly && userId) return <AddExemptionDialog userId={userId} etaMs={etaMs} accessWarning onClose={() => setAccountOnly(false)} onSaved={onClose} />
  return <Dialog open fullWidth maxWidth="sm" fullScreen={mobile} onClose={close} aria-labelledby="global-exception-title">
    <DialogTitle id="global-exception-title" sx={{ display: 'flex', alignItems: 'center' }}><Box component="span" sx={{ flex: 1 }}>{t(`${P}title`)}</Box><IconButton disabled={busy} aria-label={t('common:actions.close')} onClick={close}><CloseIcon /></IconButton></DialogTitle>
    <DialogContent dividers><Stack spacing={2.5}>
      {error && <Alert severity="error">{t(`${P}failed`)}<Typography variant="caption">{error}</Typography></Alert>}
      <TextField label={t('admin:access_control.test.target')} value={normalized.target ?? target} slotProps={{ input: { readOnly: true } }} />
      <ToggleButtonGroup exclusive value="global" aria-label={t(`${P}scope`)}><ToggleButton value="global" disabled={busy}>{t(`${P}global`)}</ToggleButton><ToggleButton value="account" disabled={busy || !userId} onClick={() => setAccountOnly(true)}>{t(`${P}account_only`)}</ToggleButton></ToggleButtonGroup>
      <FieldHint tone="amber" summary={t(`${P}global_summary`)} detail={t(`${P}global_detail`)} />
      {normalized.ip ? <Typography>{t(`${P}ip_single`)}</Typography> : <><ToggleButtonGroup exclusive value={match} aria-label={t(`${P}match`)} onChange={(_, value) => { if (value) { setMatch(value); setError('') } }}><ToggleButton disabled={busy} value="site">{t(`${P}site`)}</ToggleButton><ToggleButton disabled={busy} value="host">{t(`${P}host`)}</ToggleButton></ToggleButtonGroup><Typography variant="body2" color="text.secondary">{t(`${P}${match === 'site' ? 'site_hint' : 'host_hint'}`)}</Typography></>}
      <Typography variant="body2" color="text.secondary">{t(`${P}first_use`)}</Typography>
    </Stack></DialogContent>
    <DialogActions><Button disabled={busy} onClick={close}>{t('common:actions.cancel')}</Button><AsyncButton variant="contained" pending={busy} disabled={busy || !normalized.target} onClick={submit}>{t(`${P}save`)}</AsyncButton></DialogActions>
  </Dialog>
}
