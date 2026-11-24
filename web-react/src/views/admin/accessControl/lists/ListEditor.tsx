import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'

import {
  destErrorOf, previewDestList, type DestListInput, type DestListKind, type DestListPreview, type DestParseReport,
} from '@/api/accessControl'
import { AsyncButton } from '@/components/common/AsyncButton'
import { ObjectPage, objectErrorKind, type ObjectBack } from '@/components/common/ObjectPage'
import { useObjectLeaveGuard } from '@/components/common/ObjectTabs'
import type { CodeEditorHandle } from '@/components/CodeEditor'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { SaveBar, type SaveBarError } from '@/components/form/SaveBar'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { LevelAlert } from '@/components/LevelAlert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { IconSpinner } from '@/lib/icons'
import { isRouteImpactCancelled } from '@/utils/destRouteImpact'
import { useCreateDestList, useDestList, useDestLists, useDestPolicies, useDestStatus, useSaveDestList } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { enforcingNodeCount, limitKindKey } from '@/utils/accessControl'
import { discardChangesConfirm, switchListTypeConfirm } from '../confirmCopy'
import { ACCESS_PATH } from '../accessRoutes'
import { destErrorText } from '../destErrors'
import { PageForm } from '../PageForm'
import GeositeCategoryPicker from './GeositeCategoryPicker'
import { reasonText } from './listStatus'
import ParseReport from './ParseReport'

// Lazy: the editor pulls CodeMirror in, and only a page that shows it needs it.
const CodeEditor = lazy(() => import('@/components/CodeEditor'))

const L = 'admin:access_control.lists.'
const PREVIEW_DEBOUNCE_MS = 500
const MONO = 'font-mono text-[13px]'

interface Draft { kind: DestListKind; name: string; text: string; url: string; category: string; attrs: string[] }
const BLANK: Draft = { kind: 'custom', name: '', text: '', url: '', category: '', attrs: [] }

interface ParseLine { line: number; text: string; code: string; detail?: string }

/**
 * NEW AND EDIT LIST: a page (`…/lists/new`, `…/lists/:id`), because a list is a long document with a live preview.
 * Before saving it shows, beside the editor, how the list will be read: what is kept, rewritten and left out, and why.
 * A custom list is previewed as the text pauses (the preview writes nothing); a remote one only on [Test fetch],
 * because that really fetches; a community category by its picker. The side column says what saving does to the nodes
 * (nothing, or a restart of N nodes). The type is locked when editing. Leaving with unsaved input asks first, with
 * focus on Cancel. After an edit is saved the page stays on the list; after a create it moves to the new list's page.
 */
