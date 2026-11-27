import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from 'react'
import { useQueries } from '@tanstack/react-query'
import { useSearchParams } from 'react-router'
import type { Group } from '@/api/types'
import { getGroupScopeSettings } from '@/api/scopeSettings'
import { confirm } from '@/components/ConfirmHost'
import { Field } from '@/components/form/Field'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { settingsKeys } from '@/query/keys'
import { useAllGroups } from '@/query/groups'
import { useQueryScope } from '@/query/useQueryScope'
import { cn } from '@/lib/utils'
import { discardChangesConfirm } from '../accessControl/confirmCopy'
import { useAdminT } from '../useAdminT'

// THE AUDIENCE OF THE SETTINGS PAGE: who the settings apply to. "All users" is the global default; a group shows the
// overrides that group may have. It is a page-level choice (a rail beside the section list), not a control inside one
// section, so it stays in view while the sections change: the sections that can be set per group show that group's
// editor, the others say they cannot and keep their global form.

export const AUDIENCE_PARAM = 'audience'
/** More groups than this and the rail gets a filter box. */
const FILTER_FROM = 8

interface AudienceValue {
  groupId: number
  groups: Group[]
  groupsLoaded: boolean
  /** Switch audience; asks first when the group editor on screen holds unsaved overrides. Resolves to whether it switched. */
  choose: (next: number) => Promise<boolean>
  /** The group editor reports whether it holds unsaved overrides. */
  setEditorDirty: (dirty: boolean) => void
}

const AudienceContext = createContext<AudienceValue>({ groupId: 0, groups: [], groupsLoaded: false, choose: async () => false, setEditorDirty: () => {} })
export const useAudience = () => useContext(AudienceContext)

/** The query key prefix of the per-group override counts, so a save can refresh them. */
export const scopeCountKey = (scope: ReturnType<typeof useQueryScope>) => [...settingsKeys.all(scope), 'scope-count'] as const

export function AudienceProvider({ children }: { children: ReactNode }) {
  const t = useAdminT()
  const scope = useQueryScope()
  const groups = useAllGroups(scope)
  const [params, setParams] = useSearchParams()
  const list = useMemo<Group[]>(() => groups.data ?? [], [groups.data])
  const wanted = Number(params.get(AUDIENCE_PARAM)) || 0
  // A group that no longer exists (or that has not loaded yet) reads as the global audience, never as an empty editor.
  const groupId = wanted > 0 && list.some(g => g.id === wanted) ? wanted : 0
  const editorDirty = useRef(false)
  const setEditorDirty = useCallback((dirty: boolean) => { editorDirty.current = dirty }, [])
  const choose = useCallback(async (next: number) => {
    if (next === groupId) return false
    // Leaving a group's editor drops what was typed in it: ask first, the same as leaving the page.
    if (groupId !== 0 && editorDirty.current && !(await confirm({ ...discardChangesConfirm(t), destructive: true, initialFocus: 'cancel' }))) return false
    setParams(prev => {
      const out = new URLSearchParams(prev)
      if (next === 0) out.delete(AUDIENCE_PARAM); else out.set(AUDIENCE_PARAM, String(next))
      return out
    }, { replace: true })
    return true
  }, [groupId, setParams, t])
  const value = useMemo<AudienceValue>(() => ({ groupId, groups: list, groupsLoaded: groups.isSuccess, choose, setEditorDirty }), [groupId, list, groups.isSuccess, choose, setEditorDirty])
  return <AudienceContext.Provider value={value}>{children}</AudienceContext.Provider>
}

/** The shared look of a selected entry in the page's two lists (audience and sections). */
export const NAV_ITEM = 'flex w-full items-center justify-between gap-2 rounded-md px-3 py-2 text-left text-sm outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring/50'
export const NAV_ACTIVE = 'bg-segment-active font-semibold text-foreground shadow-xs ring-1 ring-segment-border'

/** How many settings each group overrides, one small read per group. A failed read shows no count rather than a wrong one. */
function useOverrideCounts(groups: readonly Group[]): Map<number, number> {
  const scope = useQueryScope()
  const results = useQueries({
    queries: groups.map(g => ({
      queryKey: [...scopeCountKey(scope), g.id],
      queryFn: ({ signal }: { signal: AbortSignal }) => getGroupScopeSettings(g.id, signal),
      staleTime: 30_000,
    })),
  })
  const map = new Map<number, number>()
  groups.forEach((g, i) => { const d = results[i]?.data; if (d) map.set(g.id, Object.keys(d.overrides ?? {}).length) })
  return map
}

/**
 * The audience list: "All users (global default)" first, then every group by name with a quiet count of the settings it
 * overrides (nothing when none). A filter box appears once there are more than eight groups. On a phone it is one
 * select, like the section list.
 */
export function AudienceRail() {
  const t = useAdminT()
  const { groupId, groups, choose } = useAudience()
  const counts = useOverrideCounts(groups)
  const [query, setQuery] = useState('')
  const shown = query.trim() ? groups.filter(g => g.name.toLowerCase().includes(query.trim().toLowerCase())) : groups
  const item = (id: number, name: string) => {
    const n = counts.get(id) ?? 0
    const active = id === groupId
    return (
      <li key={id}>
        <button type="button" aria-current={active ? 'true' : undefined} onClick={() => void choose(id)} className={cn(NAV_ITEM, active && NAV_ACTIVE)}>
          <span className="min-w-0 truncate">{name}</span>
          {n > 0 && (
            <>
              <span aria-hidden className="shrink-0 text-xs font-normal text-muted-foreground tabular-nums">{n}</span>
              <span className="sr-only">{t('settings.scope.overrides_count', { count: n })}</span>
            </>
          )}
        </button>
      </li>
    )
  }
  return (
    <>
      <section aria-labelledby="settings-audience-title" className="hidden md:block">
        <h2 id="settings-audience-title" className="mb-1 px-3 text-xs font-medium tracking-wide text-muted-foreground uppercase">{t('settings.scope.audience')}</h2>
        {groups.length > FILTER_FROM && (
          <Input type="search" value={query} onChange={e => setQuery(e.target.value)} placeholder={t('settings.scope.filter')} aria-label={t('settings.scope.filter')} className="mb-1" />
        )}
        {/* The active item draws a ring and a shadow outside its box, and the focus ring is 3px: an
            overflow container clips them at its padding edge, which cut the highlight flat on three
            sides. 4px of padding (cancelled by the negative margin) leaves room for all of it. */}
        <ul className="-m-1 grid max-h-56 gap-0.5 overflow-y-auto p-1">
          {item(0, t('settings.scope.all_users'))}
          {shown.map(g => item(g.id, g.name))}
        </ul>
      </section>
      <div className="md:hidden">
        <Field label={t('settings.scope.audience')}>
          <NativeSelect id="settings-audience" value={String(groupId)} onChange={e => void choose(Number(e.target.value))}>
            <option value="0">{t('settings.scope.all_users')}</option>
            {groups.map(g => <option key={g.id} value={g.id}>{g.name}</option>)}
          </NativeSelect>
        </Field>
      </div>
    </>
  )
}
