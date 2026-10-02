// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { QueryClient } from '@tanstack/react-query'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))

import { diagnosticsQuery } from './diagnostics'
import { diagnosticsKeys } from './keys'
import { policies } from './policies'
import { sessionScope } from './session'

const scope = sessionScope({ userId: 1, role: 'admin', authEpoch: 1 })

beforeEach(() => {
  vi.clearAllMocks()
})

describe('diagnosticsQuery', () => {
  // The page's "is it still growing?" line compares readings taken while it
  // is open, so the registry has to be re-read while nobody touches anything.
  it('re-reads the registry every minute while the page is visible', () => {
    const opts = diagnosticsQuery(scope)
    expect(opts.refetchInterval).toBe(60_000)
    expect(opts.staleTime).toBe(30_000)
    expect(opts.gcTime).toBe(5 * 60_000)
  })

  it('takes its cadence from the policy table, not from a number of its own', () => {
    const opts = diagnosticsQuery(scope)
    expect(opts.refetchInterval).toBe(policies.diagnostics.refetchInterval)
    expect(opts.staleTime).toBe(policies.diagnostics.staleTime)
    expect(opts.gcTime).toBe(policies.diagnostics.gcTime)
  })

  // A registry read cached for an admin must not be served to whoever signs
  // in next in the same tab.
  it('keys the read by session', () => {
    const key = diagnosticsQuery(scope).queryKey
    expect(key).toEqual(diagnosticsKeys.metrics(scope))
    expect(key.slice(0, 2)).toEqual(['private', scope])
    expect(key).not.toEqual(diagnosticsQuery(sessionScope({ userId: 2, role: 'admin', authEpoch: 1 })).queryKey)
    expect(key).toEqual(diagnosticsQuery(scope).queryKey)
  })

  it('reads the metrics endpoint and passes the cancellation signal on', async () => {
    const body = { version: 'v4', uptime_ms: 1, goroutines: 1, metrics: { since_unix_ms: 0, window_ms: 1, counters: [], gauges: [], histograms: [] } }
    api.get.mockResolvedValue({ data: body })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const data = await client.fetchQuery(diagnosticsQuery(scope))
    expect(data).toEqual(body)
    expect(api.get).toHaveBeenCalledTimes(1)
    const [url, config] = api.get.mock.calls[0]
    expect(url).toBe('/admin/diagnostics/metrics')
    expect(config?.signal).toBeInstanceOf(AbortSignal)
  })
})