export default function ListEditor({ editId, initialKind = 'custom', back, onCreated }: {
  editId: number | null
  initialKind?: DestListKind
  back: ObjectBack
  /** After a create: the list's id, so the route can move to its page. */
  onCreated: (id: number) => void
}) {
  const { t, i18n } = useTranslation(['admin'])
  const scope = useQueryScope()
  const editing = editId !== null
  const detail = useDestList(scope, editId, { text: true, enabled: editing })
  const policies = useDestPolicies(scope)
  const lists = useDestLists(scope)
  const status = useDestStatus(scope)
  const create = useCreateDestList(scope)
  const save = useSaveDestList(scope)
  const editor = useRef<CodeEditorHandle>(null)
  // The preview effect must not restart whenever t changes identity.
  const tRef = useRef(t)
  tRef.current = t
  const navigate = useNavigate()
  const finished = useRef(false)
  const savingRef = useRef(false)
  const [saving, setSaving] = useState(false)

  const [draft, setDraft] = useState<Draft>({ ...BLANK, kind: initialKind })
  const [baseline, setBaseline] = useState<Draft>({ ...BLANK, kind: initialKind })
  const [seededFor, setSeededFor] = useState<string | null>(null)
  const [reload, setReload] = useState(0)
  const [stale, setStale] = useState(false)
  const [preview, setPreview] = useState<DestListPreview | null>(null)
  const [previewing, setPreviewing] = useState(false)
  const [previewError, setPreviewError] = useState<string | null>(null)
  const [parseLines, setParseLines] = useState<ParseLine[]>([])
  const [fetchError, setFetchError] = useState<string | null>(null)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [fieldErrors, setFieldErrors] = useState<{ name?: string; url?: string }>({})
  // Only the field the user has left (or a save attempt) is judged: typing in the editor must not mark the name.
  const [nameTouched, setNameTouched] = useState(false)
  // The version the last save returned: the next save of the same page is made against it, not against a read that may still be on its way.
  const [savedAt, setSavedAt] = useState<number | null>(null)

  // An edit seeds from the read (with the pasted source) once per version of it.
  useEffect(() => {
    const d = detail.data
    if (!editing || !d) return
    const key = `${d.list.id}:${d.list.updated_at}:${reload}`
    if (seededFor === key) return
    const seeded: Draft = {
      kind: d.list.kind, name: d.list.name, text: d.source_text ?? '', url: d.list.source_url ?? '',
      category: d.list.geosite_category ?? '', attrs: d.list.geosite_attrs ?? [],
    }
    setDraft(seeded); setBaseline(seeded); setSeededFor(key)
    setStale(false); setSaveError(null); setParseLines([]); setPreview(null); setSavedAt(null)
  }, [editing, detail.data, reload, seededFor])

  const dirty = JSON.stringify(draft) !== JSON.stringify(baseline)
  useObjectLeaveGuard({ dirty, copy: { ...discardChangesConfirm(t), initialFocus: 'cancel' }, bypass: finished })
  const set = (patch: Partial<Draft>) => {
    setDraft(d => ({ ...d, ...patch }))
    // A refusal of the name or the address was about what was typed before; the next Save judges again.
    if ('name' in patch || 'url' in patch) setFieldErrors(f => ({ ...f, ...('name' in patch ? { name: undefined } : {}), ...('url' in patch ? { url: undefined } : {}) }))
  }

  // Custom: preview as the typing pauses.
  useEffect(() => {
    if (draft.kind !== 'custom') return
    if (draft.text.trim() === '') { setPreview(null); setPreviewError(null); setParseLines([]); return }
    const ctl = new AbortController()
    setPreviewing(true)
    const id = setTimeout(() => {
      previewDestList({ kind: 'custom', side: 'dest', text: draft.text }, { signal: ctl.signal })
        .then(p => { setPreview(p); setPreviewError(null); setParseLines([]) })
        .catch(err => {
          if (ctl.signal.aborted) return
          const e = destErrorOf(err)
          if (e?.code === 'dest_list_parse_failed' && Array.isArray(e.body.lines)) {
            setParseLines(e.body.lines as ParseLine[]); setPreview(null); setPreviewError(null)
          } else {
            setParseLines([]); setPreviewError(destErrorText(tRef.current, err))
          }
        })
        .finally(() => { if (!ctl.signal.aborted) setPreviewing(false) })
    }, PREVIEW_DEBOUNCE_MS)
    return () => { clearTimeout(id); ctl.abort(); setPreviewing(false) }
  }, [draft.kind, draft.text])

  // A category: the count straight away.
  useEffect(() => {
    if (draft.kind !== 'geosite' || !draft.category) return
    const ctl = new AbortController()
    previewDestList({ kind: 'geosite', geosite_category: draft.category, geosite_attrs: draft.attrs }, { signal: ctl.signal })
      .then(setPreview).catch(() => { if (!ctl.signal.aborted) setPreview(null) })
    return () => ctl.abort()
  }, [draft.kind, draft.category, draft.attrs])

  const httpsOk = /^https:\/\/\S+$/i.test(draft.url.trim())
  const urlError = draft.kind === 'remote' && draft.url.trim() !== '' && !httpsOk ? t(`${L}dialog.url_https`) : fieldErrors.url
  const nameError = nameTouched && draft.kind !== 'geosite' && draft.name.trim() === '' ? t(`${L}dialog.name_required`) : fieldErrors.name

  const testFetch = async () => {
    setFetchError(null); setPreview(null)
    try {
      setPreview(await previewDestList({ kind: 'remote', side: 'dest', source_url: draft.url.trim() }))
    } catch (err) {
      const e = destErrorOf(err)
      setFetchError(e?.code === 'dest_list_insecure_url' ? t(`${L}dialog.url_https`) : destErrorText(t, err))
    }
  }

  const switchKind = async (next: DestListKind) => {
    if (next === draft.kind || editing) return
    const hasInput = draft.text.trim() !== '' || draft.url.trim() !== '' || draft.category !== ''
    if (hasInput && !(await confirm({ ...switchListTypeConfirm(t), initialFocus: 'cancel' }))) return
    setDraft(d => ({ ...BLANK, kind: next, name: d.name }))
    setPreview(null); setPreviewError(null); setParseLines([]); setFetchError(null)
  }

  const submit = async () => {
    if (savingRef.current) return
    savingRef.current = true
    setSaving(true)
    try {
      await submitOnce()
    } finally {
      savingRef.current = false
      setSaving(false)
    }
  }
  const submitOnce = async () => {
    setNameTouched(true); setSaveError(null); setFieldErrors({})
    // The body is exactly what the old dialog sent; the server reads `side` only for custom and remote lists.
    const input: DestListInput = draft.kind === 'custom'
      ? { kind: 'custom', side: 'dest', name: draft.name.trim(), text: draft.text }
      : draft.kind === 'remote'
        ? { kind: 'remote', side: 'dest', name: draft.name.trim(), source_url: draft.url.trim() }
        : { kind: 'geosite', geosite_category: draft.category, geosite_attrs: draft.attrs }
    try {
      const out = editing && detail.data
        ? await save.mutateAsync({ id: editId, input: { ...input, updated_at: savedAt ?? detail.data.list.updated_at } })
        : await create.mutateAsync(input)
      pushSnack(t(`${L}snack.saved`, { name: out.list.name }), 'success')
      if (editing) {
        // Continue from what the server stored (a trimmed name and address), not from what was typed: the refetch
        // reseeds the stored values, and a baseline made from the typed ones would stay "changed" for good.
        const stored: Draft = { ...draft, name: out.list.name, url: draft.kind === 'remote' ? (out.list.source_url ?? draft.url.trim()) : draft.url }
        setDraft(stored); setBaseline(stored); setSavedAt(out.list.updated_at)
        return
      }
      setBaseline(draft)
      finished.current = true
      onCreated(out.list.id)
    } catch (err) {
      if (isRouteImpactCancelled(err)) return
      const e = destErrorOf(err)
      if (!e) { setSaveError(destErrorText(t, err)); return }
      if (e.code === 'dest_list_stale') setStale(true)
      else if (e.code === 'dest_name_taken') setFieldErrors({ name: t(`${L}dialog.name_taken`) })
      else if (e.code === 'dest_list_insecure_url') setFieldErrors({ url: t(`${L}dialog.url_https`) })
      else if (e.code === 'dest_list_parse_failed' && Array.isArray(e.body.lines)) setParseLines(e.body.lines as ParseLine[])
      else if (e.code === 'dest_list_too_large') {
        setSaveError(t(`${L}dialog.too_large`, {
          what: t(`admin:access_control.verdict.what.${limitKindKey(e.body.kind as string)}`), used: e.body.used, limit: e.body.limit,
        }))
      } else if (e.code === 'dest_list_too_broad') setSaveError(t(`${L}dialog.too_broad`, { entry: String(e.body.entry ?? '') }))
      else setSaveError(destErrorText(t, err))
    }
  }

  // The footer: what saving does to the nodes.
  const enabledIds = useMemo(
    () => new Set([...(policies.data?.allow ?? []), ...(policies.data?.block ?? [])].filter(p => p.state !== 'off').map(p => p.id)),
    [policies.data])
  const usedEnabled = (detail.data?.list.used_by ?? []).filter(u => enabledIds.has(u.id)).length
  const nodeCount = enforcingNodeCount(status.data)
  const unchanged = editing && (
    (draft.kind === 'custom' && draft.text === baseline.text) || (draft.kind === 'remote' && draft.url === baseline.url)
    || (draft.kind === 'geosite' && draft.category === baseline.category && draft.attrs.join() === baseline.attrs.join()))
  let footer: string
  if (!editing || usedEnabled === 0) footer = t(`${L}dialog.footer_unused`)
  else if (unchanged) footer = t(`${L}dialog.footer_unchanged`)
  else {
    footer = t(`${L}dialog.footer_used`, {
      count: usedEnabled,
      nodes: nodeCount === null ? t(`${L}dialog.footer_nodes_unknown`) : t(`${L}dialog.footer_nodes`, { count: nodeCount }),
    })
  }

  const report: DestParseReport | null = preview?.report ?? null
  const title = editing ? (baseline.name || detail.data?.list.name || '') : t(`${L}dialog.title_new`)

  // Why Save is off, in the order the person meets it. A field that already says what is wrong under itself is not repeated here.
  const blockers: SaveBarError[] = []
  if (previewing) blockers.push({ message: t(`${L}dialog.previewing`) })
  if (parseLines.length > 0) blockers.push({ message: t(`${L}dialog.parse_failed`) })
  if (stale) blockers.push({ message: t(`${L}dialog.stale`) })
  if (draft.kind !== 'geosite' && draft.name.trim() === '' && !nameError) blockers.push({ label: t(`${L}dialog.name`), message: t(`${L}dialog.name_required`) })
  if (draft.kind === 'remote' && draft.url.trim() === '') blockers.push({ label: t(`${L}dialog.url`), message: t(`${L}dialog.url_https`) })
  if (draft.kind === 'geosite' && draft.category === '') blockers.push({ label: t(`${L}dialog.category`), message: t(`${L}dialog.category_required`) })

  let state: React.ComponentProps<typeof ObjectPage>['state']
  if (editing && detail.isPending) state = { kind: 'loading' }
  else if (editing && detail.isError) {
    state = objectErrorKind(detail.error) === 'not_found'
      ? { kind: 'not_found', title: t(`${L}dialog.not_found`) }
      : { kind: 'error', reason: detail.error, retry: () => void detail.refetch(), title: t(`${L}dialog.load_failed`) }
  }

  return (
    <ObjectPage back={back} title={title} state={state}
      subtitle={editing ? t(`${L}kind.${draft.kind}`) : undefined}
      secondary={editing && editId !== null
        ? <Button variant="outline" onClick={() => void navigate(`${ACCESS_PATH}?tab=lists&sheet=list&list=${editId}`)}>{t(`${L}menu.view`)}</Button>
        : undefined}>
      <FormValidityProvider>
        <PageForm>
          <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_21rem] lg:items-start">
            <div className="flex min-w-0 flex-col gap-4">
              {stale && (
                <LevelAlert level="warning">
                  <span className="flex flex-wrap items-center justify-between gap-2">
                    <span>{t(`${L}dialog.stale`)}</span>
                    <Button size="sm" variant="outline" onClick={() => { setStale(false); setReload(n => n + 1); void detail.refetch() }}>
                      {t(`${L}dialog.load_latest`)}
                    </Button>
                  </span>
                </LevelAlert>
              )}
              {saveError && <LevelAlert level="error">{saveError}</LevelAlert>}

              {/* Locked while a save runs: what is typed then would be wiped by the reseed from the saved list. */}
              <fieldset disabled={saving} className="m-0 flex min-w-0 flex-col gap-4 border-0 p-0">
              {/* Three long labels do not fit a 375px phone: the control scrolls inside its own box instead of widening the page. */}
              <div className="max-w-full overflow-x-auto">
                <SegmentedControl aria-label={t(`${L}dialog.type`)} value={draft.kind} disabled={editing} onChange={v => void switchKind(v)}
                  options={[
                    { value: 'custom', label: t(`${L}kind.custom`) },
                    { value: 'remote', label: t(`${L}kind.remote`) },
                    { value: 'geosite', label: t(`${L}kind.geosite`) },
                  ]} />
              </div>

              {draft.kind !== 'geosite' && (
                <Field label={t(`${L}dialog.name`)} required error={nameError}>
                  <Input value={draft.name} onChange={e => set({ name: e.target.value })} onBlur={() => setNameTouched(true)} />
                </Field>
              )}

              {draft.kind === 'custom' && (
                <>
                  <div className="flex flex-col gap-1.5">
                    <span className="text-sm font-medium">{t(`${L}dialog.text`)}</span>
                    <div className="overflow-hidden rounded-lg border">
                      <Suspense fallback={<Skeleton className="h-52 w-full rounded-none" />}>
                        <CodeEditor ref={editor} language="plain" minRows={16} value={draft.text} ariaLabel={t(`${L}dialog.text`)}
                          readOnly={saving} onChange={text => set({ text })} />
                      </Suspense>
                    </div>
                    <p className="text-xs text-muted-foreground">{t('admin:rules.editor_keyboard_hint')}</p>
                  </div>
                  <details className="text-[13px]">
                    <summary className="cursor-pointer">{t(`${L}dialog.syntax_title`)}</summary>
                    <pre className={`${MONO} m-2 whitespace-pre-wrap [overflow-wrap:anywhere]`}>{t(`${L}dialog.syntax`)}</pre>
                  </details>
                </>
              )}

              {draft.kind === 'remote' && (
                <>
                  <div className="flex flex-wrap items-start gap-2">
                    <Field label={t(`${L}dialog.url`)} required error={urlError} className="min-w-0 flex-1 basis-64">
                      <Input value={draft.url} inputMode="url" className={MONO} onChange={e => set({ url: e.target.value })} />
                    </Field>
                    {/* The button sits level with the input, not with the label above it. */}
                    <AsyncButton variant="outline" className="mt-[1.625rem]" disabled={!httpsOk} onClick={testFetch}>{t(`${L}dialog.test_fetch`)}</AsyncButton>
                  </div>
                  <p className="text-[13px] text-muted-foreground">{t(`${L}dialog.refresh_follows`, { hours: lists.data?.refresh_hours ?? '…' })}</p>
                  {fetchError && <LevelAlert level="error">{t(`${L}dialog.fetch_failed`, { error: fetchError })}</LevelAlert>}
                  {preview && preview.http_status !== undefined && (
                    <p className="text-[13px]">
                      {t(`${L}dialog.fetch_result`, {
                        code: preview.http_status,
                        size: new Intl.NumberFormat(i18n.language, { style: 'unit', unit: 'kilobyte', maximumFractionDigits: 0 }).format(Math.ceil((preview.bytes ?? 0) / 1024)),
                        accepted: preview.report?.accepted ?? preview.entry_count, ignored: preview.report?.ignored ?? 0,
                      })}
                    </p>
                  )}
                </>
              )}

              {draft.kind === 'geosite' && (
                <>
                  <GeositeCategoryPicker category={draft.category} attrs={draft.attrs} disabled={editing}
                    onChange={(category, attrs) => set({ category, attrs })} />
                  <p className="text-[13px] text-muted-foreground">
                    {t(`${L}dialog.geosite_note`)}
                    {preview && draft.category ? ` ${t(`${L}dialog.geosite_counts`, { entries: preview.entry_count, regexps: preview.regexp_count })}` : ''}
                  </p>
                </>
              )}

              </fieldset>

              {previewing && <p className="flex items-center gap-2 text-[13px]"><IconSpinner aria-hidden className="size-3.5 animate-spin" />{t(`${L}dialog.previewing`)}</p>}
              {previewError && <LevelAlert level="warning">{t(`${L}dialog.preview_failed`, { error: previewError })}</LevelAlert>}
              {parseLines.length > 0 && (
                <LevelAlert level="error">
                  <p className="text-[13px] font-medium">{t(`${L}dialog.parse_failed`)}</p>
                  {parseLines.slice(0, 20).map(l => (
                    <div key={`${l.line}:${l.code}`} className="text-[13px]">
                      <button type="button" className="underline-offset-2 hover:underline focus-visible:underline" onClick={() => editor.current?.revealLine(l.line)}>
                        {t(`${L}dialog.line_n`, { line: l.line })}
                      </button>
                      {' '}<span className={MONO}>{l.text}</span>{' — '}{l.detail ? l.detail : reasonText(t, l.code)}
                    </div>
                  ))}
                </LevelAlert>
              )}
            </div>

            {/* What saving does and how the list was read, kept in view beside the editor on a wide screen. */}
            <aside className="flex min-w-0 flex-col gap-3 rounded-xl border bg-card p-4 lg:sticky lg:top-4">
              <p className="text-sm text-muted-foreground" data-testid="list-save-effect">{footer}</p>
              {report && draft.kind !== 'geosite' && !previewing && (
                <ParseReport report={report} onJump={draft.kind === 'custom' ? n => editor.current?.revealLine(n) : undefined} />
              )}
            </aside>
          </div>
          {/* A new list is always offered for saving; an edit only once something changed. */}
          <SaveBar dirty={!editing || dirty} pending={saving} errors={blockers} onSave={() => void submit()}
            onDiscard={() => { if (editing) { setDraft(baseline); setFieldErrors({}); setSaveError(null); setNameTouched(false) } else void navigate(back.to) }} />
        </PageForm>
      </FormValidityProvider>
    </ObjectPage>
  )
}
