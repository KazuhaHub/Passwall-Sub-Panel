import { useId, useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { destErrorOf, type DestGroup, type DestGroupPut, type DestList, type DestState } from '@/api/accessControl'
import { AsyncButton } from '@/components/common/AsyncButton'
import { FieldHint } from '@/components/common/HelpTip'
import { KpiGrid, KpiTile } from '@/components/common/KpiTile'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/feedback/toasts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'
import { useDirtyClose } from '@/hooks/useDirtyClose'
import { cn } from '@/lib/utils'
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
  const scope = useQueryScope()
  const qc = useQueryClient()
  const uid = useId()
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
  // Named for what it does: the saved state is just saved, a different one is that state.
  const label = editing && state === group?.state ? t(`${E}save`) : t(state === 'on' ? `${E}turn_on` : editing ? `${E}back_to_report` : `${E}start_report`)
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
  const toggleList = (id: number, on: boolean) => {
    setListIds(prev => on ? (prev.includes(id) ? prev : [...prev, id]) : prev.filter(x => x !== id))
    setTouched(true)
  }
  // What can be ticked: the free destination lists, plus any chosen one the free set does not hold (kept so it can be unticked).
  const shownLists = [...options, ...chosen.filter(l => !options.some(o => o.id === l.id))]

  return (
    <>
      <Dialog open={open} onOpenChange={next => { if (!next) guard(onClose) }}>
        <DialogContent className="max-h-[calc(100dvh-2rem)] grid-rows-[auto_minmax(0,1fr)_auto] sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>{t(editing ? `${E}title_edit` : `${E}title`)}</DialogTitle>
            <p className="text-sm text-muted-foreground sm:hidden">{t(`${E}step_of`, { n: step + 1, total: STEPS.length })}{' · '}{stepLabel(STEPS[step])}</p>
            <ol className="hidden items-center gap-2 text-sm sm:flex" aria-label={t(`${E}title`)}>
              {STEPS.map((s, i) => (
                <li key={s} className="flex items-center gap-2">
                  {i > 0 && <span aria-hidden className="h-px w-6 bg-border" />}
                  <button type="button" disabled={i >= step} aria-current={i === step ? 'step' : undefined} onClick={() => setStep(i)}
                    className={cn('inline-flex items-center gap-1.5 rounded-md px-1.5 py-0.5 outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
                      i === step ? 'font-medium text-foreground' : 'text-muted-foreground', i < step && 'hover:text-foreground')}>
                    <span aria-hidden className={cn('flex size-5 items-center justify-center rounded-full text-xs tabular-nums', i === step ? 'bg-primary text-primary-foreground' : 'bg-muted')}>{i + 1}</span>
                    {stepLabel(s)}
                  </button>
                </li>
              ))}
            </ol>
          </DialogHeader>

          <div className="-mx-4 min-h-0 overflow-y-auto px-4">
            {step === 0 && (
              <div className="grid gap-2">
                <p className="text-sm text-muted-foreground">{t(`${E}step1_hint`)}</p>
                {candidates.length > SEARCH_OVER && (
                  <Input value={search} onChange={e => setSearch(e.target.value)} aria-label={t(`${E}search`)} placeholder={t(`${E}search`)} />
                )}
                {candidates.length === 0
                  ? <p>{t(`${E}none_left`)}</p>
                  : (
                    <div role="radiogroup" aria-label={t(`${E}step.group`)} className="grid">
                      {shownCandidates.map(g => (
                        <label key={g.group_id} className="flex min-h-11 cursor-pointer items-center gap-2 rounded-md px-1 hover:bg-muted">
                          <input type="radio" name="allowlist-group" className="size-4 accent-[var(--primary)]" value={String(g.group_id)}
                            checked={groupId === g.group_id} onChange={() => pick(g.group_id)} />
                          <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">{g.group_name}</span>
                          <span className="shrink-0 text-sm text-muted-foreground tabular-nums">{members(g)}</span>
                        </label>
                      ))}
                    </div>
                  )}
              </div>
            )}

            {step === 1 && group && (
              <div className="grid gap-5">
                <p className="font-semibold">{t(`${E}step2_title`)}</p>
                <div className="grid gap-1.5">
                  <p className="text-sm font-medium" id={`${uid}-lists`}>{t(`${E}pick_lists`)}</p>
                  <div role="group" aria-labelledby={`${uid}-lists`} className="grid max-h-56 gap-0.5 overflow-y-auto rounded-lg border p-1">
                    {lists.isPending && <Skeleton className="m-1 h-5 w-1/2" />}
                    {shownLists.map(l => (
                      <label key={l.id} className="flex min-h-9 cursor-pointer items-center gap-2 rounded-md px-1.5 hover:bg-muted">
                        <Checkbox checked={listIds.includes(l.id)} onCheckedChange={c => toggleList(l.id, c)} aria-label={l.name} />
                        <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">{t(`${E}list_chip`, { name: l.name, entries: num.format(l.entry_count) })}</span>
                      </label>
                    ))}
                  </div>
                  {preview.isError && <p role="alert" className="text-sm text-destructive">{destErrorText(t, preview.error)}</p>}
                  {pending.length > 0 && <FieldHint summary={t(`${E}not_ready_summary`)} detail={t(`${E}not_ready_detail`)} />}
                </div>

                <div className="grid gap-1.5">
                  <Label htmlFor={`${uid}-base`} className="font-semibold">{t(`${E}base`)}</Label>
                  {hasRow
                    ? <p className="text-sm">{t(`${E}base_existing`, { count: group.base_count })}</p>
                    : (
                      <>
                        {!editBase && !noTemplateHosts && (
                          <div className="flex flex-wrap items-center gap-2">
                            {pv || preview.isError ? <p className="text-sm [overflow-wrap:anywhere]">{t(`${E}base_prefilled`, { count: hosts.length, hosts: hosts.join(t(`${E}host_separator`)) })}</p> : <Skeleton className="h-4 w-56" />}
                            <Button size="sm" variant="outline" onClick={() => setEditBase(true)}>{t(`${E}base_edit`)}</Button>
                          </div>
                        )}
                        {noTemplateHosts && <FieldHint summary={t(`${E}base_none_summary`)} detail={t(`${E}base_none_detail`)} />}
                        {(editBase || noTemplateHosts) && (
                          <Textarea id={`${uid}-base`} rows={4} aria-label={t(`${E}base`)} value={baseText ?? hosts.join('\n')}
                            placeholder={t(`${E}base_placeholder`)} onChange={e => { setBaseText(e.target.value); setTouched(true) }} />
                        )}
                        <p className="text-sm text-muted-foreground">{t(`${E}base_hint`)}</p>
                      </>
                    )}
                </div>
                {!hasRow && (
                  <div className="grid gap-0.5">
                    <p className="font-semibold">{t(`${E}extra`)}</p>
                    <p className="text-sm text-muted-foreground">{t(`${E}extra_hint`)}</p>
                  </div>
                )}
              </div>
            )}

            {step === 2 && group && (
              <div className="grid gap-3">
                {preview.isError && (
                  <Alert variant="destructive">
                    <AlertDescription className="flex flex-wrap items-center gap-2">
                      <span className="flex-1">{destErrorText(t, preview.error)}</span>
                      <Button size="sm" variant="outline" onClick={() => setStep(1)}>{t(`${E}back_to_lists`)}</Button>
                    </AlertDescription>
                  </Alert>
                )}
                {pv
                  ? (
                    <KpiGrid className="my-0">
                      <KpiTile label={t(`${E}kpi_members`)} value={num.format(pv.members)} />
                      <KpiTile label={t(`${E}kpi_usable`)} value={pv.eligible_nodes} caption={t(`${E}kpi_of`, { total: pv.total_nodes })} />
                      <KpiTile label={t(`${E}kpi_lost`)} value={pv.nodes_lost.length} />
                    </KpiGrid>
                  )
                  : !preview.isError && <Skeleton data-testid="enable-kpi-skeleton" className="h-16 w-full" />}
                {pv && pv.eligible_nodes === 0 && (
                  <div data-testid="enable-no-nodes" className="rounded-lg bg-destructive/10 p-3 text-sm">{t(`${E}no_nodes`)}</div>
                )}
                <div role="radiogroup" aria-label={t(`${E}step.impact`)} className="grid gap-1">
                  {(['report', 'on'] as const).map(v => (
                    <label key={v} className="flex min-h-9 cursor-pointer items-start gap-2 py-1">
                      <input type="radio" name="allowlist-state" className="mt-0.5 size-4 accent-[var(--primary)]" value={v} checked={state === v}
                        onChange={() => { setState(v); setTouched(true) }} />
                      <span className="text-sm">{t(`${E}state_${v}`)}</span>
                    </label>
                  ))}
                </div>
                {pv && (
                  <div className="grid gap-1 text-sm">
                    {pv.nodes_lost.length > 0
                      ? (
                        <>
                          <p>{t(`${E}lost_intro`, { count: pv.nodes_lost.length })}</p>
                          <ul className="m-0 grid list-disc gap-0.5 ps-5 sm:grid-cols-2">
                            {pv.nodes_lost.map(n => <li key={n.panel_id} className="[overflow-wrap:anywhere]">{t(`${E}lost_item`, { name: n.name, reason: reason(n.reason) })}</li>)}
                          </ul>
                        </>
                      )
                      : <p>{t(`${E}lost_none`)}</p>}
                    {pv.non_recording_nodes > 0 && <p className="text-muted-foreground">{t(`${E}non_recording`, { count: pv.non_recording_nodes })}</p>}
                    {!hasRow && hosts.length === 0 && <FieldHint summary={t(`${E}base_empty_summary`)} detail={t(`${E}base_empty_detail`)} />}
                  </div>
                )}
                <p className="text-sm text-muted-foreground">{t(`${E}report_note`)}</p>
                {error && <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert>}
              </div>
            )}
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => guard(onClose)}>{t(`${E}cancel`)}</Button>
            <span className="hidden flex-1 sm:block" />
            {step > (initial ? 1 : 0) && <Button variant="outline" onClick={() => setStep(step - 1)}>{t(`${E}back`)}</Button>}
            {step < 2
              ? <Button disabled={!canNext} onClick={() => setStep(step + 1)}>{t(`${E}next`)}</Button>
              : <AsyncButton variant={state === 'on' ? 'destructive' : 'default'} disabled={!pv || preview.isFetching || preview.isError} onClick={submit}>{label}</AsyncButton>}
          </DialogFooter>
        </DialogContent>
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
