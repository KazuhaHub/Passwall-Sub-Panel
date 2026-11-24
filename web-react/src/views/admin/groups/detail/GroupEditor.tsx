import { useCallback, useEffect, useState, type ComponentProps } from 'react'
import { useQueryClient } from '@tanstack/react-query'

import { createGroup, updateGroup } from '@/api/groups'
import type { Group } from '@/api/types'
import { confirm } from '@/components/ConfirmHost'
import { ObjectPage } from '@/components/common/ObjectPage'
import { ObjectTabs, useObjectLeaveGuard, type ObjectTab } from '@/components/common/ObjectTabs'
import { pushToast } from '@/components/feedback/toasts'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { SaveBar } from '@/components/form/SaveBar'
import { loadScopeState, saveScopeState, scopeChanged, scopeDiscarded, type ScopeState } from '@/components/scope/scopeOverrides'
import { useQueryScope } from '@/query/useQueryScope'
import { useCan } from '@/utils/permissions'
import { discardChangesConfirm } from '../../accessControl/confirmCopy'
import { GeneralTab } from '../GeneralTab'
import GroupDestinationTab from '../GroupDestinationTab'
import { GROUP_TABS, type GroupTab } from '../groupRoutes'
import { LAYOUT_SCOPE_KEYS, LayoutTab } from '../LayoutTab'
import { LimitsTab } from '../LimitsTab'
import { createRequest, dirtyFormTabs, EMPTY_FORM, formOf, sameForm, updateRequest, type GroupForm } from '../model'
import { NodesTab } from '../NodesTab'
import { OverridesTab } from '../OverridesTab'
import type { ScopeLoad } from '../ScopeGate'
import { useGroupsT } from '../useGroupsT'
import { matchKey, overridesKey } from '../useGroupStats'

type Frame = Pick<ComponentProps<typeof ObjectPage>, 'title' | 'subtitle' | 'status' | 'more' | 'secondary' | 'primary'>

/** The scope state seen through only the layout keys (inside) or only the others, so each tab can tell whether it holds edits. */
const through = (s: ScopeState, inside: boolean): ScopeState => ({ ...s, overridable: s.overridable.filter(k => (LAYOUT_SCOPE_KEYS as readonly string[]).includes(k) === inside) })

/**
 * The group editor: six tabs (General; Nodes and lines; Layout; Limits; Overrides, and for administrators
 * Destinations) over ONE draft and ONE save bar. A save does what the old side sheet's did, in the same order: PUT the
 * group (name, tag filter, remark, require 2FA, limits with clear flags), then write the per-group overrides that
 * changed (PUT changed or new, DELETE those switched back to following global). Unlike the sheet it does not close:
 * the page stays, and the overrides are read again so what it shows is what the server holds.
 *
 * A tab is built on its first visit and kept, so a draft survives a look at another tab; the guard asks once when
 * the PAGE is left (another group, the list, a link elsewhere), never on a tab switch.
 */
