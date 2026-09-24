// @vitest-environment jsdom
import { act, cleanup, render, screen } from '@testing-library/react'
import type { AxiosAdapter, InternalAxiosRequestConfig } from 'axios'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { trackWrites, useWriteInProgress } from './requestProgress'
import RequestProgressBar from '@/components/RequestProgressBar'

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }))

function deferredAdapter() {
  const calls: Array<{ resolve: () => void; reject: (e: unknown) => void }> = []
  const adapter: AxiosAdapter = () => new Promise((resolve, reject) => {
    calls.push({ resolve: () => resolve({ data: {}, status: 200, statusText: 'OK', headers: {}, config: {} as InternalAxiosRequestConfig }), reject })
  })
  return { adapter, calls }
}

const request = (method: string, extra: Partial<InternalAxiosRequestConfig> = {}) =>
  ({ method, url: '/x', headers: {}, ...extra }) as InternalAxiosRequestConfig

function Probe() {
  return <span data-testid="busy">{String(useWriteInProgress())}</span>
}

describe('write progress tracking', () => {
  afterEach(() => { cleanup(); vi.useRealTimers() })

  // A WRITE IS WHAT THE OPERATOR IS WAITING ON. Reads include every background
  // poll on the page, and a bar that tracked them would never go away.
  it('counts writes while they are on the wire, and never reads', async () => {
    const { adapter, calls } = deferredAdapter()
    const tracked = trackWrites(adapter)
    render(<Probe />)
    const read = tracked(request('get'))
    expect(screen.getByTestId('busy').textContent).toBe('false')

    const save = tracked(request('post'))
    const remove = tracked(request('delete'))
    await act(async () => {})
    expect(screen.getByTestId('busy').textContent).toBe('true')

    await act(async () => { calls[1].resolve(); await save })
    expect(screen.getByTestId('busy').textContent).toBe('true')
    // A failed write ends the wait just the same.
    await act(async () => { calls[2].reject(new Error('500')); await remove.catch(() => {}) })
    expect(screen.getByTestId('busy').textContent).toBe('false')
    calls[0].resolve(); await read
  })

  it('lets a caller opt a background write out, and a user-started read in', async () => {
    const { adapter, calls } = deferredAdapter()
    const tracked = trackWrites(adapter)
    render(<Probe />)
    const quiet = tracked(request('post', { _skipProgress: true }))
    await act(async () => {})
    expect(screen.getByTestId('busy').textContent).toBe('false')
    const probe = tracked(request('get', { _trackProgress: true }))
    await act(async () => {})
    expect(screen.getByTestId('busy').textContent).toBe('true')
    await act(async () => { calls[0].resolve(); calls[1].resolve(); await Promise.all([quiet, probe]) })
    expect(screen.getByTestId('busy').textContent).toBe('false')
  })

  // ON A FAST LINK THE BAR NEVER APPEARS: a write that answers inside the delay
  // shows nothing, so the bar is news only when there is a wait.
  it('shows the bar only once a write has been pending past the delay', async () => {
    vi.useFakeTimers()
    const { adapter, calls } = deferredAdapter()
    const tracked = trackWrites(adapter)
    render(<RequestProgressBar />)
    const fast = tracked(request('put'))
    await act(async () => {})
    await act(async () => { vi.advanceTimersByTime(100) })
    await act(async () => { calls[0].resolve(); await fast })
    await act(async () => { vi.advanceTimersByTime(500) })
    expect(screen.queryByRole('progressbar')).toBeNull()

    const slow = tracked(request('put'))
    // Let the store update render first, so the delay timer is armed.
    await act(async () => {})
    await act(async () => { vi.advanceTimersByTime(300) })
    expect(screen.getByRole('progressbar', { name: 'common:status.working' })).toBeTruthy()
    await act(async () => { calls[1].resolve(); await slow })
    expect(screen.queryByRole('progressbar')).toBeNull()
  })
})
