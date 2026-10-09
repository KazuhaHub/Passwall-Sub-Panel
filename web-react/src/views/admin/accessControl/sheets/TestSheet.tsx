import { useEffect, useRef, useState } from 'react'
import { Alert, Box, Button, Drawer, IconButton, MenuItem, Skeleton, Stack, TextField, ToggleButton, ToggleButtonGroup, Typography, useTheme } from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import { useAccessTranslation } from '@/views/admin/accessControl/useAccessTranslation'
import { testDestination, type DestinationStatus, type DestinationTestInput, type DestinationTestResult } from '@/api/accessControl'
import UserAutocomplete from '@/components/UserAutocomplete'
import { AsyncButton } from '@/components/AsyncButton'
import { ToneBadge } from '@/components/ToneBadge'
import { useDestinationPublication } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import type { DrawerHistoryState } from '@/hooks/useDrawerParam'
import { accessTone, fallbackState, pipelineSteps } from '@/utils/accessControl'
import { destinationError } from '../errors'
import PendingActionGuard from '../PendingActionGuard'
import EvalTrace from '../EvalTrace'
import { testDraftInput, testTarget } from './testDraft'
import GlobalExceptionDialog from './GlobalExceptionDialog'
const P = 'admin:access_control.test.'
const notes = ['ip_domain_rules_not_matched', 'no_native_client', 'virtual_scope', 'user_not_in_node_roster', 'protocol_not_testable', 'paused']
export default function TestSheet({ onClose, onOpenPolicy, status, prefill }: { onClose: () => void; onOpenPolicy: (id: number) => void; status?: DestinationStatus; prefill?: DrawerHistoryState['prefill'] }) {
  const { t } = useAccessTranslation(['admin', 'common']), theme = useTheme(), publication = useDestinationPublication(useQueryScope())
  const [target, setTarget] = useState(prefill?.target ?? ''), [port, setPort] = useState(String(prefill?.port ?? 443)), [network, setNetwork] = useState<'tcp' | 'udp'>(prefill?.network === 'udp' ? 'udp' : 'tcp')
  const [userId, setUserId] = useState<number | null>(prefill?.userId ?? null), [panelId, setPanelId] = useState<number | null>(null)
  const [result, setResult] = useState<DestinationTestResult | null>(null), [error, setError] = useState(''), [pending, setPending] = useState(false), [validated, setValidated] = useState(false), [showAll, setShowAll] = useState(false)
  const [publishRequested, setPublishRequested] = useState(false), [publishError, setPublishError] = useState(''), publishAdmission = useRef(false)
  const [exception, setException] = useState(false)
  const [hostOnly, setHostOnly] = useState(false)
  const request = useRef<AbortController | null>(null), mounted = useRef(true)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; request.current?.abort() } }, [])
  const normalized = testTarget(target), input = testDraftInput(target, port, network, userId, panelId)
  const edit = (change: () => void) => { change(); setResult(null); setError(''); setPublishRequested(false); setPublishError(''); setShowAll(false); setHostOnly(false) }
  const run = async (value: DestinationTestInput | null) => {
    setValidated(true)
    if (!value || request.current) return
    if (normalized.url) { setTarget(value.target); setHostOnly(true) }
    const controller = new AbortController(); request.current = controller; setPending(true); setResult(null); setError(''); setPublishRequested(false); setPublishError('')
    try { const response = await testDestination(value, { signal: controller.signal }); if (mounted.current && !controller.signal.aborted) setResult(response) }
    catch (err) { if (mounted.current && !controller.signal.aborted) setError(destinationError(err).error) }
    finally { if (request.current === controller) request.current = null; if (mounted.current) setPending(false) }
  }
  const publish = async () => {
    if (publishAdmission.current || publishRequested) return
    publishAdmission.current = true; setPublishError('')
    try { await publication.mutateAsync({}); if (mounted.current) setPublishRequested(true) }
    catch (err) { if (mounted.current) setPublishError(destinationError(err).error) }
    finally { publishAdmission.current = false }
  }
  const close = () => { if (!publishAdmission.current && !exception) onClose() }
  const blocked = pending || publication.isPending, hit = result?.steps.find(row => row.result === 'hit' && row.step === result.terminating_step)
  const label = result?.terminating_step === 'group' ? result.verdict === 'block' ? 'deny' : result.verdict === 'observe' ? 'trial' : result.verdict : result?.verdict
  const tone = accessTone(theme, label ?? 'direct')
  const unpublished = result?.unpublished || !!status && status.generation !== status.published_generation
  const visibleNodes = result?.nodes.filter(node => panelId === null || node.panel_id === panelId) ?? []
  return <Drawer anchor="right" open onClose={close} slotProps={{ paper: { role: 'dialog', 'aria-labelledby': 'test-sheet-title', sx: { width: { xs: '100vw', sm: 560 }, maxWidth: '100vw', bgcolor: theme.palette.md.surfaceContainerLow, borderTopLeftRadius: { xs: 0, sm: 16 } } } }}>
    <PendingActionGuard hold={publication.isPending} />
    <Box sx={{ p: 2.5, display: 'flex', alignItems: 'center' }}><Typography id="test-sheet-title" component="h2" variant="h6" sx={{ flex: 1, minWidth: 0, overflowWrap: 'anywhere' }}>{t(`${P}title`)}</Typography><IconButton disabled={publication.isPending} aria-label={t('common:actions.close')} onClick={close} sx={{ minWidth: 44, minHeight: 44 }}><CloseIcon /></IconButton></Box>
    <Stack spacing={2.5} sx={{ px: 2.5, pt: 1, pb: 2.5, overflowY: 'auto' }}>
      <Box component="form" aria-label={t(`${P}title`)} onSubmit={event => { event.preventDefault(); void run(input) }} noValidate>
        <Stack component="fieldset" disabled={blocked} spacing={2} sx={{ p: 0, m: 0, border: 0, minWidth: 0 }}>
          <TextField label={t(`${P}target`)} value={target} required error={validated && !normalized.target} helperText={validated && !normalized.target ? t(`${P}target_invalid`) : normalized.url || hostOnly ? t(`${P}url_host_only`) : undefined} onChange={e => edit(() => setTarget(e.target.value))} />
          <Stack direction="row" spacing={1}><TextField label={t(`${P}port`)} value={port} type="number" required error={validated && (!/^\d+$/.test(port) || Number(port) < 1 || Number(port) > 65535)} helperText={validated && !input && normalized.target ? t(`${P}port_invalid`) : undefined} onChange={e => edit(() => setPort(e.target.value))} sx={{ flex: 1, minWidth: 0 }} slotProps={{ htmlInput: { min: 1, max: 65535 } }} /><ToggleButtonGroup exclusive value={network} aria-label={t(`${P}network`)} onChange={(_, value) => { if (value) edit(() => setNetwork(value)) }}><ToggleButton value="tcp" sx={{ minWidth: 44, minHeight: 44 }}>TCP</ToggleButton><ToggleButton value="udp" sx={{ minWidth: 44, minHeight: 44 }}>UDP</ToggleButton></ToggleButtonGroup></Stack>
          <Box sx={{ '& .MuiAutocomplete-root': { width: '100%' }, '& .MuiAutocomplete-popupIndicator, & .MuiAutocomplete-clearIndicator': { minWidth: 44, minHeight: 44 }, '& .MuiAutocomplete-endAdornment': { top: '50%', transform: 'translateY(-50%)' }, '& .MuiAutocomplete-inputRoot': { minHeight: 56, pr: '90px !important' } }}><UserAutocomplete disabled={blocked} label={t(`${P}account`)} value={userId} optionMinHeight={44} onChange={value => edit(() => setUserId(value))} /></Box>
          <TextField select disabled={blocked} label={t(`${P}node`)} value={panelId ?? ''} onChange={e => edit(() => setPanelId(Number(e.target.value) || null))} helperText={!status ? t(`${P}nodes_unavailable`) : undefined} slotProps={{ select: { MenuProps: { slotProps: { list: { sx: { '& .MuiMenuItem-root': { minHeight: 44, whiteSpace: 'normal', overflowWrap: 'anywhere' } } } } } } }}><MenuItem value="">{t(`${P}any_node`)}</MenuItem>{status?.nodes.map(node => <MenuItem key={node.panel_id} value={node.panel_id}>{node.panel_name}</MenuItem>)}</TextField>
          <AsyncButton type="submit" variant="contained" pending={pending} disabled={blocked} fullWidth sx={{ minHeight: 44 }}>{t(`${P}submit`)}</AsyncButton>
        </Stack>
      </Box>
      {pending ? <Stack spacing={1} aria-busy="true">{[0,1,2].map(key => <Skeleton key={key} height={72} variant="rounded" />)}</Stack> : error ? <Alert severity="error" action={<AsyncButton pending={pending} onClick={() => run(input)} sx={{ minHeight: 44 }}>{t('common:actions.retry')}</AsyncButton>}>{t(`${P}failed`)}</Alert> : result ? <Stack spacing={2}>
        {unpublished && <Alert severity="warning" action={<AsyncButton pending={publication.isPending} disabled={blocked || publishRequested} onClick={publish} sx={{ minHeight: 44 }}>{t('admin:access_control.publish')}</AsyncButton>}>{t(`${P}unpublished`)}</Alert>}
        {publishRequested && <Alert severity="info">{t('admin:access_control.publication_requested')}</Alert>}{publishError && <Alert severity="error">{t('admin:access_control.write_failed', { error: publishError })}</Alert>}
        <Box role="status"><ToneBadge wrap tone={tone} label={t(`${P}verdict_${label}`)} />{hit && <Typography sx={{ mt: 1, overflowWrap: 'anywhere' }}>{t(`${P}terminated`, { step: pipelineIndex(result, hit.step), name: hit.name ?? t(`${P}step_${hit.step}`) })}</Typography>}</Box>
        {result.steps.length > 0 && <EvalTrace result={result} />}
        <Typography variant="body2" color="text.secondary">{t(`${P}published_hint`)}</Typography>
        {(showAll ? visibleNodes : visibleNodes.slice(0,5)).map(node => <TestNodeStatus key={node.panel_id} node={node} selected={panelId !== null} />)}
        {visibleNodes.length > 5 && <Button onClick={() => setShowAll(!showAll)} sx={{ minHeight: 44 }}>{t(`${P}${showAll ? 'less_nodes' : 'more_nodes'}`, { count: visibleNodes.length - 5 })}</Button>}
        <Typography variant="body2" color="text.secondary">{t(`${P}destination_only`)}</Typography>
        {notes.filter(note => result.notes.includes(note)).map(note => <Typography key={note} variant="body2" color="text.secondary">{t(`${P}note_${note}`)}</Typography>)}
        {hit?.policy_id && <Button disabled={publication.isPending} onClick={() => onOpenPolicy(hit.policy_id!)} sx={{ minHeight: 44 }}>{t(`${P}open_policy`, { name: hit.name ?? `#${hit.policy_id}` })}</Button>}
      </Stack> : <Typography color="text.secondary">{t(`${P}initial`)}</Typography>}
      {result && <Button disabled={blocked} onClick={() => setException(true)} sx={{ minHeight: 44 }}>{t('admin:access_control.exception.title')}</Button>}
    </Stack>
    {exception && input && <GlobalExceptionDialog target={input.target} userId={input.user_id} etaMs={status?.apply_eta_ms} onClose={() => setException(false)} />}
  </Drawer>
}
function TestNodeStatus({ node, selected }: { node: DestinationTestResult['nodes'][number]; selected: boolean }) {
  const { t, number } = useAccessTranslation(['admin'])
  const execution = node.execution, fallback = execution ? fallbackState(execution) : 'none'
  const prolonged = node.state === 'pending' && execution?.pending_since != null && Date.now() - execution.pending_since > 600000
  return <Box sx={{ minWidth: 0, overflowWrap: 'anywhere' }}>
    <Typography variant="body2" sx={{ fontWeight: 600 }}>{node.name}</Typography>
    <Typography variant="body2" color={node.state === 'applied' ? 'text.secondary' : 'warning.main'}>{t(node.state === 'none' ? `${P}node_none` : `admin:access_control.coverage.description_${prolonged ? 'pending_long' : node.state}`)}</Typography>
    {fallback !== 'none' && <Alert severity={fallback === 'stopping' || fallback === 'exhausted' ? 'error' : 'warning'}>{t(`admin:access_control.coverage.fallback_${fallback}`)}{fallback === 'applied' && <Typography variant="body2">{t('admin:access_control.coverage.fallback_members')}</Typography>}</Alert>}
    {execution?.applied_rules != null && <Typography variant="body2">{t('admin:access_control.coverage.executing_rules')} {number(execution.applied_rules)}</Typography>}
    {selected && node.state !== 'applied' && <Typography variant="caption">{t(`${P}actual_may_differ`)}</Typography>}
  </Box>
}
function pipelineIndex(result: DestinationTestResult, step: string): number {
  return pipelineSteps(result.steps.some(row => row.step === 'group')).findIndex(value => value === step) + 1
}
