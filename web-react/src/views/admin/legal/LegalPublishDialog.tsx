import { useEffect, useRef, useState } from 'react'
import { Alert, Button, Checkbox, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, Stack, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { getLegalAffectedUsers, type LegalKind } from '@/api/legal'
import { countLegalChanges, type LegalChanges } from './legalText'

export default function LegalPublishDialog({ kind, locale, version, previous, content, busy, onCancel, onPublish }: {
  kind: LegalKind; locale: string; version: number; previous: string; content: string; busy: boolean
  onCancel: () => void; onPublish: (major: boolean) => Promise<void>
}) {
  const { t } = useTranslation('admin')
  const [major, setMajor] = useState(false)
  const [count, setCount] = useState<number | null>(null)
  const [failed, setFailed] = useState(false)
  const [retry, setRetry] = useState(0)
  const [changes, setChanges] = useState<LegalChanges | null | undefined>(undefined)
  const submitting = useRef(false)
  useEffect(() => {
    let live = true
    setChanges(undefined)
    void countLegalChanges(previous, content).then(value => { if (live) setChanges(value) })
    return () => { live = false }
  }, [previous, content])
  useEffect(() => {
    setCount(null); setFailed(false)
    if (!major) return
    const controller = new AbortController()
    void getLegalAffectedUsers(controller.signal).then(value => {
      if (!controller.signal.aborted) setCount(value)
    }).catch(() => { if (!controller.signal.aborted) setFailed(true) })
    return () => controller.abort()
  }, [major, retry])
  async function submit() {
    if (submitting.current || busy || (major && count === null)) return
    submitting.current = true
    try { await onPublish(major) } finally { submitting.current = false }
  }
  return <Dialog open onClose={(_, reason) => { if (!busy && reason !== 'backdropClick') onCancel() }} aria-labelledby="legal-publish-title" maxWidth="sm" fullWidth sx={{ zIndex: theme => theme.zIndex.modal + 2 }}>
    <DialogTitle id="legal-publish-title">{t('legal.publish_title', { name: t(`legal.${kind}`), locale, version })}</DialogTitle>
    <DialogContent>
      <Stack spacing={2}>
        <Typography variant="body2" aria-live="polite">{changes === undefined ? t('legal.diff_loading') : changes === null ? t('legal.diff_unavailable') : t('legal.diff', { ...changes })}</Typography>
        <FormControlLabel control={<Checkbox checked={major} disabled={busy} onChange={e => setMajor(e.target.checked)} />} label={t('legal.major')} />
        <Typography variant="body2" color="text.secondary">{t('legal.major_hint')}</Typography>
        {major && (failed ? <Alert severity="error" action={<Button onClick={() => setRetry(value => value + 1)}>{t('legal.retry')}</Button>}>{t('legal.count_failed')}</Alert> : count === null ? <Typography role="status">{t('legal.count_loading')}</Typography> : <Typography aria-live="polite">{t('legal.affected', { count })}</Typography>)}
      </Stack>
    </DialogContent>
    <DialogActions sx={{ p: 2, flexDirection: { xs: 'column-reverse', sm: 'row' }, '& button': { minHeight: 44, width: { xs: '100%', sm: 'auto' } } }}>
      <Button disabled={busy} onClick={onCancel}>{t('legal.cancel')}</Button>
      <Button variant="contained" disabled={busy || (major && count === null)} onClick={() => void submit()} startIcon={busy ? <CircularProgress size={16} /> : undefined}>{t('legal.publish')}</Button>
    </DialogActions>
  </Dialog>
}
