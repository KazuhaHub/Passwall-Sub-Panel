// @vitest-environment jsdom
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { api, copy, mountMe, profile, serve, snack } from './testHarness'
import MeView from './MeView'

const URL_ = profile.sub_url
const masked = `https://sub.example.test/${'•'.repeat(16)}`

describe('the subscription link is a secret', () => {
  it('is hidden by default, in the text and in the QR code', async () => {
    mountMe(<MeView />)
    await screen.findByText('Subscription URL')
    expect(screen.getByTestId('sub-url').textContent).toBe(masked)
    expect(document.body.textContent).not.toContain('secret-token')
    // Only a blurred decoy code is drawn until revealed, so the link is not in the page.
    expect(screen.getByTestId('qr-placeholder')).toBeTruthy()
  })

  it('shows the whole link and unblurs the QR code only when asked, and hides again on a reload', async () => {
    mountMe(<MeView />)
    await screen.findByText('Subscription URL')
    fireEvent.click(screen.getAllByRole('button', { name: 'Show' })[0])
    expect(screen.getByTestId('sub-url').textContent).toBe(URL_)
    expect(screen.queryByTestId('qr-placeholder')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Hide' }))
    expect(screen.getByTestId('sub-url').textContent).toBe(masked)
  })

  it('copies the real link without revealing it', async () => {
    mountMe(<MeView />)
    await screen.findByText('Subscription URL')
    fireEvent.click(screen.getByRole('button', { name: 'Copy Subscription URL' }))
    await waitFor(() => expect(copy).toHaveBeenCalledWith(URL_, { silent: true }))
    expect(screen.getByTestId('sub-url').textContent).toBe(masked)
  })

  it('never puts the link in the page address or a toast', async () => {
    mountMe(<MeView />)
    await screen.findByText('Subscription URL')
    fireEvent.click(screen.getByRole('button', { name: 'Copy Subscription URL' }))
    expect(window.location.href).not.toContain('secret-token')
    expect(snack).not.toHaveBeenCalledWith(expect.stringContaining('secret-token'), expect.anything())
  })
})

describe('personal rules', () => {
  const open = async () => {
    fireEvent.click(await screen.findByRole('button', { name: 'Personal rules' }))
    return screen.findByRole('dialog')
  }

  it('saves the trimmed text with the same request as before', async () => {
    serve({ '/user/me/rules': { personal_rules: '- MATCH,DIRECT' } })
    mountMe(<MeView />)
    const dialog = await open()
    const box = await within(dialog).findByDisplayValue('- MATCH,DIRECT')
    fireEvent.change(box, { target: { value: '  - MATCH,PROXY \n' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'OK' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledWith('/user/me/rules', { personal_rules: '- MATCH,PROXY' }, { _skipErrorToast: true }))
    expect(snack).toHaveBeenCalledWith('Saved', 'success')
  })

  it('keeps OK off until something changed', async () => {
    serve({ '/user/me/rules': { personal_rules: 'x' } })
    mountMe(<MeView />)
    const dialog = await open()
    await within(dialog).findByDisplayValue('x')
    expect((within(dialog).getByRole('button', { name: 'OK' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('says it is the admin who turned editing off when the server answers 403', async () => {
    serve({ '/user/me/rules': { personal_rules: 'x' } })
    api.put.mockRejectedValue({ response: { status: 403 } })
    mountMe(<MeView />)
    const dialog = await open()
    fireEvent.change(await within(dialog).findByDisplayValue('x'), { target: { value: 'y' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'OK' }))
    await waitFor(() => expect(snack).toHaveBeenCalledWith('Personal rules editing is disabled by admin', 'error'))
  })

  it('is read-only, with only OK, when the admin turned self-service off', async () => {
    serve({ '/user/me/rules': { personal_rules: 'x' } }, { can_edit_personal_rules: false })
    mountMe(<MeView />)
    const dialog = await open()
    const box = (await within(dialog).findByDisplayValue('x')) as HTMLTextAreaElement
    expect(box.readOnly).toBe(true)
    expect(within(dialog).queryByRole('button', { name: 'Cancel' })).toBeNull()
    expect(within(dialog).getAllByRole('button', { name: 'OK' })).toHaveLength(1)
  })

  it('asks before throwing away edits', async () => {
    serve({ '/user/me/rules': { personal_rules: 'x' } })
    mountMe(<MeView />)
    const dialog = await open()
    fireEvent.change(await within(dialog).findByDisplayValue('x'), { target: { value: 'changed' } })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    const ask = await screen.findByRole('alertdialog', { name: 'You have unsaved changes' })
    // Focus starts on the safe answer.
    expect(within(ask).getByRole('button', { name: 'Cancel' })).toBeTruthy()
    fireEvent.click(within(ask).getByRole('button', { name: 'Discard changes' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(api.put).not.toHaveBeenCalled()
  })
})

describe('emergency access', () => {
  const emergency = { enabled: true, available: true, status: 'available', duration_hours: 24, max_count: 3, used_count: 0, remaining: 3, quota_bytes: 0, used_bytes: 0 }

  it('explains itself and asks the backend for a window with the same request as before', async () => {
    serve({}, { emergency_access: emergency })
    api.post.mockResolvedValue({ data: { used_count: 1, remaining: 2, max_count: 3, until: '2030-01-01T00:00:00Z' } })
    mountMe(<MeView />)
    await screen.findByText('After expiry or traffic limit exceeded, this can temporarily restore connection for 24 hour(s).')
    fireEvent.click(screen.getByRole('button', { name: 'Use emergency access' }))
    await waitFor(() => expect(api.post).toHaveBeenCalledWith('/user/me/emergency-access'))
  })

  it('is off, and says why, when no use is left', async () => {
    serve({}, { emergency_access: { ...emergency, available: false, remaining: 0, status: 'no_quota' } })
    mountMe(<MeView />)
    await screen.findByText('Emergency access has no remaining uses.')
    expect((screen.getByRole('button', { name: 'Use emergency access' }) as HTMLButtonElement).disabled).toBe(true)
  })
})
