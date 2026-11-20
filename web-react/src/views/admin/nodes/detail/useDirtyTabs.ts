import { useMemo, useState } from 'react'

/**
 * Which tabs of a page hold unsaved edits. Every tab is given its own reporter, stable for the life of the page,
 * so a tab can put it in an effect dependency list without restarting the effect on every render of the page.
 */
export function useDirtyTabs<T extends string>(tabs: readonly T[]) {
  const [dirty, setDirty] = useState<ReadonlySet<T>>(new Set())
  const reporters = useMemo(() => Object.fromEntries(tabs.map(tab => [tab, (isDirty: boolean) => setDirty(prev => {
    if (prev.has(tab) === isDirty) return prev
    const next = new Set(prev)
    if (isDirty) next.add(tab); else next.delete(tab)
    return next
  })])) as Record<T, (dirty: boolean) => void>, [tabs])
  // In the tabs' own order, so the sentence that names them does not depend on which was edited first.
  return { dirty: tabs.filter(tab => dirty.has(tab)), report: reporters }
}
