// @vitest-environment jsdom
//
// Regression test for a bug the real-browser passkey ceremony found and no other test could have: the FIRST passkey mints
// one-time recovery codes, the server returns them exactly once, and the UI dropped them on the floor.
//
// The mechanism is a parent/child state interaction, which is why it needs a test at this level and not a unit test of
// either piece:
//
//   1. PasskeyDialog.addPasskey() calls onChanged(), which is MeView's load().
//   2. load() refetches the profile. If the page showed a full-page spinner for ANY loading, the whole tree, dialog
//      included, unmounted.
//   3. When the profile came back the dialog remounted with open=true, and its own effect resets recoveryCodes on that path.
//
// So the codes were set on a component that was being discarded, and the user was left with a passkey-as-2FA account and no
// fallback, which the server believes they were shown. Surfacing them is the entire point of returning them once.
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api, mountMe, profile, serve, zeroUsage } from './testHarness'
import MeView from './MeView'

vi.mock('@simplewebauthn/browser', () => ({
  startRegistration: vi.fn(async () => ({ id: 'cred', type: 'public-key', response: {} })),
  startAuthentication: vi.fn(async () => ({ id: 'cred', type: 'public-key', response: {} })),
}))

const RECOVERY = ['AAAA-BBBB', 'CCCC-DDDD', 'EEEE-FFFF']
const base = {
  email: '', totp_available: true, totp_enabled: false, passkey_available: true, passkey_enabled: true,
  passkey_credentials: [] as { id: number; name: string; created_at: string }[], recovery_codes_remaining: 0,
}

beforeEach(() => {
  // jsdom has no WebAuthn; the dialog checks for it before it asks the server to begin a ceremony.
  Object.defineProperty(window, 'PublicKeyCredential', { value: class {}, configurable: true })
})

async function openPasskeys() {
  fireEvent.click(await screen.findByRole('tab', { name: 'Security' }))
  const row = (await screen.findByText('Passkeys', { selector: 'div' })).closest('li')!
  fireEvent.click(within(row).getByRole('button', { name: 'Manage' }))
  return screen.findByRole('dialog')
}

describe('first passkey shows its one-time recovery codes', () => {
  it('surfaces the codes the server returns once, after the profile refresh', async () => {
    serve({}, base)
    // The reload that follows a successful enrolment: same account, one more credential. It has to be genuinely
    // asynchronous. With every mock resolving in one microtask batch the page never renders a loading state and the bug
    // this test exists for does not reproduce.
    const enrolled = { ...profile, ...base, passkey_credentials: [{ id: 1, name: 'p', created_at: '2026-09-17T00:00:00Z' }], recovery_codes_remaining: RECOVERY.length }
    let enrolledYet = false
    api.get.mockImplementation(async (url: string) => {
      await new Promise(r => setTimeout(r, 25))
      if (url === '/user/me') return { data: enrolledYet ? enrolled : { ...profile, ...base } }
      if (url === '/user/me/traffic') return { data: zeroUsage }
      if (url === '/user/me/traffic/history') return { data: { items: [] } }
      if (url === '/user/me/server-status') return { data: { nodes: [] } }
      throw new Error(`Unexpected GET ${url}`)
    })
    api.post.mockImplementation(async (url: string) => {
      if (url === '/user/me/passkeys/begin') return { data: { session_id: 's1', publicKey: { challenge: 'c', rp: { id: 'localhost' }, user: { id: 'dXNlcg' } } } }
      if (url.startsWith('/user/me/passkeys/finish')) { enrolledYet = true; return { data: { passkeys: enrolled.passkey_credentials, recovery_codes: RECOVERY } } }
      return { data: {} }
    })

    mountMe(<MeView />)
    const dialog = await openPasskeys()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add passkey' }))
    fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: 'my phone' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Continue' }))

    // The assertion that matters: the one-time codes are on screen.
    await waitFor(() => expect(screen.getByText(RECOVERY[0])).toBeTruthy(), { timeout: 5000 })
    expect(screen.getByText('Save your recovery codes')).toBeTruthy()
    // What was sent: the name goes in the query, not the body, and the session id with it.
    expect(api.post).toHaveBeenCalledWith('/user/me/passkeys/finish?session=s1&name=my%20phone', expect.anything(), expect.anything())

    // The reload has landed; the codes are still there, and Escape does not dismiss them (they are shown only once).
    await waitFor(() => expect(enrolledYet).toBe(true))
    await new Promise(r => setTimeout(r, 60))
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' })
    expect(screen.getByText(RECOVERY[0])).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: "I've saved them" }))
    await waitFor(() => expect(screen.queryByText(RECOVERY[0])).toBeNull())
  })

  it('says so, without asking the server, when the browser has no WebAuthn', async () => {
    Object.defineProperty(window, 'PublicKeyCredential', { value: undefined, configurable: true })
    serve({}, base)
    mountMe(<MeView />)
    const dialog = await openPasskeys()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add passkey' }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Continue' }))
    await within(dialog).findByText(/does not support passkeys/)
    expect(api.post).not.toHaveBeenCalled()
  })

  it('says a cancelled prompt changed nothing', async () => {
    const { startRegistration } = await import('@simplewebauthn/browser')
    vi.mocked(startRegistration).mockRejectedValueOnce(Object.assign(new Error('cancel'), { name: 'NotAllowedError' }))
    serve({}, base)
    api.post.mockResolvedValue({ data: { session_id: 's', publicKey: {} } })
    mountMe(<MeView />)
    const dialog = await openPasskeys()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add passkey' }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Continue' }))
    await within(dialog).findByText(/closed before it finished/)
    expect(api.post).toHaveBeenCalledTimes(1)
  })
})
