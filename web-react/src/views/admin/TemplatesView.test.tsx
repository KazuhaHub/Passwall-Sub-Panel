// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter } from 'react-router'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import TemplatesView from './TemplatesView'

const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/i18n', () => ({ default: { t: (k: string) => k, language: 'zh-CN' } }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn(), default: () => null }))
vi.mock('@/components/ConfirmHost', () => ({ confirm: vi.fn(async () => true) }))
// The YAML editor pulls in CodeMirror, which is irrelevant here.
vi.mock('@/components/CodeEditor', () => ({
  default: ({ value, onChange }: { value: string; onChange: (v: string) => void }) =>
    <textarea aria-label="code" value={value} onChange={e => onChange(e.target.value)} />,
}))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_k: string, o?: { defaultValue?: string }) => o?.defaultValue ?? _k,
    i18n: { language: 'zh-CN' },
  }),
}))

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })

function mount() {
  render(
    <MemoryRouter>
      <ThemeProvider theme={theme}>
        <TemplatesView />
      </ThemeProvider>
    </MemoryRouter>,
    { wrapper: queryWrapper(makeTestQueryClient()) },
  )
}

beforeEach(() => vi.clearAllMocks())
afterEach(cleanup)

describe('TemplatesView', () => {
  it('reports a failed read instead of showing an empty template list', async () => {
    // The loader had no catch on two parallel reads, so a failure raised an
    // unhandled rejection and rendered an empty table — indistinguishable from
    // "no templates are configured".
    api.get.mockRejectedValue(new Error('offline'))
    mount()

    await waitFor(() => expect(screen.getByText('暂时无法加载配置方案')).toBeTruthy())
  })
})
