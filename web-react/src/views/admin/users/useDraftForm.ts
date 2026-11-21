import { useCallback, useMemo, useState } from 'react'
import { draftProblems, isDirty, type DraftField, type UserDraft } from './draft'

/**
 * A form's draft over a base. Untouched, the form follows the base (a refetch is not an edit); the first edit
 * copies it. A field shows its problem once it has been left or a save was tried, so typing is not nagged.
 */
export function useDraftForm(base: UserDraft, mode: 'create' | 'edit') {
  const [edit, setEdit] = useState<UserDraft | null>(null)
  const [touched, setTouched] = useState<ReadonlySet<DraftField>>(new Set())
  const [attempted, setAttempted] = useState(false)
  const draft = edit ?? base
  const dirty = edit !== null && isDirty(edit, base)
  const problems = useMemo(() => draftProblems(draft, mode), [draft, mode])

  const patch = useCallback((p: Partial<UserDraft>) => setEdit(prev => ({ ...(prev ?? base), ...p })), [base])
  const blur = useCallback((f: DraftField) => setTouched(s => (s.has(f) ? s : new Set(s).add(f))), [])
  const reset = useCallback(() => { setEdit(null); setTouched(new Set()); setAttempted(false) }, [])

  return {
    draft,
    dirty,
    edited: edit !== null,
    problems,
    hasProblems: Object.keys(problems).length > 0,
    patch,
    blur,
    reset,
    tryAgain: () => setAttempted(true),
    error: (f: DraftField) => (attempted || touched.has(f) ? problems[f] : undefined),
  }
}
