import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router'
import { useTranslation } from 'react-i18next'

import {
  destErrorOf, previewDestPolicy, type DestAction, type DestBudget, type DestList, type DestPolicy, type DestState,
} from '@/api/accessControl'
import { AccessBadge } from '@/components/access/AccessBadge'
import { FieldHint } from '@/components/common/HelpTip'
import { ObjectPage, type ObjectBack } from '@/components/common/ObjectPage'
import { useObjectLeaveGuard } from '@/components/common/ObjectTabs'
import { confirm } from '@/components/ConfirmHost'
import { pushToast } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { useCreateDestPolicy, useDestLists, useDestPolicies, useDestStatus, useUpdateDestPolicy } from '@/query/accessControl'
import { useAllGroups } from '@/query/groups'
import { useServersList } from '@/query/servers'
import { useQueryScope } from '@/query/useQueryScope'
import { anyBudgetOver, applyMinutes, enforcingNodeCount, pipelineSteps, policyStateOptions } from '@/utils/accessControl'
import { convertToBlockConfirm, discardChangesConfirm } from '../confirmCopy'
import { destErrorText } from '../destErrors'
import { PageForm } from '../PageForm'
import { matchSummary, summaryText } from './matchSummary'
import { MatchFields, ScopeFields } from './PolicyEditorFields'
import {
  changesWire, DESCRIPTION_MAX_BYTES, emptyForm, formOf, inputOf, lineNumbersOf, NAME_MAX_BYTES, PREVIEW_DEBOUNCE_MS, RENDERED_FIELDS,
  utf8Bytes, type PolicyForm,
} from './policyForm'
import QuotaMeters from './QuotaMeters'
import { useFirstPublishGate } from './useFirstPublish'

const D = 'admin:access_control.policies.editor.'

/**
 * THE POLICY PAGE: create or edit one block or allow policy. It is a page (`…/policies/new`, `…/policies/:id`)
 * because the match and the scope need room: the lists to pick, the accounts or servers it reaches, and beside
 * them, in a column that stays in view, where the policy lands in the match order, what it matches, how many nodes
 * restart and what the quotas will be after. Leaving with unsaved changes asks (useObjectLeaveGuard). Save is
 * always "Save": the apply is debounced, so a "save and apply" would promise an immediacy it does not have.
 *
 * The state is the page's own, built once per object (the route keys it by id): a refetch of the policy list
 * while the form is open never rewrites what is being typed. After an edit is saved the page stays and
 * continues from what the server returned; after a create it moves to the new policy's page.
 */
