// @vitest-environment jsdom
import { QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'
import type { Server } from '@/api/servers'
import { makeTestQueryClient } from '@/test/queryTestUtils'
import { useServerBusy, useServerUpgrades } from './useServerUpgrades'

const gate = vi.hoisted(() => ({ release: (() => {}) as () => void }))
vi.mock('react-i18next', async importOriginal => ({ ...await importOriginal<typeof import('react-i18next')>(), useTranslation: () => ({ t: (k: string) => k }) }))
vi.mock('@/components/ConfirmHost', () => ({ confirm: vi.fn(() => Promise.resolve(true)) }))
vi.mock('@/components/feedback/toasts', () => ({ pushSnack: vi.fn() }))
vi.mock('@/api/servers', async importOriginal => ({
  ...await importOriginal<typeof import('@/api/servers')>(),
  upgradeXray: vi.fn(() => new Promise<void>(resolve => { gate.release = resolve })),
}))

const client = makeTestQueryClient()
const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>

describe('upgrade state shared between the list and a server page', () => {
  it('a batch started by one hook instance marks its servers busy for another, until it finishes', async () => {
    const list = renderHook(() => useServerUpgrades({ probeServer: async () => {}, refresh: () => {}, clearSelection: () => {} }), { wrapper })
    const detail = renderHook(() => useServerBusy(7), { wrapper })
    const other = renderHook(() => useServerBusy(8), { wrapper })
    expect(detail.result.current).toBe(false)

    let done!: Promise<void>
    await act(async () => { done = list.result.current.batchUpgradeXray([{ id: 7, name: 'a' } as Server]); await Promise.resolve(); await Promise.resolve() })
    expect(detail.result.current).toBe(true)
    expect(other.result.current).toBe(false)

    await act(async () => { gate.release(); await done })
    expect(detail.result.current).toBe(false)
  })
})
