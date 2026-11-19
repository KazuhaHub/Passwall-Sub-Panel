import { createContext, useCallback, useContext, useEffect, useId, useMemo, useState, type ReactNode } from 'react'

export interface ValidityIssue { id: string; message: string }

interface Registry {
  issues: ValidityIssue[]
  report: (id: string, message: string | null) => void
}

const ValidityContext = createContext<Registry | null>(null)

/**
 * Wrap a form (or a section with its own SaveBar) in this. Fields inside report
 * whether their input is usable (NumberField, TagsField, anything calling
 * useReportValidity), and a SaveBar inside reads the result and switches itself off
 * with the reason. Without it fields still show their own messages; nothing blocks.
 *
 * The page keeps its own state and its own validation; this only carries the
 * question the page cannot answer: is there text in a box that never became a value?
 * (a number being typed, "9999" in a field limited to 600, a box emptied but not
 * filled), so Save cannot store something other than what the box shows.
 */
export function FormValidityProvider({ children }: { children: ReactNode }) {
  const [map, setMap] = useState<Record<string, string>>({})
  const report = useCallback((id: string, message: string | null) => {
    setMap(prev => {
      if (message === null) {
        if (!(id in prev)) return prev
        const { [id]: _gone, ...rest } = prev
        void _gone
        return rest
      }
      return prev[id] === message ? prev : { ...prev, [id]: message }
    })
  }, [])
  const value = useMemo<Registry>(() => ({
    issues: Object.entries(map).map(([id, message]) => ({ id, message })),
    report,
  }), [map, report])
  return <ValidityContext.Provider value={value}>{children}</ValidityContext.Provider>
}

/** What the nearest provider currently knows: the problems, and whether there are any. */
export function useFormValidity(): { issues: ValidityIssue[]; blocked: boolean } {
  const ctx = useContext(ValidityContext)
  return { issues: ctx?.issues ?? [], blocked: (ctx?.issues.length ?? 0) > 0 }
}

/**
 * A field reports its problem (null when fine). Cleans up on unmount, so a field that
 * leaves the form (an advanced section removed, an editor switched away) stops blocking it.
 */
export function useReportValidity(message: string | null): void {
  const ctx = useContext(ValidityContext)
  const id = useId()
  const report = ctx?.report
  useEffect(() => {
    if (!report) return
    report(id, message)
    return () => report(id, null)
  }, [report, id, message])
}
