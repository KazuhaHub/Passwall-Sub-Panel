// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { MemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import { makeTestQueryClient, queryWrapper } from '@/test/queryTestUtils'
import { useAuthStore } from '@/stores/auth'
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

beforeEach(() => {
  vi.clearAllMocks()
  useAuthStore.setState({ role: 'admin', userId: 1, hasToken: true })
})
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

  it('warns when bound Mihomo sub-rules have no template placeholder', async () => {
    const template = {
      slug: 'custom-mihomo', name: 'Custom Mihomo', client_type: 'mihomo', is_default: false,
      rule_sets: ['advanced'], proxy_group_order: [], content: 'rules:\n  {{ rules_common }}',
    }
    const rule = {
      slug: 'advanced', name: 'Advanced', sort: 1, enabled: true,
      direct_subscription_domain: false, proxy_group_order: [], content: '- MATCH,DIRECT',
      mihomo_sub_rules: [{ name: 'ai-rules', content: '- MATCH,DIRECT' }],
    }
    api.get.mockImplementation(async (url: string) => {
      if (url === '/admin/templates') return { data: { items: [template], total: 1, page: 1, page_size: 25 } }
      if (url === '/admin/rules') return { data: { items: [rule], total: 1, page: 1, page_size: 25 } }
      throw new Error(`Unexpected GET ${url}`)
    })
    mount()

    const row = (await screen.findByText('Custom Mihomo')).closest('tr')!
    fireEvent.click(within(row).getByTestId('EditOutlinedIcon').closest('button')!)
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('admin:templates.hint.missing_mihomo_sub_rules')).toBeTruthy()
  })
})