export function GroupEditor({ group, back, frame, bypass, onSaved, onCreated }: {
  /** The saved group; null on the create page. */
  group: Group | null
  back: { to: string; label: string }
  frame: Frame
  /** Set before an exit that makes the draft moot (a delete, a finished create). */
  bypass: { current: boolean }
  /** An edit went through (the saved group, which does not carry the member count). */
  onSaved?: (saved: Group) => void
  onCreated?: (created: Group) => void
}) {
  const t = useGroupsT()
  const qScope = useQueryScope()
  const queryClient = useQueryClient()
  const canAccess = useCan('access.view')
  const [saved, setSaved] = useState<GroupForm>(() => (group ? formOf(group) : EMPTY_FORM))
  const [form, setForm] = useState<GroupForm>(saved)
  const [attempted, setAttempted] = useState(false)
  const [pending, setPending] = useState(false)

  // The group's overrides, read with the global values they sit on (loadScopeState reads both).
  const [scope, setScope] = useState<ScopeLoad>({ status: 'loading' })
  const groupId = group?.id ?? null
  const loadScope = useCallback((signal?: AbortSignal, quiet = false) => {
    if (groupId === null) return
    // Quiet: after a save, swap in the fresh state without blanking the editor (its open sections would collapse).
    if (!quiet) setScope({ status: 'loading' })
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

  const ready = scope.status === 'ready' ? scope.scope : null
  const dirtyTabs = new Set<GroupTab>(dirtyFormTabs(form, saved))
  if (ready && scopeChanged(through(ready, true))) dirtyTabs.add('layout')
  if (ready && scopeChanged(through(ready, false))) dirtyTabs.add('overrides')
  const dirty = !sameForm(form, saved) || (ready !== null && scopeChanged(ready))
  const names = GROUP_TABS.filter(id => dirtyTabs.has(id)).map(id => t(`admin:groups.tabs.${id}`)).join(t('admin:groups.detail.tab_separator'))
  useObjectLeaveGuard({ dirty, copy: { ...discardChangesConfirm(t as never), message: t('admin:groups.detail.unsaved_in', { tabs: names }) }, bypass })

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
        // writes are idempotent, so the page re-reads and shows the state the server actually holds.
        let scopeFailed = false
        const wroteScope = scope.status === 'ready' && scopeChanged(scope.scope)
        if (scope.status === 'ready' && wroteScope) {
          try { await saveScopeState(group.id, scope.scope) } catch {
            scopeFailed = true
            pushToast({ message: t('admin:groups.scope.save_error'), severity: 'error' })
          }
          void queryClient.invalidateQueries({ queryKey: overridesKey(qScope, group.id) })
        }
        if (scopeFailed) { loadScope(); onSaved?.(res.group); return }
        if (wroteScope) loadScope(undefined, true)
        pushToast({ message: t('admin:groups.toast.updated'), severity: 'success' })
        if (res.resync_errors?.length) {
          pushToast({ message: t('admin:groups.toast.resync_partial', { count: res.resync_errors.length }), severity: 'warning' })
        }
        onSaved?.(res.group)
      } else {
        const created = await createGroup(createRequest(form))
        pushToast({ message: t('admin:groups.toast.created'), severity: 'success' })
        bypass.current = true
        onCreated?.(created)
      }
    } catch { /* The request layer already surfaced the failure. */ } finally { setPending(false) }
  }

  const ids = canAccess ? [...GROUP_TABS] : GROUP_TABS.filter(x => x !== 'destination')
  const tabs: ObjectTab<GroupTab>[] = ids.map(id => ({ id, label: t(`admin:groups.tabs.${id}`), dirty: dirtyTabs.has(id) }))
  const panels: Record<GroupTab, React.ReactNode> = {
    general: <GeneralTab form={form} setForm={setForm} group={group} slugError={slugError} nameError={nameError} />,
    nodes: <NodesTab form={form} setForm={setForm} groupId={groupId} />,
    layout: <LayoutTab group={group} scope={scope} onScope={setScopeState} reloadScope={() => loadScope()} />,
    limits: <LimitsTab form={form} setForm={setForm} />,
    overrides: <OverridesTab group={group} scope={scope} onScope={setScopeState} reloadScope={() => loadScope()} />,
    destination: canAccess ? <GroupDestinationTab groupId={groupId} /> : null,
  }

  return (
    <ObjectPage back={back} {...frame}>
      <FormValidityProvider>
        <form onSubmit={e => { e.preventDefault(); void save() }} noValidate className="flex flex-col gap-4">
          <ObjectTabs tabs={tabs} panels={panels} unsavedLabel={t('admin:groups.tabs.unsaved')} />
          <SaveBar dirty={dirty} pending={pending} onSave={() => void save()}
            onDiscard={() => {
              setForm(saved); setAttempted(false)
              if (scope.status === 'ready') setScopeState(scopeDiscarded(scope.scope))
            }} />
        </form>
      </FormValidityProvider>
    </ObjectPage>
  )
}
