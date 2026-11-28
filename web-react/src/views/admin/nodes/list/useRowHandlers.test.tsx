// @vitest-environment jsdom
import { QueryClientProvider } from '@tanstack/react-query'
import { renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { useRowHandlers, type LineRowHandlers } from './useRowHandlers'

vi.mock('../shared/useNodesT', () => ({ useNodesT: () => (k: string) => k }))

const client = makeTestQueryClient()
const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>
const noop = () => {}
const base: LineRowHandlers = { edit: noop, add: noop, toggle: noop, remove: noop }
const mount = (lines: LineRowHandlers) => renderHook(() => useRowHandlers({ lines, moveTo: noop, editSeparator: noop, deletion: { deleteNode: noop, removeFromPanel: noop } }), { wrapper }).result.current

describe('useRowHandlers multiplier entry', () => {
  it('leaves editMultiplier undefined when the caller has no multiplier handler', () => {
    expect(mount(base).editMultiplier).toBeUndefined()
  })
  it('defines it, forwarding to the handler, when the caller provides one', () => {
    const multiplier = vi.fn()
    const h = mount({ ...base, multiplier })
    h.editMultiplier?.({ kind: 'line' } as never)
    expect(multiplier).toHaveBeenCalledOnce()
  })
})
