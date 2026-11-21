import { Component, lazy, Suspense, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'

import { DATA_COLLECTION_MARKER, LEGAL_CONTENT_MAX_BYTES, type LegalKind } from '@/api/legal'
import { FieldHint } from '@/components/common/HelpTip'
import type { CodeEditorHandle } from '@/components/CodeEditor'
import { confirm } from '@/components/ConfirmHost'
import { DetailSheet } from '@/components/data/DetailSheet'
import { ErrorState } from '@/components/feedback/ErrorState'
import { Field } from '@/components/form/Field'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import LegalDocument, { splitAtMarker } from '@/components/LegalDocument'
import { Button } from '@/components/ui/button'
import { NativeSelect } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { SUPPORTED_LANGUAGES } from '@/i18n'
import { useAdminLegal } from '@/query/legal'
import { useQueryScope } from '@/query/useQueryScope'
import { cn } from '@/lib/utils'
import { Notice } from '../Notice'
import { useAdminT } from '../useAdminT'
import LegalPublishDialog from './LegalPublishDialog'
import { useDark } from './useDark'

// CodeMirror is heavy; it loads when an editor opens.
const CodeEditor = lazy(() => import('@/components/CodeEditor'))

const PREVIEW_DELAY_MS = 300
// The built-in languages come first; zh-TW is generated from zh-CN and never written by hand.
const BUILTIN = ['zh-CN', 'en-US']
// Both panes are as tall as the sheet's body leaves room for, so each scrolls by itself and the bar below stays in view.
const PANE_HEIGHT = 'calc(100dvh - 20rem)'

/** The preview is the right-hand pane's alone: a document that cannot be drawn says so there, and the editor and the publish button keep working. */
export class PreviewBoundary extends Component<{ message: string; children: ReactNode }, { failed: boolean }> {
  state = { failed: false }
  static getDerivedStateFromError() { return { failed: true } }
  componentDidUpdate(prev: { children: ReactNode }) {
    if (this.state.failed && prev.children !== this.props.children) this.setState({ failed: false })
  }
  render() {
    return this.state.failed ? <Notice>{this.props.message}</Notice> : this.props.children
  }
}

const bytesOf = (s: string) => new TextEncoder().encode(s).length

interface Props {
  kind: LegalKind
  onClose: () => void
  /** The settings draft's "disclose per-site usage" switch, saved or not: the preview follows it. */
  discloseUsage?: boolean
}

/**
 * The editor of one document, in a wide sheet over the settings page: Markdown on the left, the public
 * page's own rendering (LegalDocument) on the right, and a bar at the bottom that offers "Publish…" while the
 * text differs from what is published. Nothing is saved by typing: a write is the publish dialog's alone, and
 * independent of the settings draft. Closing, switching language and leaving the page all ask before the text is lost.
 */
export default function LegalEditorSheet({ kind, onClose, discloseUsage = false }: Props) {
  const t = useAdminT()
  const phone = useMediaQuery('(max-width: 639px)')
  const dark = useDark()
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
  // A privacy policy starts from the one line the panel can write for the owner: where the generated list goes.
  // The legal text itself is theirs.
  const skeleton = kind === 'privacy' ? `${DATA_COLLECTION_MARKER}\n` : ''
  const baseline = published?.content ?? skeleton

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
  // `edited`: the person typed something (what is lost on close). `unpublished`: the text is not what readers see now.
  const edited = draft !== null && draft !== baseline
  const unpublished = draft !== null && draft !== (published?.content ?? '')
  const used = bytesOf(text)
  const over = used > LEGAL_CONTENT_MAX_BYTES
  const empty = text.trim() === ''
  const hasMarker = splitAtMarker(text).length > 1
  // The generated list is the server's, from the SAVED settings. The switch may be unsaved: switched off, the
  // usage line is taken out; switched on and not in the list, the preview says why (an unsaved switch, or no node records it).
  const saved = q.data?.data_collection
  const previewCollection = useMemo(() => {
    if (!saved) return null
    if (discloseUsage || !saved.dest_usage) return saved
    const { dest_usage: _off, ...rest } = saved
    return rest
  }, [saved, discloseUsage])
  const usageMissing = discloseUsage && !!saved && !saved.dest_usage?.enabled
  const nextVersion = Math.max(0, ...(q.data?.versions ?? []).filter(v => v.locale === locale).map(v => v.version)) + 1

  const discard = useMemo(() => ({
    title: t('admin:settings.legal.editor.discard_title'), message: t('admin:settings.legal.editor.discard_message'),
    confirmText: t('admin:settings.legal.editor.discard_confirm'), destructive: true, initialFocus: 'cancel' as const,
  }), [t])
  const guard = useDirtyClose(edited, discard)
  // The sheet is not a route, but the settings page behind it is: a tab or menu click must not drop the draft either.
  useLeaveGuard(edited, discard, () => true)

  async function changeLocale(next: string) {
    if (next === locale) return
    if (edited && !(await confirm({
      title: t('admin:settings.legal.editor.switch_title'), message: t('admin:settings.legal.editor.switch_message'),
      confirmText: t('admin:settings.legal.editor.discard_confirm'), destructive: true, initialFocus: 'cancel',
    }))) return
    setLocale(next)
    load(q.data?.documents.find(d => d.locale === next)?.content ?? skeleton)
  }

  const bar = (
    <SaveBar
      dirty={unpublished && !!q.data}
      saveLabel={t('admin:settings.legal.editor.publish')}
      message={t('admin:settings.legal.editor.unpublished')}
      errors={[
        ...(over ? [{ message: t('admin:settings.legal.publish.too_large', { max: LEGAL_CONTENT_MAX_BYTES.toLocaleString('en-US') }) }] : []),
        ...(empty && unpublished ? [{ message: t('admin:settings.legal.editor.empty') }] : []),
      ]}
      onSave={() => setPublishing(true)}
      onDiscard={() => load(baseline)}
    />
  )

  const editorPane = (
    <Suspense fallback={<Skeleton className="h-96 rounded-lg" />}>
      <CodeEditor ref={editor} value={text} onChange={setDraft} language="plain" dark={dark} height={phone ? '60dvh' : PANE_HEIGHT} />
    </Suspense>
  )
  const previewPane = (
    <div className="overflow-y-auto px-1 sm:px-3" style={{ height: phone ? undefined : PANE_HEIGHT }}>
      {!hasMarker && (
        <div className="mb-3"><FieldHint summary={t('admin:settings.legal.editor.no_marker_summary')} detail={t('admin:settings.legal.editor.no_marker_detail')} /></div>
      )}
      {usageMissing && (
        <div className="mb-3"><FieldHint summary={t('admin:settings.legal.editor.usage_missing_summary')} detail={t('admin:settings.legal.editor.usage_missing_detail')} /></div>
      )}
      <PreviewBoundary message={t('admin:settings.legal.editor.preview_failed')}>
        <LegalDocument content={preview} dataCollection={previewCollection} />
      </PreviewBoundary>
    </div>
  )

  return (
    <>
      <DetailSheet
        open
        onClose={() => guard(onClose)}
        title={t('admin:settings.legal.editor.title', { doc })}
        description={published
          ? t('admin:settings.legal.editor.current', { version: published.version, date: new Date(published.published_at).toLocaleDateString(undefined, { month: 'long', day: 'numeric' }) })
          : t('admin:settings.legal.editor.current_none')}
        className="data-[side=right]:sm:w-[min(72rem,96vw)] data-[side=right]:sm:max-w-[min(72rem,96vw)]"
      >
        <FormValidityProvider>
          <div className="flex flex-wrap items-end gap-3 pb-3">
            <Field label={t('admin:settings.legal.editor.language')} className="w-40">
              <NativeSelect value={locale} onChange={e => void changeLocale(e.target.value)}>
                {locales.map(l => <option key={l} value={l}>{l}</option>)}
              </NativeSelect>
            </Field>
            <div className="ml-auto flex flex-wrap items-center gap-3 pb-1">
              <Button variant="outline" size="sm" disabled={hasMarker || draft === null || (phone && pane === 'preview')}
                onClick={() => editor.current?.insertLine(DATA_COLLECTION_MARKER)}>
                {t('admin:settings.legal.editor.insert')}
              </Button>
              {/* Over the limit it is said in words, not only in red: the bar below also names it. */}
              <span data-over={over} className={cn('text-xs tabular-nums', over ? 'font-semibold text-destructive' : 'text-muted-foreground')}>
                {t('admin:settings.legal.editor.bytes', { used: used.toLocaleString('en-US'), max: LEGAL_CONTENT_MAX_BYTES.toLocaleString('en-US') })}
              </span>
            </div>
          </div>
          {q.isError && !q.data ? (
            <ErrorState compact title={t('admin:settings.legal.load_failed')} retry={() => q.refetch()} />
          ) : q.isLoading || draft === null ? (
            <Skeleton className="h-96 rounded-lg" />
          ) : phone ? (
            <>
              <Tabs value={pane} onValueChange={v => setPane(v as 'edit' | 'preview')} className="mb-2">
                <TabsList>
                  <TabsTrigger value="edit">{t('admin:settings.legal.editor.tab_edit')}</TabsTrigger>
                  <TabsTrigger value="preview">{t('admin:settings.legal.editor.tab_preview')}</TabsTrigger>
                </TabsList>
              </Tabs>
              {pane === 'edit' ? editorPane : previewPane}
            </>
          ) : (
            <div className="grid grid-cols-2 gap-4">{editorPane}{previewPane}</div>
          )}
          {bar}
        </FormValidityProvider>
      </DetailSheet>
      {publishing && (
        <LegalPublishDialog kind={kind} locale={locale} version={nextVersion} previous={published?.content ?? null} content={text}
          onClose={() => setPublishing(false)} onPublished={onClose} />
      )}
    </>
  )
}
