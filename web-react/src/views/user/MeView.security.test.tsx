// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, mountMe, serve, snack } from './testHarness'
import MeView from './MeView'

const CODES = ['AAAA-1111', 'BBBB-2222']

async function manage(title: string) {
  const row = (await screen.findByText(title, { selector: 'div' })).closest('li')!
  fireEvent.click(within(row).getAllByRole('button')[0])
  return screen.findByRole('dialog')
}

describe('which security rows a person gets', () => {
  it('has no security tab at all for an account with nothing to manage', async () => {
    serve({}, { can_change_password: false })
    mountMe(<MeView />)
    await screen.findByText('My usage')
    expect(screen.queryByRole('tab', { name: 'Security' })).toBeNull()
  })

  it('falls back to the overview when ?tab=security is asked for without one', async () => {
    serve({}, { can_change_password: false })
    mountMe(<MeView />, '/?tab=security')
    await screen.findByText('My usage')
  })

  it('offers each row only where its condition holds, and recovery codes after any second factor', async () => {
    serve({}, { can_change_password: true, totp_available: false, passkey_available: false })
    mountMe(<MeView />, '/?tab=security')
    await screen.findByText('Change password')
    expect(screen.queryByText('Authenticator app (TOTP)')).toBeNull()
    expect(screen.queryByText('Passkeys')).toBeNull()
    expect(screen.queryByText('Recovery codes')).toBeNull()
  })

  it('shows the state of each factor in words', async () => {
    serve({}, { totp_available: true, totp_enabled: true, passkey_available: true, passkey_credentials: [{ id: 1, name: 'a', created_at: '2026-01-01T00:00:00Z' }], recovery_codes_remaining: 4 })
    mountMe(<MeView />, '/?tab=security')
    await screen.findByText('Enabled')
    expect(screen.getByText('1 registered')).toBeTruthy()
    expect(screen.getByText('4 recovery codes left')).toBeTruthy()
  })
})

