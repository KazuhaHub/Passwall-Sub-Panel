import { useCallback, useEffect, useState } from 'react'
import { useQueryClient, type QueryKey, type UseQueryResult } from '@tanstack/react-query'

/**
 * The draft of one SSO provider's configuration. It is seeded once from the first read and never re-seeded by a
 * background revalidation (that would reset a form being typed in); `adopt` puts the server's answer to a save into
 * the cache and the draft. Dirty is a comparison with what the cache holds, plus whatever secret has been typed.
 */
export function useSsoDraft<C>(query: UseQueryResult<C>, queryKey: QueryKey, normalize: (c: C) => C, secretTyped: boolean, report: (dirty: boolean) => void) {
  const queryClient = useQueryClient()
  const [draft, setDraft] = useState<C | null>(null)
  useEffect(() => { if (draft === null && query.data) setDraft(normalize(query.data)) }, [draft, query.data, normalize])

  const saved = query.data ? normalize(query.data) : null
  const dirty = secretTyped || (draft !== null && saved !== null && JSON.stringify(draft) !== JSON.stringify(saved))
  useEffect(() => { report(dirty) }, [dirty, report])
  useEffect(() => () => report(false), [report])

  const patch = useCallback((p: Partial<C>) => setDraft(d => (d ? { ...d, ...p } : d)), [])
  const adopt = useCallback((answer: C) => {
    queryClient.setQueryData(queryKey, answer)
    setDraft(normalize(answer))
  }, [queryClient, queryKey, normalize])
  const discard = useCallback(() => { if (query.data) setDraft(normalize(query.data)) }, [query.data, normalize])
  return { draft, setDraft, patch, dirty, adopt, discard, saved }
}
