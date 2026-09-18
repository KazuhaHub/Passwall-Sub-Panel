// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter } from 'react-router'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import MeView from './MeView'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn(), default: () => null }))
vi.mock('@/components/ConfirmHost', () => ({ confirm: vi.fn(async () => true) }))
// The real module initializes i18next on import, which rejects in a jsdom test
// without its full setup and surfaces as an unhandled error.
vi.mock('@/i18n', () => ({ default: { t: (k: string) => k, language: 'en-US' } }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_k: string, o?: { defaultValue?: string }) => o?.defaultValue ?? _k,
    i18n: { language: 'en-US' },
  }),
}))

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

function mount() {
  render(
    <MemoryRouter>
      <ThemeProvider theme={theme}>
        <MeView />
      </ThemeProvider>
    </MemoryRouter>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

beforeEach(() => vi.clearAllMocks())
afterEach(cleanup)

describe('MeView', () => {
  it('explains itself when the profile read fails instead of rendering a blank page', async () => {
    // The page used to `return null` when the profile was missing, and the
    // loader had no catch — so a failed read produced a completely blank
    // screen with no spinner, no message and nothing to retry.
    api.get.mockRejectedValue(new Error('boom'))
    mount()

    await waitFor(() => expect(screen.getByText('暂时无法加载，请稍后重试')).toBeTruthy())
  })
})