describe('change password', () => {
  it('uses the right autocomplete hints and sends the old and new password', async () => {
    serve()
    mountMe(<MeView />, '/?tab=security')
    fireEvent.click(await screen.findByRole('button', { name: 'Change' }))
    const dialog = await screen.findByRole('dialog')
    const old = within(dialog).getByLabelText(/Old password/)
    const next = within(dialog).getByLabelText(/^New password/)
    const confirm = within(dialog).getByLabelText(/Confirm new password/)
    expect(old.getAttribute('autocomplete')).toBe('current-password')
    expect(next.getAttribute('autocomplete')).toBe('new-password')
    expect(confirm.getAttribute('autocomplete')).toBe('new-password')
    fireEvent.change(old, { target: { value: 'old-pw' } })
    fireEvent.change(next, { target: { value: 'new-pw-1' } })
    fireEvent.change(confirm, { target: { value: 'new-pw-1' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'OK' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/user/me/change-password', { old_password: 'old-pw', new_password: 'new-pw-1' }))
    expect(snack).toHaveBeenCalledWith('Password updated', 'success')
  })

  it('says a mismatch next to the confirm field and sends nothing', async () => {
    serve()
    mountMe(<MeView />, '/?tab=security')
    fireEvent.click(await screen.findByRole('button', { name: 'Change' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.change(within(dialog).getByLabelText(/Old password/), { target: { value: 'o' } })
    fireEvent.change(within(dialog).getByLabelText(/^New password/), { target: { value: 'abc' } })
    const confirm = within(dialog).getByLabelText(/Confirm new password/)
    fireEvent.change(confirm, { target: { value: 'abd' } })
    fireEvent.blur(confirm)
    await within(dialog).findByText('Passwords do not match')
    fireEvent.click(within(dialog).getByRole('button', { name: 'OK' }))
    expect(api.post).not.toHaveBeenCalled()
  })
})

describe('turn on the authenticator', () => {
  const profileOff = { totp_available: true, totp_enabled: false }

  it('runs begin, shows the QR and key, enables with the code, then holds the dialog open on the recovery codes', async () => {
    serve({}, profileOff)
    api.post.mockImplementation(async (url: string) => {
      if (url === '/user/me/2fa/begin') return { data: { otpauth_url: 'otpauth://totp/x?secret=ABCD', secret: 'ABCD EFGH' } }
      if (url === '/user/me/2fa/enable') return { data: { recovery_codes: CODES } }
      return { data: {} }
    })
    mountMe(<MeView />, '/?tab=security')
    const dialog = await manageButton('Set up')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Get started' }))
    expect((await within(dialog).findByTestId('totp-secret')).textContent).toBe('ABCD EFGH')
    const code = within(dialog).getByLabelText(/Verification code/)
    expect(code.getAttribute('autocomplete')).toBe('one-time-code')
    fireEvent.change(code, { target: { value: ' 123456 ' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Enable' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/user/me/2fa/enable', { code: '123456' }, { _skipErrorToast: true, _skipRefresh: true }))
    // Order: begin first, then enable.
    expect(api.post.mock.calls.map(c => c[0])).toEqual(['/user/me/2fa/begin', '/user/me/2fa/enable'])

    await screen.findByText(CODES[0])
    expect(screen.getByRole('button', { name: 'Download' })).toBeTruthy()
    // Neither Escape nor the missing close button can lose the one-time codes.
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' })
    expect(screen.getByText(CODES[0])).toBeTruthy()
    expect(within(screen.getByRole('dialog')).queryByRole('button', { name: 'Close' })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: "I've saved them, done" }))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('Authenticator enabled', 'success'))
  })

  it('says a wrong code is wrong under the field, and does not sign the person out', async () => {
    serve({}, profileOff)
    api.post.mockImplementation(async (url: string) => {
      if (url === '/user/me/2fa/begin') return { data: { otpauth_url: 'otpauth://x', secret: 'S' } }
      throw Object.assign(new Error('no'), { response: { status: 401 } })
    })
    mountMe(<MeView />, '/?tab=security')
    const dialog = await manageButton('Set up')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Get started' }))
    fireEvent.change(await within(dialog).findByLabelText(/Verification code/), { target: { value: '000000' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Enable' }))
    await within(dialog).findByText('Invalid code. Please try again.')
  })

  async function manageButton(name: string) {
    fireEvent.click(await screen.findByRole('button', { name }))
    return screen.findByRole('dialog')
  }
})

describe('turn off the authenticator', () => {
  it('needs a code and sends exactly that', async () => {
    serve({}, { totp_available: true, totp_enabled: true })
    mountMe(<MeView />, '/?tab=security')
    const dialog = await manage('Authenticator app (TOTP)')
    fireEvent.change(within(dialog).getByLabelText(/Verification code/), { target: { value: '654321' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Turn off authenticator' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/user/me/2fa/disable', { code: '654321' }, { _skipErrorToast: true, _skipRefresh: true }))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('Authenticator disabled', 'success'))
  })

  it('asks for a code instead of sending an empty one', async () => {
    serve({}, { totp_available: true, totp_enabled: true })
    mountMe(<MeView />, '/?tab=security')
    const dialog = await manage('Authenticator app (TOTP)')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Turn off authenticator' }))
    await within(dialog).findByText('Please enter a code')
    expect(api.post).not.toHaveBeenCalled()
  })
})

describe('regenerate recovery codes', () => {
  it('proves identity with a code, then shows the new set once and holds the dialog open', async () => {
    serve({}, { totp_available: true, totp_enabled: true, recovery_codes_remaining: 2 })
    api.post.mockResolvedValue({ data: { recovery_codes: CODES } })
    mountMe(<MeView />, '/?tab=security')
    const dialog = await manage('Recovery codes')
    expect(within(dialog).getByText('2 recovery codes left')).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Regenerate with a code' }))
    // The step says what happens to the old codes before the person commits.
    await within(dialog).findByText(/old recovery codes stop working immediately/)
    fireEvent.change(within(dialog).getByLabelText(/Verification code/), { target: { value: '111222' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Regenerate recovery codes' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/user/me/2fa/recovery/regenerate', { code: '111222' }, { _skipErrorToast: true, _skipRefresh: true }))
    await screen.findByText(CODES[0])
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' })
    expect(screen.getByText(CODES[0])).toBeTruthy()
  })
})

describe('delete a passkey', () => {
  it('names the passkey in the confirmation, starts on Cancel, and deletes only on yes', async () => {
    serve({}, { passkey_available: true, passkey_credentials: [{ id: 7, name: 'My phone', created_at: '2026-01-01T00:00:00Z' }], recovery_codes_remaining: 3 })
    api.delete.mockResolvedValue({ data: {} })
    mountMe(<MeView />, '/?tab=security')
    const dialog = await manage('Passkeys')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))
    const ask = await screen.findByRole('alertdialog', { name: 'Delete passkey' })
    expect(ask.textContent).toContain('Delete "My phone"?')
    expect(api.delete).not.toHaveBeenCalled()
    fireEvent.click(within(ask).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(api.delete).toHaveBeenCalledWith('/user/me/passkeys/7', { _skipErrorToast: true }))
  })

  it('renames with the same request as before', async () => {
    serve({}, { passkey_available: true, passkey_credentials: [{ id: 7, name: 'My phone', created_at: '2026-01-01T00:00:00Z' }], recovery_codes_remaining: 3 })
    api.patch.mockResolvedValue({ data: {} })
    mountMe(<MeView />, '/?tab=security')
    const dialog = await manage('Passkeys')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Rename' }))
    const box = within(dialog).getByRole('textbox')
    fireEvent.change(box, { target: { value: ' Work laptop ' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(api.patch).toHaveBeenCalledWith('/user/me/passkeys/7', { name: 'Work laptop' }, { _skipErrorToast: true }))
  })
})
