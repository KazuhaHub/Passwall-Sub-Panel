import { useEffect, useRef, useState } from 'react'
import { Button, CircularProgress, Stack, TextField, Typography } from '@mui/material'
import ContentCopyIcon from '@mui/icons-material/ContentCopy'
import { useTranslation } from 'react-i18next'
import { copyToClipboard } from '@/utils/clipboard'

/** Display only: issuance, identity binding and expiry remain owned by the installation dialog. */
export default function NodeInstallCommand({ command, expiresAt, disabled = false, onExpired }: {
  command: string
  expiresAt: string
  disabled?: boolean
  onExpired: () => void
}) {
  const { t, i18n } = useTranslation('admin')
  const expires = new Date(expiresAt)
  const expiryLabel = Number.isFinite(expires.getTime()) ? expires.toLocaleString(i18n.language) : expiresAt
  const [feedback, setFeedback] = useState<'copied' | 'copy_failed' | ''>('')
  const [copying, setCopying] = useState(false)
  const intent = useRef(0)
  useEffect(() => {
    intent.current += 1
    setFeedback('')
    setCopying(false)
    return () => { intent.current += 1 }
  }, [command, expiresAt, disabled])

  async function copy() {
    if (disabled || copying || !command) return
    if (Date.parse(expiresAt) <= Date.now()) { onExpired(); return }
    const current = intent.current
    setCopying(true)
    setFeedback('')
    try {
      const copied = await copyToClipboard(command)
      if (intent.current === current) setFeedback(copied ? 'copied' : 'copy_failed')
    } catch {
      if (intent.current === current) setFeedback('copy_failed')
    } finally {
      if (intent.current === current) setCopying(false)
    }
  }

  return <Stack spacing={1}>
    {!disabled && <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ alignItems: { sm: 'center' } }}>
      <TextField label={t('admin:servers.native.install_command')} value={command} autoComplete="off" fullWidth
        slotProps={{ input: { readOnly: true, sx: { fontFamily: 'monospace', fontSize: 13 } } }} />
      <Button variant="outlined" disabled={copying} startIcon={copying ? <CircularProgress size={16} /> : <ContentCopyIcon />}
        sx={{ flexShrink: 0 }} onClick={() => void copy()}>{t('admin:servers.native.copy_command')}</Button>
    </Stack>}
    {disabled && <Button variant="outlined" disabled sx={{ alignSelf: 'flex-start' }}>{t('admin:servers.native.copy_command')}</Button>}
    <Typography variant="caption" color={disabled ? 'warning.main' : 'text.secondary'}>
      {t(disabled ? 'admin:servers.native.command_expired' : 'admin:servers.native.command_safety', { time: expiryLabel })}
    </Typography>
    {feedback && <Typography role={feedback === 'copy_failed' ? 'alert' : 'status'} variant="body2"
      color={feedback === 'copy_failed' ? 'warning.main' : 'success.main'}>{t(`admin:servers.native.${feedback}`)}</Typography>}
  </Stack>
}
