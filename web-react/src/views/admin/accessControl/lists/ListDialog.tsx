import { lazy, Suspense, useEffect, useRef, useState } from 'react'
import { Accordion, AccordionDetails, AccordionSummary, Alert, Box, Button, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, IconButton, Skeleton, Stack, TextField, ToggleButton, ToggleButtonGroup, Typography, useMediaQuery, useTheme } from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import { useQuery } from '@tanstack/react-query'
import { useAccessTranslation } from '@/views/admin/accessControl/useAccessTranslation'
import { getDestinationList, previewDestinationList, type DestinationListDetail, type DestinationListInput, type DestinationListSummary, type DestinationPoliciesView, type DestinationStatus } from '@/api/accessControl'
import type { CodeEditorHandle } from '@/components/CodeEditor'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { useSaveDestinationList } from '@/query/accessControl'
import { accessControlKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { discardSettingsCopy } from '../confirmCopy'
import { destinationError, destinationListFailure } from '../errors'
import ParseReport from './ParseReport'
import GeositeCategoryPicker from './GeositeCategoryPicker'
import { MAX_CUSTOM_BYTES, listContentImpact, listPreviewBlocksSave, validRemoteURL } from './listDraft'
const CodeEditor = lazy(() => import('@/components/CodeEditor'))
const P = 'admin:access_control.list_editor.'
function inputFrom(detail?: DestinationListDetail): DestinationListInput {
  if (!detail) return { name: '', kind: 'custom', text: '' }
  return { name: detail.name, kind: detail.kind, ...(detail.kind === 'custom' ? { text: detail.source_text ?? '' } : detail.kind === 'remote' ? { source_url: detail.source_url } : { geosite_category: detail.geosite_category, geosite_attrs: detail.geosite_attrs }) }
}
function contentIdentity(input: DestinationListInput): string { const { name: _name, ...content } = input; return JSON.stringify(content) }
export default function ListDialog({ existing, policies, status, refreshHours, onClose, onSaved, onSettings }: {
  existing?: DestinationListSummary; policies?: DestinationPoliciesView; status?: DestinationStatus; refreshHours: number
  onClose: () => void; onSaved: (list: DestinationListDetail) => void; onSettings?: () => void
}) {
  const { t } = useAccessTranslation(['admin', 'common']), scope = useQueryScope(), mobile = useMediaQuery(useTheme().breakpoints.down('sm'))
  const [seed, setSeed] = useState(inputFrom()), [draft, setDraft] = useState(inputFrom())
  const [detail, setDetail] = useState<DestinationListDetail>(), [loaded, setLoaded] = useState(!existing)
  const [error, setError] = useState<{ error: string; field?: string }>({ error: '' })
  const [busy, setBusy] = useState(false), admission = useRef(false), editor = useRef<CodeEditorHandle>(null)
  const save = useSaveDestinationList(scope)
  useEffect(() => {
    if (!existing) return
    const controller = new AbortController()
    void getDestinationList(existing.id, true, { signal: controller.signal, silent: true }).then(value => {
      if (controller.signal.aborted) return
      const next = inputFrom(value); setDetail(value); setSeed(next); setDraft(next); setLoaded(true)
    }).catch(err => { if (!controller.signal.aborted) setError(destinationError(err)) })
    return () => controller.abort()
  }, [existing])
  const serialized = JSON.stringify(draft), dirty = serialized !== JSON.stringify(seed)
  const content = contentIdentity(draft), [settled, setSettled] = useState<string | null>(null)
  useEffect(() => { const timer = window.setTimeout(() => setSettled(content), 500); return () => window.clearTimeout(timer) }, [content])
  const [tested, setTested] = useState<string | null>(null)
  const byteCount = new TextEncoder().encode(draft.text ?? '').length
  const validName = draft.name.trim().length > 0 && [...draft.name.trim()].length <= 128
  const validSource = draft.kind === 'custom' ? byteCount <= MAX_CUSTOM_BYTES : draft.kind === 'remote' ? validRemoteURL(draft.source_url ?? '') && (draft.source_url?.length ?? 0) <= 1024 : !!draft.geosite_category
  const autoPreview = draft.kind !== 'remote' && settled === content && validSource
  const previewContent = draft.kind === 'remote' ? tested : settled
  const currentPreview = previewContent === content
  const preview = useQuery({ queryKey: [...accessControlKeys.listPreviews(scope), previewContent],
    queryFn: ({ signal }) => previewDestinationList({ ...JSON.parse(previewContent!), name: 'Preview' }, signal),
    enabled: loaded && validSource && (autoPreview || draft.kind === 'remote' && tested === content), staleTime: 0, retry: false,
    refetchOnWindowFocus: false, refetchOnReconnect: false, refetchOnMount: false })
  const previewData = currentPreview ? preview.data : undefined, currentError = currentPreview ? preview.error : null
  const previewError = destinationError(currentError).error
  const previewFailure = destinationListFailure(currentError)
  const blockingPreview = currentError ? listPreviewBlocksSave(draft.kind, previewError) : listPreviewBlocksSave(draft.kind, '', previewData?.entry_count)
  const sameSource = loaded && content === contentIdentity(seed)
  const currentReport = previewData?.parse_report ?? (sameSource ? detail?.parse_report : undefined)
  const enabledReferences = policies ? [...policies.allow, ...policies.block, ...policies.observe].filter(row => row.enabled && row.list_ids.includes(existing?.id ?? 0)).length : undefined
  const used = existing?.owner_group_id || existing?.used_by.some(ref => ref.kind === 'group') ? undefined : enabledReferences ?? (existing?.used_by.some(ref => ref.kind === 'policy') ? undefined : 0)
  const impact = used === undefined ? 'unknown' : listContentImpact(used, detail?.content_sha256, previewData?.content_sha256 ?? (sameSource ? detail?.content_sha256 : undefined))
  const nodeCount = status?.nodes.filter(node => node.kind === 'psp' && node.supports.policy && !['offline', 'unsupported_version'].includes(node.state)).length
  const canSave = loaded && validName && validSource && !blockingPreview && (!existing || dirty) && error.error !== 'dest_list_stale'
  const copy = discardSettingsCopy(t), dirtyClose = useDirtyClose(dirty, copy)
  useLeaveGuard(dirty, copy, (next, current) => next.pathname !== current.pathname || next.search !== current.search, busy)
  const close = async () => { if (!admission.current && await dirtyClose()) onClose() }
  const change = (next: Partial<DestinationListInput>) => { setDraft(old => ({ ...old, ...next })); setError(old => old.error === 'dest_list_stale' ? old : { error: '' }) }
  const switchKind = async (kind: DestinationListInput['kind']) => {
    if (admission.current || kind === draft.kind) return
    admission.current = true; setBusy(true)
    try {
      if (contentIdentity(draft) !== contentIdentity(inputFrom()) && !await confirm({ title: t(`${P}switch_title`), message: t(`${P}switch_message`), confirmText: t(`${P}switch_action`) })) return
      setDraft({ name: draft.name, kind, ...(kind === 'custom' ? { text: '' } : kind === 'remote' ? { source_url: '' } : { geosite_category: '', geosite_attrs: '' }) }); setTested(null); setError({ error: '' })
    } finally { admission.current = false; setBusy(false) }
  }
  const testFetch = () => { if (validSource && !busy && !preview.isFetching) { if (tested === content) void preview.refetch(); else setTested(content) } }
  const reload = async () => {
    if (!existing || admission.current) return
    admission.current = true; setBusy(true)
    try { const latest = await getDestinationList(existing.id, true, { silent: true }); const next = inputFrom(latest); setDetail(latest); setSeed(next); setDraft(next); setLoaded(true); setTested(null); setError({ error: '' }) }
    catch (err) { setError(destinationError(err)) } finally { admission.current = false; setBusy(false) }
  }
  const submit = async () => {
    if (!canSave || admission.current) return
    admission.current = true; setBusy(true)
    try {
      const value = await save.mutateAsync({ input: { ...draft, name: draft.name.trim() }, existing: detail ? { id: detail.id, updated_at: detail.updated_at } : undefined })
      setSeed(draft); pushSnack(t(`${P}saved`, { name: value.name }), 'success'); onSaved(value)
    } catch (err) { const details = destinationError(err); setError({ ...details, field: details.error === 'dest_name_taken' ? 'name' : details.field?.replace(/^list\./, '') }) }
    finally { admission.current = false; setBusy(false) }
  }
  return <Dialog open maxWidth="md" fullWidth fullScreen={mobile} onClose={() => void close()} aria-labelledby="access-list-editor-title">
    <DialogTitle component="div" id="access-list-editor-heading" sx={{ display: 'flex', alignItems: 'center' }}><Typography component="h2" variant="h6" id="access-list-editor-title" sx={{ flex: 1 }}>{t(`${P}${existing ? 'edit_title' : 'create_title'}`)}</Typography><IconButton aria-label={t('common:actions.close')} disabled={busy} onClick={() => void close()}><CloseIcon /></IconButton></DialogTitle>
    <DialogContent dividers><Stack spacing={2}>
      {error.error && error.field !== 'name' && <Alert severity="error" action={existing && (!loaded || error.error === 'dest_list_stale') ? <Button disabled={busy} onClick={() => void reload()}>{t(`${P}reload`)}</Button> : undefined}>{t(`${P}${error.error}`, { defaultValue: error.error })}</Alert>}
      {!loaded ? <Skeleton variant="rounded" height={240} /> : <>
        <Box component="fieldset" disabled={busy} sx={{ border: 0, p: 0, m: 0, minWidth: 0 }}><Stack spacing={2}>
          <ToggleButtonGroup exclusive fullWidth value={draft.kind} disabled={!!existing || busy} onChange={(_, kind) => { if (kind) void switchKind(kind) }}>{(['custom', 'remote', 'geosite'] as const).map(kind => <ToggleButton key={kind} value={kind}>{t(`admin:access_control.lists.${kind}`)}</ToggleButton>)}</ToggleButtonGroup>
          <TextField autoFocus label={t(`${P}name`)} value={draft.name} error={error.field === 'name' || !!draft.name && !validName} helperText={error.field === 'name' ? t(`${P}${error.error}`, { defaultValue: error.error }) : undefined} onChange={e => change({ name: e.target.value })} />
          {draft.kind === 'custom' && <><Box sx={{ minWidth: 0 }}><Typography variant="body2" sx={{ mb: 1 }}>{t(`${P}text`)}</Typography><Suspense fallback={<Skeleton height={220} />}><CodeEditor ref={editor} language="plain" minRows={10} ariaLabel={t(`${P}text`)} readOnly={busy} value={draft.text ?? ''} onChange={text => change({ text })} /></Suspense></Box>
            {byteCount > MAX_CUSTOM_BYTES && <Alert severity="error">{t(`${P}dest_list_too_large`)}</Alert>}
            <Accordion><AccordionSummary expandIcon={<ExpandMoreIcon />}>{t(`${P}formats_title`)}</AccordionSummary><AccordionDetails><Typography component="pre" variant="body2" sx={{ m: 0, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{t(`${P}formats`)}</Typography></AccordionDetails></Accordion></>}
          {draft.kind === 'remote' && <Stack spacing={1}><TextField label={t(`${P}url`)} value={draft.source_url ?? ''} error={!!draft.source_url && !validSource} helperText={!!draft.source_url && !validSource ? t(`${P}https_only`) : undefined} onChange={e => change({ source_url: e.target.value })} /><Button disabled={!validSource || busy || preview.isFetching} onClick={testFetch}>{t(`${P}test_fetch`)}</Button></Stack>}
          {draft.kind === 'geosite' && <GeositeCategoryPicker category={draft.geosite_category ?? ''} attrs={draft.geosite_attrs ?? ''} disabled={busy} onChange={(geosite_category, geosite_attrs) => change({ geosite_category, geosite_attrs })} />}
          {draft.kind !== 'custom' && <Typography variant="caption" color="text.secondary">{t(`${P}refresh`, { hours: refreshHours })}{onSettings && <Button size="small" disabled={busy} onClick={async () => { if (await dirtyClose()) onSettings() }}>{t(`${P}change_refresh`)}</Button>}</Typography>}
        </Stack></Box>
        {preview.isFetching && <CircularProgress size={20} aria-label={t(`${P}parsing`)} />}
        {currentError && <Alert severity={blockingPreview ? 'error' : 'warning'}>{t(`${P}${previewError}`, { defaultValue: previewError })}{previewFailure.httpStatus && <Typography variant="body2">HTTP {previewFailure.httpStatus}</Typography>}{previewFailure.bad.map((item, index) => <Typography key={index} variant="body2" sx={{ fontFamily: 'monospace', overflowWrap: 'anywhere' }}>{item.line > 0 && `${t('admin:access_control.parse_report.line', { line: item.line })}: `}{item.entry}</Typography>)}</Alert>}
        {previewData && draft.kind !== 'custom' && previewData.entry_count === 0 && <Alert severity="error">{t(`${P}dest_list_empty_after_filter`)}</Alert>}
        {previewData && draft.kind === 'remote' && <Typography variant="body2">{t(`${P}fetch_result`, { code: previewData.http_status, bytes: previewData.bytes, count: previewData.entry_count })}</Typography>}
        <ParseReport kind={draft.kind} report={currentReport ?? null} onLine={line => editor.current?.revealLine(line)} />
        {!!previewData?.entries.length && <Accordion><AccordionSummary expandIcon={<ExpandMoreIcon />}>{t(`${P}preview_entries`)}</AccordionSummary><AccordionDetails><Typography component="pre" sx={{ fontSize: 13, m: 0, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{previewData.entries.join('\n')}</Typography></AccordionDetails></Accordion>}
      </>}
    </Stack></DialogContent>
    <DialogActions sx={{ px: 3, py: 2, flexWrap: 'wrap', gap: 1 }}><Typography variant="body2" color="text.secondary" sx={{ flex: '1 1 100%' }}>{t(`${P}impact_${impact}`, { count: used, nodes: nodeCount === undefined ? t('admin:access_control.confirm.each_node') : t('admin:access_control.confirm.node_count', { count: nodeCount }) })}</Typography><Button disabled={busy} onClick={() => void close()}>{t('common:actions.cancel')}</Button><Button variant="contained" disabled={busy || !canSave} onClick={() => void submit()}>{t(busy ? 'admin:access_control.settings.saving' : 'common:actions.save')}</Button></DialogActions>
  </Dialog>
}
