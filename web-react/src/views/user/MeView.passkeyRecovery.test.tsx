// @vitest-environment jsdom
//
// Regression test for a bug the real-browser passkey ceremony found and no other
// test could have: the FIRST passkey mints one-time recovery codes, the server
// returns them exactly once, and the UI dropped them on the floor.
//
// The mechanism is a parent/child state interaction, which is why it needs a test
// at this level and not a unit test of either piece:
//
//   1. PasskeyDialog.addPasskey() calls onChanged() -- which is MeView's load().
//   2. load() sets loading = true, and MeView rendered a full-page spinner for
//      ANY loading, so the whole tree -- dialog included -- unmounted.
//   3. When the profile came back the dialog remounted with open=true, and its
//      own effect resets recoveryCodes to null on that path.
//
// So the codes were set on a component that was being discarded, and the user was
// left with a passkey-as-2FA account and no fallback, which the server believes
// they were shown. Surfacing them is the entire point of returning them once.
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { api, installReads, mount } from '@/test/adminSaveHarness'
import MeView from './MeView'

vi.mock('@simplewebauthn/browser', () => ({
  startRegistration: vi.fn(async () => ({ id: 'cred', type: 'public-key', response: {} })),
}))

const RECOVERY = ['AAAA-BBBB', 'CCCC-DDDD', 'EEEE-FFFF']

const baseProfile = {
  id: 1, upn: 'admin@example.test', display_name: 'admin', email: '',
  role: 'admin', enabled: true,
  can_change_password: true, totp_available: true, totp_enabled: false,
  passkey_available: true, passkey_enabled: true,
  passkey_credentials: [] as { id: number; name: string; created_at: string }[],
  recovery_codes_remaining: 0,
  group_name: 'default', created_at: '2026-01-01T00:00:00Z',
  sub_token: 'tok', sub_url: 'https://sub.example.test/x', uuid: 'u',
}

describe('first passkey shows its one-time recovery codes', () => {
  it('surfaces the codes the server returns once, after the profile refresh', async () => {
    installReads({
      '/user/me': baseProfile,
      '/user/me/rules': { personal_rules: '' },
      '/user/me/server-status': { nodes: [] },
      '/user/me/traffic': {},
      '/user/me/traffic/history': { points: [] },
    })

    // The reload that follows a successful enrolment: same account, one more
    // credential. This is what replaces `loading` with a fresh profile and, in
    // the bug, remounts the dialog underneath the recovery screen.
    api.get.mockImplementation(async (url: string) => {
      // The reload has to be genuinely asynchronous. With every mock resolving in
      // the same microtask batch, loading never reaches a render, the tree is
      // never taken down, and the bug this test exists for does not reproduce --
      // it passed before the fix until this was added.
      await new Promise(r => setTimeout(r, 25))
      if (url === '/user/me') {
        return { data: { ...baseProfile, passkey_credentials: [{ id: 1, name: 'p', created_at: '2026-09-17T00:00:00Z' }], recovery_codes_remaining: RECOVERY.length } }
      }
      if (url === '/user/me/rules') return { data: { personal_rules: '' } }
      if (url === '/user/me/server-status') return { data: { nodes: [] } }
      if (url === '/user/me/traffic') return { data: {} }
      if (url === '/user/me/traffic/history') return { data: { points: [] } }
      throw new Error(`Unexpected GET ${url}`)
    })

    api.post.mockImplementation(async (url: string) => {
      if (url === '/user/me/passkeys/begin') {
        return { data: { session_id: 's1', publicKey: { challenge: 'c', rp: { id: 'localhost' }, user: { id: 'dXNlcg' } } } }
      }
      if (url.startsWith('/user/me/passkeys/finish')) {
        return { data: { passkeys: [{ id: 1, name: 'p', created_at: '2026-09-17T00:00:00Z' }], recovery_codes: RECOVERY } }
      }
      return { data: {} }
    })

    mount(<MeView />)

    // open the kebab menu and the passkey dialog
    const kebab = await screen.findByRole('button', { name: '' })
    void kebab
    const buttons = await screen.findAllByRole('button')
    const kebabBtn = buttons.find(b => b.querySelector('svg') && !(b.textContent || '').trim())
    expect(kebabBtn).toBeTruthy()
    fireEvent.click(kebabBtn!)

    fireEvent.click(await screen.findByRole('menuitem', { name: /passkeys/i }))
    fireEvent.click(await screen.findByRole('button', { name: /passkey\.add|add passkey/i }))

    const dialog = (await screen.findAllByRole('dialog')).at(-1)!
    fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: 'my phone' } })
    fireEvent.click(within(dialog).getByRole('button', { name: /passkey\.continue|^continue$/i }))

    // The assertion that matters: the one-time codes are on screen.
    await waitFor(() => {
      expect(screen.getByText(RECOVERY[0])).toBeTruthy()
    }, { timeout: 5000 })
    expect(screen.getByText(/save your recovery codes|passkey\.recovery_title/i)).toBeTruthy()
  })
})
