// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, mountPage, serve, toast, user } from './testHarness'
import UsersPage from './UsersPage'

vi.mock('../risk/drawer/RiskUserDrawer', () => ({ default: () => null }))

const SECURITY = '/admin/users?user=2&tab=security'

it('resets a password to a typed one and shows it once, with the subscription link masked', async () => {
  serve([user])
  api.post.mockResolvedValue({ data: { password: 'Typed-pw-1' } })
  mountPage(<UsersPage />, SECURITY)
  fireEvent.click(await screen.findByRole('button', { name: 'Reset password' }))
  const dialog = await screen.findByRole('dialog', { name: /Reset login password/ })
  expect(dialog.textContent).toContain(user.upn)
  fireEvent.change(within(dialog).getByLabelText('New password (blank = random)'), { target: { value: 'Typed-pw-1' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'Reset password' }))

  await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/users/2/reset-password', { password: 'Typed-pw-1' }))
  const result = await screen.findByRole('dialog', { name: 'Password reset' })
  expect(within(result).getByText('Typed-pw-1')).toBeTruthy()
  // The link is a secret: masked until asked for.
  const link = within(result).getByLabelText('Subscription URL') as HTMLInputElement
  expect(link.type).toBe('password')
})

it('sends an empty password for a generated one, and refuses a weak typed one without asking the server', async () => {
  serve([user])
  api.post.mockResolvedValue({ data: { password: 'Generated1x' } })
  mountPage(<UsersPage />, SECURITY)
  fireEvent.click(await screen.findByRole('button', { name: 'Reset password' }))
  const dialog = await screen.findByRole('dialog', { name: /Reset login password/ })
  fireEvent.change(within(dialog).getByLabelText('New password (blank = random)'), { target: { value: 'weak' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'Reset password' }))
  expect(api.post).not.toHaveBeenCalled()
  fireEvent.change(within(dialog).getByLabelText('New password (blank = random)'), { target: { value: '' } })
  fireEvent.click(within(dialog).getByRole('button', { name: 'Reset password' }))
  await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/users/2/reset-password', { password: '' }))
})

it('reads the personal rules when opened and saves the trimmed text', async () => {
  serve([user], { '/admin/users/2/rules': { personal_rules: 'DOMAIN,a.test,DIRECT' } })
  mountPage(<UsersPage />, '/admin/users?user=2')
  fireEvent.click(await screen.findByRole('button', { name: 'Personal rules' }))
  const dialog = await screen.findByRole('dialog', { name: /Personal rules — / })
  const box = await within(dialog).findByDisplayValue('DOMAIN,a.test,DIRECT')
  const save = within(dialog).getByRole('button', { name: 'Save' }) as HTMLButtonElement
  expect(save.disabled).toBe(true)
  fireEvent.change(box, { target: { value: '  DOMAIN,b.test,PROXY \n' } })
  fireEvent.click(save)
  await waitFor(() => expect(api.put).toHaveBeenCalledWith('/admin/users/2/rules', { personal_rules: 'DOMAIN,b.test,PROXY' }))
  await waitFor(() => expect(toast).toHaveBeenCalledWith('Personal rules saved', 'success'))
})

it('lists the passkeys and revokes one after naming it, with focus on Cancel', async () => {
  serve([{ ...user, passkey_count: 2 }], { '/admin/users/2/passkeys': { passkeys: [
    { id: 11, name: 'Laptop', created_at: '2026-01-01T00:00:00Z' }, { id: 12, name: 'Phone', created_at: '2026-01-02T00:00:00Z' },
  ] } })
  mountPage(<UsersPage />, SECURITY)
  fireEvent.click(await screen.findByRole('button', { name: 'Manage passkeys' }))
  const dialog = await screen.findByRole('dialog', { name: /Passkeys/ })
  expect(await within(dialog).findByText('Laptop')).toBeTruthy()
  fireEvent.click(within(dialog).getByRole('button', { name: 'Revoke passkey Laptop' }))
  const ask = await screen.findByRole('alertdialog')
  expect(ask.textContent).toContain('Laptop')
  expect(ask.textContent).toContain(user.upn)
  await waitFor(() => expect(document.activeElement).toBe(within(ask).getByRole('button', { name: 'Cancel' })))
  fireEvent.click(within(ask).getByRole('button', { name: 'Revoke' }))
  await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/admin/users/2/passkeys/11'))
  await waitFor(() => expect(within(dialog).queryByText('Laptop')).toBeNull())
})

it('regenerates recovery codes after asking, and shows them once', async () => {
  serve([{ ...user, totp_enabled: true }])
  api.post.mockResolvedValue({ data: { recovery_codes: ['AAAA-1111', 'BBBB-2222'] } })
  mountPage(<UsersPage />, SECURITY)
  fireEvent.click(await screen.findByRole('button', { name: 'Regenerate recovery codes' }))
  const ask = await screen.findByRole('alertdialog')
  await waitFor(() => expect(document.activeElement).toBe(within(ask).getByRole('button', { name: 'Cancel' })))
  fireEvent.click(within(ask).getByRole('button', { name: 'Regenerate' }))
  expect(await screen.findByText('AAAA-1111')).toBeTruthy()
  expect(api.post).toHaveBeenCalledWith('/admin/users/2/2fa/recovery/regenerate')
})

it('runs the reconcile for an admin and says what it found', async () => {
  serve([user])
  api.post.mockResolvedValue({ data: { scanned: 10, fixed: 2, issues: [{ panel_name: 'srv', client_email: 'u@x', code: 'enable_skew', fixed: true }] } })
  mountPage(<UsersPage />)
  await screen.findByText('old-name')
  const more = screen.getByRole('button', { name: 'More' })
  fireEvent.pointerDown(more, { button: 0, pointerType: 'mouse' })
  fireEvent.mouseDown(more, { button: 0 })
  fireEvent.click(more)
  fireEvent.click(await screen.findByRole('menuitem', { name: 'Reconcile' }))
  const dialog = await screen.findByRole('dialog', { name: 'Reconcile result' })
  expect(dialog.textContent).toContain('Scanned 10 nodes/users, auto-fixed 2 issues.')
  fireEvent.click(within(dialog).getByRole('button', { name: 'Show details' }))
  expect(within(dialog).getByText('enable_skew')).toBeTruthy()
})
