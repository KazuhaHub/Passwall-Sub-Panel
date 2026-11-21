import { useCallback, useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'

import { createGroup, updateGroup } from '@/api/groups'
import type { Group } from '@/api/types'
import { confirm } from '@/components/ConfirmHost'
import { DetailSheet } from '@/components/data/DetailSheet'
import { pushToast } from '@/components/feedback/toasts'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import { loadScopeState, saveScopeState, scopeChanged, scopeDiscarded, type ScopeState } from '@/components/scope/scopeOverrides'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { urlCodecs, useUrlState } from '@/hooks/useUrlState'
import { useQueryScope } from '@/query/useQueryScope'
import { matchKey, overridesKey } from './useGroupStats'
import { useSheetGuard } from '../subscription/useSheetGuard'
import GroupDestinationTab from './GroupDestinationTab'
import { GeneralTab } from './GeneralTab'
import { LayoutTab } from './LayoutTab'
import { LimitsTab } from './LimitsTab'
import { NodesTab } from './NodesTab'
import { OverridesTab } from './OverridesTab'
import type { ScopeLoad } from './ScopeGate'
import { createRequest, EMPTY_FORM, formOf, sameForm, updateRequest, type GroupForm } from './model'
import { useGroupsT } from './useGroupsT'

/** What the sheet is for: a new group, or an existing one. */
export type GroupTarget = { kind: 'new' } | { kind: 'edit'; group: Group }

const TABS = ['general', 'nodes', 'layout', 'limits', 'overrides', 'destination'] as const
type Tab = (typeof TABS)[number]

export function GroupSheet({ target, canAccess, onClose, onSaved }: {
  target: GroupTarget | null
  /** The destinations tab belongs to administrators (access.view). */
  canAccess: boolean
  onClose: () => void
  /** A save went through. `closes` is false when the group saved but something beside it did not, and the sheet stays to say so. */
  onSaved: (saved: Group | null, wasEdit: boolean, closes: boolean) => void
}) {
  if (!target) return null
  return <SheetBody key={target.kind === 'edit' ? `edit:${target.group.id}` : 'new'} target={target} canAccess={canAccess} onClose={onClose} onSaved={onSaved} />
}

/**
 * The group editor: a wide sheet with five tabs (General; Nodes and lines; Layout; Limits; Overrides) and, for
 * administrators, a sixth that summarises destination access. ONE draft and ONE save bar for all of them, and a
 * save does what the old dialog's did, in the same order: PUT the group (name, tag filter, remark, require 2FA,
 * limits with clear flags), then write the per-group overrides that changed (PUT changed or new, DELETE those
 * switched back to following global). The tab is in the address (`dtab=`), so the access-control page can link
 * straight to the destinations tab; a draft is lost only when the sheet or the page is left, and that asks.
 */
function SheetBody({ target, canAccess, onClose, onSaved }: {
  target: GroupTarget
  canAccess: boolean
  onClose: () => void
  onSaved: (saved: Group | null, wasEdit: boolean, closes: boolean) => void
}) {
  const t = useGroupsT()
  const qScope = useQueryScope()
  const queryClient = useQueryClient()
  const group = target.kind === 'edit' ? target.group : null
  const [saved, setSaved] = useState<GroupForm>(() => (group ? formOf(group) : EMPTY_FORM))
  const [form, setForm] = useState<GroupForm>(saved)
  const [attempted, setAttempted] = useState(false)
  const [pending, setPending] = useState(false)
  const [asked, setTab] = useUrlState('dtab', urlCodecs.oneOf(TABS, 'general'))
  const tab: Tab = asked === 'destination' && !canAccess ? 'general' : asked

  // The group's overrides, read with the global values they sit on (loadScopeState reads both).
  const [scope, setScope] = useState<ScopeLoad>({ status: 'loading' })
  const groupId = group?.id ?? null
  const loadScope = useCallback((signal?: AbortSignal) => {
    if (groupId === null) return
    setScope({ status: 'loading' })
    loadScopeState(groupId, signal)
      .then(s => { if (!signal?.aborted) setScope({ status: 'ready', scope: s }) })
      .catch(() => { if (!signal?.aborted) setScope({ status: 'error' }) })
  }, [groupId])
  useEffect(() => {
    const ctl = new AbortController()
    loadScope(ctl.signal)
    return () => ctl.abort()
  }, [loadScope])
  const setScopeState = (next: ScopeState) => setScope({ status: 'ready', scope: next })

  const dirty = !sameForm(form, saved) || (scope.status === 'ready' && scopeChanged(scope.scope))
  const { close, finish } = useSheetGuard({ dirty, pending, onClose })

  const slugError = attempted && !group && !form.slug ? t('admin:groups.validate.slug_required') : undefined
  const nameError = attempted && !form.name ? t('admin:groups.validate.name_required') : undefined

  async function save() {
    if ((!group && !form.slug) || !form.name) {
      setAttempted(true)
      pushToast({ message: t(!group && !form.slug ? 'admin:groups.validate.slug_required' : 'admin:groups.validate.name_required'), severity: 'warning' })
      return
    }
    // Requiring (or no longer requiring) two-step verification reaches every member: say how many before it goes out.
    if (group && form.require_2fa !== saved.require_2fa && group.members > 0) {
      const key = form.require_2fa ? 'require_2fa_on' : 'require_2fa_off'
      const ok = await confirm({
        title: t(`admin:groups.confirm.${key}.title`),
        message: t(`admin:groups.confirm.${key}.message`, { count: group.members }),
        confirmText: t(`admin:groups.confirm.${key}.action`),
        initialFocus: 'cancel',
      })
      if (!ok) return
    }
    setPending(true)
    try {
      if (group) {
        const res = await updateGroup(group.id, updateRequest(form))
        setSaved(form)
        // The list's matched-nodes count for this group is read again.
        void queryClient.invalidateQueries({ queryKey: matchKey(qScope, group.id) })
        // Overrides go after the group itself. A write that fails midway can leave them partly applied; the backend
        // writes are idempotent, so the sheet stays open on the state the server actually holds.
        let scopeFailed = false
        if (scope.status === 'ready' && scopeChanged(scope.scope)) {
          try { await saveScopeState(group.id, scope.scope) } catch {
            scopeFailed = true
            pushToast({ message: t('admin:groups.scope.save_error'), severity: 'error' })
          }
          void queryClient.invalidateQueries({ queryKey: overridesKey(qScope, group.id) })
        }
        if (scopeFailed) { loadScope(); onSaved(res.group, true, false); return }
        pushToast({ message: t('admin:groups.toast.updated'), severity: 'success' })
        if (res.resync_errors?.length) {
          pushToast({ message: t('admin:groups.toast.resync_partial', { count: res.resync_errors.length }), severity: 'warning' })
        }
        finish(() => onSaved(res.group, true, true))
      } else {
        const created = await createGroup(createRequest(form))
        pushToast({ message: t('admin:groups.toast.created'), severity: 'success' })
        finish(() => onSaved(created, false, true))
      }
    } catch { /* The request layer already surfaced the failure. */ } finally { setPending(false) }
  }

  const tabs: Tab[] = canAccess ? [...TABS] : TABS.filter(x => x !== 'destination')
  return (
    <DetailSheet
      open
      onClose={close}
      title={group ? t('admin:groups.edit_title') : t('admin:groups.create')}
      description={group ? group.slug : undefined}
      className="data-[side=right]:sm:w-[min(56rem,100vw)] data-[side=right]:sm:max-w-none"
    >
      <FormValidityProvider>
        <form onSubmit={e => { e.preventDefault(); void save() }} noValidate className="flex flex-col gap-4">
          <Tabs value={tab} onValueChange={v => setTab(v as Tab)}>
            {/* Six tabs do not fit a phone: the list scrolls sideways inside itself instead of widening the sheet. */}
            <TabsList className="max-w-full justify-start overflow-x-auto">
              {tabs.map(name => <TabsTrigger key={name} value={name}>{t(`admin:groups.tabs.${name}`)}</TabsTrigger>)}
            </TabsList>
            <TabsContent value="general" keepMounted>
              <GeneralTab form={form} setForm={setForm} group={group} slugError={slugError} nameError={nameError} />
            </TabsContent>
            <TabsContent value="nodes" keepMounted>
              <NodesTab form={form} setForm={setForm} groupId={groupId} />
            </TabsContent>
            <TabsContent value="layout" keepMounted>
              <LayoutTab group={group} scope={scope} onScope={setScopeState} reloadScope={() => loadScope()} />
            </TabsContent>
            <TabsContent value="limits" keepMounted>
              <LimitsTab form={form} setForm={setForm} />
            </TabsContent>
            <TabsContent value="overrides" keepMounted>
              <OverridesTab group={group} scope={scope} onScope={setScopeState} reloadScope={() => loadScope()} />
            </TabsContent>
            {canAccess && (
              <TabsContent value="destination" keepMounted={false}>
                <GroupDestinationTab groupId={groupId} />
              </TabsContent>
            )}
          </Tabs>
          <SaveBar dirty={dirty} pending={pending} onSave={() => void save()}
            onDiscard={() => {
              setForm(saved); setAttempted(false)
              if (scope.status === 'ready') setScopeState(scopeDiscarded(scope.scope))
            }} />
        </form>
      </FormValidityProvider>
    </DetailSheet>
  )
}
