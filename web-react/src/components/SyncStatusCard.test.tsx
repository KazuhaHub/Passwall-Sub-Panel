// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { WATCH_BUDGET_MS, WATCH_INTERVAL_MS } from '@/query/syncStatus'
import { announcePending } from '@/api/syncPending'
import SyncStatusCard from './SyncStatusCard'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/i18n', () => ({ default: { t: (k: string) => k, language: 'zh-CN' } }))
vi.mock('@/stores/site', () => ({
  useSiteStore: (sel: (s: { timezone: string }) => unknown) => sel({ timezone: 'UTC' }),
}))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    // Interpolate every placeholder from the options, so a default template can
    // be asserted on its shape rather than on a stubbed-away value.
    t: (k: string, o?: Record<string, unknown>) =>
      ((o?.defaultValue as string) ?? k).replace(/\{\{(\w+)\}\}/g, (_m, name: string) =>
        String(o?.[name] ?? '')),
    i18n: { language: 'zh-CN' },
  }),
}))

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

function statusBody(over: Record<string, unknown> = {}) {
  return {
    target_type: 'user', target_id: 7, target_exists: true,
    observed_at: '2026-09-18T12:00:00Z',
    covered_task_types: ['user_delete', 'user_resync', 'user_push_config', 'user_migrate'],
    state: 'no_active_tasks',
    active_tasks: [], active_tasks_truncated: false,
    recent_terminal_tasks: [], history_truncated: false,
    history_scope: 'retained_only',
    ...over,
  }
}

const pendingTask = {
  id: 1, type: 'user_resync', status: 'pending', attempts: 3,
  next_run_at: '2026-09-18T12:00:00Z', created_at: '2026-09-18T11:00:00Z',
  updated_at: '2026-09-18T11:30:00Z', has_error: true,
}

