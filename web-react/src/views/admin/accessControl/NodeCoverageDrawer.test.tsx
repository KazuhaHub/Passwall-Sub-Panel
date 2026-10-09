/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter, useNavigate } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import AppRouter from '@/router/AppRouter'
import SnackbarHost from '@/components/SnackbarHost'
import { createAppTheme } from '@/theme'
import { useAuthStore } from '@/stores/auth'
import { destinationNode, destinationStatus } from '@/test/accessControlFixtures'
const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string, options?: { error?: string }) => options?.error ? `${key}: ${options.error}` : key }) }))
import NodeCoverageDrawer from './NodeCoverageDrawer'
afterEach(() => { cleanup(); vi.clearAllMocks() })

it('shows a late retry failure through the real snackbar host after the drawer route unmounts', async () => {
  useAuthStore.setState({ userId: 42, role: 'admin', authEpoch: 5 })
  let reject!: (error: unknown) => void
  api.post.mockImplementation(() => new Promise((_resolve, fail) => { reject = fail }))
  function Page() {
    const navigate = useNavigate()
    return <NodeCoverageDrawer status={destinationStatus({ nodes: [destinationNode({ state: 'rejected' })] })}
      loading={false} failed={false} refreshing={false} onRetryRead={vi.fn()}
      onClose={() => void navigate('/away')} onLists={vi.fn()} onSettings={vi.fn()} />
  }
  const router = createMemoryRouter([{ path: '/', element: <Page /> }, { path: '/away', element: <p>Destination</p> }])
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}>
    <QueryClientProvider client={client}><AppRouter router={router} /></QueryClientProvider><SnackbarHost />
  </ThemeProvider>)
  fireEvent.click(screen.getByRole('button', { name: 'admin:access_control.coverage.retry' }))
  await waitFor(() => expect(api.post).toHaveBeenCalledOnce())
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.close' }))
  await screen.findByText('Destination')
  reject({ isAxiosError: true, response: { status: 503, data: { error: 'retry_failed' } } })
  expect((await screen.findByRole('alert')).textContent).toContain('admin:access_control.write_failed: retry_failed')
  expect(screen.queryByRole('dialog')).toBeNull(); expect(api.post).toHaveBeenCalledOnce()
})
