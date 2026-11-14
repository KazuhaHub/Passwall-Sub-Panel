import { useMemo, useState } from 'react'
import {
  Alert, Autocomplete, Box, Button, Chip, Dialog, DialogActions, DialogContent, DialogTitle, FormControlLabel, InputAdornment, Radio, RadioGroup,
  Skeleton, Step, StepButton, Stepper, TextField, Typography, useMediaQuery, useTheme,
} from '@mui/material'
import SearchIcon from '@mui/icons-material/Search'
import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { destErrorOf, type DestGroup, type DestGroupPut, type DestList, type DestState } from '@/api/accessControl'
import { AsyncButton } from '@/components/AsyncButton'
import FieldHint from '@/components/FieldHint'
import KpiTile, { KpiGrid } from '@/components/KpiTile'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { useDestGroupPreview, useDestLists, useDestStatus, usePutDestGroup } from '@/query/accessControl'
import { accessControlKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { applyMinutes } from '@/utils/accessControl'
import { allowlistNoNodesConfirm, allowlistRemoveListsConfirm, discardChangesConfirm, minutesWords } from '../confirmCopy'
import { destErrorText } from '../destErrors'
import { useFirstPublishGate } from '../policies/useFirstPublish'
import TurnOnDialog from './TurnOnDialog'
import { groupHasAllowlist, stateDay } from './allowlistState'

const E = 'admin:access_control.allowlist.enable.'
const STEPS = ['group', 'lists', 'impact'] as const
/** More groups than this and the first step gets a search box. */
const SEARCH_OVER = 8

export interface EnableSaved { groupId: number; state: DestState; /** The group had no allowlist before. */ created: boolean }

export interface EnableAllowlistDialogProps {
  open: boolean
  /** Every group, as the allowlist tab read them. */
  groups: DestGroup[]
  /** A group to start on (step 2): the tab's "edit the lists", "turn on again" and the groups page's link. */
  initialGroupId: number | null
  onClose: () => void
  onSaved: (saved: EnableSaved) => void
  /** The checklist's "see the report" for a group that has run report-only. */
  onOpenGroupReport: (groupId: number) => void
  onViewNodes: () => void
}

const lines = (text: string) => text.split('\n').map(l => l.trim()).filter(Boolean)

/**
 * ENABLING AN ALLOWLIST (S9), in three steps that each do one thing: choose the
 * group, choose its lists, see what it does. The state is chosen on the last
 * step (report-only is recommended and is the default; on is one click away and
 * asks through the checklist). The whole dialog sends ONE write, which on a
 * group's first allowlist also creates its two lists on the server, so a failure
 * leaves nothing behind. Editing a group that already has an allowlist is the
 * same dialog from step 2, with the updated_at it was read at.
 */
export default function EnableAllowlistDialog({ open, groups, initialGroupId, onClose, onSaved, onOpenGroupReport, onViewNodes }: EnableAllowlistDialogProps) {
  const { t, i18n } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const phone = useMediaQuery(theme.breakpoints.down('sm'))
  const scope = useQueryScope()
  const qc = useQueryClient()
  const num = new Intl.NumberFormat(i18n.language)
  const initial = groups.find(g => g.group_id === initialGroupId) ?? null

  const [step, setStep] = useState(initial ? 1 : 0)
  const [groupId, setGroupId] = useState<number | null>(initial?.group_id ?? null)
  const [listIds, setListIds] = useState<number[]>(initial?.list_ids ?? [])
  const [baseText, setBaseText] = useState<string | null>(null)
  const [editBase, setEditBase] = useState(false)
  const [state, setState] = useState<DestState>(initial && initial.state !== 'off' ? initial.state : 'report')
  const [search, setSearch] = useState('')
  const [touched, setTouched] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [turnOn, setTurnOn] = useState(false)
  const guard = useDirtyClose(touched, discardChangesConfirm(t))

  const lists = useDestLists(scope, { enabled: open })
  const status = useDestStatus(scope, { enabled: open })
  const put = usePutDestGroup(scope)
  const gate = useFirstPublishGate()
  const group = groups.find(g => g.group_id === groupId) ?? null
  const hasRow = group !== null && groupHasAllowlist(group)
  const editing = group !== null && group.state !== 'off'
  const preview = useDestGroupPreview(scope, groupId, state, listIds, { enabled: open && groupId !== null && step >= 1 })
  const pv = preview.data

  const candidates = groups.filter(g => g.state === 'off')
  const shownCandidates = candidates.filter(g => !search.trim() || g.group_name.toLowerCase().includes(search.trim().toLowerCase()))
  const options = useMemo(() => (lists.data?.items ?? []).filter(l => l.side === 'dest' && l.owner_group_id === 0 && !(l.kind === 'custom' && (l.empty || l.entry_count === 0))), [lists.data])
  const chosen: DestList[] = listIds.map(id => (lists.data?.items ?? []).find(l => l.id === id)).filter((l): l is DestList => !!l)
  const pending = chosen.filter(l => l.state === 'pending')
  const hosts = baseText !== null ? lines(baseText) : pv?.base_hosts ?? []
  const noTemplateHosts = !hasRow && pv?.base_hosts_source === 'none' && hosts.length === 0

  const pick = (id: number) => {
    const g = groups.find(x => x.group_id === id)
    setGroupId(id)
    setListIds(g?.list_ids ?? [])
    setBaseText(null)
    setTouched(true)
  }
  const canNext = step === 0 ? groupId !== null : step === 1 ? listIds.length > 0 : false
  const label = editing ? t(`${E}save`) : t(state === 'on' ? `${E}turn_on` : `${E}start_report`)
  const needsChecklist = state === 'on' && group?.state !== 'on'

  const body = (): DestGroupPut => (hasRow && group?.updated_at != null
    ? { state, list_ids: listIds, updated_at: group.updated_at }
    : { state, list_ids: listIds, base_hosts: hosts })
  /** The write. A refusal is thrown to whoever asked. */
  const write = async () => {
    if (!group) return
    await put.mutateAsync({ id: group.group_id, input: body() })
    const minutes = minutesWords(t, applyMinutes(status.data))
    pushSnack(t(`${E}saved_${editing ? 'edit' : state}`, { group: group.group_name, minutes }), 'success')
    onSaved({ groupId: group.group_id, state, created: !hasRow })
    onClose()
  }

  const submit = async () => {
    if (!group) return
    setError(null)
    // The one question that asks even when the rest is fine: nobody could reach anything.
    if (pv && pv.eligible_nodes === 0 && !(await confirm(allowlistNoNodesConfirm(t, group.group_name, pv.members, pv.total_nodes, state === 'on' ? 'on' : 'report')))) return
    // Lists taken out of an allowlist that is already on make what they covered refused at once.
    const removed = group.state === 'on' && state === 'on' ? group.lists.filter(l => !listIds.includes(l.id)).map(l => l.name) : []
    if (removed.length > 0 && !(await confirm(allowlistRemoveListsConfirm(t, group.group_name, removed)))) return
    if (!(await gate(true))) return
    if (needsChecklist) { setTurnOn(true); return }
    try {
      await write()
    } catch (err) {
      if (destErrorOf(err)?.code === 'dest_group_stale') {
        pushSnack(t(`${E}stale`), 'warning')
        void qc.invalidateQueries({ queryKey: accessControlKeys.groupsAll(scope) })
        onClose()
      } else setError(destErrorText(t, err))
    }
  }

  const stepLabel = (s: (typeof STEPS)[number]) => t(`${E}step.${s}`)
  const members = (g: DestGroup) => t(`${E}people`, { count: g.members })
  const reason = (r: string) => t(`admin:access_control.allowlist.reason.${r}`)

  return (
    <>
      <Dialog open={open} onClose={() => guard(onClose)} fullWidth maxWidth="md" fullScreen={phone}
        slotProps={{ paper: { sx: { bgcolor: md.surfaceContainerHigh, borderRadius: phone ? 0 : 3 } } }}>
        <DialogTitle>{t(editing ? `${E}title_edit` : `${E}title`)}</DialogTitle>
        <DialogContent>
          {phone
            ? <Typography variant="body2" sx={{ color: md.onSurfaceVariant, mb: 1.5 }}>{t(`${E}step_of`, { n: step + 1, total: STEPS.length })}{' · '}{stepLabel(STEPS[step])}</Typography>
            : (
              <Stepper activeStep={step} nonLinear sx={{ mb: 2.5 }}>
                {STEPS.map((s, i) => (
                  <Step key={s} completed={i < step}>
                    <StepButton disabled={i >= step} onClick={() => setStep(i)}>{stepLabel(s)}</StepButton>
                  </Step>
                ))}
              </Stepper>
            )}

          {step === 0 && (
            <Box sx={{ display: 'grid', gap: 1 }}>
              <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${E}step1_hint`)}</Typography>
              {candidates.length > SEARCH_OVER && (
                <TextField size="small" value={search} onChange={e => setSearch(e.target.value)} label={t(`${E}search`)}
                  slotProps={{ input: { startAdornment: <InputAdornment position="start"><SearchIcon fontSize="small" /></InputAdornment> } }} />
              )}
              {candidates.length === 0
                ? <Typography>{t(`${E}none_left`)}</Typography>
                : (
                  <RadioGroup value={groupId === null ? '' : String(groupId)} onChange={(_, v) => pick(Number(v))}>
                    {shownCandidates.map(g => (
                      <Box key={g.group_id} sx={{ display: 'flex', alignItems: 'center', gap: 1, minHeight: 44 }}>
                        <FormControlLabel sx={{ flex: 1, minWidth: 0, m: 0 }} value={String(g.group_id)} control={<Radio />}
                          label={<Typography sx={{ overflowWrap: 'anywhere' }}>{g.group_name}</Typography>} />
                        <Typography variant="body2" sx={{ color: md.onSurfaceVariant, flex: '0 0 auto' }}>{members(g)}</Typography>
                      </Box>
                    ))}
                  </RadioGroup>
                )}
            </Box>
          )}

          {step === 1 && group && (
            <Box sx={{ display: 'grid', gap: 2 }}>
              <Typography sx={{ fontWeight: 600 }}>{t(`${E}step2_title`)}</Typography>
              <Box sx={{ display: 'grid', gap: 0.5 }}>
                <Autocomplete multiple options={options} value={chosen} getOptionLabel={l => l.name} isOptionEqualToValue={(a, b) => a.id === b.id}
                  loading={lists.isPending} filterSelectedOptions disableCloseOnSelect={false}
                  onChange={(_, v) => { setListIds(v.map(l => l.id)); setTouched(true) }}
                  renderValue={(v, getItemProps) => v.map((l, i) => {
                    const { key, ...rest } = getItemProps({ index: i })
                    return <Chip key={key} {...rest} size="small" label={t(`${E}list_chip`, { name: l.name, entries: num.format(l.entry_count) })} />
                  })}
                  renderInput={params => <TextField {...params} label={t(`${E}pick_lists`)} />} />
                {preview.isError && <Typography variant="body2" role="alert" sx={{ color: md.error }}>{destErrorText(t, preview.error)}</Typography>}
                {pending.length > 0 && <FieldHint tone="amber" summary={t(`${E}not_ready_summary`)} detail={t(`${E}not_ready_detail`)} />}
              </Box>

              <Box sx={{ display: 'grid', gap: 0.5 }}>
                <Typography sx={{ fontWeight: 600 }}>{t(`${E}base`)}</Typography>
                {hasRow
                  ? <Typography variant="body2">{t(`${E}base_existing`, { count: group.base_count })}</Typography>
                  : (
                    <>
                      {!editBase && !noTemplateHosts && (
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
                          {pv || preview.isError ? <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>{t(`${E}base_prefilled`, { count: hosts.length, hosts: hosts.join(t(`${E}host_separator`)) })}</Typography> : <Skeleton width={220} />}
                          <Button size="small" onClick={() => setEditBase(true)}>{t(`${E}base_edit`)}</Button>
                        </Box>
                      )}
                      {noTemplateHosts && <FieldHint tone="amber" summary={t(`${E}base_none_summary`)} detail={t(`${E}base_none_detail`)} />}
                      {(editBase || noTemplateHosts) && (
                        <TextField multiline minRows={4} maxRows={6} fullWidth size="small" label={t(`${E}base`)} value={baseText ?? hosts.join('\n')}
                          placeholder={t(`${E}base_placeholder`)} onChange={e => { setBaseText(e.target.value); setTouched(true) }} />
                      )}
                      <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${E}base_hint`)}</Typography>
                    </>
                  )}
              </Box>
              {!hasRow && (
                <Box sx={{ display: 'grid', gap: 0.25 }}>
                  <Typography sx={{ fontWeight: 600 }}>{t(`${E}extra`)}</Typography>
                  <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${E}extra_hint`)}</Typography>
                </Box>
              )}
            </Box>
          )}

          {step === 2 && group && (
            <Box sx={{ display: 'grid', gap: 1.5 }}>
              {preview.isError && (
                <Alert severity="error" action={<Button color="inherit" size="small" onClick={() => setStep(1)}>{t(`${E}back_to_lists`)}</Button>}>
                  {destErrorText(t, preview.error)}
                </Alert>
              )}
              {pv
                ? (
                  <KpiGrid>
                    <KpiTile label={t(`${E}kpi_members`)} value={num.format(pv.members)} />
                    <KpiTile label={t(`${E}kpi_usable`)} value={pv.eligible_nodes} caption={t(`${E}kpi_of`, { total: pv.total_nodes })} />
                    <KpiTile label={t(`${E}kpi_lost`)} value={pv.nodes_lost.length} />
                  </KpiGrid>
                )
                : <Skeleton variant="rounded" height={64} />}
              {pv && pv.eligible_nodes === 0 && (
                <Box data-testid="enable-no-nodes" sx={{ p: 1.5, borderRadius: 2, bgcolor: md.errorContainer, color: md.onErrorContainer }}>
                  <Typography variant="body2">{t(`${E}no_nodes`)}</Typography>
                </Box>
              )}
              <RadioGroup value={state} onChange={(_, v) => { setState(v as DestState); setTouched(true) }}>
                <FormControlLabel value="report" control={<Radio />} label={t(`${E}state_report`)} />
                <FormControlLabel value="on" control={<Radio />} label={t(`${E}state_on`)} />
              </RadioGroup>
              {pv && (
                <Box sx={{ display: 'grid', gap: 0.5 }}>
                  {pv.nodes_lost.length > 0
                    ? (
                      <>
                        <Typography variant="body2">{t(`${E}lost_intro`, { count: pv.nodes_lost.length })}</Typography>
                        <Box component="ul" sx={{ m: 0, pl: 2.5, display: 'grid', gap: 0.25, gridTemplateColumns: { xs: '1fr', sm: 'repeat(2, minmax(0, 1fr))' } }}>
                          {pv.nodes_lost.map(n => <li key={n.panel_id}><Typography variant="body2" component="span" sx={{ overflowWrap: 'anywhere' }}>{t(`${E}lost_item`, { name: n.name, reason: reason(n.reason) })}</Typography></li>)}
                        </Box>
                      </>
                    )
                    : <Typography variant="body2">{t(`${E}lost_none`)}</Typography>}
                  {pv.non_recording_nodes > 0 && <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${E}non_recording`, { count: pv.non_recording_nodes })}</Typography>}
                  {!hasRow && hosts.length === 0 && <FieldHint tone="amber" summary={t(`${E}base_empty_summary`)} detail={t(`${E}base_empty_detail`)} />}
                </Box>
              )}
              <Typography variant="body2" sx={{ color: md.onSurfaceVariant }}>{t(`${E}report_note`)}</Typography>
              {error && <Alert severity="error">{error}</Alert>}
            </Box>
          )}
        </DialogContent>
        <DialogActions sx={{ ...(phone ? { position: 'sticky', bottom: 0, bgcolor: md.surfaceContainerHigh } : {}) }}>
          <Button onClick={() => guard(onClose)}>{t(`${E}cancel`)}</Button>
          <Box sx={{ flex: 1 }} />
          {step > (initial ? 1 : 0) && <Button onClick={() => setStep(step - 1)}>{t(`${E}back`)}</Button>}
          {step < 2
            ? <Button variant="contained" disabled={!canNext} onClick={() => setStep(step + 1)}>{t(`${E}next`)}</Button>
            : <AsyncButton variant="contained" color={state === 'on' ? 'error' : 'primary'} disabled={!pv || preview.isFetching || preview.isError} onClick={submit}>{label}</AsyncButton>}
        </DialogActions>
      </Dialog>
      {group && turnOn && (
        <TurnOnDialog open group={{ id: group.group_id, name: group.group_name, members: group.members }} listIds={listIds}
          savedListIds={group.list_ids} hasRow={hasRow} baseCount={hasRow ? undefined : hosts.length}
          reportDay={group.state === 'report' ? stateDay(group.state_changed_at, Date.now()) : null}
          onConfirm={write} onClose={() => setTurnOn(false)}
          onViewReport={() => { setTurnOn(false); onClose(); onOpenGroupReport(group.group_id) }}
          onViewNodes={() => { setTurnOn(false); onClose(); onViewNodes() }} />
      )}
    </>
  )
}
