/** @vitest-environment jsdom */
import { ThemeProvider } from '@mui/material'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter } from 'react-router'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import AppRouter from '@/router/AppRouter'
import { createAppTheme } from '@/theme'
import { useAuthStore } from '@/stores/auth'
import type { DestinationExemptionView } from '@/api/accessControl'
const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }))
vi.mock('@/api/client', () => ({ client: api }))
const confirmation = vi.hoisted(() => vi.fn())
vi.mock('@/components/ConfirmHost', () => ({ confirm: confirmation }))
const snack = vi.hoisted(() => vi.fn())
vi.mock('@/components/SnackbarHost', () => ({ pushSnack: snack }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }))
vi.mock('@/components/UserAutocomplete', () => ({ default: (p: { onChange: (id: number) => void }) => <button onClick={() => p.onChange(13)}>Choose account</button> }))
import AddExemptionDialog from './AddExemptionDialog'
import { exemptionExpiry, localExpiry, validateExemption } from './exemptionDraft'
const P = 'admin:access_control.exemptions.', at = 1791260000000
const existing: DestinationExemptionView = { user_id: 13, upn: 'alice@test', reason: 'Diagnostics', created_by: 42, created_by_upn: 'admin@test', created_at: at - 10000, expires_at: at + 86400123, expired: false }
beforeEach(() => {
  vi.clearAllMocks(); vi.spyOn(Date, 'now').mockReturnValue(at)
  useAuthStore.setState({ userId: 42, role: 'admin', authEpoch: 5 })
  api.get.mockResolvedValue({ data: { group: null, exemption: null } })
  api.post.mockResolvedValue({ data: existing }); api.put.mockResolvedValue({ data: existing })
  confirmation.mockResolvedValue(true)
})
afterEach(() => { cleanup(); vi.restoreAllMocks() })
function mount(props: Partial<Parameters<typeof AddExemptionDialog>[0]> = {}) {
  const onClose = vi.fn(), client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const router = createMemoryRouter([{ path: '/admin/access-control', element: <AddExemptionDialog onClose={onClose} {...props} /> }], { initialEntries: ['/admin/access-control'] })
  render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'en-US' })}><QueryClientProvider client={client}><AppRouter router={router} /></QueryClientProvider></ThemeProvider>)
  return { onClose, router }
}
it('rejects missing accounts, blank/overlong reasons, malformed and past expiries using Unicode characters', () => {
  expect(validateExemption(null, 'Reason', null, at).user).toBe(true)
  expect(validateExemption(13, ' ', null, at).reason).toBe(true)
  expect(validateExemption(13, '😀'.repeat(255), null, at).reason).toBe(false)
  expect(validateExemption(13, '😀'.repeat(256), null, at).reason).toBe(true)
  expect(validateExemption(13, 'Reason', at, at).expiry).toBe(true)
  expect(validateExemption(13, 'Reason', exemptionExpiry('custom', '2026-02-30T15:30', at), at).expiry).toBe(true)
  expect(validateExemption(13, 'Reason', exemptionExpiry('custom', '', at), at).expiry).toBe(true)
  expect(exemptionExpiry('custom', localExpiry(existing.expires_at!), at, existing)).toBe(existing.expires_at)
})
it('requires a selected account and resolves 24 hours from save time, without a separate confirmation', async () => {
  const { onClose } = mount()
  const save = screen.getByRole('button', { name: `${P}add` })
  fireEvent.change(screen.getByRole('textbox', { name: `${P}reason` }), { target: { value: ' Diagnostics ' } })
  expect(save.hasAttribute('disabled')).toBe(true)
  fireEvent.click(screen.getByRole('button', { name: 'Choose account' }))
  await screen.findByRole('textbox', { name: `${P}reason` })
  vi.mocked(Date.now).mockReturnValue(at + 600000)
  fireEvent.click(save)
  await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
  expect(api.post).toHaveBeenCalledWith('/admin/dest/exemptions', { user_id: 13, reason: 'Diagnostics', expires_at: at + 600000 + 86400000 }, { _skipErrorToast: true })
  expect(confirmation).not.toHaveBeenCalled()
})
it('preserves the account and exact stored expiry in a reason-only edit', async () => {
  const { onClose } = mount({ existing })
  expect(screen.getByRole('textbox', { name: `${P}account` }).getAttribute('readonly')).not.toBeNull()
  fireEvent.change(screen.getByRole('textbox', { name: `${P}reason` }), { target: { value: 'Updated' } })
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.save' }))
  await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
  expect(api.put).toHaveBeenCalledWith('/admin/dest/exemptions/13', { reason: 'Updated', expires_at: existing.expires_at }, { _skipErrorToast: true })
  expect(api.post).not.toHaveBeenCalled()
})
it('keeps a duplicate account draft and prevents repeated creation until the account changes', async () => {
  api.post.mockRejectedValue({ isAxiosError: true, response: { status: 409, data: { error: 'dest_exemption_exists' } } })
  const { onClose } = mount({ userId: 13, upn: 'alice@test' })
  fireEvent.change(screen.getByRole('textbox', { name: `${P}reason` }), { target: { value: 'Diagnostics' } })
  fireEvent.click(screen.getByRole('button', { name: `${P}add` }))
  await screen.findByText(`${P}dest_exemption_exists`)
  expect(screen.getByRole('textbox', { name: `${P}reason` }).getAttribute('value') ?? (screen.getByRole('textbox', { name: `${P}reason` }) as HTMLTextAreaElement).value).toBe('Diagnostics')
  expect(screen.getByRole('button', { name: `${P}add` }).hasAttribute('disabled')).toBe(true)
  expect(onClose).not.toHaveBeenCalled()
})
it('guards dirty cancellation and admits only one pending save while preventing closure', async () => {
  let resolve!: (value: { data: DestinationExemptionView }) => void
  api.post.mockImplementation(() => new Promise(done => { resolve = done }))
  confirmation.mockResolvedValue(false)
  const { onClose } = mount({ userId: 13 })
  fireEvent.change(screen.getByRole('textbox', { name: `${P}reason` }), { target: { value: 'Diagnostics' } })
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.cancel' }))
  await waitFor(() => expect(confirmation).toHaveBeenCalledOnce())
  expect(onClose).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: `${P}add` })); fireEvent.click(screen.getByRole('button', { name: 'admin:access_control.settings.saving' }))
  expect(screen.getByRole('button', { name: 'common:actions.close' }).hasAttribute('disabled')).toBe(true)
  await waitFor(() => expect(api.post).toHaveBeenCalledOnce())
  resolve({ data: existing })
  await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
})
it('shows the allowlist exception warning from the selected account without creating an exemption', async () => {
  api.get.mockResolvedValue({ data: { group: { id: 7, name: 'Visitors', mode: 'allowlist', stage: 'trial' }, exemption: null } })
  mount({ userId: 13 })
  expect(await screen.findByRole('button', { name: `${P}allowlist_summary` })).toBeTruthy()
  expect(api.post).not.toHaveBeenCalled()
})
