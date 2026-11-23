import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import type { Group as GroupRow } from '@/api/types'
import { pushSnack } from '@/components/feedback/toasts'
import { ErrorState } from '@/components/feedback/ErrorState'
import { CardSkeleton } from '@/components/feedback/LoadingState'
import { Field } from '@/components/form/Field'
import { NativeSelect } from '@/components/ui/native-select'
import { confirm } from '@/components/ConfirmHost'
import ScopeOverridesEditor from '@/components/scope/ScopeOverridesEditor'
import { loadScopeState, saveScopeState, type ScopeState } from '@/components/scope/scopeOverrides'
import { useAllGroups } from '@/query/groups'
import { useQueryScope } from '@/query/useQueryScope'
import { discardChangesConfirm } from '../accessControl/confirmCopy'
import { useAdminT } from '../useAdminT'
import { SectionForm } from './parts'

/** A section with a global form and a per-group editor reports one dirty flag for both. */
export function useScopedDirty(report: (dirty: boolean) => void) {
  const [form, setForm] = useState(false)
  const [scope, setScope] = useState(false)
  useEffect(() => { report(form || scope) }, [form, scope, report])
  useEffect(() => () => report(false), [report])
  return { setForm, setScope }
}

/**
 * The audience of a section whose settings can also be set per group: "All users" edits the global form
 * (`children`), a group edits only the overrides that group may have (the inherit/override editor, which has its own
 * save). The global form stays mounted, hidden, while a group is shown, so its draft is not lost by looking at a group.
 * A failed read of the group list leaves the global form as the only audience.
 */
export function ScopeShell({ categories, onScopeDirty, children }: {
  categories: string[]
  onScopeDirty: (dirty: boolean) => void
  children: ReactNode
}) {
  const t = useAdminT()
  const scope = useQueryScope()
  const groups = useAllGroups(scope)
  const [groupId, setGroupId] = useState(0)
  const editorDirty = useRef(false)
  const reportEditor = useCallback((dirty: boolean) => { editorDirty.current = dirty; onScopeDirty(dirty) }, [onScopeDirty])

  async function choose(next: number) {
    if (next === groupId) return
    // Leaving a group's editor drops what was typed in it: ask first, the same as leaving the page.
    if (groupId !== 0 && editorDirty.current && !(await confirm({ ...discardChangesConfirm(t), destructive: true, initialFocus: 'cancel' }))) return
    setGroupId(next)
  }

  const list: GroupRow[] = groups.data ?? []
  return (
    <div className="flex flex-col gap-5">
      <Field label={t('settings.scope.audience')} hint={groupId === 0 ? undefined : t('settings.scope.override_hint')}>
        <NativeSelect id="settings-audience" value={String(groupId)} onChange={e => void choose(Number(e.target.value))}>
          <option value="0">{t('settings.scope.all_users')}</option>
          {list.map(g => <option key={g.id} value={g.id}>{g.name}</option>)}
        </NativeSelect>
      </Field>
      <div hidden={groupId !== 0}>{children}</div>
      {groupId !== 0 && <GroupEditor key={groupId} groupId={groupId} categories={categories} report={reportEditor} />}
    </div>
  )
}

/** One group's sparse overrides for the categories of the section it is shown in. */
function GroupEditor({ groupId, categories, report }: { groupId: number; categories: string[]; report: (dirty: boolean) => void }) {
  const t = useAdminT()
  const [state, setState] = useState<ScopeState | null>(null)
  const [failed, setFailed] = useState(false)
  const [initial, setInitial] = useState('')
  const [pending, setPending] = useState(false)

  const load = useCallback(() => {
    setFailed(false)
    return loadScopeState(groupId).then(s => { setState(s); setInitial(JSON.stringify(s.edit)) }).catch(() => setFailed(true))
  }, [groupId])
  useEffect(() => { void load() }, [load])

  const dirty = state !== null && JSON.stringify(state.edit) !== initial
  useEffect(() => { report(dirty) }, [dirty, report])
  useEffect(() => () => report(false), [report])

  async function save() {
    if (!state) return
    setPending(true)
    try {
      await saveScopeState(groupId, state)
      await load()
      pushSnack(t('settings.saved'), 'success')
    } catch {
      pushSnack(t('groups.scope.save_error'), 'error')
    } finally { setPending(false) }
  }

  if (failed) return <ErrorState title={t('settings.load_failed')} retry={() => load()} />
  if (!state) return <CardSkeleton lines={4} />
  return (
    <SectionForm dirty={dirty} pending={pending} onSave={() => void save()} onDiscard={() => setState({ ...state, edit: JSON.parse(initial) })}>
      <ScopeOverridesEditor scope={state} onChange={setState} categories={categories} />
    </SectionForm>
  )
}
