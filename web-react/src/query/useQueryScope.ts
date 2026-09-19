import { useMemo } from 'react'
import { useAuthStore } from '@/stores/auth'
import { sessionScope, type QueryScope } from './session'

/**
 * The scope every private query key must be built from. Reading it from the
 * auth store (rather than a context) keeps one source of truth: the same
 * values that define the QueryClient's session also prefix its keys, so a key
 * can never outlive the client that created it.
 */
export function useQueryScope(): QueryScope {
  const userId = useAuthStore(s => s.userId)
  const role = useAuthStore(s => s.role)
  const authEpoch = useAuthStore(s => s.authEpoch)
  return useMemo(
    () => sessionScope({ userId, role, authEpoch }),
    [userId, role, authEpoch],
  )
}
