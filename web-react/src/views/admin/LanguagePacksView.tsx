import { useEffect, useRef, useState } from 'react'
import {
  Box,
  Button,
  Card,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  IconButton,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  TextField,
  Tooltip,
  Typography,
  useTheme,
} from '@mui/material'
import UploadIcon from '@mui/icons-material/UploadFileOutlined'
import DownloadIcon from '@mui/icons-material/DownloadOutlined'
import DeleteIcon from '@mui/icons-material/DeleteOutlined'
import WarningAmberIcon from '@mui/icons-material/WarningAmberOutlined'
import { useTranslation } from 'react-i18next'

import { useCan } from '@/utils/permissions'
import { listLocales, saveLocale, deleteLocale, MAX_LOCALE_PACK_BYTES, type LocaleMeta, type LocalePack } from '@/api/locales'
import { loadBuiltinSource, LANGUAGE_PACK_FORMAT } from '@/i18n'
import { getVersion } from '@/api/version'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import PageHeader from '@/components/PageHeader'

// Admin surface for runtime-uploaded UI language packs. A pack is a single JSON
// file (see api/locales.ts LocalePack); uploading it makes the language
// selectable after a page reload. Writes are admin-only (gated on config.write
// here + adminGroup server-side); the public read endpoints serve the pack to
// every client, including the pre-auth login screen.
export default function LanguagePacksView() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['admin', 'common'])
  const canConfig = useCan('config.write')

  const [items, setItems] = useState<LocaleMeta[]>([])
  const [loading, setLoading] = useState(false)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [jsonText, setJsonText] = useState('')
  const [busy, setBusy] = useState(false)
  const [exporting, setExporting] = useState(false)
  // Current panel version, for the base_version staleness warning.
  const [currentVersion, setCurrentVersion] = useState('')
  const fileInputRef = useRef<HTMLInputElement>(null)

  useEffect(() => { void load(); void loadVersion() }, [])

  async function load() {
    setLoading(true)
    try {
      setItems(await listLocales())
    } finally { setLoading(false) }
  }

  async function loadVersion() {
    try { setCurrentVersion((await getVersion()).version) } catch { /* advisory only */ }
  }

  function openUpload() {
    setJsonText('')
    setDialogOpen(true)
  }

  function pickFile() {
    fileInputRef.current?.click()
  }

  async function onFileChosen(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    // Reset the input so choosing the same file again re-fires change.
    e.target.value = ''
    if (!file) return
    try {
      setJsonText(await file.text())
    } catch {
      pushSnack(t('admin:languagePacks.toast.read_failed'), 'error')
    }
  }

  async function submit() {
    // Client-side size guard so the user gets a clear message rather than a raw
    // 413 from the backend cap.
    if (new Blob([jsonText]).size > MAX_LOCALE_PACK_BYTES) {
      pushSnack(t('admin:languagePacks.toast.too_large', { max: Math.round(MAX_LOCALE_PACK_BYTES / 1024) }), 'error')
      return
    }
    let pack: LocalePack
    try {
      pack = JSON.parse(jsonText)
    } catch {
      pushSnack(t('admin:languagePacks.toast.invalid_json'), 'error')
      return
    }
    // Cheap client-side guardrails; the server re-validates authoritatively.
    if (!pack || typeof pack !== 'object' || !pack.code || !pack.name || !pack.namespaces) {
      pushSnack(t('admin:languagePacks.toast.missing_fields'), 'warning')
      return
    }
    setBusy(true)
    try {
      await saveLocale(pack)
      pushSnack(t('admin:languagePacks.toast.saved'), 'success')
      setDialogOpen(false)
      await load()
      // The switcher's language list is built at boot, so a freshly-uploaded
      // pack only appears after a reload.
      pushSnack(t('admin:languagePacks.toast.reload_hint'), 'info')
    } finally { setBusy(false) }
  }

  async function confirmDelete(m: LocaleMeta) {
    const ok = await confirm({
      title: t('admin:languagePacks.confirm.delete_title'),
      message: t('admin:languagePacks.confirm.delete_message', { code: m.code, name: m.name }),
      destructive: true,
    })
    if (!ok) return
    await deleteLocale(m.code)
    pushSnack(t('admin:languagePacks.toast.deleted'), 'success')
    await load()
  }

  // Export the shipped en-US strings as a fill-in-the-blank pack template so a
  // translator starts from the exact current keys. Entirely client-side — the
  // base lives in the JS bundle, not the backend.
  async function exportBase() {
    setExporting(true)
    try {
      const namespaces = await loadBuiltinSource('en-US')
      let baseVersion = ''
      try { baseVersion = (await getVersion()).version } catch { /* version is advisory */ }
      const pack: LocalePack = {
        psp_language_pack: LANGUAGE_PACK_FORMAT,
        code: '',
        name: '',
        author: '',
        base_language: 'en-US',
        base_version: baseVersion,
        namespaces: namespaces as Record<string, Record<string, unknown>>,
      }
      const blob = new Blob([JSON.stringify(pack, null, 2)], { type: 'application/json' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = 'psp-language-pack-base-en-US.json'
      a.click()
      URL.revokeObjectURL(url)
    } finally { setExporting(false) }
  }

  return (
    <Box sx={{ p: 3 }}>
      <PageHeader
        title={t('admin:languagePacks.title')}
        subtitle={t('admin:languagePacks.subtitle')}
        actions={canConfig && (
          <>
            <Button variant="outlined" startIcon={exporting ? <CircularProgress size={16} /> : <DownloadIcon />}
              disabled={exporting} onClick={exportBase}>
              {t('admin:languagePacks.export_base')}
            </Button>
            <Button variant="contained" startIcon={<UploadIcon />} onClick={openUpload}>
              {t('admin:languagePacks.upload')}
            </Button>
          </>
        )}
      />
      <Card sx={{ mt: 2, bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)', overflow: 'hidden' }}>
        <TableContainer>
          <Table>
            <TableHead>
              <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' } }}>
                <TableCell>{t('admin:languagePacks.table.code')}</TableCell>
                <TableCell>{t('admin:languagePacks.table.name')}</TableCell>
                <TableCell>{t('admin:languagePacks.table.author')}</TableCell>
                <TableCell>{t('admin:languagePacks.table.base_version')}</TableCell>
                <TableCell align="right">{t('admin:languagePacks.table.actions')}</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {loading && items.length === 0 && (
                <TableRow><TableCell colSpan={5} sx={{ textAlign: 'center', py: 6 }}><CircularProgress size={24} /></TableCell></TableRow>
              )}
              {!loading && items.length === 0 && (
                <TableRow><TableCell colSpan={5} sx={{ textAlign: 'center', py: 6, color: md.onSurfaceVariant }}>
                  {t('admin:languagePacks.empty')}
                </TableCell></TableRow>
              )}
              {items.map(m => (
                <TableRow key={m.code} hover sx={{ '& td': { borderBottom: `1px solid ${md.outlineVariant}`, whiteSpace: 'nowrap' } }}>
                  <TableCell sx={{ fontSize: 13 }}>{m.code}</TableCell>
                  <TableCell sx={{ fontWeight: 500 }}>{m.name}</TableCell>
                  <TableCell sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{m.author || '—'}</TableCell>
                  <TableCell sx={{ fontSize: 13, color: md.onSurfaceVariant }}>
                    <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 0.5 }}>
                      {m.base_version || '—'}
                      {m.base_version && currentVersion && m.base_version !== currentVersion && (
                        <Tooltip title={t('admin:languagePacks.stale_warning', { base: m.base_version, current: currentVersion })}>
                          <WarningAmberIcon sx={{ fontSize: 16, color: md.error }} />
                        </Tooltip>
                      )}
                    </Box>
                  </TableCell>
                  <TableCell align="right">
                    {canConfig && (
                      <Tooltip title={t('common:actions.delete')}>
                        <IconButton size="small" onClick={() => confirmDelete(m)} sx={{ color: md.error }}>
                          <DeleteIcon fontSize="small" />
                        </IconButton>
                      </Tooltip>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableContainer>
      </Card>
      <input ref={fileInputRef} type="file" accept="application/json,.json" hidden onChange={onFileChosen} />
      <Dialog open={dialogOpen} onClose={() => !busy && setDialogOpen(false)}
        slotProps={{
          paper: { sx: { borderRadius: 3, bgcolor: md.surfaceContainerHigh, width: 720, maxWidth: '95vw' } }
        }}>
        <DialogTitle>{t('admin:languagePacks.upload_title')}</DialogTitle>
        <DialogContent>
          <Typography variant="body2" sx={{ color: md.onSurfaceVariant, mb: 2 }}>
            {t('admin:languagePacks.upload_help')}
          </Typography>
          <Button variant="outlined" size="small" startIcon={<UploadIcon />} onClick={pickFile} sx={{ mb: 2 }}>
            {t('admin:languagePacks.choose_file')}
          </Button>
          <TextField fullWidth multiline minRows={10} maxRows={20}
            placeholder={t('admin:languagePacks.paste_placeholder')}
            value={jsonText}
            onChange={e => setJsonText(e.target.value)}
            sx={{ '& textarea': { fontFamily: 'monospace', fontSize: 12 } }} />
          <Typography variant="caption" sx={{ color: md.onSurfaceVariant, mt: 1, display: 'block' }}>
            {t('admin:languagePacks.size_hint', { max: Math.round(MAX_LOCALE_PACK_BYTES / 1024) })}
          </Typography>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setDialogOpen(false)} disabled={busy} variant="text">{t('common:actions.cancel')}</Button>
          <Button onClick={submit} variant="contained" disabled={busy || !jsonText.trim()}
            startIcon={busy ? <CircularProgress size={16} color="inherit" /> : null}>
            {t('admin:languagePacks.upload')}
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  );
}
