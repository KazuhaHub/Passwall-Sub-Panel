// @vitest-environment jsdom

import type { PropsWithChildren } from 'react'
import { act, renderHook, waitFor } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { useTabParam } from './useTabParam'

function wrapper(initialEntry: string) {
  return function Wrapper({ children }: PropsWithChildren) {
    return <MemoryRouter initialEntries={[initialEntry]}>{children}</MemoryRouter>
  }
}

function useTestTab() {
  const [tab, setTab] = useTabParam('tab', 'overview', ['overview', 'rules', 'traffic'] as const)
  const location = useLocation()
  return { tab, setTab, search: location.search }
}

describe('useTabParam', () => {
  it('reads an allowed value and falls back for an unknown value', () => {
    const allowed = renderHook(useTestTab, { wrapper: wrapper('/?tab=rules') })
    expect(allowed.result.current.tab).toBe('rules')

    const unknown = renderHook(useTestTab, { wrapper: wrapper('/?tab=unknown') })
    expect(unknown.result.current.tab).toBe('overview')
  })

  it('writes non-default values while preserving unrelated parameters', async () => {
    const { result } = renderHook(useTestTab, { wrapper: wrapper('/?page=3') })
    act(() => result.current.setTab('traffic'))

    await waitFor(() => expect(result.current.tab).toBe('traffic'))
    expect(new URLSearchParams(result.current.search)).toEqual(new URLSearchParams('page=3&tab=traffic'))
  })

  it('removes the parameter when returning to the fallback tab', async () => {
    const { result } = renderHook(useTestTab, { wrapper: wrapper('/?tab=rules&page=2') })
    act(() => result.current.setTab('overview'))

    await waitFor(() => expect(result.current.tab).toBe('overview'))
    expect(result.current.search).toBe('?page=2')
  })
})
