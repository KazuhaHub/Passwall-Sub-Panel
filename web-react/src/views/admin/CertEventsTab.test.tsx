// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import CertEventsTab from './CertEventsTab'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/i18n', () => ({ default: { t: (k: string) => k, language: 'zh-CN' } }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_k: string, o?: { defaultValue?: string }) => o?.defaultValue ?? _k,
    i18n: { language: 'zh-CN' },
  }),
}))

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

function mount() {
  render(
    <ThemeProvider theme={theme}>
      <CertEventsTab />
    </ThemeProvider>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

beforeEach(() => vi.clearAllMocks())
afterEach(cleanup)

describe('CertEventsTab', () => {
  it('reports a failed read instead of showing an empty certificate log', async () => {
    // The loader swallowed the error and rendered the empty state, so a failed
    // read looked identical to "this panel has issued no certificates".
    api.get.mockRejectedValue(new Error('offline'))
    mount()

    await waitFor(() => expect(screen.getByText('暂时无法加载证书日志')).toBeTruthy())
  })

  it('still shows the empty state for a successful empty read', async () => {
    api.get.mockResolvedValue({ data: { events: [], total: 0 } })
    mount()

    await waitFor(() => expect(screen.getByText('—')).toBeTruthy())
    expect(screen.queryByText('暂时无法加载证书日志')).toBeNull()
  })
})
