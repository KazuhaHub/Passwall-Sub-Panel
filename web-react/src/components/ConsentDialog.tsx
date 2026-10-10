import { useState } from 'react'
import { Alert, Button, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, Typography, useMediaQuery, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { acceptLegalConsent } from '@/api/legal'
import { pushSnack } from './SnackbarHost'
import LegalLinks from './LegalLinks'

interface Props {
  pending: boolean
  version: number
  sessionKey: string
  onRefresh: () => Promise<void>
}

function deferredInSession(key: string): boolean {
  try { return sessionStorage.getItem(key) === 'later' } catch { return false }
}

// The parent keys this component by authenticated session. A token refresh
// keeps that key, while a new login gets a new deferral scope.
export default function ConsentDialog({ pending, version, sessionKey, onRefresh }: Props) {
  const { t } = useTranslation('user')
  const theme = useTheme()
  const mobile = useMediaQuery(theme.breakpoints.down('sm'))
  const [deferred, setDeferred] = useState(() => deferredInSession(sessionKey))
  const [acceptedVersion, setAcceptedVersion] = useState(0)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<'failed' | 'outdated' | ''>('')
  const open = pending && version > 0 && !deferred && acceptedVersion !== version

  function later() {
    if (busy) return
    try { sessionStorage.setItem(sessionKey, 'later') } catch { /* local state still closes this prompt */ }
    setDeferred(true)
  }

  async function accept() {
    if (busy) return
    setBusy(true)
    setError('')
    try {
      await acceptLegalConsent(version)
    } catch (err) {
      const status = (err as { response?: { status?: number } }).response?.status
      setError(status === 409 ? 'outdated' : 'failed')
      if (status === 409) {
        try { await onRefresh() } catch { /* retain the update warning and allow retry */ }
      }
      setBusy(false)
      return
    }
    setAcceptedVersion(version)
    setBusy(false)
    pushSnack(t('legal.recorded'), 'success')
    // Consent has already committed. A profile refresh failure must not be
    // reported as a failed acceptance or reopen the same accepted version.
    void onRefresh().catch(() => {})
  }

  return <Dialog open={open} maxWidth="sm" fullWidth fullScreen={mobile} aria-labelledby="legal-consent-title"
    onClose={(_event, reason) => { if (reason === 'escapeKeyDown') later() }}>
    <DialogTitle id="legal-consent-title">{t('legal.title')}</DialogTitle>
    <DialogContent>
      <Typography sx={{ mb: 2 }}>{t('legal.description')}</Typography>
      <LegalLinks newTab version={version} />
      {error && <Alert severity="error" sx={{ mt: 2 }}>{t(`legal.${error}`)}</Alert>}
    </DialogContent>
    <DialogActions sx={{ px: 3, pb: 3, display: 'flex', flexDirection: { xs: 'column', sm: 'row' }, gap: 1, '& > button': { minHeight: 44, width: { xs: '100%', sm: 'auto' }, m: '0 !important' } }}>
      <Button disabled={busy} onClick={later}>{t('legal.later')}</Button>
      <Button variant="contained" disabled={busy} onClick={() => void accept()} startIcon={busy ? <CircularProgress size={16} color="inherit" /> : undefined}>{t('legal.accept')}</Button>
    </DialogActions>
  </Dialog>
}
