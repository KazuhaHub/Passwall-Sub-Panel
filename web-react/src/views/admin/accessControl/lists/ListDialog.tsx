import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react'
import {
  Alert, Box, Button, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, IconButton, Link,
  Skeleton, TextField, ToggleButton, ToggleButtonGroup, Typography, useMediaQuery, useTheme,
} from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import { useTranslation } from 'react-i18next'

import {
  destErrorOf, previewDestList, type DestListInput, type DestListKind, type DestListPreview, type DestParseReport,
} from '@/api/accessControl'
import { AsyncButton } from '@/components/AsyncButton'
import type { CodeEditorHandle } from '@/components/CodeEditor'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useCreateDestList, useDestList, useDestLists, useDestPolicies, useDestStatus, useSaveDestList } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { enforcingNodeCount, limitKindKey } from '@/utils/accessControl'
import { discardChangesConfirm, switchListTypeConfirm } from '../confirmCopy'
import { destErrorText } from '../destErrors'
import GeositeCategoryPicker from './GeositeCategoryPicker'
import { reasonText } from './listStatus'
import ParseReport from './ParseReport'

// Lazy: the editor pulls CodeMirror in, and only an open dialog needs it.
const CodeEditor = lazy(() => import('@/components/CodeEditor'))

const L = 'admin:access_control.lists.'
const PREVIEW_DEBOUNCE_MS = 500

interface Draft { kind: DestListKind; name: string; text: string; url: string; category: string; attrs: string[] }
const BLANK: Draft = { kind: 'custom', name: '', text: '', url: '', category: '', attrs: [] }

interface ParseLine { line: number; text: string; code: string; detail?: string }

/**
 * NEW AND EDIT LIST (S7). Before saving it shows how the list will be read:
 * what is kept, rewritten and left out, and why. A custom list is previewed
 * as the text pauses (the preview writes nothing); a remote one only on [Test
 * fetch], because that really fetches; a community category by its picker.
 * The type is locked when editing. Closing with unsaved input asks first.
 */
