import type { ReactElement, ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, type RenderResult } from '@testing-library/react'

/**
 * A QueryClient per test. Caches must never leak between tests, and the
 * production retry policy would turn a single-failure assertion into a
 * multi-second wait — tests that actually exercise retries should build their
 * own client with the real policy.
 */
export function makeTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: 0, gcTime: Infinity },
      mutations: { retry: false },
    },
  })
}

export function queryWrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>
  }
}

/** Renders inside a throwaway client and returns it for cache assertions. */
export function renderWithQuery(ui: ReactElement): { client: QueryClient; result: RenderResult } {
  const client = makeTestQueryClient()
  const result = render(ui, { wrapper: queryWrapper(client) })
  return { client, result }
}
