/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import AppRouter from '@/router/AppRouter'
import { useAuthStore } from '@/stores/auth'
import { destinationPolicies, destinationStatus } from '@/test/accessControlFixtures'
import type { DestinationListDetail, DestinationListPreview, DestinationListSummary } from '@/api/accessControl'
const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }))
const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn() }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en-US' } }) }))
vi.mock('@/components/CodeEditor', () => ({ default: (p: { value: string; onChange: (s: string) => void; ariaLabel: string }) => <textarea aria-label={p.ariaLabel} value={p.value} onChange={e => p.onChange(e.target.value)} /> }))
import ListDialog from './ListDialog'
const P = 'admin:access_control.list_editor.'
const report = { accepted: 1, ignored: 1, ignored_broad: 1, rewritten: 1, samples: [{ line: 2, text: '*.Example.COM.', reason: 'normalized', normalized: 'domain:example.com' }, { line: 3, text: 'regexp:.*', reason: 'broad_entry' }] }
const preview: DestinationListPreview = { content_sha256: 'full-one', entry_count: 1, regexp_count: 0, entries: ['domain:example.com'], parse_report: report, http_status: 200, bytes: 80 }
const detail: DestinationListDetail = { id: 7, name: 'Original', kind: 'custom', source_url: '', geosite_category: '', geosite_attrs: '', owner_group_id: 0, updated_at: 3000, entry_count: 1, regexp_count: 0, state: 'ready', last_fetched_at: null, last_error: '', content_sha256: 'full-one', entries: ['domain:example.com'], source_text: '# comment\n*.Example.COM.\nregexp:.*', parse_report: report }
const summary: DestinationListSummary = { ...detail, parse_report_summary: report, used_by: [{ kind: 'policy', id: 12, name: 'No mail' }] }
function mount(existing?: DestinationListSummary) {
  const saved = vi.fn(), close = vi.fn()
  const query = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const router = createMemoryRouter([{ path: '/', element: <ListDialog existing={existing} policies={destinationPolicies({ block: [{ ...destinationPolicies().block[0], list_ids: [7] }] })} status={destinationStatus()} refreshHours={12} onClose={close} onSaved={saved} /> }])
  render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}><QueryClientProvider client={query}><AppRouter router={router} /></QueryClientProvider></ThemeProvider>)
  return { saved, close, query }
}
beforeEach(() => {
  vi.clearAllMocks(); useAuthStore.setState({ userId: 42, role: 'admin', authEpoch: 5 })
  api.get.mockResolvedValue({ data: detail }); api.post.mockResolvedValue({ data: preview }); api.put.mockResolvedValue({ data: detail }); confirmation.mockResolvedValue(true)
})
afterEach(cleanup)
it.each([
  ['custom', 'remote'], ['custom', 'geosite'], ['remote', 'custom'],
  ['remote', 'geosite'], ['geosite', 'custom'], ['geosite', 'remote'],
] as const)('switches an empty %s source to %s without confirmation and preserves its name', async (from, to) => {
  api.get.mockResolvedValue({ data: { categories: [], updated_at: 1000, refreshing: false } })
  mount()
  fireEvent.change(screen.getByRole('textbox', { name: `${P}name` }), { target: { value: 'Named draft' } })
  if (from !== 'custom') {
    fireEvent.click(screen.getByRole('button', { name: `admin:access_control.lists.${from}` }))
    await waitFor(() => expect(screen.getByRole('button', { name: `admin:access_control.lists.${from}` }).getAttribute('aria-pressed')).toBe('true'))
  }
  confirmation.mockClear()
  fireEvent.click(screen.getByRole('button', { name: `admin:access_control.lists.${to}` }))
  await waitFor(() => expect(screen.getByRole('button', { name: `admin:access_control.lists.${to}` }).getAttribute('aria-pressed')).toBe('true'))
  expect(confirmation).not.toHaveBeenCalled()
  expect((screen.getByRole('textbox', { name: `${P}name` }) as HTMLInputElement).value).toBe('Named draft')
  expect(api.put).not.toHaveBeenCalled()
})
it.each(['custom', 'remote'] as const)('preserves a filled %s source when type switching is declined, then clears only the source on acceptance', async kind => {
  mount()
  fireEvent.change(screen.getByRole('textbox', { name: `${P}name` }), { target: { value: 'Preserved draft' } })
  if (kind === 'remote') fireEvent.click(screen.getByRole('button', { name: 'admin:access_control.lists.remote' }))
  const sourceName = `${P}${kind === 'custom' ? 'text' : 'url'}`
  const source = await screen.findByRole('textbox', { name: sourceName })
  const text = kind === 'custom' ? 'domain:example.com' : 'https://example.org/list'
  fireEvent.change(source, { target: { value: text } })
  confirmation.mockResolvedValueOnce(false)
  const target = kind === 'custom' ? 'remote' : 'custom'
  fireEvent.click(screen.getByRole('button', { name: `admin:access_control.lists.${target}` }))
  await waitFor(() => expect(confirmation).toHaveBeenCalledOnce())
  await waitFor(() => expect(screen.getByRole('button', { name: `admin:access_control.lists.${target}` }).hasAttribute('disabled')).toBe(false))
  expect((screen.getByRole('textbox', { name: sourceName }) as HTMLInputElement).value).toBe(text)
  expect(screen.getByRole('button', { name: `admin:access_control.lists.${kind}` }).getAttribute('aria-pressed')).toBe('true')
  fireEvent.click(screen.getByRole('button', { name: `admin:access_control.lists.${target}` }))
  await waitFor(() => expect(screen.getByRole('button', { name: `admin:access_control.lists.${target}` }).getAttribute('aria-pressed')).toBe('true'))
  expect(confirmation).toHaveBeenCalledTimes(2)
  expect((screen.getByRole('textbox', { name: `${P}${target === 'custom' ? 'text' : 'url'}` }) as HTMLInputElement).value).toBe('')
  expect((screen.getByRole('textbox', { name: `${P}name` }) as HTMLInputElement).value).toBe('Preserved draft')
  expect(api.put).not.toHaveBeenCalled()
})
it('keeps a title-only editor name and touch-sized draft, report and save actions', async () => {
  mount()
  await screen.findByRole('dialog', { name: `${P}create_title` })
  expect(screen.getByRole('group', { name: `${P}type` })).toBeTruthy()
  await screen.findAllByRole('button', { name: 'admin:access_control.parse_report.line' })
  for (const button of screen.getAllByRole('button')) expect(parseFloat(getComputedStyle(button).minHeight)).toBeGreaterThanOrEqual(44)
})
it('keeps explicit remote fetch touch-accessible without an automatic network preview', async () => {
  mount()
  fireEvent.click(screen.getByRole('button', { name: 'admin:access_control.lists.remote' }))
  const fetch = screen.getByRole('button', { name: `${P}test_fetch` })
  expect(parseFloat(getComputedStyle(fetch).minHeight)).toBeGreaterThanOrEqual(44)
  expect(api.post).not.toHaveBeenCalled()
})
it('uses a touch-sized explicit reload after a failed original read, with no write', async () => {
  api.get.mockRejectedValue({ isAxiosError: true, response: { status: 503, data: { error: 'dest_list_fetch_failed' } } })
  mount(summary)
  const reload = await screen.findByRole('button', { name: `${P}reload` })
  expect(parseFloat(getComputedStyle(reload).minHeight)).toBeGreaterThanOrEqual(44)
  expect(screen.getByText(`${P}read_error`)).toBeTruthy()
  expect(screen.queryByText(`${P}dest_list_fetch_failed`)).toBeNull()
  expect(document.querySelector('.MuiSkeleton-root')).toBeNull()
  expect((screen.getByRole('button', { name: 'common:actions.save' }) as HTMLButtonElement).disabled).toBe(true)
  expect(api.post).not.toHaveBeenCalled()
  let finishRead!: (value: { data: DestinationListDetail }) => void
  api.get.mockReturnValueOnce(new Promise(resolve => { finishRead = resolve }))
  fireEvent.click(reload)
  await waitFor(() => expect(reload.getAttribute('aria-busy')).toBe('true'))
  expect((screen.getByRole('button', { name: 'common:actions.save' }) as HTMLButtonElement).disabled).toBe(true)
  fireEvent.click(reload)
  expect(api.get).toHaveBeenCalledTimes(2)
  finishRead({ data: detail })
  await waitFor(() => expect((screen.getByRole('textbox', { name: `${P}text` }) as HTMLTextAreaElement).value).toBe(detail.source_text))
  expect((screen.getByRole('textbox', { name: `${P}name` }) as HTMLInputElement).value).toBe(detail.name)
  expect(screen.queryByText(`${P}dest_list_fetch_failed`)).toBeNull()
  expect(screen.queryByText(`${P}read_error`)).toBeNull()
  expect(api.get).toHaveBeenCalledTimes(2)
  expect(api.put).not.toHaveBeenCalled()
})
it('loads preserved original text, exposes normalization and saves with the edit version while excluding broad lines', async () => {
  const { saved } = mount(summary)
  const source = await screen.findByRole('textbox', { name: `${P}text` })
  await waitFor(() => expect((source as HTMLTextAreaElement).value).toBe(detail.source_text))
  expect(api.get).toHaveBeenCalledWith('/admin/dest/lists/7', expect.objectContaining({ params: { text: 1 } }))
  fireEvent.change(screen.getByRole('textbox', { name: `${P}name` }), { target: { value: 'Renamed' } })
  await screen.findByText('domain:example.com')
  await screen.findByText('admin:access_control.parse_report.broad_removed')
  expect(screen.getByText(`${P}impact_unchanged`)).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.save' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledWith('/admin/dest/lists/7', { name: 'Renamed', kind: 'custom', text: detail.source_text, updated_at: 3000 }, { _skipErrorToast: true }))
  expect(saved).toHaveBeenCalledWith(detail)
})
it('never previews a remote input automatically and blocks a known broad result', async () => {
  mount(); fireEvent.click(screen.getByRole('button', { name: 'admin:access_control.lists.remote' }))
  fireEvent.change(screen.getByRole('textbox', { name: `${P}name` }), { target: { value: 'Remote' } })
  fireEvent.change(screen.getByRole('textbox', { name: `${P}url` }), { target: { value: 'https://example.org/list' } })
  await new Promise(resolve => setTimeout(resolve, 650))
  expect(api.post).not.toHaveBeenCalled()
  api.post.mockRejectedValue({ isAxiosError: true, response: { status: 400, data: { error: 'dest_list_too_broad', bad: [{ line: 2, entry: 'regexp:.*' }] } } })
  fireEvent.click(screen.getByRole('button', { name: `${P}test_fetch` }))
  await screen.findByText(`${P}dest_list_too_broad`)
  await screen.findByText(/regexp:\.\*/)
  expect((screen.getByRole('button', { name: 'common:actions.save' }) as HTMLButtonElement).disabled).toBe(true)
})
it('shows the fetch status and keeps save available after a temporary download failure', async () => {
  mount(); fireEvent.click(screen.getByRole('button', { name: 'admin:access_control.lists.remote' }))
  fireEvent.change(screen.getByRole('textbox', { name: `${P}name` }), { target: { value: 'Remote' } })
  fireEvent.change(screen.getByRole('textbox', { name: `${P}url` }), { target: { value: 'https://example.org/list' } })
  api.post.mockRejectedValueOnce({ isAxiosError: true, response: { status: 503, data: { error: 'dest_list_fetch_failed', http_status: 404 } } })
  fireEvent.click(screen.getByRole('button', { name: `${P}test_fetch` }))
  await screen.findByText('HTTP 404')
  expect((screen.getByRole('button', { name: 'common:actions.save' }) as HTMLButtonElement).disabled).toBe(false)
})
it('preserves a stale draft until explicit reload chooses the latest version', async () => {
  mount(summary)
  const name = await screen.findByRole('textbox', { name: `${P}name` })
  await screen.findByRole('textbox', { name: `${P}text` })
  fireEvent.change(name, { target: { value: 'Draft' } })
  api.put.mockRejectedValueOnce({ isAxiosError: true, response: { status: 409, data: { error: 'dest_list_stale' } } })
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.save' }))
  const reload = await screen.findByRole('button', { name: `${P}reload` })
  expect((name as HTMLInputElement).value).toBe('Draft')
  api.get.mockResolvedValueOnce({ data: { ...detail, name: 'Latest', updated_at: 4000 } })
  fireEvent.click(reload)
  await waitFor(() => expect((name as HTMLInputElement).value).toBe('Latest'))
})
it('loads categories only when selected and does not download missing data automatically', async () => {
  api.get.mockRejectedValue({ isAxiosError: true, response: { status: 503, data: { error: 'dest_geosite_unavailable' } } })
  mount(); expect(api.get).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'admin:access_control.lists.geosite' }))
  await screen.findByText('admin:access_control.categories.missing')
  expect(parseFloat(getComputedStyle(screen.getByRole('button', { name: 'admin:access_control.categories.download' })).minHeight)).toBeGreaterThanOrEqual(44)
  expect(api.post).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'admin:access_control.categories.download' }))
  await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/dest/geosite/refresh', undefined, { _skipErrorToast: true }))
})