export default function ListDialog({ open, onClose, editId, initialKind = 'custom', onSaved }: {
  open: boolean
  onClose: () => void
  editId: number | null
  initialKind?: DestListKind
  /** After a save: the list's id, so the page can show its entries. */
  onSaved: (id: number) => void
}) {
  const { t, i18n } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const fullScreen = useMediaQuery(theme.breakpoints.down('sm'))
  const scope = useQueryScope()
  const editing = editId !== null
  const detail = useDestList(scope, editId, { text: true, enabled: open && editing })
  const policies = useDestPolicies(scope)
  const lists = useDestLists(scope)
  const status = useDestStatus(scope)
  const create = useCreateDestList(scope)
  const save = useSaveDestList(scope)
  const editor = useRef<CodeEditorHandle>(null)
  // The preview effect must not restart whenever t changes identity.
  const tRef = useRef(t)
  tRef.current = t

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

  // A new dialog starts blank, in the kind it was opened for.
  useEffect(() => {
    if (!open || editing) return
    const blank = { ...BLANK, kind: initialKind }
    setDraft(blank); setBaseline(blank); setSeededFor(null)
    setPreview(null); setPreviewError(null); setParseLines([]); setFetchError(null); setSaveError(null)
    setFieldErrors({}); setStale(false); setNameTouched(false)
  }, [open, editing, initialKind])

  // An edit seeds from the read (with the pasted source) once per version of it.
  useEffect(() => {
    const d = detail.data
    if (!open || !editing || !d) return
    const key = `${d.list.id}:${d.list.updated_at}:${reload}`
    if (seededFor === key) return
    const seeded: Draft = {
      kind: d.list.kind, name: d.list.name, text: d.source_text ?? '', url: d.list.source_url ?? '',
      category: d.list.geosite_category ?? '', attrs: d.list.geosite_attrs ?? [],
    }
    setDraft(seeded); setBaseline(seeded); setSeededFor(key)
    setStale(false); setSaveError(null); setParseLines([]); setPreview(null)
  }, [open, editing, detail.data, reload, seededFor])

  const dirty = JSON.stringify(draft) !== JSON.stringify(baseline)
  const guard = useDirtyClose(dirty, discardChangesConfirm(t))
  const requestClose = () => guard(onClose)
  const set = (patch: Partial<Draft>) => { setDraft(d => ({ ...d, ...patch })) }

  // Custom: preview as the typing pauses.
  useEffect(() => {
    if (!open || draft.kind !== 'custom') return
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
  }, [open, draft.kind, draft.text])

  // A category: the count straight away.
  useEffect(() => {
    if (!open || draft.kind !== 'geosite' || !draft.category) return
    const ctl = new AbortController()
    previewDestList({ kind: 'geosite', geosite_category: draft.category, geosite_attrs: draft.attrs }, { signal: ctl.signal })
      .then(setPreview).catch(() => { if (!ctl.signal.aborted) setPreview(null) })
    return () => ctl.abort()
  }, [open, draft.kind, draft.category, draft.attrs])

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
    if (hasInput && !(await confirm(switchListTypeConfirm(t)))) return
    setDraft(d => ({ ...BLANK, kind: next, name: d.name }))
    setPreview(null); setPreviewError(null); setParseLines([]); setFetchError(null)
  }

  const canSave = !previewing && parseLines.length === 0 && (
    draft.kind === 'custom' ? draft.name.trim() !== ''
      : draft.kind === 'remote' ? draft.name.trim() !== '' && httpsOk
        : draft.category !== ''
  ) && (!editing || (detail.data !== undefined && !stale))

  const submit = async () => {
    setNameTouched(true); setSaveError(null); setFieldErrors({})
    const input: DestListInput = draft.kind === 'custom'
      ? { kind: 'custom', side: 'dest', name: draft.name.trim(), text: draft.text }
      : draft.kind === 'remote'
        ? { kind: 'remote', side: 'dest', name: draft.name.trim(), source_url: draft.url.trim() }
        : { kind: 'geosite', geosite_category: draft.category, geosite_attrs: draft.attrs }
    try {
      const out = editing && detail.data
        ? await save.mutateAsync({ id: editId, input: { ...input, updated_at: detail.data.list.updated_at } })
        : await create.mutateAsync(input)
      pushSnack(t(`${L}snack.saved`, { name: out.list.name }), 'success')
      setBaseline(draft)
      onClose()
      onSaved(out.list.id)
    } catch (err) {
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
  const mono = { fontFamily: 'ui-monospace, monospace', fontSize: 13 }
  const title = editing
    ? t(`${L}dialog.title_edit`, { name: baseline.name || detail.data?.list.name || '' })
    : t(`${L}dialog.title_new`)

  return (
    <Dialog open={open} onClose={requestClose} fullWidth maxWidth="md" fullScreen={fullScreen}
      slotProps={{ paper: { sx: { bgcolor: md.surfaceContainerHigh, borderRadius: fullScreen ? 0 : 3 } } }}>
      <DialogTitle sx={{ pr: 6 }}>
        {title}
        <IconButton aria-label={t(`${L}dialog.close`)} onClick={requestClose} sx={{ position: 'absolute', right: 8, top: 8 }}>
          <CloseIcon />
        </IconButton>
      </DialogTitle>
      <DialogContent sx={{ display: 'grid', gap: 2, alignContent: 'start' }}>
        {editing && detail.isPending && <Skeleton variant="rounded" height={160} />}
        {editing && detail.isError && <Alert severity="error" action={<Button color="inherit" size="small" onClick={() => void detail.refetch()}>{t('admin:access_control.retry')}</Button>}>{t(`${L}dialog.load_failed`)}</Alert>}
        {stale && (
          <Alert severity="warning" action={
            <Button color="inherit" size="small" onClick={() => { setStale(false); setReload(n => n + 1); void detail.refetch() }}>
              {t(`${L}dialog.load_latest`)}
            </Button>}>
            {t(`${L}dialog.stale`)}
          </Alert>
        )}
        {saveError && <Alert severity="error">{saveError}</Alert>}

        <ToggleButtonGroup exclusive fullWidth={fullScreen} size="small" value={draft.kind} disabled={editing}
          onChange={(_, v: DestListKind | null) => { if (v) void switchKind(v) }}>
          <ToggleButton value="custom">{t(`${L}kind.custom`)}</ToggleButton>
          <ToggleButton value="remote">{t(`${L}kind.remote`)}</ToggleButton>
          <ToggleButton value="geosite">{t(`${L}kind.geosite`)}</ToggleButton>
        </ToggleButtonGroup>

        {draft.kind !== 'geosite' && (
          <TextField size="small" required label={t(`${L}dialog.name`)} value={draft.name} onChange={e => set({ name: e.target.value })} onBlur={() => setNameTouched(true)}
            error={!!nameError} helperText={nameError} />
        )}

        {draft.kind === 'custom' && (
          <>
            <Box>
              <Typography component="label" sx={{ fontSize: 12.5, color: md.onSurfaceVariant }}>{t(`${L}dialog.text`)}</Typography>
              <Suspense fallback={<Skeleton variant="rounded" height={208} />}>
                <CodeEditor ref={editor} language="plain" minRows={fullScreen ? 10 : 12} value={draft.text}
                  onChange={text => set({ text })} />
              </Suspense>
            </Box>
            <Box component="details" sx={{ fontSize: 13 }}>
              <Box component="summary" sx={{ cursor: 'pointer' }}>{t(`${L}dialog.syntax_title`)}</Box>
              <Box component="pre" sx={{ ...mono, whiteSpace: 'pre-wrap', m: 1, overflowWrap: 'anywhere' }}>{t(`${L}dialog.syntax`)}</Box>
            </Box>
          </>
        )}

        {draft.kind === 'remote' && (
          <>
            <Box sx={{ display: 'flex', gap: 1, alignItems: 'flex-start', flexWrap: 'wrap' }}>
              <TextField size="small" required label={t(`${L}dialog.url`)} value={draft.url} sx={{ flex: '1 1 260px', minWidth: 0 }}
                onChange={e => set({ url: e.target.value })} error={!!urlError} helperText={urlError}
                slotProps={{ htmlInput: { style: mono, inputMode: 'url' } }} />
              <AsyncButton variant="outlined" disabled={!httpsOk} onClick={testFetch}>{t(`${L}dialog.test_fetch`)}</AsyncButton>
            </Box>
            <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>
              {t(`${L}dialog.refresh_follows`, { hours: lists.data?.refresh_hours ?? '…' })}
            </Typography>
            {fetchError && <Alert severity="error">{t(`${L}dialog.fetch_failed`, { error: fetchError })}</Alert>}
            {preview && preview.http_status !== undefined && (
              <Typography sx={{ fontSize: 13 }}>
                {t(`${L}dialog.fetch_result`, {
                  code: preview.http_status,
                  size: new Intl.NumberFormat(i18n.language, { style: 'unit', unit: 'kilobyte', maximumFractionDigits: 0 }).format(Math.ceil((preview.bytes ?? 0) / 1024)),
                  accepted: preview.report?.accepted ?? preview.entry_count, ignored: preview.report?.ignored ?? 0,
                })}
              </Typography>
            )}
          </>
        )}

        {draft.kind === 'geosite' && (
          <>
            <GeositeCategoryPicker category={draft.category} attrs={draft.attrs} disabled={editing}
              onChange={(category, attrs) => set({ category, attrs })} />
            <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>
              {t(`${L}dialog.geosite_note`)}
              {preview && draft.category ? ` ${t(`${L}dialog.geosite_counts`, { entries: preview.entry_count, regexps: preview.regexp_count })}` : ''}
            </Typography>
          </>
        )}

        {previewing && <Box sx={{ display: 'flex', gap: 1, alignItems: 'center' }}><CircularProgress size={14} /><Typography sx={{ fontSize: 13 }}>{t(`${L}dialog.previewing`)}</Typography></Box>}
        {previewError && <Alert severity="warning">{t(`${L}dialog.preview_failed`, { error: previewError })}</Alert>}
        {parseLines.length > 0 && (
          <Alert severity="error">
            <Typography sx={{ fontWeight: 600, fontSize: 13 }}>{t(`${L}dialog.parse_failed`)}</Typography>
            {parseLines.slice(0, 20).map(l => (
              <Box key={`${l.line}:${l.code}`} sx={{ fontSize: 13 }}>
                <Link component="button" type="button" underline="hover" color="inherit" sx={{ font: 'inherit' }}
                  onClick={() => editor.current?.revealLine(l.line)}>
                  {t(`${L}dialog.line_n`, { line: l.line })}
                </Link>
                {' '}<Box component="span" sx={mono}>{l.text}</Box>{' — '}{l.detail ? l.detail : reasonText(t, l.code)}
              </Box>
            ))}
          </Alert>
        )}
        {report && draft.kind !== 'geosite' && !previewing && (
          <ParseReport report={report} onJump={draft.kind === 'custom' ? n => editor.current?.revealLine(n) : undefined} />
        )}
      </DialogContent>
      <Box sx={{ px: 3, pt: 1, fontSize: 13, color: md.onSurfaceVariant }}>{footer}</Box>
      <DialogActions>
        <Button onClick={requestClose}>{t(`${L}dialog.cancel`)}</Button>
        <AsyncButton variant="contained" disabled={!canSave} onClick={submit}>{t(`${L}dialog.save`)}</AsyncButton>
      </DialogActions>
    </Dialog>
  )
}
