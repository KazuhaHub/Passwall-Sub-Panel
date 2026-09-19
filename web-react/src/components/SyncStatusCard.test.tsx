// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import SyncStatusCard from './SyncStatusCard'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/i18n', () => ({ default: { t: (k: string) => k, language: 'zh-CN' } }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_k: string, o?: { defaultValue?: string; count?: number }) =>
      (o?.defaultValue ?? _k).replace('{{count}}', String(o?.count ?? '')),
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

function mount(userId = 7) {
  render(
    <ThemeProvider theme={theme}>
      <SyncStatusCard userId={userId} />
    </ThemeProvider>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

beforeEach(() => vi.clearAllMocks())
afterEach(cleanup)

describe('SyncStatusCard', () => {
  it('says nothing is pending, not that upstream is in sync', async () => {
    api.get.mockResolvedValue({ data: statusBody() })
    mount()

    await waitFor(() => expect(screen.getByText('当前未发现待处理任务')).toBeTruthy())
    // The card must never assert an upstream verdict.
    expect(screen.queryByText(/已同步|同步成功|upstream/)).toBeNull()
  })

  it('lists active tasks and surfaces that an error was recorded', async () => {
    api.get.mockResolvedValue({
      data: statusBody({
        state: 'active_tasks',
        active_tasks: [{
          id: 1, type: 'user_resync', status: 'pending', attempts: 3,
          next_run_at: '2026-09-18T12:00:00Z', created_at: '2026-09-18T11:00:00Z',
          updated_at: '2026-09-18T11:30:00Z', has_error: true,
        }],
      }),
    })
    mount()

    await waitFor(() => expect(screen.getByText('user_resync')).toBeTruthy())
    expect(screen.getByText('重试 3 次')).toBeTruthy()
    expect(screen.getByText('有错误记录')).toBeTruthy()
  })

  it('reports an unknown state rather than an empty queue when the read fails', async () => {
    api.get.mockRejectedValue(new Error('offline'))
    mount()

    await waitFor(() => expect(screen.getByText('同步状态暂时未知')).toBeTruthy())
    // "Nothing pending" is a claim the failed read cannot support.
    expect(screen.queryByText('当前未发现待处理任务')).toBeNull()
  })
})
