import { lazy, Suspense, useId, useRef, useState, type ReactNode } from 'react'
import { Alert, Box, Button, Checkbox, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, Radio, RadioGroup, Skeleton, Stack, Step, StepButton, Stepper, TextField, Typography, useMediaQuery, useTheme } from '@mui/material'
import { AsyncButton } from '@/components/AsyncButton'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import type { DestinationGroupSummary } from '@/api/destinationGroups'
import type { DestinationListSummary } from '@/api/accessControl'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { useDestinationGroups, useDestinationGroupPreview, useSaveDestinationGroup } from '@/query/destinationGroups'
import { useDestinationLists } from '@/query/accessControl'
import type { QueryScope } from '@/query/session'
import { discardSettingsCopy } from '../confirmCopy'
import { destinationError } from '../errors'
import { useAccessTranslation } from '../useAccessTranslation'

const CodeEditor = lazy(() => import('@/components/CodeEditor'))
const P = 'admin:access_control.allowlist_enable.'
interface Props { scope: QueryScope; initialGroupID?: number; editing?: boolean; onClose: () => void; onStarted: (id: number) => void; onList: (id: number) => void }
function Frame({ title, children, actions, onClose, busy = false }: { title: string; children: ReactNode; actions?: ReactNode; onClose: () => void; busy?: boolean }) {
  const id = useId(), theme = useTheme(), fullScreen = useMediaQuery(theme.breakpoints.down('sm'))
  return <Dialog open fullWidth maxWidth="md" fullScreen={fullScreen} onClose={() => { if (!busy) onClose() }} aria-labelledby={id} slotProps={{ paper: { sx: { '& button': { minHeight: 44 } } } }}>
    <DialogTitle id={id}>{title}</DialogTitle><DialogContent dividers>{children}</DialogContent><DialogActions sx={{ p: 2, gap: 1, flexWrap: 'wrap' }}>{actions}</DialogActions>
  </Dialog>
}
export default function EnableAllowlistDialog(props: Props) {
  const { t } = useAccessTranslation(['admin', 'common'])
  const groups = useDestinationGroups(props.scope), lists = useDestinationLists(props.scope)
  if ((!groups.data || !lists.data) && (groups.error || lists.error)) return <Frame title={t(`${P}title`)} onClose={props.onClose} actions={<Button onClick={props.onClose}>{t('common:actions.cancel')}</Button>}>
    <Alert severity="error" action={<AsyncButton pending={groups.isFetching || lists.isFetching} onClick={() => Promise.all([groups.refetch(), lists.refetch()])}>{t('common:actions.retry')}</AsyncButton>}>{t(`${P}read_failed`)}</Alert>
  </Frame>
  if (!groups.data || !lists.data) return <Frame title={t(`${P}title`)} onClose={props.onClose} actions={<Button onClick={props.onClose}>{t('common:actions.cancel')}</Button>}><Stack spacing={2} aria-busy="true">{[0, 1, 2].map(key => <Skeleton key={key} variant="rounded" height={64} />)}</Stack></Frame>
  return <Editor {...props} groups={groups.data.items} lists={lists.data.items} readFailed={!!groups.error || !!lists.error} retry={() => Promise.all([groups.refetch(), lists.refetch()])} reload={async () => {
    const result = await groups.refetch()
    if (result.error || !result.data) throw new Error('Group reload failed')
    return result.data.items
  }} />
}
function Editor({ scope, initialGroupID, editing = false, groups, lists, reload, readFailed, retry, onClose, onStarted, onList }: Props & { groups: DestinationGroupSummary[]; lists: DestinationListSummary[]; reload: () => Promise<DestinationGroupSummary[]>; readFailed: boolean; retry: () => Promise<unknown> }) {
  const { t } = useAccessTranslation(['admin', 'common']), theme = useTheme(), mobile = useMediaQuery(theme.breakpoints.down('sm'))
  const [baseline, setBaseline] = useState(() => groups.find(group => group.group_id === initialGroupID && group.mode === (editing ? 'allowlist' : 'open')))
  const [step, setStep] = useState(editing ? 1 : 0), [search, setSearch] = useState('')
  const [selected, setSelected] = useState<number[]>(baseline?.list_ids ?? [])
  const [baseDraft, setBaseDraft] = useState(''), [baseEdited, setBaseEdited] = useState(false), [showBase, setShowBase] = useState(false)
  const [touched, setTouched] = useState(false), [submitting, setSubmitting] = useState(false), [conflict, setConflict] = useState(false), [failed, setFailed] = useState(false)
  const originalStage = useRef(baseline?.stage ?? ''), closing = useRef(false), admission = useRef(false), departure = useRef('unblocked')
  const save = useSaveDestinationGroup(scope), busy = submitting || save.isPending
  const preview = useDestinationGroupPreview(scope, baseline?.group_id ?? 0, selected, step > 0)
  const facts = !preview.error ? preview.data : undefined
  const choices = groups.filter(group => group.mode === 'open')
  const visibleGroups = choices.filter(group => group.group_name.toLocaleLowerCase().includes(search.toLocaleLowerCase()))
  const available = lists.filter(list => (list.owner_group_id === 0 || list.owner_group_id === baseline?.group_id) && list.id !== baseline?.base_list_id && list.id !== baseline?.extra_list_id)
  const selectedLists = selected.map(id => available.find(list => list.id === id))
  const compatible = baseline && (editing ? baseline.mode === 'allowlist' && baseline.stage === originalStage.current : baseline.mode === 'open')
  const baseHosts = (baseEdited ? baseDraft : facts?.base_hosts.join('\n') ?? '').split(/\r?\n/).map(host => host.trim()).filter(Boolean)
  const hasLists = selected.length > 0 && selectedLists.every(Boolean)
  const dirty = touched && !closing.current
  const mayClose = useDirtyClose(dirty, discardSettingsCopy(t))
  departure.current = useLeaveGuard(dirty, discardSettingsCopy(t), (next, current) => !closing.current && (next.pathname !== current.pathname || next.search !== current.search), busy)
  const close = async () => {
    if (busy || admission.current) return
    admission.current = true
    try { if (await mayClose()) { closing.current = true; onClose() } } finally { admission.current = false }
  }
  const review = async () => {
    try { const fresh = (await reload()).find(group => group.group_id === baseline?.group_id); if (!fresh) throw new Error('Missing group'); setBaseline(fresh); setConflict(false); setFailed(false) }
    catch { setFailed(true) }
  }
  const submit = async () => {
    if (admission.current || busy || readFailed || !compatible || conflict || !hasLists || !facts || preview.isFetching || !baseline) return
    admission.current = true; setSubmitting(true); setFailed(false)
    try {
      if (!editing && facts.eligible_nodes === 0 && !(await confirm({ title: t(`${P}zero_confirm_title`), message: t(`${P}zero_confirm_message`, { name: baseline.group_name }), confirmText: t(`${P}start`), destructive: true }))) return
      const input = { mode: 'allowlist' as const, stage: editing && baseline.stage === 'enforce' ? 'enforce' as const : 'trial' as const, list_ids: [...selected], updated_at: baseline.updated_at, ...(baseline.base_list_id === 0 ? { base_hosts: baseHosts } : {}) }
      await save.mutateAsync({ groupID: baseline.group_id, input })
      closing.current = true; setTouched(false)
      pushSnack(t(`${P}${editing ? 'saved' : 'started'}`, { name: baseline.group_name }), 'success')
      if (departure.current !== 'blocked') onStarted(baseline.group_id)
    } catch (error) { if (destinationError(error).status === 409) setConflict(true); else setFailed(true) }
    finally { admission.current = false; setSubmitting(false) }
  }
  const next = () => { if (step === 0 && baseline || step === 1 && hasLists) setStep(value => value + 1) }
  const steps = ['group_step', 'lists_step', 'impact_step']
  return <Frame title={t(`${P}${editing ? 'edit_title' : 'title'}`, { name: baseline?.group_name })} onClose={close} busy={busy} actions={<>
    <Button disabled={busy} onClick={close}>{t('common:actions.cancel')}</Button>
    {step > (editing ? 1 : 0) && <Button disabled={busy} onClick={() => setStep(value => value - 1)}>{t(`${P}back`)}</Button>}
    {step < 2 ? <Button variant="contained" disabled={busy || (step === 0 ? !baseline : !hasLists)} onClick={next}>{t(`${P}next`)}</Button> : <AsyncButton variant="contained" pending={busy} disabled={readFailed || !compatible || conflict || !hasLists || !facts || preview.isFetching} onClick={submit}>{t(editing ? 'common:actions.save' : `${P}start`)}</AsyncButton>}
  </>}>
    <Stack spacing={2}>
      {mobile ? <Typography variant="body2">{t(`${P}mobile_step`, { step: step + 1, total: 3 })}</Typography> : <Stepper activeStep={step}>{steps.map((key, index) => <Step key={key} completed={index < step}><StepButton disabled={busy || index > step || editing && index === 0} onClick={() => setStep(index)}>{t(`${P}${key}`)}</StepButton></Step>)}</Stepper>}
      {conflict && <Alert severity="warning" action={<AsyncButton onClick={review}>{t(`${P}review`)}</AsyncButton>}>{t(`${P}conflict`)}</Alert>}
      {failed && <Alert severity="error">{t(`${P}save_failed`)}</Alert>}
      {readFailed && <Alert severity="error" action={<AsyncButton onClick={retry}>{t('common:actions.retry')}</AsyncButton>}>{t(`${P}read_failed`)}</Alert>}
      {!compatible && baseline && <Alert severity="warning">{t(`${P}changed_mode`)}</Alert>}
      {step === 0 && <>
        {choices.length > 8 && <TextField label={t(`${P}search`)} value={search} onChange={e => setSearch(e.target.value)} />}
        {choices.length === 0 ? <Typography color="text.secondary">{t(`${P}all_enabled`)}</Typography> : <RadioGroup value={baseline?.group_id ?? ''} onChange={event => { const chosen = choices.find(group => group.group_id === Number(event.target.value)); setBaseline(chosen); setSelected(chosen?.list_ids ?? []); setBaseEdited(false); setShowBase(false); setTouched(true); setConflict(false); originalStage.current = chosen?.stage ?? '' }}>
          {visibleGroups.map(group => <FormControlLabel key={group.group_id} value={group.group_id} control={<Radio />} label={<Box><Typography>{group.group_name}</Typography><Typography variant="body2" color="text.secondary">{t(`${P}members`, { count: group.members })}</Typography></Box>} sx={{ mx: 0, py: .5, minHeight: 44 }} />)}
        </RadioGroup>}
      </>}
      {step === 1 && <>
        <Typography variant="body2">{t(`${P}lists_notice`)}</Typography>
        <Box component="fieldset" disabled={busy} sx={{ border: 0, p: 0, m: 0, minWidth: 0 }}>
          <Stack>{available.map(list => <FormControlLabel key={list.id} control={<Checkbox checked={selected.includes(list.id)} onChange={event => { setSelected(ids => event.target.checked ? [...ids, list.id] : ids.filter(id => id !== list.id)); setTouched(true) }} />} label={<Typography sx={{ overflowWrap: 'anywhere' }}>{list.name}</Typography>} sx={{ minHeight: 44 }} />)}</Stack>
        </Box>
        {!hasLists && <Typography variant="body2" color="text.secondary">{t(`${P}choose_list`)}</Typography>}
        {selectedLists.some(list => list && list.kind !== 'custom' && list.last_fetched_at === null) && <Alert severity="warning">{t(`${P}unready`)}</Alert>}
        {baseline?.base_list_id ? <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}><Typography variant="body2">{t(`${P}base_retained`)}</Typography><Button disabled={busy} onClick={async () => { if (await mayClose()) { closing.current = true; onList(baseline.base_list_id) } }}>{t(`${P}open_base`)}</Button></Stack> : <>
          {facts && !facts.base_hosts_available && <Alert severity="warning">{t(`${P}dns_unknown`)}</Alert>}
          <Typography variant="body2">{t(`${P}base_notice`, { count: baseHosts.length, hosts: baseHosts.join(' · ') })}</Typography>
          <Button disabled={busy} onClick={() => setShowBase(value => !value)}>{t(`${P}edit_base`)}</Button>
          {(showBase || facts && !facts.base_hosts_available) && <Suspense fallback={<Skeleton height={120} />}><CodeEditor value={baseEdited ? baseDraft : facts?.base_hosts.join('\n') ?? ''} onChange={value => { setBaseDraft(value); setBaseEdited(true); setTouched(true) }} language="plain" minRows={6} ariaLabel={t(`${P}base_hosts`)} readOnly={busy} /></Suspense>}
          <Typography variant="body2" color="text.secondary">{t(`${P}base_dns_explanation`)}</Typography>
        </>}
        <Typography variant="body2" color="text.secondary">{t(`${P}extra_notice`)}</Typography>
      </>}
      {step > 0 && preview.error && <Alert severity="error" action={<AsyncButton pending={preview.isFetching} onClick={() => preview.refetch()}>{t('common:actions.retry')}</AsyncButton>}>{t(`${P}preview_failed`)}</Alert>}
      {step === 2 && (facts ? <>
        <Typography variant="h6" component="h3">{baseline?.group_name}</Typography>
        <Typography>{t(`${P}impact`, { members: facts.members, ok: facts.eligible_nodes, excluded: facts.total_nodes - facts.eligible_nodes })}</Typography>
        {facts.eligible_nodes === 0 && <Box sx={{ p: 2, borderRadius: 2, bgcolor: theme.palette.md.errorContainer, color: theme.palette.md.onErrorContainer }}>{t(`${P}zero_nodes`)}</Box>}
        <Stack spacing={1}>{facts.ineligible.map(node => <Box key={node.panel_id}><Typography>{node.name}</Typography><Typography variant="body2" color="text.secondary">{t(`admin:access_control.allowlist.reason.${node.reason}`)}</Typography></Box>)}</Stack>
        {!facts.base_hosts_available && baseline?.base_list_id === 0 && <Alert severity="warning">{t(`${P}dns_unknown`)}</Alert>}
        <Typography variant="body2">{t(`${P}trial_notice`)}</Typography>
      </> : <Stack spacing={1} aria-busy="true">{[0, 1, 2].map(key => <Skeleton key={key} height={64} />)}</Stack>)}
    </Stack>
  </Frame>
}
