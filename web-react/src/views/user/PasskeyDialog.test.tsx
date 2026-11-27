// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { api } from './testHarness'
import PasskeyDialog from './PasskeyDialog'

vi.mock('@simplewebauthn/browser', () => ({ startRegistration: vi.fn(async () => ({ id: 'c', type: 'public-key', response: {} })) }))

function setup(recovery: string[] | undefined) {
  Object.defineProperty(window, 'PublicKeyCredential', { value: class {}, configurable: true })
  api.post.mockImplementation(async (url: string) => (url.includes('/begin')
    ? { data: { session_id: 's', publicKey: {} } }
    : { data: { passkeys: [], recovery_codes: recovery } }))
  const onChanged = vi.fn()
  render(<PasskeyDialog open available credentials={[]} onClose={() => {}} onChanged={onChanged} />)
  fireEvent.click(screen.getByRole('button', { name: 'Add passkey' }))
  fireEvent.click(screen.getByRole('button', { name: 'Continue' }))
  return onChanged
}

describe('PasskeyDialog onChanged sequence', () => {
  it('holds onChanged back until the first-passkey recovery codes are acknowledged', async () => {
    const onChanged = setup(['AAAA-BBBB'])
    await screen.findByText('AAAA-BBBB')
    expect(onChanged).not.toHaveBeenCalled()
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: "I've saved them" }))
    expect(onChanged).toHaveBeenCalledTimes(1)
  })

  it('calls onChanged at once when no codes come back', async () => {
    const onChanged = setup(undefined)
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1))
  })
})
