import { useMemo, useState, type ReactNode } from 'react'

import type { ConfirmOpts } from '@/components/ConfirmHost'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { useTabParam } from '@/hooks/useTabParam'
import { cn } from '@/lib/utils'

export interface ObjectTab<T extends string> {
  id: T
  label: ReactNode
  /** The tab holds unsaved edits: a small dot after its label, named by `unsavedLabel`. */
  dirty?: boolean
  /**
   * `form` (default) keeps the content to a readable column; `full` lets a table or a chart use the page's width.
   * Chosen per tab: a page usually has both kinds.
   */
  width?: 'form' | 'full'
}

/**
 * The tab bar and bodies of an object page. The selected tab is `?tab=` (the first one leaves the URL clean), so a
 * tab is a link and a reload stays put. A tab is built on its first visit and then KEPT, hidden, so a draft in one
 * survives a look at another and a switch never asks anything; the page asks once when it is LEFT
 * (`useObjectLeaveGuard`). Give the tabs' owner a `key` per object so another object starts from clean forms.
 *
 * `panels` holds an element per tab id; only visited ones are mounted, so building the map every render is cheap.
 */
export function ObjectTabs<T extends string>({ tabs, panels, unsavedLabel, param = 'tab', className }: {
  tabs: readonly ObjectTab<T>[]
  panels: Record<T, ReactNode>
  /** The dot's accessible name ("Unsaved changes"). */
  unsavedLabel: string
  param?: string
  className?: string
}) {
  const ids = useMemo(() => tabs.map(t => t.id), [tabs])
  const [tab, setTab] = useTabParam<T>(param, ids[0], ids)
  const [visited, setVisited] = useState<ReadonlySet<T>>(new Set([tab]))
  const shown = visited.has(tab) ? visited : new Set(visited).add(tab)
  const go = (next: T) => { setVisited(v => (v.has(next) ? v : new Set(v).add(next))); setTab(next) }
  return (
    <Tabs value={tab} onValueChange={v => go(v as T)} className={className}>
      {/* Scrolls sideways inside itself on a phone instead of widening the page. */}
      <div className="max-w-full overflow-x-auto"><TabsList>
        {tabs.map(t => (
          <TabsTrigger key={t.id} value={t.id}>
            {t.label}
            {t.dirty && <span role="img" aria-label={unsavedLabel} title={unsavedLabel} className="size-1.5 rounded-full bg-warning" />}
          </TabsTrigger>
        ))}
      </TabsList></div>
      {tabs.map(t => (
        // keepMounted: a tab builds its own form, which the Tabs component cannot see, so it would unmount the draft.
        <TabsContent key={t.id} value={t.id} keepMounted>
          <div className={cn(t.width === 'full' ? '' : 'max-w-3xl')}>{shown.has(t.id) ? panels[t.id] : null}</div>
        </TabsContent>
      ))}
    </Tabs>
  )
}

/**
 * Leaving the PAGE with unsaved tabs asks once (a tab switch or a drawer param is not leaving: only a change of
 * path is). `copy.message` should name the tabs with edits. `bypass` is for the two exits that make the drafts moot,
 * a delete done from the page and a create that just finished; set it BEFORE navigating, because the dirty state
 * clears a render later.
 */
export function useObjectLeaveGuard({ dirty, copy, bypass }: { dirty: boolean; copy: ConfirmOpts; bypass?: { current: boolean } }) {
  useLeaveGuard(dirty, copy, (next, current) => !bypass?.current && next.pathname !== current.pathname)
}
