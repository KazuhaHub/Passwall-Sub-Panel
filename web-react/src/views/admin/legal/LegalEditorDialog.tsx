import { Component, lazy, Suspense, useEffect, useRef, useState, type ReactNode } from 'react'
import { Alert, Box, Button, CircularProgress, Dialog, IconButton, MenuItem, Stack, Tab, Tabs, TextField, Typography, useMediaQuery, useTheme } from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import { useBlocker } from 'react-router'
import { useTranslation } from 'react-i18next'
import { getLegalLatest, publishLegal, type DataCollection, type LegalAdminDocument, type LegalKind, type LegalPublication } from '@/api/legal'
import type { CodeEditorHandle } from '@/components/CodeEditor'
import LegalDocument, { splitCollectionMarker } from '@/components/LegalDocument'
import FieldHint from '@/components/FieldHint'
import { pushSnack } from '@/components/SnackbarHost'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { legalBytes, MAX_LEGAL_BYTES } from './legalText'
import LegalPublishDialog from './LegalPublishDialog'

const CodeEditor = lazy(() => import('@/components/CodeEditor'))

class PreviewBoundary extends Component<{ children: ReactNode; fallback: ReactNode }, { failed: boolean }> {
  state = { failed: false }
  static getDerivedStateFromError() { return { failed: true } }
  render() { return this.state.failed ? this.props.fallback : this.props.children }
}

