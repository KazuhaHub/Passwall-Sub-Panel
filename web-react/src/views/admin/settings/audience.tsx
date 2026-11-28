import { createContext, useCallback, useContext, useMemo, useRef, type ReactNode } from 'react'
import { useQueries } from '@tanstack/react-query'
import { useSearchParams } from 'react-router'
import type { Group } from '@/api/types'
import { getGroupScopeSettings } from '@/api/scopeSettings'
import { confirm } from '@/components/ConfirmHost'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuLabel, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { IconChevronDown, IconUsers } from '@/lib/icons'
import { settingsKeys } from '@/query/keys'
import { useAllGroups } from '@/query/groups'
import { useQueryScope } from '@/query/useQueryScope'
import { cn } from '@/lib/utils'
import { discardChangesConfirm } from '../accessControl/confirmCopy'
import { useAdminT } from '../useAdminT'

// THE AUDIENCE OF THE SETTINGS PAGE: who the settings apply to. "All users" is the global default; a group shows the
// overrides that group may have. It is a page-level choice (a picker beside the page title), not a control inside one
// section, so it stays in view while the sections change: the sections that can be set per group show that group's
// editor, the others say they cannot and keep their global form.

export const AUDIENCE_PARAM = 'audience'

interface AudienceValue {
  groupId: number
  groups: Group[]
  groupsLoaded: boolean
  /** Switch audience; asks first when the group editor on screen holds unsaved overrides. Resolves to whether it switched. */
  choose: (next: number) => Promise<boolean>
  /** The group editor reports whether it holds unsaved overrides. */
  setEditorDirty: (editor: string, dirty: boolean) => void
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
  // Every tab that can override holds its own editor and keeps it built while hidden, so this is the set of those with edits.
  const editorDirty = useRef<Set<string>>(new Set())
  const setEditorDirty = useCallback((editor: string, dirty: boolean) => { if (dirty) editorDirty.current.add(editor); else editorDirty.current.delete(editor) }, [])
  const choose = useCallback(async (next: number) => {
    if (next === groupId) return false
    // Leaving a group's editor drops what was typed in it: ask first, the same as leaving the page.
    if (groupId !== 0 && editorDirty.current.size > 0 && !(await confirm({ ...discardChangesConfirm(t), destructive: true, initialFocus: 'cancel' }))) return false
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

/** The shared look of an entry in the section list, and of the selected one. */
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
 * The audience picker, in the page header: "Applies to: All users" with a menu of "All users (global default)" and
 * every group by name, each with a quiet count of the settings it overrides (nothing when none). It used to be a second
 * list stacked over the section list, which made the side rail two scrolling lists with two highlighted rows; who the
 * page edits is one page-level choice, so it reads as one, beside the title. Picking a group colours the button, so it
 * is plain at a glance that the page shows a group's overrides and not the global default.
 */
export function AudiencePicker() {
  const t = useAdminT()
  const { groupId, groups, choose } = useAudience()
  const counts = useOverrideCounts(groups)
  const current = groupId === 0 ? t('settings.scope.all_users') : (groups.find(g => g.id === groupId)?.name ?? '')
  const item = (id: number, name: string) => {
    const n = counts.get(id) ?? 0
    return (
      <DropdownMenuRadioItem key={id} value={String(id)}>
        <span className="flex min-w-0 flex-1 items-center justify-between gap-3">
          <span className="min-w-0 truncate">{name}</span>
          {n > 0 && (
            <>
              <span aria-hidden className="shrink-0 text-xs text-muted-foreground tabular-nums">{n}</span>
              <span className="sr-only">{t('settings.scope.overrides_count', { count: n })}</span>
            </>
          )}
        </span>
      </DropdownMenuRadioItem>
    )
  }
  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={(
        <Button variant="outline" id="settings-audience" className={cn('max-w-full', groupId !== 0 && 'border-primary/60 bg-primary/10 hover:bg-primary/15')} />
      )}>
        <IconUsers aria-hidden />
        <span className="text-muted-foreground">{t('settings.scope.applies_to')}</span>
        <span className="min-w-0 truncate font-semibold">{current}</span>
        <IconChevronDown aria-hidden className="opacity-60" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="max-h-80 min-w-60">
        <DropdownMenuGroup>
          <DropdownMenuLabel>{t('settings.scope.audience')}</DropdownMenuLabel>
          <DropdownMenuRadioGroup value={String(groupId)} onValueChange={v => void choose(Number(v))}>
            {item(0, t('settings.scope.all_users'))}
            {groups.length > 0 && <DropdownMenuSeparator />}
            {groups.map(g => item(g.id, g.name))}
          </DropdownMenuRadioGroup>
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
