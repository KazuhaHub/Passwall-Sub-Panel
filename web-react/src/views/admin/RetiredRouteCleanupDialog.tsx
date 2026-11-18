import { useState } from 'react'
import { isAxiosError } from 'axios'
import { Alert, Button, Dialog, DialogActions, DialogContent, DialogTitle, Stack, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { cleanRetiredRoutes, previewRetiredRoutes, type RetiredRoutePreview, type Server } from '@/api/servers'

export default function RetiredRouteCleanupDialog({ server, onClose }: { server: Server; onClose: () => void }) {
  const { t } = useTranslation(['admin'])
  const [preview, setPreview] = useState<RetiredRoutePreview | null>(null)
  const [removed, setRemoved] = useState<number | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function inspect() {
    setBusy(true)
    setError('')
    setPreview(null)
    try {
      setPreview(await previewRetiredRoutes(server.id))
    } catch {
      setError(t('admin:servers.retired_routes.preview_failed'))
    } finally {
      setBusy(false)
    }
  }

  async function apply() {
    if (!preview?.rule_tags.length) return
    setBusy(true)
    setError('')
    try {
      const result = await cleanRetiredRoutes(server.id, preview.revision)
      setRemoved(result.rule_tags.length)
    } catch (err) {
      setError(t(isAxiosError(err) && err.response?.status === 409
        ? 'admin:servers.retired_routes.stale' : 'admin:servers.retired_routes.cleanup_failed'))
    } finally {
      setPreview(null)
      setBusy(false)
    }
  }

  return (
    <Dialog open onClose={busy ? undefined : onClose} fullWidth maxWidth="sm">
      <DialogTitle>{t('admin:servers.retired_routes.title', { server: server.name })}</DialogTitle>
      <DialogContent dividers>
        <Stack spacing={2}>
          <Typography>{t('admin:servers.retired_routes.description')}</Typography>
          {error && <Alert severity="error">{error}</Alert>}
          {removed !== null && <Alert severity="success">{t('admin:servers.retired_routes.removed', { count: removed })}</Alert>}
          {preview && (preview.rule_tags.length === 0
            ? <Alert severity="info">{t('admin:servers.retired_routes.empty')}</Alert>
            : <>
                <Alert severity="warning">{t('admin:servers.retired_routes.confirm', { count: preview.rule_tags.length })}</Alert>
                <ul style={{ overflowWrap: 'anywhere', margin: 0 }}>
                  {preview.rule_tags.map((tag, index) => <li key={`${tag}-${index}`}><code>{tag}</code></li>)}
                </ul>
              </>)}
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button disabled={busy} onClick={onClose}>{t('admin:servers.retired_routes.close')}</Button>
        {removed === null && <Button disabled={busy} onClick={() => void inspect()}>{t('admin:servers.retired_routes.preview')}</Button>}
        {!!preview?.rule_tags.length && (
          <Button color="error" variant="contained" disabled={busy} onClick={() => void apply()}>
            {t('admin:servers.retired_routes.cleanup')}
          </Button>
        )}
      </DialogActions>
    </Dialog>
  )
}
