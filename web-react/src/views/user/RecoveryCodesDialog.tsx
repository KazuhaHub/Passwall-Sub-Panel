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
import { useTranslation } from 'react-i18next'
import type { AxiosError } from 'axios'

import FingerprintIcon from '@mui/icons-material/Fingerprint'

import { regenerate2FARecovery, regenerateRecoveryWithPasskey } from '@/api/me'
import type { M3Tokens } from '@/theme'
import { pushSnack } from '@/components/SnackbarHost'
import { copyToClipboard } from '@/utils/clipboard'

interface Props {
  open: boolean
  /** Unused recovery codes left on the account (drives the count + "low" warning). */
  remaining: number
  /** Whether the account has a passkey — enables regenerating via a passkey
   *  assertion (no TOTP/recovery code needed). */
  hasPasskey: boolean
  md: M3Tokens
  onClose: () => void
  /** Called after a successful regenerate so the parent can refresh the count. */
  onChanged: () => void
}

// RecoveryCodesDialog manages a user's one-time recovery codes independently of
// TOTP — so a passkey-only account (whose codes were issued at passkey enrollment)
// can view how many remain and regenerate them. Regeneration is a step-up: it
// needs a current authenticator code OR one of the existing recovery codes as
// proof, then shows the fresh set ONCE.
export default function RecoveryCodesDialog({ open, remaining, hasPasskey, md, onClose, onChanged }: Props) {
  const { t } = useTranslation('user')
  type Step = 'view' | 'regenerate' | 'codes'
  const [step, setStep] = useState<Step>('view')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [code, setCode] = useState('')
  const [codes, setCodes] = useState<string[]>([])

  useEffect(() => {
    if (open) {
      setStep('view')
      setError('')
      setCode('')
      setCodes([])
    }
  }, [open])

  async function submitRegenerate(e: FormEvent) {
    e.preventDefault()
    const c = code.trim()
    if (!c) {
      setError(t('twofa.code_required'))
      return
    }
    setBusy(true)
    setError('')
    try {
      setCodes(await regenerate2FARecovery(c))
      setStep('codes')
    } catch (err) {
      const e = err as AxiosError<{ error?: string }>
      setError(e.response?.data?.error || t('twofa.code_invalid'))
      setCode('')
    } finally {
      setBusy(false)
    }
  }

  // Regenerate proven by a passkey assertion — no TOTP/recovery code needed, so
  // it works for a passkey holder who lost their authenticator (or has no codes).
  async function regenViaPasskey() {
    setBusy(true)
    setError('')
    try {
      setCodes(await regenerateRecoveryWithPasskey())
      setStep('codes')
    } catch (err) {
      const name = (err as { name?: string })?.name
      if (name !== 'NotAllowedError' && name !== 'AbortError') {
        const e = err as AxiosError<{ error?: string }>
        setError(e.response?.data?.error || t('twofa.generic_error'))
      }
    } finally {
      setBusy(false)
    }
  }

  function finishCodes() {
    pushSnack(t('twofa.regen_done_toast', { defaultValue: '已重新生成备用码' }), 'success')
    onChanged()
    onClose()
  }

  const low = remaining > 0 && remaining <= 3

  return (
    <Dialog open={open}
      onClose={(_e, reason) => {
        if (busy) return
        // Don't lose freshly-shown codes to a backdrop click; force the button.
        if (step === 'codes' && (reason === 'backdropClick' || reason === 'escapeKeyDown')) return
        onClose()
      }}
      fullWidth maxWidth="xs"
      PaperProps={{ sx: { bgcolor: md.surfaceContainerHigh } }}>
      <DialogTitle>{t('recovery.title', { defaultValue: '备用码' })}</DialogTitle>

      {step === 'view' && (
        <>
          <DialogContent>
            <Typography variant="body2" sx={{ color: md.onSurfaceVariant, mb: 2 }}>
              {t('recovery.intro', { defaultValue: '当你无法使用验证器或通行密钥时，可用一次性备用码登录。每个备用码只能用一次。' })}
            </Typography>
            <Alert severity={remaining === 0 ? 'warning' : low ? 'warning' : 'info'} sx={{ mb: 1 }}>
              {remaining === 0
                ? t('recovery.none_left', { defaultValue: '你已没有可用的备用码，请重新生成一组。' })
                : t('recovery.remaining', { count: remaining, defaultValue: '剩余 {{count}} 个备用码' })}
            </Alert>
            {error && <Alert severity="error" sx={{ mb: 1 }}>{error}</Alert>}
          </DialogContent>
          <DialogActions sx={{ flexWrap: 'wrap' }}>
            <Button onClick={onClose} disabled={busy}>{t('twofa.cancel')}</Button>
            {hasPasskey && (
              <Button startIcon={<FingerprintIcon />} disabled={busy} onClick={regenViaPasskey}>
                {t('recovery.regen_passkey', { defaultValue: '用通行密钥重新生成' })}
              </Button>
            )}
            <Button variant="contained" onClick={() => { setStep('regenerate'); setCode(''); setError('') }}>
              {t('recovery.regen_code', { defaultValue: '用验证码重新生成' })}
            </Button>
          </DialogActions>
        </>
      )}

      {step === 'regenerate' && (
        <Box component="form" onSubmit={submitRegenerate}>
          <DialogContent>
            <Typography variant="body2" sx={{ color: md.onSurfaceVariant, mb: 2 }}>
              {t('twofa.regenerate_hint', { defaultValue: '输入当前验证器代码或一个备用码以重新生成一组新的备用码。旧的备用码将立即作废。' })}
            </Typography>
            <TextField label={t('twofa.code')} value={code} onChange={e => setCode(e.target.value)}
              autoFocus fullWidth autoComplete="one-time-code" placeholder="123456" />
            {error && <Alert severity="error" sx={{ mt: 2 }}>{error}</Alert>}
          </DialogContent>
          <DialogActions>
            <Button onClick={() => { setStep('view'); setCode(''); setError('') }} disabled={busy}>
              {t('twofa.cancel')}
            </Button>
            <Button type="submit" variant="contained" disabled={busy}
              startIcon={busy ? <CircularProgress size={16} color="inherit" /> : undefined}>
              {t('twofa.regenerate', { defaultValue: '重新生成备用码' })}
            </Button>
          </DialogActions>
        </Box>
      )}

      {step === 'codes' && (
        <>
          <DialogContent>
            <Alert severity="warning" sx={{ mb: 2 }}>{t('twofa.recovery_warning')}</Alert>
            <Box sx={{
              p: 2, bgcolor: md.surfaceContainerHighest, borderRadius: 1,
              fontFamily: 'monospace', fontSize: 14, lineHeight: 1.9,
              display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 0.5,
            }}>
              {codes.map(c => <Box key={c}>{c}</Box>)}
            </Box>
            <Button size="small" startIcon={<ContentCopyIcon fontSize="small" />} sx={{ mt: 1.5 }}
              onClick={() => { void copyToClipboard(codes.join('\n')) }}>
              {t('twofa.copy_all')}
            </Button>
          </DialogContent>
          <DialogActions>
            <Button variant="contained" onClick={finishCodes}>{t('twofa.done')}</Button>
          </DialogActions>
        </>
      )}
    </Dialog>
  )
}
