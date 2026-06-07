import { useEffect, useState, type FormEvent } from 'react'
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  TextField,
  Typography,
} from '@mui/material'
import ContentCopyIcon from '@mui/icons-material/ContentCopy'
import { QRCodeSVG } from 'qrcode.react'
import { useTranslation } from 'react-i18next'
import type { AxiosError } from 'axios'

import FingerprintIcon from '@mui/icons-material/Fingerprint'

import { begin2FA, disable2FA, disableTOTPWithPasskey, enable2FA } from '@/api/me'
import type { M3Tokens } from '@/theme'
import { pushSnack } from '@/components/SnackbarHost'
import { copyToClipboard } from '@/utils/clipboard'

interface Props {
  open: boolean
  /** Whether 2FA is currently enabled on the account (drives enroll vs disable). */
  enabled: boolean
  /** Whether the account has a passkey — enables disabling TOTP via a passkey
   *  assertion (for users who lost their authenticator). */
  hasPasskey: boolean
  md: M3Tokens
  onClose: () => void
  /** Called after a successful enable/disable so the parent can refresh profile. */
  onChanged: () => void
}

// TwoFactorDialog drives the authenticator (TOTP) self-service flow in one modal:
//   not enabled → intro → (begin) enroll(QR+code) → (enable) recovery codes → done
//   enabled     → disable (requires a current code, or a passkey assertion)
// Recovery-code regeneration lives in RecoveryCodesDialog — kept out of here so the
// disable flow stays single-purpose and the footer isn't crowded.
export default function TwoFactorDialog({ open, enabled, hasPasskey, md, onClose, onChanged }: Props) {
  const { t } = useTranslation('user')
  type Step = 'intro' | 'enroll' | 'recovery' | 'disable'
  const [step, setStep] = useState<Step>(enabled ? 'disable' : 'intro')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [otpauthURL, setOtpauthURL] = useState('')
  const [secret, setSecret] = useState('')
  const [code, setCode] = useState('')
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([])

  // Reset to the correct starting step every time the dialog (re)opens.
  useEffect(() => {
    if (open) {
      setStep(enabled ? 'disable' : 'intro')
      setError('')
      setCode('')
      setOtpauthURL('')
      setSecret('')
      setRecoveryCodes([])
    }
  }, [open, enabled])

  function inlineError(err: unknown): string {
    const e = err as AxiosError<{ error?: string }>
    return e.response?.data?.error || t('twofa.generic_error')
  }

  async function startEnroll() {
    setBusy(true)
    setError('')
    try {
      const { otpauth_url, secret } = await begin2FA()
      setOtpauthURL(otpauth_url)
      setSecret(secret)
      setStep('enroll')
    } catch (err) {
      setError(inlineError(err))
    } finally {
      setBusy(false)
    }
  }

  async function confirmEnroll(e: FormEvent) {
    e.preventDefault()
    const c = code.trim()
    if (!c) {
      setError(t('twofa.code_required'))
      return
    }
    setBusy(true)
    setError('')
    try {
      const codes = await enable2FA(c)
      setRecoveryCodes(codes)
      setStep('recovery')
    } catch (err) {
      setError(t('twofa.code_invalid'))
      setCode('')
    } finally {
      setBusy(false)
    }
  }

  async function submitDisable(e: FormEvent) {
    e.preventDefault()
    const c = code.trim()
    if (!c) {
      setError(t('twofa.code_required'))
      return
    }
    setBusy(true)
    setError('')
    try {
      await disable2FA(c)
      pushSnack(t('twofa.disabled_toast'), 'success')
      onChanged()
      onClose()
    } catch (err) {
      setError(t('twofa.code_invalid'))
      setCode('')
    } finally {
      setBusy(false)
    }
  }

  // Disable TOTP proven by a passkey assertion — for a user who has a passkey but
  // no longer has their authenticator (or recovery codes) to type a code.
  async function disableViaPasskey() {
    setBusy(true)
    setError('')
    try {
      await disableTOTPWithPasskey()
      pushSnack(t('twofa.disabled_toast'), 'success')
      onChanged()
      onClose()
    } catch (err) {
      const name = (err as { name?: string })?.name
      if (name !== 'NotAllowedError' && name !== 'AbortError') setError(inlineError(err))
    } finally {
      setBusy(false)
    }
  }

  function finishRecovery() {
    pushSnack(t('twofa.enabled_toast'), 'success')
    onChanged()
    onClose()
  }

  const recoveryText = recoveryCodes.join('\n')

  return (
    <Dialog
      open={open}
      onClose={(_e, reason) => {
        if (busy) return
        // In the recovery step a backdrop click / Escape would silently lose the
        // one-time recovery codes (stored server-side only as hashes) AND skip
        // finishRecovery → onChanged, leaving the profile showing 2FA "off" even
        // though it's now active. Force the explicit Done button there.
        if (step === 'recovery' && (reason === 'backdropClick' || reason === 'escapeKeyDown')) return
        onClose()
      }}
      fullWidth
      maxWidth="xs"
    >
      <DialogTitle>{t('twofa.title')}</DialogTitle>

      {step === 'intro' && (
        <>
          <DialogContent>
            <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>
              {t('twofa.intro')}
            </Typography>
            {error && <Alert severity="error" sx={{ mt: 2 }}>{error}</Alert>}
          </DialogContent>
          <DialogActions>
            <Button onClick={onClose} disabled={busy}>{t('twofa.cancel')}</Button>
            <Button variant="contained" onClick={startEnroll} disabled={busy}
              startIcon={busy ? <CircularProgress size={16} color="inherit" /> : undefined}>
              {t('twofa.start')}
            </Button>
          </DialogActions>
        </>
      )}

      {step === 'enroll' && (
        <Box component="form" onSubmit={confirmEnroll}>
          <DialogContent>
            <Typography variant="body2" sx={{ color: md.onSurfaceVariant, mb: 2 }}>
              {t('twofa.scan_hint')}
            </Typography>
            <Box sx={{ display: 'flex', justifyContent: 'center', mb: 2 }}>
              <Box sx={{ p: 1.5, bgcolor: '#fff', borderRadius: 1, display: 'inline-flex' }}>
                {otpauthURL && <QRCodeSVG value={otpauthURL} size={176} />}
              </Box>
            </Box>
            <Typography variant="caption" sx={{ color: md.onSurfaceVariant }}>
              {t('twofa.manual_hint')}
            </Typography>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 2, mt: 0.5 }}>
              <Typography sx={{ fontFamily: 'monospace', fontSize: 14, wordBreak: 'break-all', flex: 1 }}>
                {secret}
              </Typography>
              <Button size="small" startIcon={<ContentCopyIcon fontSize="small" />}
                onClick={() => { void copyToClipboard(secret) }}>
                {t('twofa.copy')}
              </Button>
            </Box>
            <TextField
              label={t('twofa.code')}
              value={code}
              onChange={e => setCode(e.target.value)}
              autoFocus
              fullWidth
              autoComplete="one-time-code"
              placeholder="123456"
            />
            {error && <Alert severity="error" sx={{ mt: 2 }}>{error}</Alert>}
          </DialogContent>
          <DialogActions>
            <Button onClick={onClose} disabled={busy}>{t('twofa.cancel')}</Button>
            <Button type="submit" variant="contained" disabled={busy}
              startIcon={busy ? <CircularProgress size={16} color="inherit" /> : undefined}>
              {t('twofa.confirm')}
            </Button>
          </DialogActions>
        </Box>
      )}

      {step === 'recovery' && (
        <>
          <DialogContent>
            <Alert severity="warning" sx={{ mb: 2 }}>{t('twofa.recovery_warning')}</Alert>
            <Box sx={{
              p: 2, bgcolor: md.surfaceContainerHigh, borderRadius: 1,
              fontFamily: 'monospace', fontSize: 14, lineHeight: 1.9,
              display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 0.5,
            }}>
              {recoveryCodes.map(c => <Box key={c}>{c}</Box>)}
            </Box>
            <Box sx={{ display: 'flex', gap: 1, mt: 1.5 }}>
              <Button size="small" startIcon={<ContentCopyIcon fontSize="small" />}
                onClick={() => { void copyToClipboard(recoveryText) }}>
                {t('twofa.copy_all')}
              </Button>
            </Box>
          </DialogContent>
          <DialogActions>
            <Button variant="contained" onClick={finishRecovery}>{t('twofa.done')}</Button>
          </DialogActions>
        </>
      )}

      {step === 'disable' && (
        <Box component="form" onSubmit={submitDisable}>
          <DialogContent>
            <Typography variant="body2" sx={{ color: md.onSurfaceVariant, mb: 2 }}>
              {t('twofa.disable_hint')}
            </Typography>
            <TextField
              label={t('twofa.code')}
              value={code}
              onChange={e => setCode(e.target.value)}
              autoFocus
              fullWidth
              autoComplete="one-time-code"
              placeholder="123456"
              helperText={t('twofa.disable_code_hint')}
            />
            {hasPasskey && (
              <Button size="small" startIcon={<FingerprintIcon />} disabled={busy} sx={{ mt: 1 }}
                onClick={disableViaPasskey}>
                {t('twofa.disable_passkey', { defaultValue: '或用通行密钥停用' })}
              </Button>
            )}
            {error && <Alert severity="error" sx={{ mt: 2 }}>{error}</Alert>}
          </DialogContent>
          <DialogActions>
            <Button onClick={onClose} disabled={busy}>{t('twofa.cancel')}</Button>
            <Button type="submit" color="error" variant="contained" disabled={busy}
              startIcon={busy ? <CircularProgress size={16} color="inherit" /> : undefined}>
              {t('twofa.disable')}
            </Button>
          </DialogActions>
        </Box>
      )}
    </Dialog>
  )
}
