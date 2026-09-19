import { QueryClient } from '@tanstack/react-query'
import { isAxiosError } from 'axios'

/**
 * Retry policy for read queries.
 *
 * The backend answers "this capability is not configured" with 503 and
 * "unsupported" with 501 — both are deterministic answers, not transient
 * faults, so retrying them only delays the error the UI needs to show. 401 is
 * already handled by the axios single-flight refresh; anything that reaches
 * here as 401 has already failed that dance.
 */
function shouldRetryQuery(failureCount: number, error: unknown): boolean {
  // At most one retry.
  if (failureCount >= 1) return false

  if (isAxiosError(error)) {
    // Caller-initiated cancellation is normal control flow.
    if (error.code === 'ERR_CANCELED') return false

    const status = error.response?.status
    // No response at all: network failure or timeout — worth one retry.
    if (status === undefined) return true
    // Recoverable server faults only. 501/503 are deliberate answers here.
    if (status === 501 || status === 503) return false
    return status >= 500 && status < 600
  }

  // A non-axios throw is a programming error, not a transient fault.
  return false
}

/**
 * Builds one QueryClient. A client is created per authenticated session and
 * discarded when the session changes (see QuerySessionProvider) — it is never
 * shared across identities and never rebuilt per render.
 */
export function makeQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // Revalidate whenever the user comes back to the tab, reconnects, or
        // remounts — the three triggers that make a long-lived page discover
        // external changes without a timer running.
        refetchOnWindowFocus: true,
        refetchOnReconnect: true,
        refetchOnMount: true,
        // Polling is opt-in per resource; never in the background.
        refetchInterval: false,
        refetchIntervalInBackground: false,
        retry: shouldRetryQuery,
        retryDelay: 1000,
      },
      mutations: {
        // A mutation here creates users, resets credentials or issues install
        // commands — replaying one after a reconnect is not safe.
        retry: false,
      },
    },
  })
}

export { shouldRetryQuery }
