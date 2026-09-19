import { useEffect, useState, type ReactNode } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { useAuthStore } from '@/stores/auth'
import { makeQueryClient } from './client'
import { scopeKey, sessionScope } from './session'

/**
 * The current session's stable identity: API base, account, role and session
 * generation. Any change to this string means a different session.
 */
function useSessionKey(): string {
  const userId = useAuthStore(s => s.userId)
  const role = useAuthStore(s => s.role)
  const authEpoch = useAuthStore(s => s.authEpoch)
  return scopeKey(sessionScope({ userId, role, authEpoch }))
}

/**
 * Owns the QueryClient for the current session.
 *
 * Keying the inner client on the session makes React tear the whole subtree
 * down and build a fresh one whenever the identity changes, so no component
 * state and no cached body survives the switch. That is deliberate: a body
 * fetched as an admin must not be readable once the session is an operator,
 * and the same URL is redacted differently per caller.
 */
export function QuerySessionProvider({ children }: { children: ReactNode }) {
  const key = useSessionKey()
  return <SessionClient key={key}>{children}</SessionClient>
}

function SessionClient({ children }: { children: ReactNode }) {
  // Lazy initializer: one client per mounted session, never rebuilt per render.
  const [client] = useState(makeQueryClient)

  useEffect(
    () => () => {
      // Retiring this session's client: cancel whatever it still had in flight
      // so a late response from the old identity cannot land anywhere.
      void client.cancelQueries()
      client.clear()
    },
    [client],
  )

  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}