function mount(userId = 7) {
  render(
    <ThemeProvider theme={theme}>
      <SyncStatusCard userId={userId} />
    </ThemeProvider>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

beforeEach(() => vi.clearAllMocks())
afterEach(() => {
  cleanup()
  vi.useRealTimers()
})

describe('SyncStatusCard', () => {
  it('says nothing is queued, not that upstream is in sync', async () => {
    api.get.mockResolvedValue({ data: statusBody() })
    mount()

    await waitFor(() => expect(screen.getByText('当前未发现待处理任务')).toBeTruthy())
    // The card must never assert an upstream verdict.
    expect(screen.queryByText(/已同步|同步成功|upstream/)).toBeNull()
  })

  it('lists active tasks with their shared labels, attempts and error indicator', async () => {
    api.get.mockResolvedValue({
      data: statusBody({ state: 'active_tasks', active_tasks: [pendingTask] }),
    })
    mount()

    // Labels come from the sync-task vocabulary, so one task reads the same way
    // here and on the Sync tasks page.
    await waitFor(() => expect(screen.getByText('user_resync')).toBeTruthy())
    expect(screen.getByText('重试 3 次')).toBeTruthy()
    expect(screen.getByText('有执行错误记录，任务待重试')).toBeTruthy()
  })

  it('reports an unknown state rather than an empty queue when the read fails', async () => {
    api.get.mockRejectedValue(new Error('offline'))
    mount()

    await waitFor(() => expect(screen.getByText('同步状态暂时未知')).toBeTruthy())
    // "Nothing pending" is a claim the failed read cannot support.
    expect(screen.queryByText('当前未发现待处理任务')).toBeNull()
  })

  it('keeps a retained snapshot but marks it historical when a later read fails', async () => {
    api.get.mockResolvedValueOnce({
      data: statusBody({ state: 'active_tasks', active_tasks: [pendingTask] }),
    })
    mount()
    await waitFor(() => expect(screen.getByText('user_resync')).toBeTruthy())

    api.get.mockRejectedValue(new Error('offline'))
    fireEvent.click(screen.getByRole('button', { name: '刷新' }))

    // Not "unknown" (there IS a snapshot) and not silently current either.
    await waitFor(() =>
      expect(screen.getByText('读取失败，以下为上次观察结果，可能已经变化')).toBeTruthy())
    expect(screen.getByText('user_resync')).toBeTruthy()
    expect(screen.getByText(/最后观察时间：/)).toBeTruthy()
    expect(screen.queryByText('同步状态暂时未知')).toBeNull()
  })

  it('stops the automatic refresh at the budget and says the result is historical', async () => {
    vi.useFakeTimers()
    api.get.mockResolvedValue({
      data: statusBody({ state: 'active_tasks', active_tasks: [pendingTask] }),
    })
    mount()

    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByText('user_resync')).toBeTruthy()

    // One full budget, during which the interval is expected to keep reading.
    const readsBefore = api.get.mock.calls.length
    await act(async () => { await vi.advanceTimersByTimeAsync(WATCH_BUDGET_MS) })
    expect(api.get.mock.calls.length).toBeGreaterThan(readsBefore)

    expect(screen.getByText('自动刷新已暂停，以下为上次观察结果，可能已经变化')).toBeTruthy()
    // Paused is not settled: the task is still on screen, and still unconfirmed.
    expect(screen.getByText('user_resync')).toBeTruthy()

    // Nothing new may be read once the window is closed.
    const readsAtPause = api.get.mock.calls.length
    await act(async () => { await vi.advanceTimersByTimeAsync(WATCH_BUDGET_MS) })
    expect(api.get.mock.calls.length).toBe(readsAtPause)
  })

  it('restarts the observation window from the refresh button', async () => {
    vi.useFakeTimers()
    api.get.mockResolvedValue({
      data: statusBody({ state: 'active_tasks', active_tasks: [pendingTask] }),
    })
    mount()
    await act(async () => { await vi.advanceTimersByTimeAsync(WATCH_BUDGET_MS) })
    expect(screen.getByText('自动刷新已暂停，以下为上次观察结果，可能已经变化')).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: '刷新' }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })

    expect(screen.queryByText('自动刷新已暂停，以下为上次观察结果，可能已经变化')).toBeNull()
  })

  it('gives up on a refusal, and keeps none of the snapshot it had', async () => {
    vi.useFakeTimers()
    // A successful read first, so there IS a snapshot to wrongly keep.
    api.get.mockResolvedValueOnce({
      data: statusBody({ state: 'active_tasks', active_tasks: [pendingTask] }),
    })
    mount()
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByText('user_resync')).toBeTruthy()

    // The target then becomes unreadable: 403 (not permitted) / 404 (gone).
    // Neither will change on its own, so neither is worth another 20 reads.
    api.get.mockRejectedValue({ response: { status: 403, data: { error: 'Forbidden' } } })
    fireEvent.click(screen.getByRole('button', { name: '刷新' }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })

    expect(screen.getByText('目标已不可查询')).toBeTruthy()
    // The snapshot was fetched under a permission the caller no longer has.
    expect(screen.queryByText('user_resync')).toBeNull()
    expect(screen.queryByText('同步状态暂时未知')).toBeNull()

    const readsAfterRefusal = api.get.mock.calls.length
    await act(async () => { await vi.advanceTimersByTimeAsync(WATCH_BUDGET_MS) })
    expect(api.get.mock.calls.length).toBe(readsAfterRefusal)
  })

  it('keeps waiting through a failure the next round might answer', async () => {
    vi.useFakeTimers()
    // Active tasks, so there is something left to watch after the failure.
    api.get.mockResolvedValueOnce({
      data: statusBody({ state: 'active_tasks', active_tasks: [pendingTask] }),
    })
    mount()
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })

    // A 503 is "cannot answer now", not "will never answer". That difference
    // from a 403 is the whole reason the two are separate branches.
    api.get.mockRejectedValue({ response: { status: 503, data: { code: 'sync_status_unavailable' } } })
    fireEvent.click(screen.getByRole('button', { name: '刷新' }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })

    expect(screen.queryByText('目标已不可查询')).toBeNull()
    expect(screen.getByText('读取失败，以下为上次观察结果，可能已经变化')).toBeTruthy()

    // The window is still open, so another round is attempted.
    const reads = api.get.mock.calls.length
    await act(async () => { await vi.advanceTimersByTimeAsync(WATCH_INTERVAL_MS) })
    expect(api.get.mock.calls.length).toBeGreaterThan(reads)
  })

  it('opens a new window when a write queues work for this user', async () => {
    vi.useFakeTimers()
    api.get.mockResolvedValue({
      data: statusBody({ state: 'active_tasks', active_tasks: [pendingTask] }),
    })
    mount()
    await act(async () => { await vi.advanceTimersByTimeAsync(WATCH_BUDGET_MS) })
    expect(screen.getByText('自动刷新已暂停，以下为上次观察结果，可能已经变化')).toBeTruthy()

    // A save in this dialog queued upstream work. The area is the thing that
    // can show it, so it must pick the work up rather than stay paused until
    // the admin closes and reopens the dialog.
    await act(async () => { announcePending('/admin/users/7/set-enabled') })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })

    expect(screen.queryByText('自动刷新已暂停，以下为上次观察结果，可能已经变化')).toBeNull()
  })

  it('ignores a write queued for somebody else', async () => {
    vi.useFakeTimers()
    api.get.mockResolvedValue({
      data: statusBody({ state: 'active_tasks', active_tasks: [pendingTask] }),
    })
    mount()
    await act(async () => { await vi.advanceTimersByTimeAsync(WATCH_BUDGET_MS) })

    await act(async () => { announcePending('/admin/users/8/set-enabled') })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })

    expect(screen.getByText('自动刷新已暂停，以下为上次观察结果，可能已经变化')).toBeTruthy()
  })
})