export default function LegalEditorDialog({ kind, languages, collection, onCollectionRetry, onClose, onPublished }: {
  kind: LegalKind; languages: string[]; collection: DataCollection | null; onCollectionRetry: () => void
  onClose: () => void; onPublished: (result: LegalPublication) => void
}) {
  const { t } = useTranslation('admin')
  const theme = useTheme()
  const mobile = useMediaQuery(theme.breakpoints.down('sm'))
  const [locale, setLocale] = useState('zh-CN')
  const [base, setBase] = useState<LegalAdminDocument | null>(null)
  const [content, setContent] = useState('')
  const [preview, setPreview] = useState('')
  const [loading, setLoading] = useState(true)
  const [failed, setFailed] = useState(false)
  const [retry, setRetry] = useState(0)
  const [busy, setBusy] = useState(false)
  const [stale, setStale] = useState(false)
  const [warning, setWarning] = useState('')
  const [confirming, setConfirming] = useState(false)
  const [pane, setPane] = useState<'edit' | 'preview'>('edit')
  const editor = useRef<CodeEditorHandle>(null)
  const dirty = !loading && !failed && content !== (base?.content ?? '')
  const askClose = useDirtyClose(dirty, {
    title: t('legal.discard_title'), message: t('legal.discard_message'),
    confirmText: t('legal.discard'), cancelText: t('legal.keep_editing'),
  })
  const guard = useRef({ askClose, busy })
  guard.current = { askClose, busy }
  const blocker = useBlocker(({ currentLocation, nextLocation }) => (dirty || busy) && (
    currentLocation.pathname !== nextLocation.pathname
    || new URLSearchParams(nextLocation.search).get('tab') !== 'legal'
  ))
  useEffect(() => {
    if (blocker.state !== 'blocked') return
    if (guard.current.busy) { blocker.reset(); return }
    let live = true
    void guard.current.askClose().then(ok => {
      if (!live) return
      if (ok) blocker.proceed()
      else blocker.reset()
    })
    return () => { live = false }
  }, [blocker])
  useEffect(() => {
    if (!dirty && !busy) return
    const beforeUnload = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = '' }
    window.addEventListener('beforeunload', beforeUnload)
    return () => window.removeEventListener('beforeunload', beforeUnload)
  }, [dirty, busy])
  useEffect(() => {
    const controller = new AbortController()
    setLoading(true); setFailed(false); setWarning(''); setStale(false)
    void getLegalLatest(kind, locale, controller.signal).then(doc => {
      if (controller.signal.aborted) return
      setBase(doc); setContent(doc?.content ?? ''); setPreview(doc?.content ?? '')
    }).catch(() => { if (!controller.signal.aborted) setFailed(true) }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [kind, locale, retry])
  useEffect(() => {
    const timer = window.setTimeout(() => setPreview(content), 300)
    return () => window.clearTimeout(timer)
  }, [content])
  const bytes = legalBytes(content)
  const hasMarker = splitCollectionMarker(content) !== null
  const valid = bytes <= MAX_LEGAL_BYTES && !content.includes('\0')
  async function close() { if (!busy && await askClose()) onClose() }
  async function changeLocale(next: string) {
    if (busy || loading || next === locale || !await askClose()) return
    setConfirming(false); setLocale(next)
  }
  async function reloadBase() {
    try {
      const latest = await getLegalLatest(kind, locale)
      setBase(latest); setStale(false); setWarning(t('legal.conflict'))
    } catch { setStale(true); setWarning(t('legal.conflict_reload_failed')) }
  }
  async function publish(major: boolean) {
    if (busy || !valid || loading || failed || stale) return
    setBusy(true); setWarning('')
    try {
      const result = await publishLegal(kind, locale, content, major, base?.version ?? 0)
      onPublished(result); pushSnack(t('legal.published'), 'success'); onClose()
    } catch (error) {
      setConfirming(false)
      if ((error as { response?: { status?: number } }).response?.status === 409) {
        setStale(true); await reloadBase()
      } else setWarning(t('legal.publish_failed'))
    } finally { setBusy(false) }
  }
  const previewPanel = <Box sx={{ p: { xs: 2, sm: 3 }, overflow: 'auto', minWidth: 0 }}>
    {!hasMarker && <FieldHint summary={t('legal.marker_missing')} detail={t('legal.marker_detail')} tone="warning" />}
    {collection ? <PreviewBoundary key={preview} fallback={<Alert severity="error">{t('legal.preview_failed')}</Alert>}>
      <LegalDocument document={{ version: base?.version ?? 0, consent_version: 0, locale, content: preview, published_at: base?.published_at ?? '', data_collection: collection }} />
    </PreviewBoundary> : <Alert severity="error" action={<Button onClick={onCollectionRetry}>{t('legal.retry')}</Button>}>{t('legal.collection_failed')}</Alert>}
  </Box>
  return <Dialog open fullScreen onClose={(_, reason) => { if (reason !== 'backdropClick') void close() }} aria-labelledby="legal-editor-title">
    <Stack direction="row" sx={{ alignItems: 'center', gap: 1, px: { xs: 1, sm: 2 }, py: 1, borderBottom: '1px solid', borderColor: 'divider' }}>
      <IconButton onClick={() => void close()} disabled={busy} aria-label={t('legal.close')} sx={{ minWidth: 44, minHeight: 44 }}><CloseIcon /></IconButton>
      <Typography id="legal-editor-title" component="h2" sx={{ flex: 1, minWidth: 0, fontSize: { xs: 18, sm: 22 }, fontWeight: 600 }}>{t('legal.edit_title', { name: t(`legal.${kind}`) })}</Typography>
      <Button onClick={() => void close()} disabled={busy} sx={{ display: { xs: 'none', sm: 'inline-flex' }, minHeight: 44 }}>{t('legal.discard')}</Button>
      <Button variant="contained" onClick={() => setConfirming(true)} disabled={busy || loading || failed || stale || !valid} sx={{ minHeight: 44, flexShrink: 0 }}>{t('legal.publish_next')}</Button>
    </Stack>
    <Stack spacing={1} sx={{ px: 2, py: 1, borderBottom: '1px solid', borderColor: 'divider' }}>
      <Stack direction="row" sx={{ gap: 2, alignItems: 'center', flexWrap: 'wrap' }}>
        <TextField select label={t('legal.language')} value={locale} disabled={busy || loading} onChange={e => void changeLocale(e.target.value)} size="small" sx={{ minWidth: 140 }}>
          {languages.map(lang => <MenuItem key={lang} value={lang}>{lang}</MenuItem>)}
        </TextField>
        {!loading && !failed && <Typography variant="caption">{base ? t('legal.current_document', { version: base.version, date: new Date(base.published_at).toLocaleDateString() }) : t('legal.unpublished')}</Typography>}
      </Stack>
      <Stack direction="row" sx={{ gap: 1, alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap' }}>
        <Button onClick={() => editor.current?.insertLine('[[data-collection]]')} disabled={busy || loading || failed || hasMarker || (mobile && pane !== 'edit')} sx={{ minHeight: 44 }}>{t('legal.insert_collection')}</Button>
        <Typography variant="caption" aria-live="polite" color={valid ? 'text.secondary' : 'error.main'}>{t('legal.bytes', { bytes: bytes.toLocaleString(), max: MAX_LEGAL_BYTES.toLocaleString() })}</Typography>
      </Stack>
      {warning && <Alert severity="warning" action={stale ? <Button disabled={busy} onClick={() => void reloadBase()}>{t('legal.retry')}</Button> : undefined}>{warning}</Alert>}
    </Stack>
    {loading ? <Box role="status" aria-label={t('legal.loading')} sx={{ p: 4 }}><CircularProgress /></Box> : failed ? <Alert severity="error" action={<Button onClick={() => setRetry(value => value + 1)}>{t('legal.retry')}</Button>}>{t('legal.load_failed')}</Alert> : <>
      {mobile && <Tabs value={pane} onChange={(_, value) => setPane(value)} variant="fullWidth"><Tab value="edit" label={t('legal.edit')} /><Tab value="preview" label={t('legal.preview')} /></Tabs>}
      <Box sx={{ flex: 1, minHeight: 0, display: 'grid', gridTemplateColumns: { xs: '1fr', sm: 'minmax(0, 1fr) minmax(0, 1fr)' }, overflow: 'hidden' }}>
        <Box sx={{ display: mobile && pane !== 'edit' ? 'none' : 'block', minWidth: 0, overflow: 'auto', borderRight: { sm: '1px solid' }, borderColor: 'divider', p: 2 }}>
          <Suspense fallback={<CircularProgress />}><CodeEditor ref={editor} language="markdown" value={content} onChange={setContent} readOnly={busy} height="100%" ariaLabel={t('legal.content')} /></Suspense>
        </Box>
        {(!mobile || pane === 'preview') && previewPanel}
      </Box>
    </>}
    {confirming && <LegalPublishDialog kind={kind} locale={locale} version={(base?.version ?? 0) + 1} previous={base?.content ?? ''} content={content} busy={busy} onCancel={() => setConfirming(false)} onPublish={publish} />}
  </Dialog>
}
