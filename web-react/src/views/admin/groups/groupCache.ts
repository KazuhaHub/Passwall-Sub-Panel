import { useMemo } from 'react'
import { useQueryClient } from '@tanstack/react-query'

import type { Group } from '@/api/types'
import { groupKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'

/**
 * What the group's page does to the cached reads after a write, in one place.
 *
 * `remember` writes a saved group through to the page's own read. A save's reply does not count members (only the
 * list and the by-id read do), so the count the page already had is kept: without it the header would say 0 members
 * and the two-step question would not name anyone after the first save.
 * `refreshLists` marks the list and the pickers' catalogue stale, so the list shows the change when it is next opened.
 */
export function useGroupCache() {
  const qc = useQueryClient()
  const scope = useQueryScope()
  return useMemo(() => ({
    remember(group: Group, members?: number) {
      qc.setQueryData<Group>(groupKeys.detail(scope, group.id), members === undefined ? group : { ...group, members })
    },
    members(id: number): number | undefined {
      return qc.getQueryData<Group>(groupKeys.detail(scope, id))?.members
    },
    forget(id: number) {
      qc.removeQueries({ queryKey: groupKeys.detail(scope, id) })
    },
    refreshLists() {
      void qc.invalidateQueries({ queryKey: groupKeys.lists(scope) })
      void qc.invalidateQueries({ queryKey: groupKeys.catalogue(scope) })
    },
  }), [qc, scope])
}
