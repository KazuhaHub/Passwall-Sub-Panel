import { lazy, Suspense, useEffect, useId, useRef, useState } from 'react'
import { Accordion, AccordionDetails, AccordionSummary, Alert, Autocomplete, Box, Button, Checkbox, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, IconButton, MenuItem, Skeleton, Stack, Switch, TextField, Typography, useMediaQuery, useTheme } from '@mui/material'
import CloseIcon from '@mui/icons-material/Close'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { getDestinationPolicies, previewDestinationPolicy, type DestinationPoliciesView, type DestinationPolicyInput, type DestinationPolicyOverviewItem, type DestinationStatus } from '@/api/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { accessControlKeys } from '@/query/keys'
import { useDestinationLists, useSaveDestinationPolicy } from '@/query/accessControl'
import { useAllGroups } from '@/query/groups'
import { destinationListAvailable } from '@/utils/destinationListAvailability'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { discardSettingsCopy, firstPublishCopy, needsFirstPublishConfirm } from '../confirmCopy'
import { destinationError } from '../errors'
import { executionChanged, matchSummary, policyInput, validatePolicyDraft } from './policyDraft'
import { summaryText } from './summaryText'
import QuotaMeters, { budgetExceeded } from './QuotaMeters'
import GeositeCategoryPicker from '../lists/GeositeCategoryPicker'
import ParseReport from '../lists/ParseReport'
import { listPreviewBlocksSave } from '../lists/listDraft'
import PolicySegments from './PolicySegments'
import { AsyncButton } from '@/components/AsyncButton'
import FieldHint from '@/components/FieldHint'
import { ToneBadge, stateTone } from '@/components/ToneBadge'
const CodeEditor = lazy(() => import('@/components/CodeEditor'))
const P = 'admin:access_control.editor.'
interface Props { initial: DestinationPolicyInput; existing?: DestinationPolicyOverviewItem; templateName?: string; policies: DestinationPoliciesView; status?: DestinationStatus; seconds?: number; onClose: () => void }
function hasAdditionalConditions(input: DestinationPolicyInput) {
  return !!(input.inline.ports?.trim() || input.inline.network || input.inline.cidrs?.some(line => line.trim()) || input.inline.protocols?.length || input.inline.private)
}
export default function PolicyEditorDialog({ initial, existing, templateName, policies, status, seconds, onClose }: Props) {
  const { t } = useTranslation(['admin', 'common'])
  const scope = useQueryScope()
  const theme = useTheme()
  const mobile = useMediaQuery(theme.breakpoints.down('sm'))
  const [seed, setSeed] = useState(() => policyInput(initial))
  const [draft, setDraft] = useState(seed)
  const [conditionsOpen, setConditionsOpen] = useState(() => hasAdditionalConditions(seed))
  const conditionsId = useId()
  const [version, setVersion] = useState(existing?.updated_at)
  const [error, setError] = useState<{ error: string; field?: string }>({ error: '' })
  const [busy, setBusy] = useState(false)
  const admission = useRef(false)
  const lists = useDestinationLists(scope)
  const groups = useAllGroups(scope)
  const save = useSaveDestinationPolicy(scope)
  const dirty = JSON.stringify(draft) !== JSON.stringify(seed)
  const validation = validatePolicyDraft(draft)
  const valid = !Object.keys(validation).length
  const input = { ...draft, inline: { ...draft.inline, cidrs: draft.inline.cidrs?.map(line => line.trim()).filter(Boolean) },
    name: draft.name.trim(), ...(draft.new_list ? { new_list: { ...draft.new_list, name: draft.new_list.name.trim() } } : {}), ...(existing ? { id: existing.id, updated_at: version } : {}) }
  const serialized = JSON.stringify(input)
  const [settled, setSettled] = useState(serialized)
  useEffect(() => { const timer = window.setTimeout(() => setSettled(serialized), 400); return () => window.clearTimeout(timer) }, [serialized])
  const preview = useQuery({ queryKey: [...accessControlKeys.policyPreviews(scope), settled],
    queryFn: ({ signal }) => previewDestinationPolicy(JSON.parse(settled), signal), enabled: valid && settled === serialized,
    staleTime: 0, retry: false })
  const quotaError = settled === serialized && (budgetExceeded(preview.data?.budget) || destinationError(preview.error).error === 'dest_policy_over_limit')
  const categoryError = settled === serialized && !!draft.new_list && listPreviewBlocksSave('geosite', preview.error ? destinationError(preview.error).error : '', preview.data?.new_list_preview?.entry_count)
  const closeCheck = useDirtyClose(dirty, discardSettingsCopy(t))
  useLeaveGuard(dirty, discardSettingsCopy(t), (next, current) => next.pathname !== current.pathname || next.search !== current.search, busy)
  const close = () => { if (!busy) void closeCheck().then(ok => { if (ok) onClose() }) }
  const change = (next: Partial<DestinationPolicyInput>) => { setDraft(old => ({ ...old, ...next })); setError(old => old.error === 'dest_policy_stale' ? old : { error: '' }) }
  const fieldError = (key: string) => error.field === key ? error.error : validation[key]
  const fieldMessage = (key: string) => fieldError(key) ? t(`${P}${key === 'cidrs' && validation.cidrs ? 'cidrs_invalid' : fieldError(key)}`, { lines: validation.cidrs, defaultValue: t(`${P}invalid`) }) : undefined
  const submit = async () => {
    if (admission.current || !valid || quotaError || categoryError || (!dirty && !!existing) || error.error === 'dest_policy_stale') return
    admission.current = true; setBusy(true)
    try {
      if (needsFirstPublishConfirm(policies, [], input) && !(await confirm(firstPublishCopy(t, status, seconds)))) return
      await save.mutateAsync({ input: policyInput(input), existing: existing ? { id: existing.id, updated_at: version! } : undefined })
      setSeed(draft)
      pushSnack(t('admin:access_control.policies.saved', { name: draft.name }), 'success'); onClose()
    } catch (err) {
      const details = destinationError(err)
      const field = details.error === 'dest_name_taken' ? 'name' : details.field
      setError({ error: details.error, field: field && ['name', 'ports', 'cidrs', 'group_ids'].includes(field) ? field : undefined })
      if (field === 'ports' || field === 'cidrs') setConditionsOpen(true)
    } finally { admission.current = false; setBusy(false) }
  }
  const reload = async () => {
    if (admission.current) return
    admission.current = true; setBusy(true)
    try {
      const latest = await getDestinationPolicies({ silent: true })
      const row = [...latest.allow, ...latest.block, ...latest.observe].find(row => row.id === existing?.id)
      if (!row) { setError({ error: 'dest_policy_missing' }); return }
      const next = policyInput(row); setSeed(next); setDraft(next); setConditionsOpen(hasAdditionalConditions(next)); setVersion(row.updated_at); setError({ error: '' })
    } catch (err) { setError(destinationError(err)) } finally { admission.current = false; setBusy(false) }
  }
  const listChoices = (lists.data?.items ?? []).filter(list => !list.owner_group_id)
  const names = new Map(listChoices.map(list => [list.id, list.name]))
  const match = matchSummary(draft)
  const additionalSummary = summaryText(t, { ...draft, list_ids: [], new_list: undefined }, names)
  const nodeCount = status?.nodes.filter(node => node.kind === 'psp' && node.supports.policy && !['offline', 'unsupported_version'].includes(node.state)).length
  const rows = policies[draft.action]
  const position = existing && existing.action === draft.action ? Math.max(1, rows.findIndex(row => row.id === existing.id) + 1) : rows.length + 1
  const saveButton = <AsyncButton variant="contained" pending={busy} disabled={!valid || quotaError || categoryError || (!!existing && !dirty) || error.error === 'dest_policy_stale'} onClick={submit}>{t('common:actions.save')}</AsyncButton>
  const closeButton = <IconButton aria-label={t('common:actions.close')} disabled={busy} onClick={close}><CloseIcon /></IconButton>
  return <Dialog open fullWidth maxWidth={false} slotProps={{ paper: { sx: { maxWidth: mobile ? 'none' : 720 } } }} fullScreen={mobile} onClose={close} aria-labelledby="access-policy-title">
    <DialogTitle component="div" id="access-policy-heading" sx={{ display: 'flex', alignItems: 'center', gap: 1, px: { xs: 1, sm: 3 } }}>
      {mobile && closeButton}<Typography component="h2" variant="h6" id="access-policy-title" sx={{ flex: 1, minWidth: 0, overflowWrap: 'anywhere' }}>{t(existing ? `${P}edit_title` : templateName ? `${P}template_title` : `${P}create_title`, { ...(existing ? { name: seed.name } : {}), template: templateName })}</Typography>{mobile ? saveButton : closeButton}
    </DialogTitle>
    <DialogContent dividers><Stack spacing={2.5}>
      {error.error && !error.field && <Alert severity="error" action={error.error === 'dest_policy_stale' ? <Button color="inherit" disabled={busy} onClick={() => void reload()}>{t(`${P}reload`)}</Button> : undefined}>{t(`${P}${error.error}`, { defaultValue: error.error })}</Alert>}
      <Box component="fieldset" disabled={busy} sx={{ border: 0, p: 0, m: 0, minWidth: 0 }}><Stack spacing={2.5}>
        <TextField autoFocus label={t(`${P}name`)} value={draft.name} onChange={e => change({ name: e.target.value })} error={!!fieldError('name')} helperText={fieldMessage('name')} />
        <PolicySegments value={draft.action} label={t(`${P}action`)} disabled={busy} options={(['block', 'observe', 'allow'] as const).map(value => ({ value, label: t(`${P}action_${value}`) }))} onChange={action => change({ action, counts_as_risk: action === 'block' && draft.counts_as_risk })} />
        <Typography variant="body2" color="text.secondary">{t(`${P}${draft.action}_hint`)}</Typography>
        {draft.action === 'allow' && <Typography variant="caption"><FieldHint tone="amber" summary={t(`${P}allow_summary`)} detail={t(`${P}allow_detail`)} /></Typography>}
        {draft.new_list && <Stack spacing={2}>
          <TextField label={t('admin:access_control.templates.list_name_field')} value={draft.new_list.name} error={!!validation.new_list} helperText={validation.new_list ? t(`${P}invalid`) : t('admin:access_control.templates.paired_save')} onChange={e => change({ new_list: { ...draft.new_list!, name: e.target.value } })} />
          <GeositeCategoryPicker category={draft.new_list.geosite_category} attrs={draft.new_list.geosite_attrs} disabled={busy} onChange={(geosite_category, geosite_attrs) => change({ new_list: { ...draft.new_list!, geosite_category, geosite_attrs } })} />
          {settled === serialized && preview.data?.new_list_preview && <ParseReport report={preview.data.new_list_preview.parse_report} kind="geosite" />}
          <Button disabled={busy} onClick={() => change({ new_list: undefined })}>{t('admin:access_control.templates.remove_category')}</Button>
        </Stack>}
        {lists.error && <Alert severity="error" action={<Button onClick={() => void lists.refetch()}>{t('common:actions.retry')}</Button>}>{t(`${P}lists_failed`)}</Alert>}
        <Autocomplete multiple options={listChoices.map(list => list.id)} value={draft.list_ids} loading={lists.isPending} disabled={busy}
          getOptionLabel={id => { const list = listChoices.find(list => list.id === id); return list ? t(`${P}list_option`, { name: list.name, kind: t(`admin:access_control.lists.${list.kind}`), count: list.entry_count, regexps: list.regexp_count }) : `#${id}` }}
          getOptionDisabled={id => listChoices.some(list => list.id === id && list.kind === 'custom' && !list.entry_count)}
          renderOption={(props, id) => {
            const { key, ...optionProps } = props
            const list = listChoices.find(list => list.id === id)!
            const empty = list.kind === 'custom' && !list.entry_count
            return <li key={key} {...optionProps} title={empty ? t(`${P}list_empty`) : undefined}
              onClick={empty ? event => { event.preventDefault(); event.stopPropagation() } : optionProps.onClick}
              style={{ ...optionProps.style, ...(empty ? { pointerEvents: 'auto' as const } : {}) }}>
              <Box sx={{ minWidth: 0, width: '100%' }}>
                <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>{t(`${P}list_option`, { name: list.name, kind: t(`admin:access_control.lists.${list.kind}`), count: list.entry_count, regexps: list.regexp_count })}</Typography>
                {list.state !== 'ready' && <ToneBadge tone={stateTone(theme, 'attention')} label={t(`${P}list_attention`)} />}
                {empty && <Typography variant="caption">{t(`${P}list_empty`)}</Typography>}
              </Box>
            </li>
          }}
          onChange={(_, list_ids) => change({ list_ids })} renderInput={p => <TextField {...p} label={t(`${P}lists`)} />} />
        {draft.list_ids.some(id => { const list = listChoices.find(list => list.id === id); return !list || !destinationListAvailable(list) }) && <Typography variant="caption"><FieldHint tone="amber" summary={t(`${P}list_pending_summary`)} detail={t(`${P}list_pending_detail`)} /></Typography>}
        <Accordion disableGutters elevation={0} expanded={conditionsOpen} onChange={(_, expanded) => setConditionsOpen(expanded)} disabled={busy} sx={{ border: 1, borderColor: 'divider', borderRadius: 2, '&:before': { display: 'none' } }}>
          <AccordionSummary expandIcon={<ExpandMoreIcon />} aria-label={t(`${P}more_conditions`)} aria-describedby={additionalSummary ? `${conditionsId}-summary` : undefined} aria-controls={`${conditionsId}-content`} id={`${conditionsId}-toggle`} sx={{ '& .MuiAccordionSummary-content': { flexWrap: 'wrap', gap: 1, alignItems: 'center', minWidth: 0 } }}>
            <Typography component="span" variant="subtitle2" sx={{ flex: '1 1 auto' }}>{t(`${P}more_conditions`)}</Typography>
            {additionalSummary && <Typography component="span" id={`${conditionsId}-summary`} variant="caption" color="text.secondary" sx={{ overflowWrap: 'anywhere' }}>{t(`${P}conditions_set`)} {additionalSummary}</Typography>}
          </AccordionSummary>
          <AccordionDetails><Stack spacing={2}>
        <Box><Typography variant="body2" sx={{ mb: 1 }}>{t(`${P}cidrs`)}</Typography><Suspense fallback={<Skeleton height={140} />}><CodeEditor language="plain" minRows={6} ariaLabel={t(`${P}cidrs`)} value={(draft.inline.cidrs ?? []).join('\n')} readOnly={busy} onChange={value => change({ inline: { ...draft.inline, cidrs: value.split('\n') } })} /></Suspense>
          <Typography variant="caption" color={fieldError('cidrs') ? 'error' : 'text.secondary'}>{fieldError('cidrs') ? fieldMessage('cidrs') : <FieldHint tone="muted" summary={t(`${P}cidrs_summary`)} detail={t(`${P}cidrs_detail`)} />}</Typography></Box>
        <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2}><TextField sx={{ flex: 1 }} label={t(`${P}ports`)} value={draft.inline.ports ?? ''} error={!!fieldError('ports')} helperText={fieldMessage('ports') ?? t(`${P}ports_hint`)} onChange={e => change({ inline: { ...draft.inline, ports: e.target.value } })} /><Box sx={{ flex: { xs: 1, sm: '0 1 220px' }, minWidth: { sm: 200 } }}><Typography variant="caption" color="text.secondary">{t(`${P}network`)}</Typography><PolicySegments value={draft.inline.network ?? ''} label={t(`${P}network`)} disabled={busy} fullWidth options={(['', 'tcp', 'udp'] as const).map(value => ({ value, label: value ? value.toUpperCase() : t(`${P}any`) }))} onChange={network => change({ inline: { ...draft.inline, network } })} /></Box></Stack>
        <FormControlLabel control={<Checkbox checked={match.bt} onChange={(_, checked) => change({ inline: { ...draft.inline, protocols: checked ? ['bittorrent'] : [] } })} />} label={t(`${P}bt`)} />
        <Typography variant="caption" color="text.secondary"><FieldHint tone="muted" summary={t(`${P}bt_summary`)} detail={t(`${P}bt_detail`)} /></Typography>
        <FormControlLabel control={<Checkbox checked={match.private} onChange={(_, checked) => change({ inline: { ...draft.inline, private: checked } })} />} label={t(`${P}private`)} />
          </Stack></AccordionDetails>
        </Accordion>
        {validation.match && <Alert severity="error">{t(`${P}no_match`)}</Alert>}
        <TextField select label={t(`${P}scope`)} value={draft.scope} onChange={e => change({ scope: e.target.value as 'all' | 'groups', group_ids: e.target.value === 'all' ? [] : draft.group_ids })}>{['all', 'groups'].map(scope => <MenuItem key={scope} value={scope}>{t(`${P}${scope}`)}</MenuItem>)}</TextField>
        {draft.scope === 'groups' && <><Autocomplete multiple options={(groups.data ?? []).map(group => group.id)} value={draft.group_ids} disabled={busy} loading={groups.isPending} getOptionLabel={id => groups.data?.find(group => group.id === id)?.name ?? `#${id}`} onChange={(_, group_ids) => change({ group_ids })} renderInput={p => <TextField {...p} label={t(`${P}groups`)} error={!!fieldError('group_ids')} helperText={fieldMessage('group_ids')} />} />{groups.error && <Alert severity="error" action={<Button onClick={() => void groups.refetch()}>{t('common:actions.retry')}</Button>}>{t(`${P}groups_failed`)}</Alert>}</>}
        {draft.action === 'block' && <FormControlLabel control={<Switch checked={draft.counts_as_risk} onChange={(_, counts_as_risk) => change({ counts_as_risk })} />} label={t(`${P}counts_as_risk`)} />}
        <FormControlLabel control={<Switch checked={draft.enabled} onChange={(_, enabled) => change({ enabled })} />} label={t(`${P}enabled`)} />
      </Stack></Box>
      <Typography variant="subtitle2">{t(`${P}quota`)}</Typography>
      {preview.error ? <Alert severity={quotaError || categoryError ? 'error' : 'warning'} action={<Button disabled={busy} onClick={() => void preview.refetch()}>{t('common:actions.retry')}</Button>}>{t(quotaError ? 'admin:access_control.quota.exceeded' : categoryError ? `admin:access_control.list_editor.${destinationError(preview.error).error}` : `${P}preview_failed`, { defaultValue: t(`${P}preview_failed`) })}</Alert> : <QuotaMeters budget={settled === serialized ? preview.data?.budget : undefined} />}
      {categoryError && !preview.error && <Alert severity="error">{t('admin:access_control.list_editor.dest_list_empty_after_filter')}</Alert>}
    </Stack></DialogContent>
    <DialogActions sx={{ position: 'sticky', bottom: 0, bgcolor: 'background.paper', flexWrap: 'wrap', px: 3, py: 2, gap: 1 }}>
      <Box sx={{ flex: '1 1 100%' }}><Typography variant="body2">{t(`${P}action_${draft.action}`)} · {t(`${P}${draft.scope}`)} · {summaryText(t, draft, names)} · {t(`${P}position`, { step: { allow: 1, block: 3, observe: 4 }[draft.action], position })}</Typography>
        {match.split && <Typography variant="caption">{t(`${P}split`)}</Typography>}
        <Typography variant="caption" sx={{ display: 'block' }} color="text.secondary">{t(!draft.enabled ? `${P}disabled_hint` : existing && !executionChanged(draft, seed) ? `${P}metadata_hint` : `${P}apply_hint`, { nodes: nodeCount === undefined ? t('admin:access_control.confirm.each_node') : t('admin:access_control.confirm.node_count', { count: nodeCount }), eta: status?.apply_eta_ms ? t('admin:access_control.confirm.eta_minutes', { minutes: Math.ceil(status.apply_eta_ms / 60000) }) : t('admin:access_control.confirm.eta_unknown') })}</Typography>
      </Box><Button disabled={busy} onClick={close}>{t('common:actions.cancel')}</Button>{!mobile && saveButton}
    </DialogActions>
  </Dialog>
}
