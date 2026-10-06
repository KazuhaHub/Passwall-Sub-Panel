/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import AppRouter from '@/router/AppRouter'
import { createAppTheme } from '@/theme'
import { useAuthStore } from '@/stores/auth'
import type { DestinationExemptionView } from '@/api/accessControl'
const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: vi.fn() }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string, args?: { upn?: string }) => key + (args?.upn ? ` ${args.upn}` : '') }) }))
vi.mock('@/components/UserAutocomplete', () => ({ default: () => <p>Account picker</p> }))
import ExemptionsSheet from './ExemptionsSheet'
const P = 'admin:access_control.exemptions.', at = 1791260000000
const row: DestinationExemptionView = { user_id: 13, upn: 'alice@test', reason: 'Diagnostics', created_by: 42, created_by_upn: 'admin@test', created_at: at - 10000, expires_at: at + 86400123, expired: false }
beforeEach(() => {
  vi.clearAllMocks(); vi.spyOn(Date, 'now').mockReturnValue(at)
  useAuthStore.setState({ userId: 42, role: 'admin', authEpoch: 5 })
  api.get.mockImplementation(async (url: string) => ({ data: url.endsWith('/exemptions') ? { items: [row] } : { group: null, exemption: row } }))
  api.delete.mockResolvedValue({ data: {} }); confirmation.mockResolvedValue(true)
})
afterEach(() => { cleanup(); vi.restoreAllMocks() })
function mount() {
  const onClose = vi.fn(), onOpenUser = vi.fn(), client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const router = createMemoryRouter([{ path: '/admin/access-control', element: <ExemptionsSheet onClose={onClose} onOpenUser={onOpenUser} etaMs={120000} /> }], { initialEntries: ['/admin/access-control'] })
  render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}><QueryClientProvider client={client}><AppRouter router={router} /></QueryClientProvider></ThemeProvider>)
  return { onClose, onOpenUser }
}
it('places expired exemptions last and shows expiry states without negative countdowns', async () => {
  api.get.mockResolvedValue({ data: { items: [{ ...row, user_id: 1, upn: 'expired@test', expires_at: at - 1, expired: true }, row, { ...row, user_id: 2, upn: 'permanent@test', expires_at: null }] } })
  mount(); await screen.findByText('permanent@test')
  const cards = Array.from(document.querySelectorAll('[data-state]')).map(node => node.getAttribute('data-state'))
  expect(cards).toEqual(['active', 'permanent', 'expired'])
  expect(screen.getByText(`${P}expired`)).toBeTruthy()
  expect(api.delete).not.toHaveBeenCalled()
})
it('opens account through its caller and does not perform any write', async () => {
  const { onOpenUser } = mount()
  fireEvent.click(await screen.findByRole('button', { name: `${P}menu alice@test` }))
  fireEvent.click(screen.getByRole('menuitem', { name: `${P}open_account` }))
  expect(onOpenUser).toHaveBeenCalledWith(13); expect(api.delete).not.toHaveBeenCalled()
})
it('opens its editor above the page drawer and locks the existing account', async () => {
  mount(); fireEvent.click(await screen.findByRole('button', { name: `${P}menu alice@test` }))
  fireEvent.click(screen.getByRole('menuitem', { name: `${P}edit` }))
  const dialog = await screen.findByRole('dialog', { name: `${P}edit` })
  expect(within(dialog).getByRole('textbox', { name: `${P}account` }).getAttribute('readonly')).not.toBeNull()
  expect(Number(getComputedStyle(document.querySelector('.MuiDialog-root')!).zIndex)).toBeGreaterThan(Number(getComputedStyle(document.querySelector('.MuiDrawer-root')!).zIndex))
})
it('confirms cancellation as an ordinary action and blocks duplicate writes and closure while pending', async () => {
  let resolve!: (value: { data: unknown }) => void
  api.delete.mockImplementation(() => new Promise(done => { resolve = done }))
  const { onClose } = mount()
  fireEvent.click(await screen.findByRole('button', { name: `${P}menu alice@test` })); fireEvent.click(screen.getByRole('menuitem', { name: `${P}cancel` }))
  await waitFor(() => expect(api.delete).toHaveBeenCalledOnce())
  expect(confirmation.mock.calls[0][0]).toMatchObject({ title: `${P}cancel_title alice@test`, confirmText: `${P}cancel` })
  expect(confirmation.mock.calls[0][0].destructive).toBeUndefined()
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.close' })); expect(onClose).not.toHaveBeenCalled()
  expect(screen.getByRole('button', { name: `${P}menu alice@test` }).hasAttribute('disabled')).toBe(true)
  resolve({ data: {} }); await waitFor(() => expect(screen.getByRole('button', { name: 'common:actions.close' }).hasAttribute('disabled')).toBe(false))
})
it('keeps cancellation declined and supports load failure retry without fabricating an empty state', async () => {
  api.get.mockRejectedValueOnce(new Error('offline'))
  const { onClose } = mount()
  await screen.findByText(`${P}load_failed`); expect(screen.queryByText(`${P}empty`)).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.retry' }))
  confirmation.mockResolvedValue(false)
  fireEvent.click(await screen.findByRole('button', { name: `${P}menu alice@test` })); fireEvent.click(screen.getByRole('menuitem', { name: `${P}cancel` }))
  await waitFor(() => expect(confirmation).toHaveBeenCalledOnce())
  expect(api.delete).not.toHaveBeenCalled(); expect(onClose).not.toHaveBeenCalled()
})