export default function PolicyEditor({ policy: initialPolicy, initial, back, onCreated }: {
  /** The policy being edited, or null to create. */
  policy: DestPolicy | null
  /** A form to start from when creating (a new block policy, a duplicate). */
  initial?: PolicyForm
  back: ObjectBack
  /** A create went through: the route moves to the new policy's page. */
  onCreated: (saved: DestPolicy) => void
}) {
  const { t } = useTranslation(['admin'])
  const navigate = useNavigate()
  const scope = useQueryScope()
  const lists = useDestLists(scope)
  const policies = useDestPolicies(scope)
  const status = useDestStatus(scope)
  const groups = useAllGroups(scope)
  const servers = useServersList(scope, { page: 1, page_size: 200 })
  const create = useCreateDestPolicy(scope)
  const update = useUpdateDestPolicy(scope)
  const gate = useFirstPublishGate()

  // The policy as the server last said it: what was read, then what a save or a reload returned.
  const [policy, setPolicy] = useState<DestPolicy | null>(initialPolicy)
  const [seed] = useState<PolicyForm>(() => (initialPolicy ? formOf(initialPolicy) : initial ?? emptyForm()))
  const [form, setForm] = useState<PolicyForm>(seed)
  const [base, setBase] = useState<PolicyForm>(seed)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [topError, setTopError] = useState<string | null>(null)
  const [stale, setStale] = useState(false)
  const [preview, setPreview] = useState<{ budget: DestBudget | null; loading: boolean; failed: boolean }>({ budget: null, loading: false, failed: false })
  const [retry, setRetry] = useState(0)
  const [touched, setTouched] = useState(false)
  // Group and panel ids the seeded policy named that no longer exist: removed from the form, counted here.
  const [removed, setRemoved] = useState(0)
  // A save is running (the confirmations before the write included): Save and Enter must not start another.
  const [saving, setSaving] = useState(false)
  const savingRef = useRef(false)
  // The create or a delete-by-others just moved the page away: the draft is moot and leaving must not ask.
  const finished = useRef(false)

  // An edit of any field drops the messages about fields: they were about what was there before, and the next Save judges again.
  const set = <K extends keyof PolicyForm>(k: K, v: PolicyForm[K]) => { setForm(f => ({ ...f, [k]: v })); setErrors({}) }
  const dirty = JSON.stringify(form) !== JSON.stringify(base)
  useObjectLeaveGuard({ dirty, copy: { ...discardChangesConfirm(t), initialFocus: 'cancel' }, bypass: finished })

  // Once the groups and servers are known, drop the ids of ones that were deleted.
  const pruned = useRef(false)
  useEffect(() => {
    // A partial server list (more servers than one page) cannot tell a deleted panel from one on
    // another page: skip the panel pruning then rather than drop live ids.
    if (!initialPolicy || pruned.current || !groups.data || !servers.data) return
    pruned.current = true
    const gone = (ids: number[], known: Set<number>) => ids.filter(id => !known.has(id))
    const knownGroups = new Set(groups.data.map(g => g.id))
    const knownPanels = new Set(servers.data.items.map(x => x.id))
    const deadG = gone(seed.groupIds, knownGroups)
    const complete = !(servers.data.total > servers.data.items.length)
    const deadP = complete ? gone(seed.panelIds, knownPanels) : []
    if (deadG.length + deadP.length === 0) return
    setForm(f => ({ ...f, groupIds: f.groupIds.filter(id => knownGroups.has(id)), panelIds: complete ? f.panelIds.filter(id => knownPanels.has(id)) : f.panelIds }))
    setRemoved(deadG.length + deadP.length)
  }, [initialPolicy, seed, groups.data, servers.data])

  const allLists: DestList[] = (lists.data?.items ?? []).filter(l => l.owner_group_id === 0 && l.side === 'dest')
  const listById = new Map(allLists.map(l => [l.id, l]))
  const notReady = form.listIds.map(id => listById.get(id)).filter((l): l is DestList => !!l && (l.state === 'pending' || (l.state === 'failed' && l.last_fetched_at === null)))
  const input = inputOf(form, { id: policy?.id })

  // A policy made from a catalog template keeps its matching: the mapping is the catalog's, not the admin's.
  const locked = !!form.templateKey
  const descTooLong = utf8Bytes(form.description) > DESCRIPTION_MAX_BYTES
  const nameTooLong = utf8Bytes(form.name) > NAME_MAX_BYTES
  const noScopePick = (form.scope === 'groups' && form.groupIds.length === 0) || (form.scope === 'panels' && form.panelIds.length === 0)

  const noMatch = form.listIds.length === 0 && !form.cidrs.trim() && !form.private && !form.bittorrent && !form.ports.trim() && !form.network
  const cidrLines = lineNumbersOf(form.cidrs)

  // The quota preview, after typing pauses. Failure is shown as "—" and never blocks saving: the server judges again.
  const key = JSON.stringify(input)
  useEffect(() => {
    if (!form.name.trim() || noMatch) return
    const ctl = new AbortController()
    const timer = setTimeout(() => {
      setPreview(p => ({ ...p, loading: true, failed: false }))
      previewDestPolicy(inputOf(form, { id: policy?.id }), { signal: ctl.signal })
        .then(r => setPreview({ budget: r.budget, loading: false, failed: false }))
        .catch(() => { if (!ctl.signal.aborted) setPreview(p => ({ ...p, loading: false, failed: true })) })
    }, PREVIEW_DEBOUNCE_MS)
    return () => { clearTimeout(timer); ctl.abort() }
    // `key` is the serialised input: the effect runs when what is previewed changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, retry])

  const over = anyBudgetOver(preview.budget ?? undefined)
  const busy = create.isPending || update.isPending || saving

  // A new block policy is recommended Report only; an edit of an existing one is not a "new policy".
  const recommend = !policy && form.action === 'block'

  // Where it lands: the step and its place among that step's policies. Report-only is its own step, after
  // every enforced one, so the same block action can land in step 3 or step 4 by its state.
  const stepOf = (p: { action: DestAction; state: DestState }) => (p.action === 'block' && p.state === 'report' ? 'report' : p.action)
  const position = useMemo(() => {
    const all = [...(policies.data?.allow ?? []), ...(policies.data?.block ?? [])]
    const here = stepOf(form)
    const same = all.filter(p => stepOf(p) === here)
    const idx = policy && stepOf(policy) === here ? same.findIndex(p => p.id === policy.id) : same.length
    return { index: idx >= 0 ? idx + 1 : same.length + 1, step: pipelineSteps({ report: true, allowlist: true }).find(s => s.id === here)?.n ?? 1, report: here === 'report' }
  }, [policies.data, policy, form]) // eslint-disable-line react-hooks/exhaustive-deps

  const summary = summaryText(t, matchSummary({
    listNames: form.listIds.map(id => listById.get(id)?.name ?? ''), cidrs: cidrLines.length, private: form.private,
    bittorrent: form.bittorrent, ports: form.ports, network: form.network,
  }))
  const split = matchSummary({ listNames: form.listIds.map(String), cidrs: cidrLines.length, private: form.private, bittorrent: form.bittorrent, ports: form.ports, network: form.network }).split

  const executors = enforcingNodeCount(status.data)
  const minutes = applyMinutes(status.data)
  let footer: string
  if (form.state === 'off' && base.state === 'off') footer = t(`${D}footer.disabled`)
  else if (policy && !changesWire(form, base)) footer = t(`${D}footer.rename_only`)
  else if (executors === null || minutes === null) footer = t(`${D}footer.applies_unknown`)
  else footer = t(`${D}footer.applies`, { count: executors, minutes })

  const validate = (): boolean => {
    const next: Record<string, string> = {}
    if (!form.name.trim()) next.name = t(`${D}errors.name_required`)
    if (noMatch) next.match = t(`${D}errors.no_match`)
    if (form.scope === 'groups' && form.groupIds.length === 0) next.scope = t(`${D}errors.scope_groups`)
    if (form.scope === 'panels' && form.panelIds.length === 0) next.scope = t(`${D}errors.scope_panels`)
    setErrors(next)
    return Object.keys(next).length === 0
  }

  const save = async () => {
    if (savingRef.current) return
    savingRef.current = true
    setSaving(true)
    try {
      await saveOnce()
    } finally {
      savingRef.current = false
      setSaving(false)
    }
  }
  const saveOnce = async () => {
    setTouched(true); setTopError(null)
    if (!validate() || over) return
    // Report-only to On is the Convert action by another door: ask the same question.
    if (policy && policy.state === 'report' && form.state === 'on' && form.action === 'block' && !(await confirm(convertToBlockConfirm({ t, minutes }, form.name.trim())))) return
    // Switching an existing, paused policy on also says who it will reach; a new one or an edit of a running one does not.
    const enabling = !!policy && policy.state === 'off' && form.state !== 'off'
    if (!(await gate(form.state !== 'off' && (!policy || policy.state === 'off'), enabling
      ? { name: form.name.trim(), action: form.action, state: form.state, scope: form.scope, groupIds: form.groupIds, panelIds: form.panelIds }
      : undefined))) return
    try {
      const saved = policy
        ? await update.mutateAsync({ id: policy.id, input: inputOf(form, { updatedAt: policy.updated_at }) })
        : await create.mutateAsync(inputOf(form))
      pushToast({ message: t(`${D}saved`, { name: saved.name }), severity: 'success' })
      setBase(form)
      if (policy) { setPolicy(saved); return }
      finished.current = true
      onCreated(saved)
    } catch (err) {
      const e = destErrorOf(err)
      if (e?.code === 'dest_policy_stale') { setStale(true); return }
      if (e?.code === 'dest_name_taken') { setErrors({ name: t('admin:access_control.errors.dest_name_taken') }); return }
      const field = e?.field?.replace(/^inline\./, '')
      if (e?.status === 400 && field && RENDERED_FIELDS.has(field)) {
        const bad = Array.isArray(e.body.bad) ? ` (${(e.body.bad as string[]).join(', ')})` : ''
        setErrors({ [field]: destErrorText(t, err) + bad }); return
      }
      setTopError(destErrorText(t, err))
    }
  }

  const reload = async () => {
    const fresh = await policies.refetch()
    const p = [...(fresh.data?.allow ?? []), ...(fresh.data?.block ?? [])].find(x => x.id === policy?.id)
    if (!p) { finished.current = true; void navigate(back.to, { replace: true }); return }
    const f = formOf(p)
    setForm(f); setBase(f); setPolicy(p); setStale(false); setErrors({})
  }

  const discard = () => {
    // An edit goes back to what was loaded; a create has nothing to go back to, so it leaves (asking if it was filled in).
    if (policy) { setForm(base); setErrors({}); setTopError(null); return }
    void navigate(back.to)
  }

  const groupOptions = (groups.data ?? []).map(g => ({ id: g.id, name: g.name }))
  const panelOptions = (servers.data?.items ?? []).filter(s => s.panel_type === 'psp').map(s => ({ id: s.id, name: s.name }))

  // Why Save is off when no field says so itself (the fields that are wrong name themselves).
  const blockReason = over ? t('admin:access_control.policies.quota.over_note')
    : noScopePick ? t(`${D}errors.${form.scope === 'groups' ? 'scope_groups' : 'scope_panels'}`) : undefined

  const savedState = policy?.state
  const badges = policy
    ? (
      <span className="flex flex-wrap items-center gap-1.5">
        {savedState === 'report'
          ? <AccessBadge kind="would_deny" label={t('admin:access_control.policies.state.report')} />
          : <AccessBadge kind={policy.action === 'block' ? 'block' : 'allow'} label={t(`admin:access_control.policies.action.${policy.action}`)} />}
        {savedState === 'off' && <AccessBadge kind="paused" label={t('admin:access_control.policies.row.paused')} />}
      </span>
    )
    : undefined

  return (
    <ObjectPage back={back} title={policy ? policy.name : t(`${D}title_new`)} badges={badges}>
      <FormValidityProvider>
        <PageForm busy={busy} onSubmit={() => void save()}>
          <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_21rem] lg:items-start">
            <div className="grid min-w-0 gap-5">
              {stale && (
                <div role="alert" className="flex flex-wrap items-center gap-2 rounded-lg border border-warning/40 bg-warning/10 p-2 text-sm">
                  <span className="min-w-0 flex-1">{t(`${D}stale`)}</span>
                  <Button size="sm" variant="outline" onClick={() => void reload()}>{t(`${D}reload`)}</Button>
                </div>
              )}
              {topError && <div role="alert" className="rounded-lg border border-destructive/30 bg-destructive/10 p-2 text-sm">{topError}</div>}
              <fieldset disabled={busy} className="m-0 grid min-w-0 gap-5 border-0 p-0">
                <Field label={t(`${D}name`)} required error={nameTooLong ? t(`${D}errors.name_too_long`, { max: NAME_MAX_BYTES }) : errors.name}>
                  <Input value={form.name} onChange={e => set('name', e.target.value)} />
                </Field>
                <Field label={t(`${D}description`)} error={descTooLong ? t(`${D}errors.description_too_long`, { max: DESCRIPTION_MAX_BYTES }) : errors.description}>
                  <Textarea rows={2} value={form.description} onChange={e => set('description', e.target.value)} />
                </Field>

                <Field label={t(`${D}action`)} hint={t(`${D}action_help.${form.action}`)}>
                  {p => (
                    <div className="grid gap-1">
                      <SegmentedControl aria-labelledby={p.labelId} disabled={locked} value={form.action}
                        onChange={(v: DestAction) => setForm(f => ({ ...f, action: v, bittorrent: v === 'allow' ? false : f.bittorrent, state: v === 'allow' && f.state === 'report' ? 'off' : f.state }))}
                        options={[
                          { value: 'block', label: t('admin:access_control.policies.action.block') },
                          { value: 'allow', label: t('admin:access_control.policies.action.allow') },
                        ]} />
                      {form.action === 'allow' && <FieldHint tone="warning" summary={t(`${D}allow_hint.summary`)} detail={t(`${D}allow_hint.detail`)} />}
                    </div>
                  )}
                </Field>

                <Field label={t(`${D}state`)}
                  hint={(
                    <>
                      {t(`admin:access_control.policies.state_help.${form.state === 'on' ? (form.action === 'allow' ? 'on_allow' : 'on_block') : form.state}`)}
                      {recommend && form.state !== 'report' && <span className="block">{t('admin:access_control.policies.state_help.recommend_note')}</span>}
                      {form.action === 'allow' && <span className="block">{t('admin:access_control.policies.state_help.allow_no_report')}</span>}
                    </>
                  )}>
                  {p => (
                    <SegmentedControl aria-labelledby={p.labelId} value={form.state} onChange={(v: DestState) => set('state', v)}
                      options={policyStateOptions(form.action).map(st => ({
                        value: st,
                        label: (
                          <>
                            {t(`admin:access_control.policies.state.${st}`)}
                            {/* Recommended for a NEW block policy only; the admin is never made to take it. */}
                            {st === 'report' && recommend && <span className="text-[11px] font-semibold text-primary">{t('admin:access_control.policies.state_help.recommend')}</span>}
                          </>
                        ),
                      }))} />
                  )}
                </Field>

                <MatchFields form={form} set={set} errors={errors} touched={touched} locked={locked} allLists={allLists} notReadyCount={notReady.length} />

                <ScopeFields form={form} set={set} errors={errors} groupOptions={groupOptions} panelOptions={panelOptions} removed={removed} />
              </fieldset>
            </div>

            {/* What saving does, kept in view beside the form on a wide screen and after it on a narrow one. */}
            <aside className="grid min-w-0 gap-4 rounded-xl border bg-card p-4 lg:sticky lg:top-4">
              <div>
                <p data-testid="editor-summary" className="text-sm font-semibold">
                  {[
                    t(form.state === 'report' ? 'admin:access_control.policies.state.report' : `admin:access_control.policies.action.${form.action}`),
                    summary,
                    position.report ? t(`${D}position_report`, { step: position.step }) : t(`${D}position`, { step: position.step, index: position.index }),
                  ].filter(Boolean).join(' · ')}
                </p>
                {split && <p className="mt-1 text-xs text-muted-foreground">{t(`${D}split`)}</p>}
                <p className="mt-1 text-xs text-muted-foreground">{footer}</p>
              </div>
              <div>
                <QuotaMeters variant="full" budget={preview.budget ?? undefined} loading={preview.loading} failed={false} />
                {preview.failed && (
                  <p className="text-sm text-muted-foreground">
                    {t('admin:access_control.policies.quota.failed')} <Button size="sm" variant="outline" onClick={() => setRetry(n => n + 1)}>{t('admin:access_control.retry')}</Button>
                  </p>
                )}
              </div>
            </aside>
          </div>
          {/* A new policy is always offered for saving (a copy or a step's blank starts filled in and is not "changed"). */}
          <SaveBar dirty={!policy || dirty} pending={busy} disabled={!!blockReason} message={blockReason} onSave={() => void save()} onDiscard={discard} />
        </PageForm>
      </FormValidityProvider>
    </ObjectPage>
  )
}
