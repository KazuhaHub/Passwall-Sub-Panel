import { useState } from 'react'
import {
  Alert, Box, Button, Checkbox, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, FormHelperText, Typography, useTheme,
} from '@mui/material'
import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { LEGAL_CONTENT_MAX_BYTES, publishLegal, type LegalKind } from '@/api/legal'
import { pushSnack } from '@/components/SnackbarHost'
import { legalKeys } from '@/query/keys'
import { useAffectedUsers } from '@/query/legal'
import { useQueryScope } from '@/query/useQueryScope'

/** How many characters were added to and removed from `from` to make `to`: what
 *  is left after trimming the text they share at both ends. A reading aid for
 *  the confirmation, not a diff. */
export function changeSize(from: string, to: string): { added: number; removed: number } {
  const a = Array.from(from)
  const b = Array.from(to)
  let head = 0
  while (head < a.length && head < b.length && a[head] === b[head]) head++
  let tail = 0
  while (tail < a.length - head && tail < b.length - head && a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail++
  return { added: b.length - head - tail, removed: a.length - head - tail }
}

interface Props {
  kind: LegalKind
  locale: string
  /** The version this publication will be (the server numbers it; this is the one it expects). */
  version: number
  /** The text now published in this locale, null when there is none. */
  previous: string | null
  content: string
  onClose: () => void
  onPublished: () => void
}

export default function LegalPublishDialog({ kind, locale, version, previous, content, onClose, onPublished }: Props) {
  const md = useTheme().palette.md
  const { t } = useTranslation('admin')
  const scope = useQueryScope()
  const qc = useQueryClient()
  const affected = useAffectedUsers(scope, true)
  const [bump, setBump] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const doc = t(`auth:legal.kind.${kind}`)
  const size = previous === null ? null : changeSize(previous, content)

  async function publish() {
    setBusy(true)
    setError('')
    try {
      const res = await publishLegal(kind, { locale, content, consent_bump: bump })
      pushSnack(t('settings.legal.publish.done', { version: res.document.version }), 'success')
      await qc.invalidateQueries({ queryKey: legalKeys.all(scope) })
      onPublished()
    } catch (err) {
      const res = (err as { response?: { status?: number; data?: { code?: string } } }).response
      if (res?.status === 409) {
        // Somebody else took the version number: show what they published, keep
        // the draft, and let the administrator decide.
        pushSnack(t('settings.legal.publish.conflict'), 'warning')
        await qc.invalidateQueries({ queryKey: legalKeys.all(scope) })
        onClose()
      } else if (res?.data?.code === 'legal_content_too_large') {
        setError(t('settings.legal.publish.too_large', { max: LEGAL_CONTENT_MAX_BYTES.toLocaleString('en-US') }))
      } else {
        setError(t('settings.legal.publish.failed'))
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open maxWidth="sm" fullWidth onClose={busy ? undefined : onClose} aria-labelledby="legal-publish-title">
      <DialogTitle id="legal-publish-title">{t('settings.legal.publish.title', { doc, locale, version })}</DialogTitle>
      <DialogContent>
        <Typography sx={{ color: md.onSurfaceVariant, fontSize: 14 }}>
          {size ? t('settings.legal.publish.diff', size) : t('settings.legal.publish.first')}
        </Typography>
        <Box sx={{ mt: 1.5 }}>
          <FormControlLabel
            control={<Checkbox checked={bump} onChange={(_, c) => setBump(c)} />}
            label={t('settings.legal.publish.bump')} />
          <FormHelperText sx={{ ml: 4, mt: -0.5, fontWeight: bump ? 600 : 400, color: bump ? md.onSurface : undefined }}>
            {affected.data
              ? t('settings.legal.publish.bump_hint', { users: affected.data.affected_users.toLocaleString('en-US') })
              : t('settings.legal.publish.bump_hint_loading')}
          </FormHelperText>
        </Box>
        {error && <Alert severity="error" sx={{ mt: 2 }}>{error}</Alert>}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose} disabled={busy}>{t('settings.legal.publish.cancel')}</Button>
        <Button variant="contained" onClick={() => void publish()} disabled={busy}>{t('settings.legal.publish.confirm')}</Button>
      </DialogActions>
    </Dialog>
  )
}
