import { Component, lazy, Suspense, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import {
  Alert, Box, Button, CircularProgress, Dialog, IconButton, MenuItem, Skeleton, Tab, Tabs, TextField, Typography,
  useMediaQuery, useTheme,
} from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'

import { useTranslation } from 'react-i18next'

import { DATA_COLLECTION_MARKER, LEGAL_CONTENT_MAX_BYTES, type LegalKind } from '@/api/legal'
import type { CodeEditorHandle } from '@/components/CodeEditor'
import FieldHint from '@/components/FieldHint'
import LegalDocument, { splitAtMarker } from '@/components/LegalDocument'
import { confirm } from '@/components/ConfirmHost'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { SUPPORTED_LANGUAGES } from '@/i18n'
import { useAdminLegal } from '@/query/legal'
import { useQueryScope } from '@/query/useQueryScope'
import LegalPublishDialog from './LegalPublishDialog'

// CodeMirror is heavy; it loads when an editor opens.
const CodeEditor = lazy(() => import('@/components/CodeEditor'))

const PREVIEW_DELAY_MS = 300
// The built-in languages come first; zh-TW is generated from zh-CN and never
// written by hand.
const BUILTIN = ['zh-CN', 'en-US']

/** The preview is the right-hand pane's alone: a document that cannot be drawn
 *  says so there, and the editor and the publish button keep working. */
export class PreviewBoundary extends Component<{ message: string; children: ReactNode }, { failed: boolean }> {
  state = { failed: false }
  static getDerivedStateFromError() { return { failed: true } }
  componentDidUpdate(prev: { children: ReactNode }) {
    if (this.state.failed && prev.children !== this.props.children) this.setState({ failed: false })
  }
  render() {
    return this.state.failed ? <Alert severity="warning">{this.props.message}</Alert> : this.props.children
  }
}

const bytesOf = (s: string) => new TextEncoder().encode(s).length

interface Props {
  kind: LegalKind
  onClose: () => void
  /** The settings draft's "disclose per-site usage" switch, saved or not: the preview follows it. */
  discloseUsage?: boolean
}

/** The full-screen editor of one document: Markdown on the left, the page's own
 *  rendering on the right. A write is independent of the settings draft. */
export default function LegalEditorDialog({ kind, onClose, discloseUsage = false }: Props) {
  const theme = useTheme()
  const md = theme.palette.md
  const phone = useMediaQuery(theme.breakpoints.down('sm'))
  const { t, i18n } = useTranslation('admin')
  const scope = useQueryScope()
  const q = useAdminLegal(scope, kind)
  const editor = useRef<CodeEditorHandle>(null)
  const doc = t(`auth:legal.kind.${kind}`)

  const [locale, setLocale] = useState('zh-CN')
  const [draft, setDraft] = useState<string | null>(null)
  const [preview, setPreview] = useState('')
  const [pane, setPane] = useState<'edit' | 'preview'>('edit')
  const [publishing, setPublishing] = useState(false)

  const locales = useMemo(() => {
    const extra = [...SUPPORTED_LANGUAGES.filter(l => !BUILTIN.includes(l) && l !== 'zh-TW'), ...(q.data?.documents ?? []).map(d => d.locale)]
    return [...BUILTIN, ...extra.filter((l, i) => !BUILTIN.includes(l) && extra.indexOf(l) === i)]
  }, [q.data])

  const published = q.data?.documents.find(d => d.locale === locale)
  // A privacy policy starts from the one line the panel can write for the owner:
  // where the generated list goes. The legal text itself is theirs.
  const baseline = published?.content ?? (kind === 'privacy' ? `${DATA_COLLECTION_MARKER}\n` : '')

  function load(text: string) { setDraft(text); setPreview(text) }

  // Seed the draft once the first read lands; a refetch never replaces edits.
  useEffect(() => {
    if (draft === null && q.data) load(baseline)
  }, [draft, q.data, baseline])

  // The preview follows the text once typing pauses.
  useEffect(() => {
    if (draft === null || draft === preview) return
    const id = setTimeout(() => setPreview(draft), PREVIEW_DELAY_MS)
    return () => clearTimeout(id)
  }, [draft, preview])

  const text = draft ?? ''
  const dirty = draft !== null && draft !== baseline
  const used = bytesOf(text)
  const over = used > LEGAL_CONTENT_MAX_BYTES
  const hasMarker = splitAtMarker(text).length > 1
  // The generated list is the server's, from the SAVED settings. The switch may
  // be unsaved: switched off, the usage line is taken out; switched on and not
  // in the list, the preview says why (an unsaved switch, or no node records it).
  const saved = q.data?.data_collection
  const previewCollection = useMemo(() => {
    if (!saved) return null
    if (discloseUsage || !saved.dest_usage) return saved
    const { dest_usage: _off, ...rest } = saved
    return rest
  }, [saved, discloseUsage])
  const usageMissing = discloseUsage && !!saved && !saved.dest_usage?.enabled
  const canPublish = draft !== null && !over && text.trim() !== '' && text !== (published?.content ?? null)
  const nextVersion = Math.max(0, ...(q.data?.versions ?? []).filter(v => v.locale === locale).map(v => v.version)) + 1

  const guard = useDirtyClose(dirty, {
    title: t('settings.legal.editor.discard_title'), message: t('settings.legal.editor.discard_message'),
    confirmText: t('settings.legal.editor.discard_confirm'), destructive: true,
  })

  async function changeLocale(next: string) {
    if (next === locale) return
    if (dirty && !(await confirm({
      title: t('settings.legal.editor.switch_title'), message: t('settings.legal.editor.switch_message'),
      confirmText: t('settings.legal.editor.discard_confirm'), destructive: true,
    }))) return
    setLocale(next)
    load(q.data?.documents.find(d => d.locale === next)?.content ?? (kind === 'privacy' ? `${DATA_COLLECTION_MARKER}\n` : ''))
  }

  const date = (iso: string) => new Date(iso).toLocaleDateString(i18n.language, { month: 'long', day: 'numeric' })
  const counter = (
    <Typography data-over={over} sx={{ fontSize: 12, color: over ? md.error : md.onSurfaceVariant, fontWeight: over ? 600 : 400 }}>
      {t('settings.legal.editor.bytes', { used: used.toLocaleString('en-US'), max: LEGAL_CONTENT_MAX_BYTES.toLocaleString('en-US') })}
    </Typography>
  )
  const languageSelect = (
    <TextField select size="small" label={t('settings.legal.editor.language')} value={locale}
      onChange={e => void changeLocale(e.target.value)} sx={{ minWidth: 130 }}>
      {locales.map(l => <MenuItem key={l} value={l}>{l}</MenuItem>)}
    </TextField>
  )
  const insertButton = (
    <Button size="small" variant="outlined" disabled={hasMarker || draft === null || (phone && pane === 'preview')}
      onClick={() => editor.current?.insertLine(DATA_COLLECTION_MARKER)}>
      {t('settings.legal.editor.insert')}
    </Button>
  )
  const publishButton = (
    <Button variant="contained" disabled={!canPublish} onClick={() => setPublishing(true)}>{t('settings.legal.editor.publish')}</Button>
  )

  const editorPane = (
    <Suspense fallback={<Skeleton variant="rounded" height={380} />}>
      <CodeEditor ref={editor} value={text} onChange={setDraft} language="plain" height={phone ? '60vh' : 'calc(100vh - 190px)'} />
    </Suspense>
  )
  const previewPane = (
    <Box sx={{ overflowY: 'auto', height: phone ? undefined : 'calc(100vh - 190px)', px: { xs: 0, sm: 2 } }}>
      {!hasMarker && (
        <Box sx={{ mb: 1.5 }}>
          <FieldHint tone="amber" summary={t('settings.legal.editor.no_marker_summary')} detail={t('settings.legal.editor.no_marker_detail')} />
        </Box>
      )}
      {usageMissing && (
        <Box sx={{ mb: 1.5 }}>
          <FieldHint tone="amber" summary={t('settings.legal.editor.usage_missing_summary')} detail={t('settings.legal.editor.usage_missing_detail')} />
        </Box>
      )}
      <PreviewBoundary message={t('settings.legal.editor.preview_failed')}>
        <LegalDocument content={preview} dataCollection={previewCollection} />
      </PreviewBoundary>
    </Box>
  )

  return (
    <>
      <Dialog open fullScreen onClose={() => guard(onClose)} aria-labelledby="legal-editor-title">
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, px: 2, py: 1, borderBottom: `1px solid ${md.outlineVariant}` }}>
          <IconButton aria-label={t('settings.legal.editor.close')} onClick={() => guard(onClose)}><CloseIcon /></IconButton>
          <Typography id="legal-editor-title" component="h2" sx={{ flex: 1, fontSize: 18, fontWeight: 600 }}>
            {t('settings.legal.editor.title', { doc })}
          </Typography>
          {!phone && <Button onClick={() => guard(onClose)}>{t('settings.legal.editor.discard_confirm')}</Button>}
          {publishButton}
        </Box>
        <Box sx={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 1.5, px: 2, py: 1 }}>
          {languageSelect}
          <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>
            {published
              ? t('settings.legal.editor.current', { version: published.version, date: date(published.published_at) })
              : t('settings.legal.editor.current_none')}
          </Typography>
          <Box sx={{ flex: 1 }} />
          {insertButton}
          {counter}
        </Box>
        {q.isLoading || draft === null ? (
          <Box sx={{ display: 'grid', placeItems: 'center', flex: 1 }}><CircularProgress /></Box>
        ) : phone ? (
          <Box sx={{ px: 2 }}>
            <Tabs value={pane} onChange={(_, v) => setPane(v)} sx={{ mb: 1 }}>
              <Tab value="edit" label={t('settings.legal.editor.tab_edit')} />
              <Tab value="preview" label={t('settings.legal.editor.tab_preview')} />
            </Tabs>
            {pane === 'edit' ? editorPane : previewPane}
          </Box>
        ) : (
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 2, px: 2 }}>
            {editorPane}
            {previewPane}
          </Box>
        )}
      </Dialog>
      {publishing && (
        <LegalPublishDialog kind={kind} locale={locale} version={nextVersion} previous={published?.content ?? null} content={text}
          onClose={() => setPublishing(false)} onPublished={onClose} />
      )}
    </>
  )
}
