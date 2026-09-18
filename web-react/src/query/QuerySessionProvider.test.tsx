// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { useQuery } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { QuerySessionProvider } from './QuerySessionProvider'
import { alertKeys } from './keys'
import { useQueryScope } from './useQueryScope'

const api = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))

/** Reads a private query so the client and its session scoping are exercised. */
function Probe() {
  const scope = useQueryScope()
  const { data } = useQuery({
    queryKey: alertKeys.all(scope),
    queryFn: () => api.get() as Promise<string>,
  })
  return <div data-testid="out">{data ?? 'none'}</div>
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  useAuthStore.setState({ userId: 1, role: 'admin', authEpoch: 1, hasToken: true })
})

afterEach(cleanup)

describe('QuerySessionProvider', () => {
  it('serves queries to the mounted tree', async () => {
    api.get.mockResolvedValue('session-one')
    render(
      <QuerySessionProvider>
        <Probe />
      </QuerySessionProvider>,
    )
    await waitFor(() => expect(screen.getByTestId('out').textContent).toBe('session-one'))
    expect(api.get).toHaveBeenCalledTimes(1)
  })

  it('re-reads from the server when the session changes', async () => {
    // The old session's cached body must not be shown to the new one; making
    // the key carry the scope is what guarantees it, so the observable effect
    // is a second fetch rather than a cache hit.
    api.get.mockResolvedValue('session-one')
    render(
      <QuerySessionProvider>
        <Probe />
      </QuerySessionProvider>,
    )
    await waitFor(() => expect(screen.getByTestId('out').textContent).toBe('session-one'))

    api.get.mockResolvedValue('session-two')
    act(() => {
      useAuthStore.setState({ userId: 2, role: 'admin', authEpoch: 2, hasToken: true })
    })

    await waitFor(() => expect(screen.getByTestId('out').textContent).toBe('session-two'))
    expect(api.get).toHaveBeenCalledTimes(2)
  })

  it('re-reads when a role change redacts a different field set', async () => {
    api.get.mockResolvedValue('as-admin')
    render(
      <QuerySessionProvider>
        <Probe />
      </QuerySessionProvider>,
    )
    await waitFor(() => expect(screen.getByTestId('out').textContent).toBe('as-admin'))

    api.get.mockResolvedValue('as-operator')
    act(() => {
      useAuthStore.setState({ role: 'operator', authEpoch: 3 })
    })

    await waitFor(() => expect(screen.getByTestId('out').textContent).toBe('as-operator'))
    expect(api.get).toHaveBeenCalledTimes(2)
  })
})
