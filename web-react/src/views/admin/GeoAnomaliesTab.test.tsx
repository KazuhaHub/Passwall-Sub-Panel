/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material/styles'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import GeoAnomaliesTab, { stateColor } from './GeoAnomaliesTab'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
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
      <GeoAnomaliesTab />
    </ThemeProvider>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

beforeEach(() => vi.clearAllMocks())
afterEach(cleanup)

// Colour is the first thing an operator reads on this table, so it has to
// track what they should DO rather than how alarming the word sounds.
describe('stateColor', () => {
  // Only flagged is actionable, and it must be the only one that looks like an
  // emergency — otherwise the ramp and the verdict are indistinguishable.
  it('reserves the alarm colour for the one actionable state', () => {
    expect(stateColor('flagged')).toBe('error')
    for (const s of ['suspect', 'clean', 'unknown', 'idle', 'exempt', 'disabled'] as const) {
      expect(stateColor(s)).not.toBe('error')
    }
  })

  // The single most important rule here. "Cannot tell" on a fleet whose geo
  // database has quietly stopped working looks EXACTLY like a clean fleet if
  // it is coloured like one — a detector that has stopped detecting would read
  // as a fleet with nobody sharing.
  it('never colours a non-verdict as clean', () => {
    for (const s of ['unknown', 'exempt', 'disabled', 'idle'] as const) {
      expect(stateColor(s)).not.toBe('success')
    }
    expect(stateColor('clean')).toBe('success')
  })

  // Suspect is the visible ramp: distinct from both a flag and a clean row, so
  // the eventual flag does not appear out of nowhere.
  it('gives the ramp its own colour', () => {
    const suspect = stateColor('suspect')
    expect(suspect).not.toBe(stateColor('flagged'))
    expect(suspect).not.toBe(stateColor('clean'))
  })
})

describe('GeoAnomaliesTab read states', () => {
  it('reports an unwired detector as its own message, not as an empty table', async () => {
    // 503 means the detector is not running in this build. Rendering that as
    // "no anomalies yet" would describe a monitored fleet when nothing is
    // being monitored at all.
    api.get.mockRejectedValue({ isAxiosError: true, response: { status: 503 } })
    mount()

    await waitFor(() => expect(screen.getByText('本部署未启用异地并发检测。')).toBeTruthy())
    expect(screen.queryByText(/还没有判定记录/)).toBeNull()
  })

  it('shows the empty state only for a successful empty read', async () => {
    api.get.mockResolvedValue({ data: { items: [] } })
    mount()

    await waitFor(() => expect(screen.getByText(/还没有判定记录/)).toBeTruthy())
    expect(screen.queryByText('本部署未启用异地并发检测。')).toBeNull()
  })
})
