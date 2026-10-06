/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import AppRouter from '@/router/AppRouter'
import { createAppTheme } from '@/theme'
import { useAuthStore } from '@/stores/auth'
import { destinationStatus } from '@/test/accessControlFixtures'
import type { DestinationTestResult } from '@/api/accessControl'
const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn() }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string, args?: Record<string, unknown>) => key + (args?.name ? ` ${args.name}` : '') }) }))
vi.mock('@/components/UserAutocomplete', () => ({ default: (p: { value: number | null; onChange: (id: number | null) => void; label: string }) => <input aria-label={p.label} value={p.value ?? ''} onChange={e => p.onChange(Number(e.target.value) || null)} /> }))
import TestSheet from './TestSheet'
const P = 'admin:access_control.test.'
const result: DestinationTestResult = { verdict: 'block', terminating_step: 'block', steps: [{ step: 'allow', result: 'miss' }, { step: 'exemption', result: 'n/a' }, { step: 'block', result: 'hit', name: 'No mail', policy_id: 12, entry: 'full:example.test' }, { step: 'observe', result: 'shadowed', name: 'Watch', policy_id: 15 }, { step: 'direct', result: 'skipped' }], notes: ['destination_only_preview'], unpublished: false, nodes: [{ panel_id: 1, name: 'Tokyo', state: 'offline' }] }
beforeEach(() => { vi.clearAllMocks(); useAuthStore.setState({ userId: 42, role: 'admin', authEpoch: 5 }); api.post.mockResolvedValue({ data: result }) })
afterEach(() => cleanup())
function mount(prefill?: { target?: string; port?: number; network?: string; userId?: number }) {
  const onClose = vi.fn(), onOpenPolicy = vi.fn(), client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const router = createMemoryRouter([{ path: '/admin/access-control', element: <TestSheet onClose={onClose} onOpenPolicy={onOpenPolicy} status={destinationStatus()} prefill={prefill} /> }], { initialEntries: ['/admin/access-control?sheet=test'] })
  const rendered = render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}><QueryClientProvider client={client}><AppRouter router={router} /></QueryClientProvider></ThemeProvider>)
  return { onClose, onOpenPolicy, client, router, ...rendered }
}
function submit(target = 'example.test') { fireEvent.change(screen.getByRole('textbox', { name: `${P}target` }), { target: { value: target } }); fireEvent.click(screen.getByRole('button', { name: `${P}submit` })) }
it('opens with state-only prefill and never tests on open, focus, editing or query invalidation', async () => {
  const { client, router } = mount({ target: 'secret.example.test', port: 587, network: 'udp', userId: 13 })
  expect((screen.getByRole('textbox', { name: `${P}target` }) as HTMLInputElement).value).toBe('secret.example.test')
  fireEvent.change(screen.getByRole('textbox', { name: `${P}target` }), { target: { value: 'other.test' } })
  window.dispatchEvent(new Event('focus')); await client.invalidateQueries()
  expect(api.post).not.toHaveBeenCalled(); expect(router.state.location.search).toBe('?sheet=test')
})
it('submits the normalized host through POST and renders exact trace and node state', async () => {
  const { onOpenPolicy } = mount(); submit('https://EXAMPLE.test/path?secret=yes')
  await screen.findByText(`${P}result_shadowed`)
  expect(api.post).toHaveBeenCalledWith('/admin/dest/test', { target: 'example.test', port: 443, network: 'tcp' }, expect.objectContaining({ _skipErrorToast: true, signal: expect.any(AbortSignal) }))
  expect(screen.getByText(`${P}url_host_only`)).toBeTruthy(); expect(screen.getByText(`${P}node_offline`)).toBeTruthy()
  expect((screen.getByRole('textbox', { name: `${P}target` }) as HTMLInputElement).value).toBe('example.test')
  expect(screen.getByText('full:example.test')).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: `${P}open_policy No mail` })); expect(onOpenPolicy).toHaveBeenCalledWith(12)
})
it('blocks duplicate submissions while pending and aborts on unmount', async () => {
  let resolve!: (value: { data: DestinationTestResult }) => void
  api.post.mockImplementation(() => new Promise(done => { resolve = done }))
  const { unmount } = mount(); submit(); fireEvent.submit(screen.getByRole('form', { name: `${P}title` }))
  expect(api.post).toHaveBeenCalledOnce(); const signal = api.post.mock.calls[0][2].signal
  unmount(); expect(signal.aborted).toBe(true); resolve({ data: result })
})
it('requires target and valid port, then clears stale results when an input changes', async () => {
  mount(); fireEvent.click(screen.getByRole('button', { name: `${P}submit` })); expect(api.post).not.toHaveBeenCalled()
  submit(); await screen.findByText(`${P}result_shadowed`)
  fireEvent.change(screen.getByRole('textbox', { name: `${P}target` }), { target: { value: 'other.test' } })
  expect(screen.queryByText(`${P}result_shadowed`)).toBeNull(); expect(api.post).toHaveBeenCalledOnce()
  fireEvent.change(screen.getByRole('spinbutton', { name: `${P}port` }), { target: { value: '65536' } }); fireEvent.click(screen.getByRole('button', { name: `${P}submit` })); expect(api.post).toHaveBeenCalledOnce()
})
it('renders a failure with an explicit retry and never automatically retries', async () => {
  api.post.mockRejectedValueOnce({ response: { status: 503, data: { error: 'unavailable' } } })
  mount(); submit(); await screen.findByText(`${P}failed`); expect(api.post).toHaveBeenCalledOnce()
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.retry' })); await screen.findByText(`${P}result_shadowed`); expect(api.post).toHaveBeenCalledTimes(2)
})
it('publishes explicitly without repeating the audited simulation or claiming application', async () => {
  api.post.mockResolvedValue({ data: { ...result, unpublished: true } }); mount(); submit(); await screen.findByText(`${P}unpublished`)
  fireEvent.click(screen.getByRole('button', { name: 'admin:access_control.publish' })); await waitFor(() => expect(api.post.mock.calls.filter(([url]) => url === '/admin/dest/publish')).toHaveLength(1))
  await screen.findByText('admin:access_control.publication_requested')
  expect(api.post.mock.calls.filter(([url]) => url === '/admin/dest/test')).toHaveLength(1)
  expect(screen.getByText(`${P}unpublished`)).toBeTruthy()
})
it('keeps protocol uncertainty and absent native-client scope visible without a fabricated terminating rule', async () => {
  api.post.mockResolvedValue({ data: { ...result, verdict: 'untestable', terminating_step: null, steps: [], notes: ['no_native_client'], nodes: [] } }); mount(); submit(); await screen.findByText(`${P}note_no_native_client`)
  expect(screen.queryByRole('button', { name: `${P}open_policy No mail` })).toBeNull()
  expect(screen.getByText(`${P}verdict_untestable`)).toBeTruthy()
})
it.each([['block', 'deny'], ['observe', 'trial']] as const)('distinguishes a group %s result from a policy verdict', async (verdict, label) => {
  api.post.mockResolvedValue({ data: { ...result, verdict, terminating_step: 'group', steps: [{ step: 'group', result: 'hit', group_id: 8, name: 'Engineering' }] } })
  mount(); submit(); await screen.findByText(`${P}verdict_${label}`)
  expect(screen.queryByRole('button', { name: `${P}open_policy Engineering` })).toBeNull()
})
